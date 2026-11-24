package runs

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// Global agents (agent workspace, phase 2; runnerwire/global.go): a run in
// one of the runner's repositories for no project and no node, started by
// the runner's owner. It is an ordinary run row with is_global set and an
// empty project and node, so the dispatcher, offers, cancel, terminal,
// events, changes, merges, workspace actions, budget and cost all apply as
// they are, while every project-scoped query (project = ?, project IN (…))
// never sees it. What needs a node or a board is off: pipelines, roles,
// fallbacks, usage-limit waits, handoffs, evidence and asks. Its realtime
// events go to its dispatcher and runner owner only (SetGlobalNotify).

// StartOutdated: the runner does not know global runs (no CapGlobalRuns);
// an older runner would start a board for no project.
const StartOutdated = "outdated"

// CodeGlobalRunsOff is StartError.Reason when the runner's owner turned
// global runs off on it (runner setting globalRuns).
const CodeGlobalRunsOff = "GLOBAL_RUNS_OFF"

// TakesGlobalRuns reports whether a runner's hello lets it be offered a
// global run now: it takes runs, understands global ones and its owner
// allows them.
func TakesGlobalRuns(info *runnerwire.Info) bool {
	return Headless(info) && slices.Contains(info.Capabilities, runnerwire.CapGlobalRuns) &&
		slices.Contains(info.Capabilities, runnerwire.CapGlobalRunsOn)
}

// SetGlobalNotify sets the hook told about global runs instead of the
// project hooks: evType is runnerwire.GlobalRunsChangedEvent,
// GlobalRunEventsEvent or GlobalRunNotifyEvent. Call it before serving.
func (d *Dispatcher) SetGlobalNotify(fn func(evType string, r Record)) { d.globalNotify = fn }

func (d *Dispatcher) tellGlobal(evType string, r Record) {
	if d.globalNotify != nil {
		d.globalNotify(evType, r)
	}
}

// GlobalStartRequest is one global run to start.
type GlobalStartRequest struct {
	Body runnerwire.CreateGlobalRunRequest
	// ActorID is who starts it; the runner must be theirs. ActorName is
	// recorded as the run's dispatcher.
	ActorID, ActorName string
	// SteppedUpAt is the dispatcher's step-up (RFC3339Nano), or "".
	SteppedUpAt string
}

// invalidField is a StartInvalid refusal about one request field.
func invalidField(field, english, chinese string, args ...any) error {
	said := locale.Errorf(english, chinese, args...)
	return &StartError{Code: StartInvalid, Field: field, Msg: said.Error(), said: said}
}

// globalValidated holds the shared fields resolved by validateGlobal for both
// single-repo and multi-repo global starts (§10.2).
type globalValidated struct {
	task        string
	title, rest string
	slug        string
	actorName   string
	runner      runnerwire.Runner
}

