package runs

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// The agent workspace (runnerwire/workspace.go). The runner's owner merges a
// run's branch into its base on the runner's machine, or discards the run's
// worktree and branch. The hub records the request, hands it to the run's
// runner (now, or when it next connects within WorkspaceActionTTL) and
// records the answer; git runs on the runner alone.
//
// What became of the worktree is Run.Workspace, not the run's status: the
// status feeds analytics, pipelines, routing and the autopilot, and a run
// that succeeded and was then discarded still succeeded.

// WorkspaceActionTTL is runnerwire.WorkspaceActionTTLSeconds as a duration.
const WorkspaceActionTTL = time.Duration(runnerwire.WorkspaceActionTTLSeconds) * time.Second

// Refusals of a workspace action, as the API returns them.
const (
	MsgWorkspacePending  = "another action on this run is waiting for the runner"
	MsgWorkspaceBusy     = "the run's workspace is being merged or discarded, or already was"
	ReasonWorkspaceStale = "the runner did not answer in time"
)

func init() {
	preconditionChinese[MsgWorkspacePending] = "這次執行已有另一個動作在等 runner"
	preconditionChinese[MsgWorkspaceBusy] = "這次執行的工作區正在合併或捨棄，或已經處理過"
}

// WorkspaceBlocked is a workspace action the run's state does not allow
// now: Code is a gate code (runnerwire.WorkspaceGate*, or has_pr for a merge
// that belongs to its PR). It is an ErrPrecondition.
type WorkspaceBlocked struct{ Code string }

// workspaceGateText is each gate code for people, in both languages.
var workspaceGateText = map[string][2]string{
	runnerwire.WorkspaceGateNotOwner:    {"only the runner's owner can do this", "只有 runner 的擁有者可以這麼做"},
	runnerwire.WorkspaceGateRunning:     {"the run is still running", "這次執行還在進行中"},
	runnerwire.WorkspaceGateDiscarded:   {"the run was discarded", "這次執行已被捨棄"},
	runnerwire.WorkspaceGateMergedLocal: {"already merged locally", "已經在本機合併過"},
	runnerwire.WorkspaceGatePRMerged:    {"its pull request is already merged", "它的 PR 已經合併"},
	runnerwire.WorkspaceGatePending:     {MsgWorkspacePending, "這次執行已有另一個動作在等 runner"},
	runnerwire.WorkspaceGateKind:        {"only an implement or test run's branch can be merged", "只有實作或測試執行的分支可以合併"},
	runnerwire.WorkspaceGateNoCommit:    {"the run committed nothing", "這次執行沒有任何提交"},
	runnerwire.WorkspaceGateOutdated:    {"update nodevas on the runner first", "請先更新 runner 上的 nodevas"},
	runnerwire.WorkspaceGateActionsOff:  {"the runner's owner turned workspace actions off (workspaceActions)", "Runner 擁有者關閉了工作區動作（workspaceActions）"},
	runnerwire.WorkspaceGateOffline:     {"the runner is offline", "Runner 目前離線"},
	runnerwire.WorkspaceGateHeadMoved:   {"the branch changed since you looked; reload", "你看過之後分支有變更；請重新載入"},
	runnerwire.WorkspaceErrHasPR:        {"the run has an open pull request; merge it there", "這次執行有開啟中的 PR；請在 PR 合併"},
	runnerwire.WorkspaceGateInUse:       {"another run or its pipeline is still working on this branch or pull request", "還有其他執行或它的流程正在使用這個分支或 PR"},
	runnerwire.WorkspaceGateContinued:   {"this work continued in a later turn", "這份工作已在後續輪次繼續"},
}

func (e *WorkspaceBlocked) Error() string        { return e.message().Error() }
func (e *WorkspaceBlocked) Is(target error) bool { return target == ErrPrecondition }

// Unwrap is the message in both languages, for the reader of a response.
func (e *WorkspaceBlocked) Unwrap() error { return e.message() }

