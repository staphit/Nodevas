package analytics

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

// stampLayout is how the hub stores times (sortable UTC text).
const stampLayout = "2006-01-02T15:04:05.000000Z07:00"

// MaxDays caps a report's window.
const MaxDays = 365

func formatStamp(t time.Time) string { return t.UTC().Format(stampLayout) }

func parseStamp(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// window is a query's days, its first instant and that instant as stored.
type window struct {
	days  int
	since time.Time
	now   time.Time
}

func windowOf(days int, now time.Time) window {
	if days <= 0 {
		days = DefaultDays
	}
	days = min(days, MaxDays)
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return window{days: days, since: today.AddDate(0, 0, -(days - 1)), now: now}
}

func (w window) sinceStamp() string { return formatStamp(w.since) }
func (w window) sinceOut() string   { return w.since.Format(time.RFC3339) }

// run is one runs row as analytics reads it.
type run struct {
	ID, Project, Node, Title, NodeType, Pipeline, RetryOf string
	Kind, Role, Agent, Model, Status, FailureKind         string
	Owner, CreatedBy, Created, Started, Finished          string
	Acceptance                                            int
	Cost                                                  float64
	Estimated                                             bool
}

// roleKey is the run's role for metrics (a legacy run's from its kind).
func (r *run) roleKey() string { return usage.RoleOf(r.Role, r.Kind) }

// modelKey is the run's model, "default" for none.
func (r *run) modelKey() string {
	if strings.TrimSpace(r.Model) == "" {
		return "default"
	}
	return r.Model
}

// track is the run's track: story for scene and event nodes.
func (r *run) track() string {
	if r.NodeType == "scene" || r.NodeType == "event" {
		return TrackStory
	}
	return TrackCode
}

func (r *run) isVerify() bool { return r.Kind == runnerwire.JobVerify }

// ended: the run reached an end it ran to (a declined offer never ran).
func (r *run) ended() bool {
	switch r.Status {
	case runnerwire.RunSucceeded, runnerwire.RunFailed, runnerwire.RunCancelled:
		return true
	}
	return false
}

func (r *run) active() bool {
	switch r.Status {
	case runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning:
		return true
	}
	return false
}

func (r *run) seconds() (float64, bool) {
	start, ok1 := parseStamp(r.Started)
	end, ok2 := parseStamp(r.Finished)
	if !ok1 || !ok2 || end.Before(start) {
		return 0, false
	}
	return end.Sub(start).Seconds(), true
}

// filter is the runs a report reads.
type filter struct {
	owner, project, viewer string
	since                  string
}

func (f filter) where(alias string) (string, []any) {
	p := alias
	if p != "" {
		p += "."
	}
	conds := []string{p + "created_at >= ?"}
	args := []any{f.since}
	if f.owner != "" {
		conds = append(conds, p+"owner_id = ?")
		args = append(args, f.owner)
	}
	if f.project != "" {
		conds = append(conds, p+"project = ?")
		args = append(args, f.project)
	}
	if f.viewer != "" {
		conds = append(conds, "("+p+"created_by = ? OR "+p+"owner_id = ?)")
		args = append(args, f.viewer, f.viewer)
	}
	return strings.Join(conds, " AND "), args
}

// history is a window's runs, pipelines, TDD evidence and verdicts.
type history struct {
	runs      []*run
	byID      map[string]*run
	pipelines map[string]string // id → status
	// evidence: verify_red/verify_green passes by unit hint (pipeline or run).
	evidence []evidenceRow
	verdicts map[string]verdictRow // run id → verdict
}

type evidenceRow struct {
	runID, pipeline, kind, at string
}

type verdictRow struct {
	runID, project, node, pipeline, role, outcome, severity, created string
	round                                                            int
	contradicted                                                     bool
}

func loadHistory(ctx context.Context, database *db.DB, f filter) (*history, error) {
	h := &history{byID: map[string]*run{}, pipelines: map[string]string{}, verdicts: map[string]verdictRow{}}
	where, args := f.where("")
	rows, err := database.QueryContext(ctx, `
SELECT id, project, node_id, node_title, node_type, pipeline_id, retry_of, kind, role, agent, model,
       status, failure_kind, owner_id, created_by, created_at, started_at, finished_at,
       acceptance_count, cost_usd, cost_estimated
  FROM runs WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		r := &run{}
		var acceptance int64
		if err := rows.Scan(&r.ID, &r.Project, &r.Node, &r.Title, &r.NodeType, &r.Pipeline, &r.RetryOf, &r.Kind, &r.Role,
			&r.Agent, &r.Model, &r.Status, &r.FailureKind, &r.Owner, &r.CreatedBy, &r.Created, &r.Started, &r.Finished,
			&acceptance, &r.Cost, &r.Estimated); err != nil {
			rows.Close()
			return nil, err
		}
		r.Acceptance = int(acceptance)
		if math.IsNaN(r.Cost) || math.IsInf(r.Cost, 0) || r.Cost < 0 {
			r.Cost = 0
		}
		h.runs = append(h.runs, r)
		h.byID[r.ID] = r
		if r.Pipeline != "" {
			h.pipelines[r.Pipeline] = ""
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := h.loadPipelines(ctx, database); err != nil {
		return nil, err
	}

	where, args = f.where("r")
	rows, err = database.QueryContext(ctx, `
SELECT e.run_id, e.pipeline_id, e.kind, e.at FROM run_evidence e JOIN runs r ON r.id = e.run_id
 WHERE `+where+` AND e.kind IN (?, ?) AND e.outcome = 'pass' ORDER BY e.at, e.id`,
		append(args, runnerwire.PhaseVerifyRed, runnerwire.PhaseVerifyGreen)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e evidenceRow
		if err := rows.Scan(&e.runID, &e.pipeline, &e.kind, &e.at); err != nil {
			rows.Close()
			return nil, err
		}
		h.evidence = append(h.evidence, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	where, args = f.where("r")
	rows, err = database.QueryContext(ctx, `
SELECT v.run_id, v.project, v.node_id, v.pipeline_id, v.role, v.round, v.outcome, v.severity, v.created_at
  FROM run_verdicts v JOIN runs r ON r.id = v.run_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var v verdictRow
		var round int64
		if err := rows.Scan(&v.runID, &v.project, &v.node, &v.pipeline, &v.role, &round, &v.outcome, &v.severity, &v.created); err != nil {
			rows.Close()
			return nil, err
		}
		v.round = int(round)
		h.verdicts[v.runID] = v
		ids = append(ids, v.runID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	contradicted, err := Contradicted(ctx, database, ids)
	if err != nil {
		return nil, err
	}
	for id, c := range contradicted {
		if v, ok := h.verdicts[id]; ok {
			v.contradicted = c
			h.verdicts[id] = v
		}
	}
	return h, nil
}

func (h *history) loadPipelines(ctx context.Context, database *db.DB) error {
	if len(h.pipelines) == 0 {
		return nil
	}
	ids := make([]string, 0, len(h.pipelines))
	for id := range h.pipelines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := database.QueryContext(ctx, `SELECT id, status FROM run_pipelines WHERE id IN (`+marks(len(chunk))+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err != nil {
				rows.Close()
				return err
			}
			h.pipelines[id] = status
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func marks(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}

// roundRate and round6 are how rates and costs are reported.
func roundRate(v float64) float64 { return math.Round(v*1e4) / 1e4 }
func round6(v float64) float64    { return math.Round(v*1e6) / 1e6 }

func rate(num, den float64) *float64 {
	if den == 0 {
		return nil
	}
	v := roundRate(num / den)
	return &v
}

func costRate(num, den float64) *float64 {
	if den == 0 {
		return nil
	}
	v := round6(num / den)
	return &v
}
