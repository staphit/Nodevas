package station

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/agents"
	"nodevas/internal/autopilot"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Limits. Counts are always complete; only the lists are capped.
const (
	// MaxProjects is how many projects a response carries, busiest first.
	MaxProjects = 60
	// maxQueried is how many readable projects one request looks at.
	maxQueried = 500
	// runLimit caps the one runs query across every project.
	runLimit = 2000
	// recentWindow is how far back ended runs are kept.
	recentWindow = 24 * time.Hour

	maxActiveRuns = 24
	maxRecentRuns = 40
	maxSessions   = 16
	maxAsks       = 3
	maxReviews    = 3
	maxTextRunes  = 120
)

// Sources is what the building is assembled from. The server's
// implementation (NewSources) reads the workspace catalog, the runs and
// autopilot stores and the agents hub; tests hand in a fake.
type Sources interface {
	// Projects is the workspace catalog (pm.List()).
	Projects() ([]project.ProjectInfo, error)
	// Root is a project's store root, the agents hub's key for it. It only
	// stats; ok is false for a project that does not resolve.
	Root(name string) (string, bool)
	// Runs are the runs of names not yet ended or made since since; viewer
	// "" means everyone's.
	Runs(ctx context.Context, names []string, viewer string, since string) ([]runs.Record, error)
	// OthersActive counts per project the active runs viewer may not see.
	OthersActive(ctx context.Context, names []string, viewer string) (map[string]int, error)
	// Sessions lists a project's agent sessions (by root).
	Sessions(root string) []agents.Session
	// PendingAsksIn and HeldIn list several projects' asks and held mail.
	PendingAsksIn(ctx context.Context, roots []string) ([]agents.Ask, error)
	HeldIn(ctx context.Context, roots []string, v agents.Viewer) ([]agents.Message, error)
	// WaitingHuman lists the live autopilots' tasks waiting for a person.
	WaitingHuman(ctx context.Context, names []string) ([]autopilot.WaitingHumanRow, error)
}

// agentKey is how the agents hub keys a project: its root, slashes trimmed.
func agentKey(root string) string {
	return strings.Trim(strings.TrimSpace(root), "/")
}

// excerpt cuts s to max runes, marking the cut.
func excerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func activeRun(status string) bool {
	switch status {
	case runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning:
		return true
	}
	return false
}