func (e *WorkspaceBlocked) message() *locale.Message {
	text, ok := workspaceGateText[e.Code]
	if !ok {
		text = [2]string{e.Code, e.Code}
	}
	return locale.Say(strings.ReplaceAll(text[0], "%", "%%"), strings.ReplaceAll(text[1], "%", "%%"))
}

func blocked(code string) error { return &WorkspaceBlocked{Code: code} }

// WorkspaceRunner is what the gates need to know beyond the run itself: its
// runner, and whether other work still needs its branch.
type WorkspaceRunner struct {
	Online bool
	// Caps is its last hello's capabilities.
	Caps []string
	// InUse: another unfinished run works on the run's PR or branch (a
	// revise or review run), or its pipeline is still running. A discard
	// would close that PR or delete that branch under it.
	InUse bool
}

// WorkspaceGates is what the run's state allows: how it would merge (mode
// "pr" or "local", or blocked) and whether it may be discarded. One
// function decides both the API's refusals and what the panel offers, so
// the two never disagree.
type WorkspaceGates struct {
	MergeMode      string
	MergeBlocked   string
	DiscardBlocked string
	// DiscardDone: already discarded; asking again changes nothing.
	DiscardDone bool
	// DiscardHubOnly: the run never had a worktree (never accepted), so
	// the hub discards it alone, without its runner.
	DiscardHubOnly bool
}

// HubOnlyDiscard reports whether discarding r needs no runner: the hub never
// heard of a branch, so as far as it knows there was never a worktree. An
// accept that came too late (the run was cancelled meanwhile) still records
// its branch (RecordLateBranch), so such a run goes to its runner.
func HubOnlyDiscard(r Record) bool { return r.Branch == "" }

// WorkspaceGate decides the merge and discard gates of r (first hit wins).
func WorkspaceGate(r Record, runner WorkspaceRunner) WorkspaceGates {
	var g WorkspaceGates
	both := func(code string) WorkspaceGates {
		g.MergeBlocked, g.DiscardBlocked = code, code
		return g
	}
	switch {
	case !runnerwire.RunFinal(r.Status):
		return both(runnerwire.WorkspaceGateRunning)
	case r.ContinuedBy != "":
		return both(runnerwire.WorkspaceGateContinued)
	case r.Workspace == runnerwire.RunWorkspaceDiscarded:
		g.DiscardDone = true
		g = both(runnerwire.WorkspaceGateDiscarded)
		if r.PRURL != "" && r.PRState == runnerwire.PROpen {
			// The discard left the PR open (a review run's, or one the
			// runner did not watch): it still merges there.
			g.MergeMode, g.MergeBlocked = runnerwire.WorkspaceMergeModePR, ""
		}
		return g
	case r.Workspace == runnerwire.RunWorkspaceMergedLocal:
		return both(runnerwire.WorkspaceGateMergedLocal)
	case r.PRState == runnerwire.PRMerged:
		return both(runnerwire.WorkspaceGatePRMerged)
	case workspacePending(r) || (r.Merge != nil && r.Merge.Status == runnerwire.MergePending):
		return both(runnerwire.WorkspaceGatePending)
	}
	// Merge: through the PR while one is open, else on the runner.
	switch {
	case r.PRURL != "" && r.PRState == runnerwire.PROpen:
		g.MergeMode = runnerwire.WorkspaceMergeModePR
	case r.Kind != runnerwire.JobImplement && r.Kind != runnerwire.JobTest:
		g.MergeBlocked = runnerwire.WorkspaceGateKind
	case r.Commit == "":
		g.MergeBlocked = runnerwire.WorkspaceGateNoCommit
	default:
		g.MergeBlocked = runnerGate(runner)
		if g.MergeBlocked == "" {
			g.MergeMode = runnerwire.WorkspaceMergeModeLocal
		}
	}
	if HubOnlyDiscard(r) {
		g.DiscardHubOnly = true
	} else {
		g.DiscardBlocked = runnerGate(runner)
	}
	// Other work still needs the branch: no discard, no local merge (which
	// deletes the branch). A PR keeps its own approval.
	if runner.InUse {
		g.DiscardBlocked, g.DiscardHubOnly = runnerwire.WorkspaceGateInUse, false
		if g.MergeMode == runnerwire.WorkspaceMergeModeLocal {
			g.MergeMode, g.MergeBlocked = "", runnerwire.WorkspaceGateInUse
		}
	}
	return g
}

