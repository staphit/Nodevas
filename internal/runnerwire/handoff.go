package runnerwire

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	// Named zones in reset messages ("resets 3pm (Europe/London)") must
	// resolve on a Windows runner, which has no zoneinfo of its own.
	_ "time/tzdata"
)

// Handoff: when an implement run stops because its agent hit a usage limit
// (FailureQuota) or was throttled (FailureRateLimit), its runner keeps the
// partial work on a local branch, HandoffBranchPrefix + the run ID, and
// writes a handoff note. The hub hands both to the next run (the next model of
// the fallback chain, or the same one once the limit resets), which starts
// from that branch instead of the base. Only the runner makes and reads these
// branches: the hub names one, and the runner checks it is its own.
//
// FailureModel and FailureAgentAbsent are not handoff-worthy: a model the
// login cannot use, or a CLI that is missing or signed out, fails before the
// agent's first turn, so there is no partial work to keep, and waiting for a
// reset would not fix either.

// HandoffBranchPrefix starts every handoff branch.
const HandoffBranchPrefix = "nodevas/handoff/"

// MaxHandoffNoteBytes caps a handoff note, the way MaxSummaryBytes caps a
// summary: it goes into the next run's prompt.
const MaxHandoffNoteBytes = 8 << 10

// MaxLimitWaits is how many times one task's lineage waits for a usage limit
// to reset before it is left failed: a limit that never seems to lift is a
// person's problem.
const MaxLimitWaits = 3

// LimitResetSlack is added to a reset time, so the retry does not land the
// second the provider's window turns.
const LimitResetSlack = time.Minute

// MaxLimitWait bounds how far ahead a retry is scheduled.
const MaxLimitWait = 7 * 24 * time.Hour

// HandoffWorthy reports whether a failure kind leaves partial work worth
// handing on, and is worth waiting out when no fallback is left.
func HandoffWorthy(kind string) bool {
	return kind == FailureQuota || kind == FailureRateLimit
}

var handoffRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// HandoffBranchName is the handoff branch for a run.
func HandoffBranchName(runID string) string { return HandoffBranchPrefix + runID }

// ValidHandoffBranch reports whether name is a handoff branch name: the
// prefix and a run ID, nothing that could be read as another ref or an
// option.
func ValidHandoffBranch(name string) bool {
	id, ok := strings.CutPrefix(name, HandoffBranchPrefix)
	return ok && handoffRunID.MatchString(id)
}

// DefaultLimitBackoff is how long to wait when the agent did not say when
// its limit resets.
func DefaultLimitBackoff(kind string) time.Duration {
	if kind == FailureRateLimit {
		return 5 * time.Minute
	}
	return time.Hour
}

// Reset-time wording, as the agent CLIs print it (see the runner's
// adapter/failure.go and its tests):
//
//	Claude Code   "Claude AI usage limit reached|1761300000" (epoch seconds),
//	              "5-hour limit reached ∙ resets 3pm",
//	              "You've hit your limit · resets 3pm (Europe/London)",
//	              a rate_limit_event's "resetsAt": 1761300000
//	Codex         "… or try again in 3 hours.", "try again in 2 days 4 hours 13 minutes",
//	              "Try again at 3:25 PM.", "Try again on Nov 2nd, 2025 3:25 PM."
//	Gemini CLI    "Please retry in 22.94s.", "retryDelay": "34s"
//	OpenCode/agy  "Retry-After: 120", "retry after 60 seconds", gRPC "retryDelay"
var (
	resetEpoch = regexp.MustCompile(`(?i)(?:limit reached\||resets_?at"?\s*[:=]\s*"?)(\d{10})\b`)
	retryAfter = regexp.MustCompile(`(?i)retry-after"?\s*[:=]?\s*"?(\d{1,6})\b`)
	retryDelay = regexp.MustCompile(`(?i)retry_?delay"?\s*[:=]\s*"?(\d+(?:\.\d+)?)s\b`)
	// relativeLead is the phrase before a duration; the duration is read by
	// parseDuration from where it ends.
	relativeLead = regexp.MustCompile(`(?i)(?:try again|retry|resets?|available again|wait|reset)\s+(?:in|after)\s+`)
	durationPart = regexp.MustCompile(`(?i)^\s*(?:,\s*|and\s+)?(\d+(?:\.\d+)?)\s*(days|day|hours|hour|hrs|hr|h|minutes|minute|mins|min|m|seconds|second|secs|sec|s|d)`)
	clockReset   = regexp.MustCompile(`(?i)(?:resets?|try again at|retry at|available at|reset at)\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s*\(([A-Za-z_]+(?:/[A-Za-z_+-]+){0,2})\))?`)
	dateReset    = regexp.MustCompile(`(?i)(?:at|on)\s+([A-Z][a-z]{2})[a-z]*\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4}),?\s+(\d{1,2}):(\d{2})\s*(am|pm)?`)
)

