package runners

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// Device links: one-time codes a signed-in person hands a new machine, which
// redeems one for a runner token (runnerwire/link.go). They live in memory
// only, as SHA-256 hashes: a restart forgets every outstanding code, which is
// the safe direction, and the database never holds anything a code could be
// recovered from.

var (
	// ErrLinkNotFound is a code that is unknown, expired or already used. The
	// three are one answer: a caller holding a stale code learns nothing more.
	ErrLinkNotFound = locale.Errorf("that device link is unknown, expired or already used; make a new one on the Runners page", "這個裝置連結不存在、已過期或已使用；請在 Runners 頁面建立新的")
	// ErrTooManyLinks is a person with MaxLinksPerUser codes outstanding.
	ErrTooManyLinks = locale.Errorf("too many device links are waiting to be used; use one or let them expire", "等待使用的裝置連結太多；請先使用或等它們過期")
	// ErrLinkThrottled is a source that failed too many redeems lately.
	ErrLinkThrottled = locale.Errorf("too many failed device link attempts; wait and try again", "裝置連結失敗次數太多；請稍候再試")
)

const (
	// linkCodeBytes is the code's entropy.
	linkCodeBytes = 32
	// maxPendingLinks bounds the whole table, so link creation cannot grow
	// memory without bound even across many accounts.
	maxPendingLinks = 1000
	// Failed redeems per source within linkFailureWindow before it is
	// refused outright for the rest of the window.
	linkFailureLimit  = 5
	linkFailureWindow = 10 * time.Minute
	// maxFailureSources bounds the failure table.
	maxFailureSources = 4096
)

// Link is a redeemed code: who made it, under what name.
type Link struct {
	Owner identity.Actor
	// Revision is the owner's account revision when the link was made; the
	// caller refuses the redeem if the account changed since (removed,
	// renamed, new password, address or role).
	Revision string
	Name     string
	Created  time.Time
	// Language is what the browser that made the link reads: the notice
	// that it was redeemed goes out in it, the redeeming runner being a
	// program that says nothing of its owner's language.
	Language locale.Language
}

type pendingLink struct {
	hash    [sha256.Size]byte
	link    Link
	expires time.Time
}

type failureCount struct {
	count int
	since time.Time
}

// Links holds outstanding device link codes.
type Links struct {
	now func() time.Time
	ttl time.Duration

	mu       sync.Mutex
	pending  []*pendingLink
	failures map[string]*failureCount
}

// NewLinks builds an empty table.
func NewLinks() *Links {
	return &Links{
		now:      time.Now,
		ttl:      runnerwire.LinkTTLSeconds * time.Second,
		failures: make(map[string]*failureCount),
	}
}

// SetClock replaces the clock; for tests.
func (l *Links) SetClock(now func() time.Time) {
	l.mu.Lock()
	l.now = now
	l.mu.Unlock()
}

func (l *Links) pruneLocked(now time.Time) {
	kept := l.pending[:0]
	for _, p := range l.pending {
		if now.Before(p.expires) {
			kept = append(kept, p)
		}
	}
	for i := len(kept); i < len(l.pending); i++ {
		l.pending[i] = nil
	}
	l.pending = kept
	for source, f := range l.failures {
		if now.Sub(f.since) >= linkFailureWindow {
			delete(l.failures, source)
		}
	}
}

// Issue makes a code for owner under name (already checked with CleanName).
// The code is returned only here. language is the maker's (Link.Language).
func (l *Links) Issue(owner identity.Actor, revision, name string, language locale.Language) (string, time.Time, error) {
	if strings.TrimSpace(owner.ID) == "" {
		return "", time.Time{}, ErrInvalid
	}
	var raw [linkCodeBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	mine := 0
	for _, p := range l.pending {
		if p.link.Owner.ID == owner.ID {
			mine++
		}
	}
	if mine >= runnerwire.MaxLinksPerUser || len(l.pending) >= maxPendingLinks {
		return "", time.Time{}, ErrTooManyLinks
	}
	expires := now.Add(l.ttl)
	l.pending = append(l.pending, &pendingLink{
		hash:    sha256.Sum256([]byte(code)),
		link:    Link{Owner: owner, Revision: revision, Name: name, Created: now, Language: language},
		expires: expires,
	})
	return code, expires, nil
}

// Redeem consumes code. source is the caller's address: a source with too
// many recent failures is refused before the code is even looked at, and a
// miss counts against it. Every pending code is compared, in constant time
// each, so how long a miss takes says nothing about the table.
func (l *Links) Redeem(code, source string) (Link, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	if f := l.failures[l.failureKey(source)]; f != nil && f.count >= linkFailureLimit {
		return Link{}, ErrLinkThrottled
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(code)))
	found := -1
	for i, p := range l.pending {
		if subtle.ConstantTimeCompare(hash[:], p.hash[:]) == 1 {
			found = i
		}
	}
	if found < 0 || code == "" {
		l.failLocked(source, now)
		return Link{}, ErrLinkNotFound
	}
	link := l.pending[found].link
	// Single use: gone before the caller has done anything with it.
	l.pending = append(l.pending[:found], l.pending[found+1:]...)
	return link, nil
}

// failureKey is the bucket a source's failures count in: its own, unless the
// table is full of sources still in their window, when newcomers share one
// rather than push anybody's failures out.
func (l *Links) failureKey(source string) string {
	if _, ok := l.failures[source]; ok || len(l.failures) < maxFailureSources {
		return source
	}
	return "overflow"
}

func (l *Links) failLocked(source string, now time.Time) {
	key := l.failureKey(source)
	f := l.failures[key]
	if f == nil {
		f = &failureCount{since: now}
		l.failures[key] = f
	}
	f.count++
}

// Outstanding counts owner's codes still redeemable.
func (l *Links) Outstanding(ownerID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.now())
	n := 0
	for _, p := range l.pending {
		if p.link.Owner.ID == ownerID {
			n++
		}
	}
	return n
}
