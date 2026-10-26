package runnerwire

import (
	"strconv"
	"testing"
	"time"
)

// Reset wording per agent CLI. The Claude and Codex lines match the rows of
// the runner's adapter/failure_test.go; the clock forms are read in the
// runner's zone (here New York), the relative ones need no zone.
func TestParseResetTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	// 2026-10-27 10:30 in New York.
	now := time.Date(2026, 10, 27, 10, 30, 0, 0, ny)
	epoch := now.Add(2 * time.Hour).Unix()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, ny) }
	cases := []struct {
		name, text string
		loc        *time.Location
		want       time.Time // zero: not read
	}{
		{"claude epoch", "Claude AI usage limit reached|" + itoa(epoch), ny, time.Unix(epoch, 0)},
		{"claude rate_limit_event", `{"rate_limit_info":{"status":"rejected","resetsAt":` + itoa(epoch) + `}}`, ny, time.Unix(epoch, 0)},
		{"claude resets 3pm", "5-hour limit reached ∙ resets 3pm", ny, at(15, 0)},
		{"claude resets with zone", "You've hit your limit · resets 3pm (Europe/London)", nil,
			time.Date(2026, 10, 27, 15, 0, 0, 0, mustLoad(t, "Europe/London"))},
		{"claude resets earlier hour is tomorrow", "limit reached ∙ resets 9am", ny, at(9, 0).AddDate(0, 0, 1)},
		{"claude bare clock without a zone", "5-hour limit reached ∙ resets 3pm", nil, time.Time{}},
		{"codex try again in hours", "You've hit your usage limit. Upgrade to Pro or try again in 3 hours.", nil, now.Add(3 * time.Hour)},
		{"codex days hours minutes", "try again in 2 days 4 hours 13 minutes.", nil,
			now.Add(52*time.Hour + 13*time.Minute)},
		{"codex try again at", "You've hit your usage limit. Try again at 3:25 PM.", ny, at(15, 25)},
		{"codex try again on date", "You've hit your usage limit. Try again on Oct 29th, 2026 9:05 AM.", ny,
			time.Date(2026, 10, 29, 9, 5, 0, 0, ny)},
		{"gemini retry in seconds", "Quota exceeded for quota metric. Please retry in 22.94s.", nil,
			now.Add(22940 * time.Millisecond)},
		{"gemini go duration", "Please retry in 1m30s", nil, now.Add(90 * time.Second)},
		{"gemini retryDelay", `{"error":{"code":429,"details":[{"retryDelay":"34s"}]}}`, nil, now.Add(34 * time.Second)},
		{"retry-after header", "HTTP 429 Too Many Requests\nRetry-After: 120", nil, now.Add(2 * time.Minute)},
		{"opencode retry after words", "APIError: Rate limit exceeded, retry after 60 seconds", nil, now.Add(time.Minute)},
		{"nothing said", "Rate limit reached for gpt-5.5 in organization org-x", ny, time.Time{}},
		{"a word, not a unit", "please wait in 5 more", nil, time.Time{}},
		{"past epoch", "Claude AI usage limit reached|1000000000", ny, time.Time{}},
		{"too far", "try again in 30 days", nil, time.Time{}},
		{"empty", "", ny, time.Time{}},
	}
	for _, c := range cases {
		got, ok := ParseResetTime(c.text, now, c.loc)
		if c.want.IsZero() {
			if ok {
				t.Errorf("%s: read %v from %q, want nothing", c.name, got, c.text)
			}
			continue
		}
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: %q = %v, %v; want %v", c.name, c.text, got, ok, c.want)
		}
	}
}

func TestHandoffBranchNames(t *testing.T) {
	for name, want := range map[string]bool{
		"nodevas/handoff/run-1":          true,
		HandoffBranchName("abc123DEF_x"): true,
		"agent/n-1-run":                  false,
		"nodevas/handoff/":               false,
		"nodevas/handoff/../main":        false,
		"nodevas/handoff/a/b":            false,
		"nodevas/handoff/-x":             true, // a run ID; the runner always passes it after "--"
		"refs/heads/nodevas/handoff/x":   false,
		"main":                           false,
		"nodevas/handoff/x y":            false,
	} {
		if got := ValidHandoffBranch(name); got != want {
			t.Errorf("ValidHandoffBranch(%q) = %v, want %v", name, got, want)
		}
	}
	if !HandoffWorthy(FailureQuota) || !HandoffWorthy(FailureRateLimit) ||
		HandoffWorthy(FailureModel) || HandoffWorthy(FailureAgentAbsent) || HandoffWorthy(FailureTask) {
		t.Error("HandoffWorthy")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("no tz database:", err)
	}
	return loc
}