// ParseResetTime reads when a usage limit resets from an agent's failure
// text. now anchors relative wording; loc is the zone a bare clock time ("resets
// 3pm") is in, the agent's own machine's. With loc nil, clock times without
// a named zone are not read (the hub does not know the runner's zone). The
// answer is after now and at most MaxLimitWait ahead, or ok is false.
func ParseResetTime(text string, now time.Time, loc *time.Location) (time.Time, bool) {
	if strings.TrimSpace(text) == "" {
		return time.Time{}, false
	}
	within := func(t time.Time) (time.Time, bool) {
		if !t.After(now) || t.Sub(now) > MaxLimitWait {
			return time.Time{}, false
		}
		return t, true
	}
	if m := resetEpoch.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			if t, ok := within(time.Unix(sec, 0)); ok {
				return t, true
			}
		}
	}
	if m := retryDelay.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseFloat(m[1], 64); err == nil && sec > 0 {
			return within(now.Add(time.Duration(sec * float64(time.Second))))
		}
	}
	if m := retryAfter.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.Atoi(m[1]); err == nil && sec > 0 {
			return within(now.Add(time.Duration(sec) * time.Second))
		}
	}
	for _, idx := range relativeLead.FindAllStringIndex(text, -1) {
		if d, ok := parseDuration(text[idx[1]:]); ok {
			return within(now.Add(d))
		}
	}
	if m := dateReset.FindStringSubmatch(text); m != nil && loc != nil {
		if t, ok := dateTime(m, loc); ok {
			return within(t)
		}
	}
	if m := clockReset.FindStringSubmatch(text); m != nil {
		zone := loc
		if m[4] != "" {
			if strings.EqualFold(m[4], "utc") {
				zone = time.UTC
			} else if named, err := time.LoadLocation(m[4]); err == nil {
				zone = named
			}
		}
		if zone != nil && (m[2] != "" || m[3] != "") {
			if t, ok := nextClock(now, zone, m[1], m[2], m[3]); ok {
				return within(t)
			}
		}
	}
	return time.Time{}, false
}

// parseDuration reads "3 hours", "2 days 4 hours 13 minutes", "1m30s",
// "22.94s" from the start of s.
func parseDuration(s string) (time.Duration, bool) {
	var total time.Duration
	read := false
	for {
		m := durationPart.FindStringSubmatchIndex(s)
		if m == nil {
			break
		}
		// A unit followed by more letters is a word ("5 more"), not a unit.
		if m[1] < len(s) && isLetter(s[m[1]]) {
			break
		}
		n, err := strconv.ParseFloat(s[m[2]:m[3]], 64)
		if err != nil {
			break
		}
		unit := time.Second
		switch u := strings.ToLower(s[m[4]:m[5]]); {
		case strings.HasPrefix(u, "d"):
			unit = 24 * time.Hour
		case strings.HasPrefix(u, "h"):
			unit = time.Hour
		case strings.HasPrefix(u, "m"):
			unit = time.Minute
		}
		total += time.Duration(n * float64(unit))
		read = true
		s = s[m[1]:]
	}
	return total, read && total > 0
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func hour24(hour int, ampm string) (int, bool) {
	switch strings.ToLower(ampm) {
	case "":
		return hour, hour >= 0 && hour < 24
	case "am":
		if hour < 1 || hour > 12 {
			return 0, false
		}
		return hour % 12, true
	default: // pm
		if hour < 1 || hour > 12 {
			return 0, false
		}
		return hour%12 + 12, true
	}
}

// nextClock is the next time the clock in loc reads hour:minute.
func nextClock(now time.Time, loc *time.Location, h, m, ampm string) (time.Time, bool) {
	hour, _ := strconv.Atoi(h)
	minute := 0
	if m != "" {
		minute, _ = strconv.Atoi(m)
	}
	hour, ok := hour24(hour, ampm)
	if !ok || minute > 59 {
		return time.Time{}, false
	}
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t, true
}

// dateTime reads "Nov 2nd, 2025 3:25 PM" in loc.
func dateTime(m []string, loc *time.Location) (time.Time, bool) {
	month, err := time.Parse("Jan", strings.ToUpper(m[1][:1])+strings.ToLower(m[1][1:]))
	if err != nil {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	hour, ok := hour24(hour, m[6])
	if !ok || day < 1 || day > 31 || minute > 59 {
		return time.Time{}, false
	}
	return time.Date(year, month.Month(), day, hour, minute, 0, 0, loc), true
}
