package runnerwire

import (
	"strings"
	"unicode/utf8"
)

// Global agents (agent workspace, phase 2): a run in one of the runner's
// repositories for no project and no node, started by the runner's owner.
// It gets no board: the runner starts no `nodevas mcp` for it.
//
// On the wire a global job is an ordinary offer with Job.Global set, an
// empty Project and NodeID, Job.Slug naming its branch
// (agent/<slug>-<run8>) and Job.NoPR when nobody asked for a pull request.
const (
	// CapGlobalRuns: the runner understands Job.Global, Slug and NoPR.
	CapGlobalRuns = "global-runs"
	// CapGlobalRunsOn: its owner allows global runs (runner setting
	// globalRuns, on unless turned off).
	CapGlobalRunsOn = "global-runs-on"
	// CapContinue: the runner supports follow-up turns, graceful End,
	// view-only reporting, work.json and open-PR turn updates.
	CapContinue = "continue"

	// MaxGlobalTaskBytes caps the whole task, UTF-8 bytes after trimming.
	MaxGlobalTaskBytes = 32 << 10
	// MaxFollowUpBytes caps a follow-up message.
	MaxFollowUpBytes = MaxGlobalTaskBytes
	// MaxGlobalTitleRunes caps its first line, the run's title.
	MaxGlobalTitleRunes = 120
	// MaxSlugBytes caps a branch slug.
	MaxSlugBytes = 40
	// DefaultGlobalSlug names the branch of a task whose title gives no
	// ASCII letters or digits.
	DefaultGlobalSlug = "task"
	// MinGroupRepos and MaxGroupRepos bound a multi-repo group start.
	MinGroupRepos = 2
	MaxGroupRepos = 30

	// Realtime, sent to the run's dispatcher and runner owner accounts only
	// (never a project room); the id is the run's.
	GlobalRunsChangedEvent = "global-runs-changed"
	GlobalRunEventsEvent   = "global-run-events"
	GlobalRunNotifyEvent   = "global-run-notify"
)

// ValidSlug reports whether s may name a global run's branch: lowercase
// ASCII letters and digits in groups joined by single dashes, at most
// MaxSlugBytes.
func ValidSlug(s string) bool {
	if s == "" || len(s) > MaxSlugBytes || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
		case c == '-' && s[i-1] != '-':
		default:
			return false
		}
	}
	return true
}

