package runnerwire

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// Coordination (§C): three layers — first come first served (busy), early
// same-file overlap (touched), and deliberate competition (contests / 比稿).
const (
	// CapTouched: the runner reports repo-relative paths touched by active runs.
	CapTouched = "touched"
	// CapCoord: the runner understands Job.Coordinated, Job.Contest, JobCompare,
	// WorkspaceOpenPR and --toolset coordinated.
	CapCoord = "coord"

	// TypeTouched (runner → hub): active run's newly touched paths (Envelope.RunID, Envelope.Touched).
	TypeTouched = "touched"

	// JobCompare is a read-only contest reviewer job that scores entries.
	JobCompare = "compare"

	// Limits on touched-file reports and coordination requests.
	MaxTouchedFiles      = 500
	MaxTouchedPerMessage = 200
	MaxTouchedPathBytes  = 1024
	MaxOverlapPaths      = 20
	MinContestEntries    = 2
	MaxContestEntries    = 3
	MaxHolderIDs         = 8
)

// OnBusy choices for busy start requests.
const (
	OnBusyAsk      = ""
	OnBusyQueue    = "queue"
	OnBusyTakeOver = "takeOver"
	OnBusyBoth     = "both"
	OnBusyContinue = "continue"
	OnBusySkip     = "skip"
)

// Contest states (Contest.State).
const (
	ContestRunning   = "running"
	ContestJudging   = "judging"
	ContestReady     = "ready"
	ContestDecided   = "decided"
	ContestCancelled = "cancelled"
)

// Contest pick apply modes (PickRequest.Apply).
const (
	ContestApplyLocal = "local"
	ContestApplyPR    = "pr"
	ContestApplyKeep  = "keep"
)

// ValidOnBusy reports whether choice is a valid onBusy value for a single-run
// start (group == false) or a multi-repo group start (group == true).
func ValidOnBusy(choice string, group bool) bool {
	if group {
		switch choice {
		case OnBusyAsk, OnBusyBoth, OnBusySkip:
			return true
		}
		return false
	}
	switch choice {
	case OnBusyAsk, OnBusyQueue, OnBusyTakeOver, OnBusyBoth, OnBusyContinue:
		return true
	}
	return false
}

// ValidHolderIDs reports whether ids is within MaxHolderIDs and contains no empty id.
func ValidHolderIDs(ids []string) bool {
	if len(ids) > MaxHolderIDs {
		return false
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return false
		}
	}
	return true
}

// Touched is Envelope.Touched: repo-relative paths an active run has changed.
type Touched struct {
	Files   []string `json:"files"`
	AddOnly []string `json:"addOnly,omitempty"`
	More    bool     `json:"more,omitempty"`
}

// JobContest is Job.Contest: a contest entry's identity and count.
type JobContest struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Of    int    `json:"of"`
}

// RunHolder is one visible active or waiting run holding a node or repository.
type RunHolder struct {
	RunID    string `json:"runId"`
	Title    string `json:"title"`
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Mode     string `json:"mode,omitempty"`
	Status   string `json:"status"`
	Waiting  bool   `json:"waiting"`
	Since    string `json:"since"`
	By       string `json:"by"`
	NodeID   string `json:"nodeId,omitempty"`
	RepoName string `json:"repoName,omitempty"`
	MayStop  bool   `json:"mayStop"`
	SameNode bool   `json:"sameNode"`
}