// runnerGate is why the run's runner cannot take a workspace action now.
func runnerGate(runner WorkspaceRunner) string {
	switch {
	case !slices.Contains(runner.Caps, runnerwire.CapRunWorkspace):
		return runnerwire.WorkspaceGateOutdated
	case !slices.Contains(runner.Caps, runnerwire.CapWorkspaceActions):
		return runnerwire.WorkspaceGateActionsOff
	case !runner.Online:
		return runnerwire.WorkspaceGateOffline
	}
	return ""
}

func workspacePending(r Record) bool {
	return r.WorkspaceAction != nil && r.WorkspaceAction.Status == runnerwire.WorkspacePending
}

// CheckWorkspaceAction says why action (merge_local or discard) cannot be
// asked for r now, or returns nil. headSHA is, for a merge, the run commit
// the owner saw. A discard of an already discarded run is not refused: the
// caller answers it with the run as it is.
func CheckWorkspaceAction(r Record, runner WorkspaceRunner, action, headSHA string) error {
	g := WorkspaceGate(r, runner)
	switch action {
	case runnerwire.WorkspaceMergeLocal:
		if g.MergeMode == runnerwire.WorkspaceMergeModePR {
			return blocked(runnerwire.WorkspaceErrHasPR)
		}
		if g.MergeBlocked != "" {
			return blocked(g.MergeBlocked)
		}
		if !strings.EqualFold(headSHA, r.Commit) {
			return blocked(runnerwire.WorkspaceGateHeadMoved)
		}
		return nil
	case runnerwire.WorkspaceDiscard:
		if g.DiscardDone {
			return nil
		}
		if g.DiscardBlocked != "" {
			return blocked(g.DiscardBlocked)
		}
		return nil
	case runnerwire.WorkspaceOpenPR:
		// open_pr is only for a succeeded contest entry with a commit, no PR
		// yet, a live workspace and a runner with CapCoord (§C.4.4, §16.3).
		switch {
		case !runnerwire.RunFinal(r.Status):
			return blocked(runnerwire.WorkspaceGateRunning)
		case r.ContinuedBy != "":
			return blocked(runnerwire.WorkspaceGateContinued)
		case r.Status != runnerwire.RunSucceeded || r.ContestID == "" || r.ContestLabel == "" || r.ContestLabel == "J":
			return blocked(runnerwire.WorkspaceGateKind)
		case r.Workspace == runnerwire.RunWorkspaceDiscarded:
			return blocked(runnerwire.WorkspaceGateDiscarded)
		case r.Workspace == runnerwire.RunWorkspaceMergedLocal:
			return blocked(runnerwire.WorkspaceGateMergedLocal)
		case r.PRState == runnerwire.PRMerged:
			return blocked(runnerwire.WorkspaceGatePRMerged)
		case r.PRURL != "":
			return blocked(runnerwire.WorkspaceErrHasPR)
		case workspacePending(r) || (r.Merge != nil && r.Merge.Status == runnerwire.MergePending):
			return blocked(runnerwire.WorkspaceGatePending)
		case r.Commit == "":
			return blocked(runnerwire.WorkspaceGateNoCommit)
		case runner.InUse:
			return blocked(runnerwire.WorkspaceGateInUse)
		case !slices.Contains(runner.Caps, runnerwire.CapCoord):
			return blocked(runnerwire.WorkspaceGateOutdated)
		}
		if code := runnerGate(runner); code != "" {
			return blocked(code)
		}
		return nil
	}
	return locale.Errorf("%w: unknown workspace action", "%w：不明的工作區動作", ErrInvalid)
}