// SlugFrom derives a branch slug from a title, by the runner's branch-name
// rules: ASCII letters lowercased and digits kept, every run of anything
// else one dash, cut to MaxSlugBytes at a dash when it can be.
// DefaultGlobalSlug when nothing is left (a title in Chinese, say).
func SlugFrom(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range title {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxSlugBytes {
		cut := s[:MaxSlugBytes]
		if i := strings.LastIndexByte(cut, '-'); i > 0 && s[MaxSlugBytes] != '-' {
			cut = cut[:i]
		}
		s = strings.Trim(cut, "-")
	}
	if s == "" {
		return DefaultGlobalSlug
	}
	return s
}

// GlobalTitle splits a global run's task into its title, the first
// non-empty line trimmed and cut to MaxGlobalTitleRunes, and its body, the
// lines after it trimmed. CRLF counts as one line break.
func GlobalTitle(task string) (title, body string) {
	rest := strings.ReplaceAll(task, "\r\n", "\n")
	for rest != "" {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", " "))
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > MaxGlobalTitleRunes {
			line = strings.TrimSpace(string([]rune(line)[:MaxGlobalTitleRunes]))
		}
		return line, strings.TrimSpace(rest)
	}
	return "", ""
}

// CreateGlobalRunRequest is the body of POST /api/global-runs (no project).
type CreateGlobalRunRequest struct {
	RunnerID string `json:"runnerId"`
	RepoID   string `json:"repoId"`
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
	// Mode is ModeHeadless ("") or ModeInteractive.
	Mode string `json:"mode,omitempty"`
	// Base is the branch to start from; "" means the repository's default.
	Base string `json:"base,omitempty"`
	// Task is what the agent should do; its first line is the run's title.
	Task string `json:"task"`
	// Slug names the branch (agent/<slug>-<run8>); "" derives it from the
	// title.
	Slug string `json:"slug,omitempty"`
	// OpenPR asks the runner to push and open a pull request when done (it
	// still may not, when its own autoPR is off). Off keeps the branch on
	// the runner for a local merge or discard.
	OpenPR bool `json:"openPR,omitempty"`
	// Simple adds the hub's fixed instruction for a person who is not a
	// programmer (runs.SimpleInstruction) to the task. It lifts no gate.
	Simple bool `json:"simple,omitempty"`
	// Coordination (§C.2.2): how to handle an active holder, and the visible
	// holder IDs the person saw (takeOver / continue).
	OnBusy    string   `json:"onBusy,omitempty"`
	HolderIDs []string `json:"holderIds,omitempty"`
}

// FollowUpRequest is POST /api/global-runs/:id/follow-up.
type FollowUpRequest struct {
	Message string `json:"message"`
	Mode    string `json:"mode,omitempty"`
}

// RunThreadResponse is GET /api/global-runs/:id/thread (oldest first, <= 50).
type RunThreadResponse struct {
	Runs []Run `json:"runs"`
}

// CreateGlobalGroupRequest is POST /api/global-runs/groups.
type CreateGlobalGroupRequest struct {
	RunnerID  string   `json:"runnerId"`
	RepoIDs   []string `json:"repoIds"`
	Agent     string   `json:"agent"`
	Model     string   `json:"model,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Base      string   `json:"base,omitempty"`
	Task      string   `json:"task"`
	Slug      string   `json:"slug,omitempty"`
	OpenPR    bool     `json:"openPR,omitempty"`
	Simple    bool     `json:"simple,omitempty"`
	OnBusy    string   `json:"onBusy,omitempty"`
	HolderIDs []string `json:"holderIds,omitempty"`
}

// ValidGroupRepoIDs reports whether repoIDs holds between MinGroupRepos and
// MaxGroupRepos distinct, non-empty repository IDs.
func ValidGroupRepoIDs(repoIDs []string) bool {
	if len(repoIDs) < MinGroupRepos || len(repoIDs) > MaxGroupRepos {
		return false
	}
	seen := make(map[string]bool, len(repoIDs))
	for _, id := range repoIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// RunGroup is a multi-repo fan-out group as the HTTP API returns it.
type RunGroup struct {
	ID            string `json:"id"`
	RunnerID      string `json:"runnerId"`
	Title         string `json:"title"`
	Agent         string `json:"agent"`
	Model         string `json:"model"`
	NoPR          bool   `json:"noPR,omitempty"`
	RepoCount     int    `json:"repoCount"`
	HeldUntil     string `json:"heldUntil,omitempty"`
	CreatedBy     string `json:"createdBy"`
	CreatedByName string `json:"createdByName"`
	CreatedAt     string `json:"createdAt"`
}

// GlobalGroupResponse is POST/GET /api/global-runs/groups(/:id).
type GlobalGroupResponse struct {
	Group RunGroup `json:"group"`
	Runs  []Run    `json:"runs"`
}

// GroupFollowUpRequest is POST /api/global-runs/groups/:id/follow-up.
type GroupFollowUpRequest struct {
	Message string `json:"message"`
	Only    string `json:"only,omitempty"` // "failed" | "all"
}

// GroupFollowUpSkipped names one group member a group follow-up skipped.
type GroupFollowUpSkipped struct {
	RunID string `json:"runId"`
	Code  string `json:"code"`
}

// GroupFollowUpResponse is POST /api/global-runs/groups/:id/follow-up.
type GroupFollowUpResponse struct {
	Started []Run                  `json:"started"`
	Skipped []GroupFollowUpSkipped `json:"skipped"`
}

// GlobalRunsResponse is the body of GET /api/global-runs: the viewer's
// global runs (every unfinished one, then the newest finished ones; More
// when older finished ones exist) and the viewer's own runners' load.
type GlobalRunsResponse struct {
	Runs    []Run        `json:"runs"`
	More    bool         `json:"more"`
	Runners []RunnerLoad `json:"runners"`
}

// Why a runner cannot take global runs or settings now (RunnerLoad).
const (
	RunnerBlockedOffline     = "offline"
	RunnerBlockedOutdated    = "outdated"
	RunnerBlockedOff         = "off"
	RunnerBlockedHeadless    = "headless"
	RunnerBlockedSettingsOff = "settings_off"
)

// RunnerLoad is one of the viewer's runners on the global agents tab.
type RunnerLoad struct {
	RunnerID string `json:"runnerId"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	// MaxConcurrent is from the hello (≤ 0 there counts as 1).
	MaxConcurrent int `json:"maxConcurrent"`
	// Ceiling is hello.MaxConcurrentCeiling; 0 means it cannot be set from
	// the hub.
	Ceiling int `json:"ceiling"`
	// Settable is "" or why maxConcurrent cannot be changed from here:
	// offline, outdated or settings_off.
	Settable string `json:"settable"`
	// GlobalRuns is "" (may start) or why not: offline, outdated, off or
	// headless.
	GlobalRuns string `json:"globalRuns"`
	// Busy counts offered and running runs, every project's and global.
	Busy   int `json:"busy"`
	Queued int `json:"queued"`
	// GlobalActive counts its queued, offered and running global runs.
	GlobalActive int         `json:"globalActive"`
	Today        RunnerToday `json:"today"`
}

// RunnerToday is a runner's use so far today (UTC), as the owner's budget
// counts it.
type RunnerToday struct {
	Runs    int     `json:"runs"`
	Tokens  int64   `json:"tokens"` // input + output
	CostUSD float64 `json:"costUsd"`
}

// CancelAllResponse is the body of POST /api/global-runs/cancel-all.
type CancelAllResponse struct {
	Cancelled int   `json:"cancelled"`
	Runs      []Run `json:"runs"`
}