func buildingRun(r runs.Record) BuildingRun {
	out := BuildingRun{
		ID: r.ID, NodeID: r.NodeID, Title: r.NodeTitle, Agent: r.Agent, Model: r.Model,
		Kind: r.Kind, Status: r.Status, RunnerID: r.RunnerID, RunnerName: r.RunnerName,
		FailureKind: r.FailureKind, RetryOf: r.RetryOf, PipelineID: r.PipelineID, RetryAt: r.RetryAt,
		PRURL: r.PRURL, PRNumber: r.PRNumber, PRState: r.PRState, CIStatus: r.CIStatus,
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
	if r.Merge != nil {
		out.Merge = &BuildingRunMerge{Status: r.Merge.Status}
	}
	return out
}

// mayAnswer is whether actor may answer ask (agents.Viewer.MayAnswer): the
// person behind the asking session, or an administrator, except for another
// person's permission prompt.
func mayAnswer(actor identity.Actor, ask agents.Ask) bool {
	return agents.Viewer{ID: actor.ID, Admin: actor.IsAdmin()}.MayAnswer(ask)
}

// Assemble builds the building for actor: the projects readable(dir) allows,
// at most MaxProjects of them, busiest first. It reads Sources only.
func Assemble(ctx context.Context, src Sources, actor identity.Actor, readable func(dir string) bool, now time.Time) (BuildingResponse, error) {
	out := BuildingResponse{Projects: []BuildingProject{}}
	catalog, err := src.Projects()
	if err != nil {
		return out, err
	}
	var infos []project.ProjectInfo
	for _, p := range catalog {
		if !p.IsFolder && (readable == nil || readable(p.Path)) {
			infos = append(infos, p)
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	out.Total = len(infos)
	if len(infos) > maxQueried {
		infos = infos[:maxQueried]
		out.Truncated = true
	}

	// A project whose root does not resolve (moved, being deleted) is left
	// out: the agents hub keys it by root, so it could not be read anyway.
	byKey := map[string]string{} // agents key → name
	roots := map[string]string{} // name → root
	var names, rootList []string
	kept := make([]project.ProjectInfo, 0, len(infos))
	for _, p := range infos {
		root, ok := src.Root(p.Name)
		if !ok {
			continue
		}
		kept = append(kept, p)
		names = append(names, p.Name)
		roots[p.Name] = root
		rootList = append(rootList, root)
		byKey[agentKey(root)] = p.Name
	}
	infos = kept
	if len(infos) == 0 {
		return out, nil
	}

	viewer := ""
	if !actor.IsAdmin() {
		viewer = actor.ID
	}
	records, err := src.Runs(ctx, names, viewer, runs.FormatStamp(now.Add(-recentWindow)))
	if err != nil {
		return out, err
	}
	others := map[string]int{}
	if viewer != "" {
		if others, err = src.OthersActive(ctx, names, viewer); err != nil {
			return out, err
		}
	}
	reviews, err := src.WaitingHuman(ctx, names)
	if err != nil {
		return out, err
	}
	asks, err := src.PendingAsksIn(ctx, rootList)
	if err != nil {
		return out, err
	}
	held, err := src.HeldIn(ctx, rootList, agents.Viewer{ID: actor.ID, Admin: actor.IsAdmin()})
	if err != nil {
		return out, err
	}

	projects := make(map[string]*BuildingProject, len(infos))
	order := make([]*BuildingProject, 0, len(infos))
	for _, p := range infos {
		style := p.OfficeStyle
		if style == "" {
			style = engine.DefaultOfficeStyle(p.Layout)
		}
		bp := &BuildingProject{
			Name: p.Name, Label: p.Label, Layout: p.Layout,
			OfficeStyle: style, OfficeStyleSet: p.OfficeStyleSet,
			Runs: []BuildingRun{}, OthersActive: others[p.Name],
			Sessions: []BuildingSession{}, Asks: []BuildingAsk{}, Reviews: []BuildingReview{},
		}
		for _, s := range src.Sessions(roots[p.Name]) {
			if !s.Online {
				continue
			}
			if len(bp.Sessions) == maxSessions {
				break
			}
			bp.Sessions = append(bp.Sessions, BuildingSession{
				ID: s.ID, Name: s.Name, Agent: s.Agent, Model: s.Model, State: s.State,
				NodeID: s.NodeID, Doing: excerpt(s.Doing, maxTextRunes), RunID: s.RunID,
				LastSeen: s.LastSeen, Online: s.Online,
			})
		}
		projects[p.Name] = bp
		order = append(order, bp)
	}

	// Runs arrive newest first; each project keeps its newest active and
	// recent ones, and counts every active one for the busy order.
	active := make(map[string]int, len(infos))
	recent := make(map[string]int, len(infos))
	keptActive := make(map[string]int, len(infos))
	for _, r := range records {
		bp := projects[r.Project]
		if bp == nil {
			continue
		}
		if activeRun(r.Status) {
			active[r.Project]++
			if keptActive[r.Project] == maxActiveRuns {
				bp.RunsTruncated = true
				continue
			}
			keptActive[r.Project]++
		} else {
			if recent[r.Project] == maxRecentRuns {
				bp.RunsTruncated = true
				continue
			}
			recent[r.Project]++
		}
		bp.Runs = append(bp.Runs, buildingRun(r))
	}
	for _, a := range asks {
		bp := projects[byKey[agentKey(a.Project)]]
		if bp == nil || !mayAnswer(actor, a) {
			continue
		}
		bp.Waiting.Asks++
		if len(bp.Asks) < maxAsks {
			kind := a.Kind
			if kind == "" {
				kind = agents.AskKindApproval
			}
			bp.Asks = append(bp.Asks, BuildingAsk{
				ID: a.ID, SessionID: a.SessionID, SessionName: a.SessionName, Agent: a.Agent, Kind: kind,
				Question: excerpt(a.Question, maxTextRunes), Options: a.Options, Tool: a.Tool, RunID: a.RunID,
				NodeID: a.NodeID, CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt,
			})
		}
	}
	for _, m := range held {
		if bp := projects[byKey[agentKey(m.Project)]]; bp != nil {
			bp.Waiting.Held++
		}
	}
	for _, row := range reviews {
		bp := projects[row.Project]
		if bp == nil {
			continue
		}
		bp.Waiting.Reviews++
		if len(bp.Reviews) < maxReviews {
			bp.Reviews = append(bp.Reviews, BuildingReview{
				AutopilotID: row.AutopilotID, GoalNodeID: row.GoalNodeID, NodeID: row.NodeID, PipelineID: row.PipelineID,
			})
		}
	}
	for _, bp := range order {
		bp.Waiting.Total = bp.Waiting.Asks + bp.Waiting.Held + bp.Waiting.Reviews
	}

	// Busiest first: work under way (the viewer's and others'), then what
	// waits on the viewer.
	busy := func(bp *BuildingProject) int { return active[bp.Name] + bp.OthersActive + bp.Waiting.Total }
	sort.SliceStable(order, func(i, j int) bool {
		if bi, bj := busy(order[i]), busy(order[j]); bi != bj {
			return bi > bj
		}
		return order[i].Name < order[j].Name
	})
	if len(order) > MaxProjects {
		order = order[:MaxProjects]
		out.Truncated = true
	}
	for _, bp := range order {
		out.Projects = append(out.Projects, *bp)
	}
	return out, nil
}
