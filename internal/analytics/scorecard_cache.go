package analytics

import (
	"sync"
	"time"

	"nodevas/internal/db"
)

// Scorecards load a window's whole run history. The same question asked
// again within scorecardTTL — the analytics page's tabs, the autopilot
// routing a batch, tiers and suggestions built on them — is answered from
// memory. InvalidateScorecards drops every answer at once; the server calls
// it when a verdict, a review vote or a run's outcome changes, and the TTL
// bounds how stale anything else can make them.

// scorecardTTL is how long ScorecardsFor keeps an answer; zero keeps none.
var scorecardTTL = time.Minute

// scorecardClock is time.Now unless a test moves it.
var scorecardClock = time.Now

// maxScorecardEntries bounds the cache: one entry per (owner, window,
// track) asked, which is a handful per person.
const maxScorecardEntries = 512

type scorecardKey struct {
	db                     *db.DB
	owner, project, viewer string
	since                  string
	days                   int
	track                  string
}

type scorecardEntry struct {
	cards      Scorecards
	expires    time.Time
	generation uint64
}

var scorecardCache = struct {
	sync.Mutex
	entries    map[scorecardKey]scorecardEntry
	generation uint64
}{entries: map[scorecardKey]scorecardEntry{}}

// InvalidateScorecards drops every cached scorecard.
func InvalidateScorecards() {
	scorecardCache.Lock()
	scorecardCache.generation++
	clear(scorecardCache.entries)
	scorecardCache.Unlock()
}

// cachedScorecards returns the cached answer to key, if a fresh one exists,
// and the generation a newly computed answer must be stored under.
func cachedScorecards(key scorecardKey) (Scorecards, bool, uint64) {
	scorecardCache.Lock()
	defer scorecardCache.Unlock()
	entry, ok := scorecardCache.entries[key]
	if !ok || entry.generation != scorecardCache.generation || !scorecardClock().Before(entry.expires) {
		return Scorecards{}, false, scorecardCache.generation
	}
	return copyScorecards(entry.cards), true, scorecardCache.generation
}

// storeScorecards keeps cards for key, unless the cache was invalidated
// while they were computed (they may predate the change).
func storeScorecards(key scorecardKey, cards Scorecards, generation uint64) {
	if scorecardTTL <= 0 {
		return
	}
	now := scorecardClock()
	scorecardCache.Lock()
	defer scorecardCache.Unlock()
	if generation != scorecardCache.generation {
		return
	}
	if len(scorecardCache.entries) >= maxScorecardEntries {
		for k, e := range scorecardCache.entries {
			if !now.Before(e.expires) {
				delete(scorecardCache.entries, k)
			}
		}
		if len(scorecardCache.entries) >= maxScorecardEntries {
			clear(scorecardCache.entries)
		}
	}
	scorecardCache.entries[key] = scorecardEntry{cards: copyScorecards(cards), expires: now.Add(scorecardTTL), generation: generation}
}

// copyScorecards copies the rows so a caller that edits its answer does
// not edit the cache.
func copyScorecards(in Scorecards) Scorecards {
	in.Rows = append([]ScorecardRow(nil), in.Rows...)
	return in
}