// BusyContinue describes the follow-up target when onBusy "continue" is offered.
type BusyContinue struct {
	RunID  string `json:"runId"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	OpenPR bool   `json:"openPR"`
}

// RepoBusy is one repository's busy status in a multi-repo busy response.
type RepoBusy struct {
	RepoID   string        `json:"repoId"`
	Holders  []RunHolder   `json:"holders"`
	Others   int           `json:"others"`
	Choices  []string      `json:"choices,omitempty"`
	Continue *BusyContinue `json:"continue,omitempty"`
}

// BusyResponse is the busy preflight body (GET /api/runs/busy) and the 409 BUSY
// payload carried by StartError.Busy.
type BusyResponse struct {
	Holders  []RunHolder   `json:"holders"`
	Others   int           `json:"others"`
	Choices  []string      `json:"choices"`
	Continue *BusyContinue `json:"continue,omitempty"`
	Repos    []RepoBusy    `json:"repos,omitempty"`
}

// GlobalBusyResponse is GET /api/global-runs/busy.
type GlobalBusyResponse struct {
	Repos []RepoBusy `json:"repos"`
}

// Overlap is one pair of runs touching the same files. When Hidden is true
// (a cross-member pair), only Hidden and Since are populated.
type Overlap struct {
	RunID       string   `json:"runId,omitempty"`
	Title       string   `json:"title,omitempty"`
	NodeID      string   `json:"nodeId,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Status      string   `json:"status,omitempty"`
	Mine        bool     `json:"mine,omitempty"`
	Hidden      bool     `json:"hidden,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	PathCount   int      `json:"pathCount,omitempty"`
	StrongCount int      `json:"strongCount,omitempty"`
	Since       string   `json:"since"`
}

// OverlapsResponse is GET /api/runs/:id/overlaps and /api/global-runs/:id/overlaps.
type OverlapsResponse struct {
	Overlaps []Overlap `json:"overlaps"`
}

// ContestEntrySpec selects a runner (optional for global contests), agent and model.
type ContestEntrySpec struct {
	RunnerID string `json:"runnerId,omitempty"`
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
}

// Contest is a deliberate competition (比稿) as the HTTP API returns it.
type Contest struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	NodeID      string            `json:"nodeId,omitempty"`
	Project     string            `json:"project,omitempty"`
	Global      bool              `json:"global"`
	State       string            `json:"state"`
	EntryCount  int               `json:"entryCount"`
	Reviewer    *ContestEntrySpec `json:"reviewer,omitempty"`
	JudgeRunID  string            `json:"judgeRunId,omitempty"`
	WinnerRunID string            `json:"winnerRunId,omitempty"`
	CreatedBy   string            `json:"createdBy"`
	CreatedAt   string            `json:"createdAt"`
	DecidedAt   string            `json:"decidedAt,omitempty"`
}

// CreateContestRequest is POST /api/runs/contests.
type CreateContestRequest struct {
	NodeID    string             `json:"nodeId"`
	RepoID    string             `json:"repoId"`
	Base      string             `json:"base,omitempty"`
	Prompt    string             `json:"prompt,omitempty"`
	Simple    bool               `json:"simple,omitempty"`
	Entries   []ContestEntrySpec `json:"entries"`
	Reviewer  *ContestEntrySpec  `json:"reviewer,omitempty"`
	OnBusy    string             `json:"onBusy,omitempty"`
	HolderIDs []string           `json:"holderIds,omitempty"`
}

// CreateGlobalContestRequest is POST /api/global-runs/contests.
type CreateGlobalContestRequest struct {
	RunnerID  string             `json:"runnerId"`
	RepoID    string             `json:"repoId"`
	Base      string             `json:"base,omitempty"`
	Task      string             `json:"task"`
	Slug      string             `json:"slug,omitempty"`
	Simple    bool               `json:"simple,omitempty"`
	Entries   []ContestEntrySpec `json:"entries"`
	Reviewer  *ContestEntrySpec  `json:"reviewer,omitempty"`
	OnBusy    string             `json:"onBusy,omitempty"`
	HolderIDs []string           `json:"holderIds,omitempty"`
}

// ContestScore is one entry's score from the compare reviewer.
type ContestScore struct {
	Entry string `json:"entry"`
	Score int    `json:"score"`
	Why   string `json:"why"`
}

// ContestScores is the parsed JSON block from a compare reviewer run.
type ContestScores struct {
	Scores []ContestScore `json:"scores"`
	Pick   string         `json:"pick"`
}

// ContestCompareEntry summarizes one entry in GET /api/contests/:id.
type ContestCompareEntry struct {
	Label     string       `json:"label"`
	RunID     string       `json:"runId"`
	Agent     string       `json:"agent"`
	Model     string       `json:"model"`
	Status    string       `json:"status"`
	Files     []FileChange `json:"files"`
	Additions int          `json:"additions"`
	Deletions int          `json:"deletions"`
	Summary   string       `json:"summary"`
}

// ContestCompare is the comparison payload of GET /api/contests/:id.
type ContestCompare struct {
	Entries []ContestCompareEntry `json:"entries"`
	Scores  *ContestScores        `json:"scores"`
}

// ContestResponse is the body of POST/GET/cancel contest endpoints.
type ContestResponse struct {
	Contest Contest         `json:"contest"`
	Runs    []Run           `json:"runs"`
	Compare *ContestCompare `json:"compare,omitempty"`
}

// JudgeContestRequest is POST /api/contests/:id/judge.
type JudgeContestRequest struct {
	RunnerID string `json:"runnerId,omitempty"`
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
}

// PickRequest is POST /api/contests/:id/pick.
type PickRequest struct {
	RunID         string `json:"runId"`
	Apply         string `json:"apply"`
	DiscardOthers bool   `json:"discardOthers"`
}

// PickActionResult reports one workspace action initiated by pick.
type PickActionResult struct {
	RunID  string `json:"runId"`
	Action string `json:"action"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

// PickResponse is the body of POST /api/contests/:id/pick.
type PickResponse struct {
	Contest Contest            `json:"contest"`
	Actions []PickActionResult `json:"actions"`
}

// RepoKey returns a stable 32-lowercase-hex identifier for a repository.
// With a remote URL, it hashes the normalised remote (scheme, userinfo and
// .git stripped, host and path lowercased, SCP-style git@h:o/r normalised to
// h/o/r) so checkouts of the same remote share one key without ever storing
// credentials in the database. Without a remote, it hashes local:<runner>/<repo>.
func RepoKey(runnerID string, repo Repo) string {
	key := normalizeRemote(repo.Remote)
	if key == "" {
		key = "local:" + runnerID + "/" + repo.ID
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

func normalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	stripGit := func(p string) string {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if len(p) >= 4 && strings.EqualFold(p[len(p)-4:], ".git") {
			p = p[:len(p)-4]
		}
		return strings.Trim(p, "/")
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			host := strings.ToLower(u.Hostname())
			if port := u.Port(); port != "" && port != "22" && port != "80" && port != "443" {
				host += ":" + port
			}
			p := stripGit(u.Path)
			if p != "" {
				return host + "/" + strings.ToLower(p)
			}
			return host
		}
	}
	// SCP-like SSH syntax: [user@]host:path (exclude Windows drive letters like C:\...)
	if colon := strings.IndexByte(s, ':'); colon > 1 && !strings.ContainsAny(s[:colon], "/\\") {
		hostPart := s[:colon]
		pathPart := s[colon+1:]
		if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		host := strings.ToLower(strings.TrimSpace(hostPart))
		p := stripGit(pathPart)
		if host != "" {
			if p != "" {
				return host + "/" + strings.ToLower(p)
			}
			return host
		}
	}
	return strings.ToLower(stripGit(s))
}