// validateGlobal checks the task, slug, base, onBusy/holderIds, runner
// ownership and global capabilities shared by StartGlobal and StartGlobalGroup.
func (d *Dispatcher) validateGlobal(ctx context.Context, task, slug, base, mode, onBusy string, holderIDs []string, runnerID, actorID, actorName string, group bool) (globalValidated, error) {
	task = strings.TrimSpace(task)
	switch {
	case task == "":
		return globalValidated{}, invalidField("task", "task is required", "請填寫任務")
	case len(task) > runnerwire.MaxGlobalTaskBytes:
		return globalValidated{}, invalidField("task", "task is over %d bytes", "任務超過 %d 位元組", runnerwire.MaxGlobalTaskBytes)
	case !utf8.ValidString(task) || strings.ContainsRune(task, 0):
		return globalValidated{}, invalidField("task", "task must be valid text", "任務須為有效的文字")
	}
	if slug != "" && !runnerwire.ValidSlug(slug) {
		return globalValidated{}, invalidField("slug", "slug must be lowercase letters, digits and single dashes, at most %d",
			"slug 只能用小寫英文字母、數字與單一連字號，最多 %d 個字元", runnerwire.MaxSlugBytes)
	}
	if !ValidBase(base) {
		return globalValidated{}, invalidField("base", "base is not a valid branch name", "base 不是有效的分支名稱")
	}
	if group {
		if mode == runnerwire.ModeInteractive {
			said := locale.Errorf("multi-repository groups run in the background; talk to any member after it finishes",
				"多資料夾群組會在背景執行；任一成員完成後即可接著對話")
			return globalValidated{}, &StartError{Code: StartInvalid, Reason: CodeGroupNeedsBackground, Field: "mode", Msg: said.Error(), said: said}
		}
		if !ValidMode(mode) {
			return globalValidated{}, invalidField("mode", `mode must be ""`, `mode 須為 ""`)
		}
	} else if !ValidMode(mode) {
		return globalValidated{}, invalidField("mode", `mode must be "" or %q`, `mode 須為 "" 或 %q`, runnerwire.ModeInteractive)
	}
	if !runnerwire.ValidOnBusy(onBusy, group) {
		return globalValidated{}, invalidField("onBusy", "onBusy is not a valid busy choice", "onBusy 不是有效的忙碌選項")
	}
	if !runnerwire.ValidHolderIDs(holderIDs) {
		return globalValidated{}, invalidField("holderIds", "holderIds is not valid", "holderIds 無效")
	}
	// The runner must be the caller's own, administrators included, as for
	// a node's run: it executes on the owner's machine with their logins.
	runner, err := d.reg.Get(ctx, runnerID)
	if err != nil || runner.OwnerID != actorID {
		return globalValidated{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}
	title, rest := runnerwire.GlobalTitle(task)
	if slug == "" {
		slug = runnerwire.SlugFrom(title)
	}
	name := actorName
	if name == "" {
		name = actorID
	}
	return globalValidated{
		task:      task,
		title:     title,
		rest:      rest,
		slug:      slug,
		actorName: name,
		runner:    runner,
	}, nil
}

// checkGlobalRunnerCaps checks that runner.Info takes global runs in mode.
func checkGlobalRunnerCaps(info *runnerwire.Info, mode string) error {
	if !Headless(info) {
		return refuse(StartPrecondition, "this runner does not take runs; update nodevas on it", "這個 runner 不接受執行；請更新它的 nodevas")
	}
	if !ModeSupported(info, mode) {
		return refuse(StartPrecondition, "this runner cannot run interactive sessions", "這個 runner 不能執行互動式工作階段")
	}
	if !slices.Contains(info.Capabilities, runnerwire.CapGlobalRuns) {
		return refuse(StartOutdated, "this runner cannot run global agents; update nodevas on it", "這個 runner 不能執行全域 agent；請更新它的 nodevas")
	}
	if !slices.Contains(info.Capabilities, runnerwire.CapGlobalRunsOn) {
		return stageRefusal(CodeGlobalRunsOff, locale.Errorf(
			"the runner's owner turned global agents off on it (nodevas runner set globalRuns on)",
			"runner 的擁有者在該機關閉了全域 agent（nodevas runner set globalRuns on）"))
	}
	return nil
}

// StartGlobal validates a global run request against the caller's own
// runner and its hello, records the run and queues it. Refusals are
// *StartError; anything else is a store failure. The task's text is never
// audited: it is the caller's, and may say anything.
func (d *Dispatcher) StartGlobal(ctx context.Context, req GlobalStartRequest) (Record, error) {
	body := req.Body
	v, err := d.validateGlobal(ctx, body.Task, body.Slug, body.Base, body.Mode, body.OnBusy, body.HolderIDs,
		body.RunnerID, req.ActorID, req.ActorName, false)
	if err != nil {
		return Record{}, err
	}
	runner := v.runner
	model, repo, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
		Agent: body.Agent, Model: body.Model, RepoID: body.RepoID,
	})
	if problem != nil {
		field := "repoId"
		if runner.Info == nil || !slices.ContainsFunc(runner.Info.Agents, func(a runnerwire.Agent) bool {
			return a.Kind == body.Agent && a.Installed
		}) {
			field = "agent"
		} else if _, err := ResolveAgentModel(runner.Info, runnerwire.AgentModel{Agent: body.Agent, Model: body.Model}); err != nil {
			field = "model"
		}
		return Record{}, &StartError{Code: StartInvalid, Field: field, Msg: problem.Error(), said: problem}
	}
	if err := checkGlobalRunnerCaps(runner.Info, body.Mode); err != nil {
		return Record{}, err
	}
	// The budget is the runner owner's: runs spend their subscription.
	budget, err := d.store.BudgetStatus(ctx, runner.OwnerID)
	if err != nil {
		return Record{}, err
	}
	if budget.Exceeded != "" {
		return Record{}, &StartError{Code: StartBudget, Msg: BudgetMessage(budget), said: BudgetSay(budget), Budget: budget}
	}
	if body.OnBusy == runnerwire.OnBusyContinue {
		return d.continueBusy(ctx, req)
	}

	repoKey := runnerwire.RepoKey(runner.ID, repo)
	in := New{
		Global: true, NodeTitle: v.title, BranchSlug: v.slug, NoPR: !body.OpenPR,
		RunnerID: runner.ID, RunnerName: runner.Name, OwnerID: runner.OwnerID,
		Agent: body.Agent, Model: model, RepoID: repo.ID, RepoName: repo.Name, Base: body.Base,
		JobPrompt: BuildPrompt(v.title, v.rest, "", withSimple("", body.Simple)),
		CreatedBy: req.ActorID, CreatedByName: v.actorName,
		SteppedUpAt: req.SteppedUpAt, Mode: body.Mode,
		// Only a person starts a global agent (its routes refuse agents),
		// and nothing starts one on its own.
		Origin:  runnerwire.OriginPerson,
		RepoKey: repoKey,
	}

	var record Record
	var outcome BusyOutcome
	err = d.store.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, "repo:"+repoKey); err != nil {
			return err
		}
		var err error
		outcome, err = d.applyBusy(ctx, tx, BusyRequest{
			Query:      BusyQuery{ActorID: req.ActorID, RepoKey: repoKey, Global: true},
			OnBusy:     body.OnBusy,
			HolderIDs:  body.HolderIDs,
			RunnerCaps: runner.Info.Capabilities,
		})
		if err != nil {
			return err
		}
		if !outcome.Proceed {
			return nil
		}
		in.AfterRun = outcome.AfterRun
		in.BusyChoice = outcome.Choice
		record, err = d.store.createWith(ctx, tx, in)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	d.afterBusy(ctx, identity.Actor{ID: req.ActorID, Name: v.actorName}, outcome)
	if err := d.store.UserFirst(ctx, record.ID, v.task); err != nil {
		return Record{}, err
	}
	d.record(AuditEntry{ActorID: req.ActorID, ActorName: v.actorName, Action: "run.global.start", Target: record.ID,
		Detail: map[string]any{
			"runner": runner.ID, "repo": repo.ID, "agent": record.Agent, "model": record.Model, "mode": record.Mode,
			"base": record.Base, "slug": v.slug, "noPR": record.NoPR, "taskBytes": len(v.task), "simple": body.Simple,
		}})
	return d.Submit(ctx, record), nil
}

