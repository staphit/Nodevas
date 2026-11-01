package audit

import (
	"context"
	"sync"
	"time"
)

// Refusals for being over a budget (a throttled sign-in, passcode request or
// device-link redeem) are something a stranger can repeat as fast as the
// limiter in front lets them, and a row each would grow the trail, and every
// backup of it, without bound. RecordThrottledOrLog keeps one row per key and
// window; the next row for the key says how many were held back since.
const (
	throttledEvery = time.Minute
	// maxThrottledKeys bounds the keys remembered. Past it the oldest is
	// forgotten, which costs at worst an extra row.
	maxThrottledKeys = 4096
)

// DetailSuppressed is the detail key a throttled refusal's row carries: how
// many refusals for the same key were not written since the previous row.
const DetailSuppressed = "suppressed"

type throttledNote struct {
	at         time.Time
	suppressed int
}

type throttledNotes struct {
	mu   sync.Mutex
	seen map[string]*throttledNote
}

// admit reports whether key is due a row at now, and how many refusals were
// held back since its last one.
func (n *throttledNotes) admit(key string, now time.Time) (bool, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil {
		n.seen = map[string]*throttledNote{}
	}
	if note := n.seen[key]; note != nil {
		if now.Sub(note.at) < throttledEvery {
			note.suppressed++
			return false, 0
		}
		held := note.suppressed
		note.at, note.suppressed = now, 0
		return true, held
	}
	if len(n.seen) >= maxThrottledKeys {
		n.forgetLocked(now)
	}
	n.seen[key] = &throttledNote{at: now}
	return true, 0
}

// forgetLocked drops every key whose window has passed and, when the set is
// still full, the oldest of the rest.
func (n *throttledNotes) forgetLocked(now time.Time) {
	oldestKey, oldest := "", time.Time{}
	for key, note := range n.seen {
		if now.Sub(note.at) >= throttledEvery {
			delete(n.seen, key)
			continue
		}
		if oldestKey == "" || note.at.Before(oldest) {
			oldestKey, oldest = key, note.at
		}
	}
	if len(n.seen) >= maxThrottledKeys {
		delete(n.seen, oldestKey)
	}
}

// RecordThrottledOrLog is RecordOrLog for a refusal for being over a budget:
// at most one row, and one line, per key (the action and the source, say) a
// minute. The request log still has a line for every request.
func (s *Store) RecordThrottledOrLog(ctx context.Context, event Event, key string) {
	if s == nil {
		return
	}
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	due, held := s.throttled.admit(event.Action+"\x00"+key, at)
	if !due {
		return
	}
	if held > 0 {
		detail := make(map[string]any, len(event.Detail)+1)
		for k, v := range event.Detail {
			detail[k] = v
		}
		detail[DetailSuppressed] = held
		event.Detail = detail
	}
	s.RecordOrLog(ctx, event)
}