// RecordLateBranch keeps the branch of an accept that came after the run
// had ended on the hub (cancelled while offered): the runner made a
// worktree and branch for it all the same. A discard the hub did alone
// meanwhile (it thought there was no worktree) no longer stands, so the run
// can be discarded on its runner.
func (s *Store) RecordLateBranch(ctx context.Context, runID, runnerID, branch string) (Record, error) {
	if _, err := s.db.ExecContext(ctx, `
UPDATE runs SET branch = ?, workspace = CASE WHEN workspace = ? THEN '' ELSE workspace END
WHERE id = ? AND runner_id = ? AND branch = ''`,
		Truncate(branch, maxShortField), runnerwire.RunWorkspaceDiscarded, runID, runnerID); err != nil {
		return Record{}, err
	}
	return s.Get(ctx, runID)
}

// WorkspaceInUse reports whether other work still needs r's branch, PR or
// work_root: an unfinished run on the same PR, branch or work_root (a revise,
// review or follow-up run, a later stage), or r's pipeline still running.
func (s *Store) WorkspaceInUse(ctx context.Context, r Record) (bool, error) {
	pr := r.PRURL
	if pr == "" {
		pr = r.JobPRURL
	}
	branch := r.Branch
	if branch == "" {
		branch = r.JobBranch
	}
	workRoot := r.WorkRoot
	if workRoot == "" {
		workRoot = r.ID
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM runs
   WHERE id <> ? AND status IN (?, ?, ?)
     AND ((? <> '' AND (job_pr_url = ? OR pr_url = ?))
       OR (? <> '' AND (job_branch = ? OR branch = ?))
       OR (? <> '' AND (id = ? OR work_root = ?))))
+ (SELECT COUNT(*) FROM run_pipelines WHERE ? <> '' AND id = ? AND status = ?)`,
		r.ID, runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning,
		pr, pr, pr, branch, branch, branch, workRoot, workRoot, workRoot,
		r.PipelineID, r.PipelineID, runnerwire.PipelineRunning).Scan(&n)
	return n > 0, err
}

const workspaceColumns = `id, run_id, action, head_sha, base, status, code, reason, note, merge_commit,
       requested_by, requested_by_name, created_at, finished_at`

func scanWorkspaceAction(row interface{ Scan(...any) error }) (runnerwire.RunWorkspaceAction, error) {
	var a runnerwire.RunWorkspaceAction
	var byID, byName string
	if err := row.Scan(&a.ID, &a.RunID, &a.Action, &a.HeadSHA, &a.Base, &a.Status, &a.Code, &a.Reason, &a.Note,
		&a.MergeCommit, &byID, &byName, &a.CreatedAt, &a.FinishedAt); err != nil {
		return runnerwire.RunWorkspaceAction{}, err
	}
	// The API shows who asked by name; the ID stays in the table.
	a.RequestedBy = byName
	if a.RequestedBy == "" {
		a.RequestedBy = byID
	}
	return a, nil
}

// staleWorkspace reports whether a pending action has outlived its TTL.
func (s *Store) staleWorkspace(a runnerwire.RunWorkspaceAction) bool {
	return a.Status == runnerwire.WorkspacePending && a.CreatedAt < formatStamp(s.clock().Add(-WorkspaceActionTTL))
}

// attachWorkspaceActions sets each run's latest workspace action. A pending
// one past its TTL is expired on the way, as attachMerges does.
func (s *Store) attachWorkspaceActions(ctx context.Context, records []*Record) error {
	if len(records) == 0 {
		return nil
	}
	byRun := make(map[string]*Record, len(records))
	args := make([]any, 0, len(records))
	for _, r := range records {
		byRun[r.ID] = r
		args = append(args, r.ID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+workspaceColumns+` FROM run_workspace_actions WHERE run_id IN (`+
		placeholders(len(args))+`) ORDER BY created_at, id`, args...)
	if err != nil {
		return err
	}
	latest := map[string]runnerwire.RunWorkspaceAction{}
	for rows.Next() {
		a, err := scanWorkspaceAction(rows)
		if err != nil {
			rows.Close()
			return err
		}
		latest[a.RunID] = a
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for runID, a := range latest {
		if s.staleWorkspace(a) {
			if expired, err := s.expireWorkspaceAction(ctx, a); err == nil {
				a = expired
			}
		}
		action := a
		byRun[runID].WorkspaceAction = &action
	}
	return nil
}

// expireWorkspaceAction marks one pending action expired. If it was answered
// meanwhile, the stored row is returned as it now stands.
func (s *Store) expireWorkspaceAction(ctx context.Context, a runnerwire.RunWorkspaceAction) (runnerwire.RunWorkspaceAction, error) {
	if _, err := s.db.ExecContext(ctx, `
UPDATE run_workspace_actions SET status = ?, code = ?, reason = ?, finished_at = ? WHERE id = ? AND status = ?`,
		runnerwire.WorkspaceExpired, runnerwire.WorkspaceExpired, ReasonWorkspaceStale, s.stamp(), a.ID,
		runnerwire.WorkspacePending); err != nil {
		return a, err
	}
	return s.WorkspaceAction(ctx, a.ID)
}

// WorkspaceAction returns one workspace action.
func (s *Store) WorkspaceAction(ctx context.Context, id string) (runnerwire.RunWorkspaceAction, error) {
	a, err := scanWorkspaceAction(s.db.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM run_workspace_actions WHERE id = ?`, id))
	if err != nil {
		return runnerwire.RunWorkspaceAction{}, ErrNotFound
	}
	return a, nil
}