// CancelAllGlobal cancels every global run the viewer may see that has not
// ended, as cancelling each one does, and returns them as they now stand.
func (d *Dispatcher) CancelAllGlobal(ctx context.Context, viewer, by string) ([]Record, error) {
	active, err := d.store.ActiveGlobal(ctx, viewer)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(active))
	for _, r := range active {
		next, err := d.Cancel(ctx, r.ID, "cancelled by "+by)
		if errors.Is(err, ErrTransition) {
			continue // it moved under us and could not be stopped; the list shows it
		}
		if err != nil {
			return out, err
		}
		out = append(out, next)
	}
	return out, nil
}

// GlobalFilter narrows ListGlobal.
type GlobalFilter struct {
	// Viewer keeps the global runs that person made or that ran on their
	// runners; required (an empty viewer lists nothing).
	Viewer string
	// Limit caps the ended runs (every unfinished one is listed); 0 is 50,
	// at most 200.
	Limit int
	// Before, when set, pages the ended runs: those made before it (the
	// createdAt of the oldest one an earlier page listed). A later page
	// lists no unfinished runs; the first one has them all.
	Before string
}

// Global run list sizes.
const (
	DefaultGlobalPage = 50
	MaxGlobalPage     = 200
)

var activeStatuses = []any{runnerwire.RunQueued, runnerwire.RunOffered, runnerwire.RunRunning}