// ExpireWorkspaceActions retires every pending action older than its TTL and
// returns them as they now stand.
func (s *Store) ExpireWorkspaceActions(ctx context.Context) ([]runnerwire.RunWorkspaceAction, error) {
	cutoff := formatStamp(s.clock().Add(-WorkspaceActionTTL))
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+workspaceColumns+` FROM run_workspace_actions WHERE status = ? AND created_at < ? ORDER BY created_at`,
		runnerwire.WorkspacePending, cutoff)
	if err != nil {
		return nil, err
	}
	var stale []runnerwire.RunWorkspaceAction
	for rows.Next() {
		a, err := scanWorkspaceAction(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		stale = append(stale, a)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []runnerwire.RunWorkspaceAction
	for _, a := range stale {
		expired, err := s.expireWorkspaceAction(ctx, a)
		if err != nil {
			return out, err
		}
		if expired.Status == runnerwire.WorkspaceExpired {
			out = append(out, expired)
		}
	}
	return out, nil
}

// CreateWorkspaceAction records the owner's request for a workspace action
// on a run. At most one waits per run; a second is refused with
// MsgWorkspacePending.
func (s *Store) CreateWorkspaceAction(ctx context.Context, runID, action, headSHA, base, byID, byName string) (runnerwire.RunWorkspaceAction, error) {
	if runID == "" || byID == "" || (action != runnerwire.WorkspaceMergeLocal && action != runnerwire.WorkspaceDiscard && action != runnerwire.WorkspaceOpenPR) {
		return runnerwire.RunWorkspaceAction{}, locale.Errorf("%w: missing field", "%w：缺少欄位", ErrInvalid)
	}
	// A stale request must not block a fresh one.
	if _, err := s.ExpireWorkspaceActions(ctx); err != nil {
		return runnerwire.RunWorkspaceAction{}, err
	}
	id, err := newID()
	if err != nil {
		return runnerwire.RunWorkspaceAction{}, err
	}
	// The partial unique index on pending rows makes the check and the
	// insert one step.
	result, err := s.db.ExecContext(ctx, `
INSERT INTO run_workspace_actions (id, run_id, action, head_sha, base, status, requested_by, requested_by_name, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`,
		id, runID, action, Truncate(headSHA, 64), Truncate(base, maxShortField), runnerwire.WorkspacePending,
		byID, Truncate(byName, maxShortField), s.stamp())
	if err != nil {
		return runnerwire.RunWorkspaceAction{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return runnerwire.RunWorkspaceAction{}, err
	} else if n == 0 {
		return runnerwire.RunWorkspaceAction{}, Precondition(MsgWorkspacePending)
	}
	return s.WorkspaceAction(ctx, id)
}

// PendingWorkspaceActions returns the jobs for a runner's pending workspace
// actions, oldest first. AccountID is the run's owner: only the owner may
// ask, so whoever asked is the owner.
func (s *Store) PendingWorkspaceActions(ctx context.Context, runnerID string) ([]runnerwire.WorkspaceActionJob, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.run_id, a.action, a.head_sha, a.base, r.owner_id, r.repo_id, r.node_id, r.branch_slug, r.kind, r.branch, r.work_root
FROM run_workspace_actions a JOIN runs r ON r.id = a.run_id
WHERE r.runner_id = ? AND a.status = ? ORDER BY a.created_at, a.id`, runnerID, runnerwire.WorkspacePending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runnerwire.WorkspaceActionJob
	for rows.Next() {
		var job runnerwire.WorkspaceActionJob
		if err := rows.Scan(&job.ID, &job.RunID, &job.Action, &job.HeadSHA, &job.Base, &job.AccountID,
			&job.RepoID, &job.NodeID, &job.Slug, &job.Kind, &job.Branch, &job.WorkRoot); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// WorkspaceJob is the job a runner is sent for action a of run r.
func WorkspaceJob(r Record, a runnerwire.RunWorkspaceAction) runnerwire.WorkspaceActionJob {
	return runnerwire.WorkspaceActionJob{ID: a.ID, RunID: r.ID, AccountID: r.OwnerID, Action: a.Action, RepoID: r.RepoID,
		NodeID: r.NodeID, Slug: r.BranchSlug, Kind: r.Kind, Branch: r.Branch, Base: a.Base, HeadSHA: a.HeadSHA, WorkRoot: r.WorkRoot}
}

// cleanText is runner text as stored: valid, NUL-free, capped.
func cleanText(s string, max int) string {
	return strings.ReplaceAll(Truncate(s, max), "\x00", "")
}

// FinishWorkspaceAction records a runner's answer: done when outcome.Code is
// "", failed otherwise. The action must be for one of the runner's runs and
// still waiting, or expired: a runner that got it before it expired and
// acted anyway has acted, and the record says so. Done sets the run's
// workspace.
func (s *Store) FinishWorkspaceAction(ctx context.Context, runnerID string, outcome runnerwire.WorkspaceOutcome) (Record, runnerwire.RunWorkspaceAction, error) {
	a, err := s.WorkspaceAction(ctx, outcome.ID)
	if err != nil || a.RunID != outcome.RunID {
		return Record{}, runnerwire.RunWorkspaceAction{}, ErrNotFound
	}
	r, err := s.Get(ctx, a.RunID)
	if err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	if r.RunnerID != runnerID {
		return Record{}, runnerwire.RunWorkspaceAction{}, ErrNotFound
	}
	status, code := runnerwire.WorkspaceDone, ""
	if outcome.Code != "" {
		status, code = runnerwire.WorkspaceFailed, Truncate(outcome.Code, 64)
	}
	mergeCommit := ""
	if a.Action == runnerwire.WorkspaceMergeLocal && status == runnerwire.WorkspaceDone {
		mergeCommit = Truncate(outcome.MergeCommit, 64)
	}
	prURL := ""
	if a.Action == runnerwire.WorkspaceOpenPR && status == runnerwire.WorkspaceDone && outcome.PRURL != "" {
		if err := ValidatePR(runnerwire.PRInfo{URL: outcome.PRURL, State: runnerwire.PROpen}); err != nil {
			return Record{}, runnerwire.RunWorkspaceAction{}, err
		}
		prURL = outcome.PRURL
	}
	reason := cleanText(outcome.Error, runnerwire.MaxWorkspaceReason)
	note := cleanText(outcome.Note, runnerwire.MaxWorkspaceReason)
	now := s.stamp()
	moved := false
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE run_workspace_actions SET status = ?, code = ?, reason = ?, note = ?, merge_commit = ?, finished_at = ?
WHERE id = ? AND status IN (?, ?)`,
			status, code, reason, note, mergeCommit, now, a.ID, runnerwire.WorkspacePending, runnerwire.WorkspaceExpired)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		moved = true
		if status != runnerwire.WorkspaceDone {
			return nil
		}
		if a.Action == runnerwire.WorkspaceOpenPR {
			if prURL != "" {
				_, err = tx.ExecContext(ctx,
					`UPDATE runs SET pr_url = ?, pr_state = CASE WHEN pr_state = '' THEN ? ELSE pr_state END WHERE id = ?`,
					prURL, runnerwire.PROpen, a.RunID)
				return err
			}
			return nil
		}
		workspace := runnerwire.RunWorkspaceDiscarded
		if a.Action == runnerwire.WorkspaceMergeLocal {
			workspace = runnerwire.RunWorkspaceMergedLocal
		}
		_, err = tx.ExecContext(ctx, `UPDATE runs SET workspace = ?, workspace_commit = ? WHERE id = ?`,
			workspace, mergeCommit, a.RunID)
		return err
	})
	if err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	if !moved {
		return r, a, locale.Errorf("%w: workspace action %s is already %s", "%w：工作區動作 %s 已經是 %s", ErrTransition, a.ID, a.Status)
	}
	if r, err = s.Get(ctx, a.RunID); err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	a, err = s.WorkspaceAction(ctx, a.ID)
	return r, a, err
}