// ListGlobal returns the viewer's global runs newest first: every one that
// has not ended (on the first page, without f.Before), and the newest
// f.Limit that have (before f.Before). more says older ended ones exist.
func (s *Store) ListGlobal(ctx context.Context, f GlobalFilter) ([]Record, bool, error) {
	if f.Viewer == "" {
		return []Record{}, false, nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultGlobalPage
	}
	limit = min(limit, MaxGlobalPage)
	active := []Record{}
	if f.Before == "" {
		var err error
		if active, err = s.ActiveGlobal(ctx, f.Viewer); err != nil {
			return nil, false, err
		}
	}
	query := `SELECT ` + runColumns + ` FROM runs WHERE is_global AND (created_by = ? OR owner_id = ?)
 AND status NOT IN (?, ?, ?)`
	args := append([]any{f.Viewer, f.Viewer}, activeStatuses...)
	if f.Before != "" {
		query += ` AND created_at < ?`
		args = append(args, f.Before)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	ended, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	more := len(ended) > limit
	if more {
		ended = ended[:limit]
	}
	out := append(active, ended...)
	slices.SortStableFunc(out, func(a, b Record) int {
		if c := strings.Compare(b.CreatedAt, a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	refs := make([]*Record, len(out))
	for i := range out {
		refs[i] = &out[i]
	}
	if err := s.attachMerges(ctx, refs); err != nil {
		return nil, false, err
	}
	if err := s.attachWorkspaceActions(ctx, refs); err != nil {
		return nil, false, err
	}
	return out, more, nil
}

// ActiveGlobal returns the viewer's global runs that have not ended, newest
// first.
func (s *Store) ActiveGlobal(ctx context.Context, viewer string) ([]Record, error) {
	if viewer == "" {
		return []Record{}, nil
	}
	args := append([]any{viewer, viewer}, activeStatuses...)
	return s.query(ctx, `SELECT `+runColumns+` FROM runs WHERE is_global AND (created_by = ? OR owner_id = ?)
 AND status IN (?, ?, ?) ORDER BY created_at DESC, id DESC`, args...)
}

// RunnerCounts is one runner's load for the global agents tab.
type RunnerCounts struct {
	// Busy is offered + running, every project's and global (what
	// MaxConcurrent is measured against); Queued its queued runs;
	// GlobalActive its global runs that have not ended.
	Busy, Queued, GlobalActive int
	// Today is its runs made today (UTC) and their use, as the owner's
	// budget counts them.
	Today runnerwire.RunnerToday
}

// RunnerLoads counts each runner's runs: what it holds and has queued now,
// and what it ran today (UTC). Runners with no runs are absent.
func (s *Store) RunnerLoads(ctx context.Context, runnerIDs []string) (map[string]RunnerCounts, error) {
	out := map[string]RunnerCounts{}
	if len(runnerIDs) == 0 {
		return out, nil
	}
	ids := make([]any, 0, len(runnerIDs))
	for _, id := range runnerIDs {
		ids = append(ids, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT runner_id, status, is_global, COUNT(*) FROM runs
 WHERE runner_id IN (`+placeholders(len(ids))+`) AND status IN (?, ?, ?)
 GROUP BY runner_id, status, is_global`, append(slices.Clone(ids), activeStatuses...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var runner, status string
		var global bool
		var n int
		if err := rows.Scan(&runner, &status, &global, &n); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[runner]
		if status == runnerwire.RunQueued {
			c.Queued += n
		} else {
			c.Busy += n
		}
		if global {
			c.GlobalActive += n
		}
		out[runner] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := s.clock().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	args := append(slices.Clone(ids), formatStamp(day), formatStamp(day.AddDate(0, 0, 1)), runnerwire.JobVerify)
	rows, err = s.db.QueryContext(ctx, `SELECT runner_id, usage, cost_usd, cost_settled FROM runs
 WHERE runner_id IN (`+placeholders(len(ids))+`) AND created_at >= ? AND created_at < ? AND kind <> ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[string]*runnerwire.BudgetStatus{}
	for rows.Next() {
		var runner string
		row := budgetRowAfter{runner: &runner, rows: rows}
		st := &runnerwire.BudgetStatus{}
		if err := addBudgetRow(&row, st); err != nil {
			return nil, err
		}
		total := used[runner]
		if total == nil {
			total = &runnerwire.BudgetStatus{}
			used[runner] = total
		}
		total.Used.Runs += st.Used.Runs
		total.Used.CostUSD += st.Used.CostUSD
		total.Used.Tokens += st.Used.Tokens
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for runner, total := range used {
		c := out[runner]
		c.Today = runnerwire.RunnerToday{Runs: total.Used.Runs, Tokens: total.Used.Tokens, CostUSD: total.Used.CostUSD}
		out[runner] = c
	}
	return out, nil
}

// budgetRowAfter scans a row whose budget columns (addBudgetRow's) follow a
// runner id.
type budgetRowAfter struct {
	runner *string
	rows   interface{ Scan(...any) error }
}

func (b *budgetRowAfter) Scan(dest ...any) error {
	return b.rows.Scan(append([]any{b.runner}, dest...)...)
}