// MarkDiscarded discards a run that never had a worktree (never accepted):
// a done discard and the run's workspace, in one transaction, with no
// runner involved. A run already discarded is returned as it is.
func (s *Store) MarkDiscarded(ctx context.Context, runID, byID, byName string) (Record, runnerwire.RunWorkspaceAction, error) {
	r, err := s.Get(ctx, runID)
	if err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	if !HubOnlyDiscard(r) {
		return r, runnerwire.RunWorkspaceAction{}, locale.Errorf("%w: the run has a worktree on its runner", "%w：這次執行在 runner 上有工作樹", ErrInvalid)
	}
	if r.Workspace == runnerwire.RunWorkspaceDiscarded {
		return r, runnerwire.RunWorkspaceAction{}, nil
	}
	if !runnerwire.RunFinal(r.Status) {
		return r, runnerwire.RunWorkspaceAction{}, blocked(runnerwire.WorkspaceGateRunning)
	}
	id, err := newID()
	if err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	now := s.stamp()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO run_workspace_actions (id, run_id, action, base, status, note, requested_by, requested_by_name, created_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, runID, runnerwire.WorkspaceDiscard, Truncate(r.Base, maxShortField), runnerwire.WorkspaceDone,
			"the run never had a worktree", byID, Truncate(byName, maxShortField), now, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE runs SET workspace = ? WHERE id = ? AND workspace = ''`,
			runnerwire.RunWorkspaceDiscarded, runID)
		return err
	})
	if err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	if r, err = s.Get(ctx, runID); err != nil {
		return Record{}, runnerwire.RunWorkspaceAction{}, err
	}
	a, err := s.WorkspaceAction(ctx, id)
	return r, a, err
}

// BlockedCode is the gate code err carries (a WorkspaceBlocked or a StartError
// with Reason), or "".
func BlockedCode(err error) string {
	var b *WorkspaceBlocked
	if errors.As(err, &b) {
		return b.Code
	}
	var s *StartError
	if errors.As(err, &s) && s.Reason != "" {
		return s.Reason
	}
	return ""
}
