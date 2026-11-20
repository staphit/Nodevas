package runs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// Follow-up refusal reason codes (StartError.Reason).
const (
	CodeContinued      = "CONTINUED"
	CodeInUse          = "IN_USE"
	CodeWorkGone       = "WORK_GONE"
	CodePREnded        = "PR_ENDED"
	CodeMergePending   = "MERGE_PENDING"
	CodeRunLive        = "RUN_LIVE"
	CodeRunnerOutdated = "RUNNER_OUTDATED"
	CodeNodeRun        = "NODE_RUN"
	CodeTurnRunning    = "TURN_RUNNING"

	// MaxThreadRuns caps GET /api/global-runs/:id/thread.
	MaxThreadRuns = 50
)

var (
	// ErrEndForbidden is returned when the caller is outside mayType (or view-only and not the runner owner).
	ErrEndForbidden = locale.Errorf("only the person who started this run or the runner's owner can end it",
		"只有啟動這次執行的人或 runner 的擁有者可以結束它")
	// ErrEndViewOnly is returned when a non-owner tries to End a view-only run.
	ErrEndViewOnly = locale.Errorf("%w: this run is view-only; only the runner's owner can end it",
		"%w：這次執行為唯讀；只有 runner 的擁有者可以結束它", ErrEndForbidden)
	// ErrEndOutdated is returned when the runner lacks CapContinue.
	ErrEndOutdated = locale.Errorf("this runner cannot end interactive runs and keep their work; update nodevas on it",
		"這個 runner 還不支援結束互動式執行並保留成果；請更新它的 nodevas")
)

// LatestID returns the latest non-withdrawn turn ID carried by a CONTINUED refusal.
func (e *StartError) LatestID() string {
	if e != nil && e.Reason == CodeContinued {
		return e.What
	}
	return ""
}

func continuedRefusal(latestID string) error {
	said := locale.Errorf("this turn was already continued; showing the latest turn", "這一輪已有後續，改顯示最新一輪")
	return &StartError{
		Code:   StartPrecondition,
		Reason: CodeContinued,
		What:   latestID,
		Msg:    said.Error(),
		said:   said,
	}
}

// CanSee reports whether actor may see a run: its dispatcher, its runner's
// owner, or an administrator.
func CanSee(actor identity.Actor, r Record) bool {
	return actor.ID != "" && (actor.IsAdmin() || actor.ID == r.CreatedBy || actor.ID == r.OwnerID)
}

// MayType reports whether actor may type into an interactive run's terminal:
// the person who dispatched it or the runner's owner, never an agent.
func MayType(actor identity.Actor, r Record) bool {
	return !actor.IsAgent() && actor.ID != "" && (actor.ID == r.CreatedBy || actor.ID == r.OwnerID)
}

// MayEnd reports whether actor may gracefully End an interactive run and keep
// its work: the MayType set, narrowed to the runner's owner on a view-only run.
func MayEnd(actor identity.Actor, r Record) bool {
	if !MayType(actor, r) {
		return false
	}
	if r.ViewOnly {
		return actor.ID == r.OwnerID
	}
	return true
}

// FollowUpRequest is one follow-up turn to start on an existing global run.
type FollowUpRequest struct {
	PriorID            string
	Body               runnerwire.FollowUpRequest
	Actor              identity.Actor
	ActorID, ActorName string
	IsAgent            bool
	SteppedUpAt        string
	Simple             bool
	// QueuedBehind skips only the RUN_LIVE check when §C's "continue" queues a
	// follow-up behind a running interactive global run (§6.3, §C.2.3).
	QueuedBehind bool
}

// FollowUp validates and records a follow-up turn continuing a global run (§6.3).
// Refusals are returned as *StartError. The message text is written as seq-0
// user event and is never audited.
func (d *Dispatcher) FollowUp(ctx context.Context, req FollowUpRequest) (Record, error) {
	actorID := req.ActorID
	if actorID == "" {
		actorID = req.Actor.ID
	}
	actorName := req.ActorName
	if actorName == "" {
		actorName = req.Actor.Name
	}
	isAgent := req.IsAgent || req.Actor.IsAgent()

	// 1. The actor is a person (not an agent) and can see the prior run.
	if isAgent {
		return Record{}, stageRefusal("PERMISSION_DENIED",
			locale.Errorf("agents cannot use global agents", "agent 不能使用全域 agent"))
	}
	prior, err := d.store.Get(ctx, req.PriorID)
	if errors.Is(err, ErrNotFound) || req.PriorID == "" {
		return Record{}, &StartError{Code: StartNotFound, What: "run", Msg: "run not found", said: ErrNotFound}
	}
	if err != nil {
		return Record{}, err
	}

	// 2. The prior run is global (v1 scope).
	if !prior.Global {
		return Record{}, stageRefusal(CodeNodeRun,
			locale.Errorf("follow-ups are only for global runs", "只有全域執行可以接續"))
	}

	// 3. actor == prior.CreatedBy == runner.OwnerID.
	if actorID == "" || actorID != prior.CreatedBy || actorID != prior.OwnerID {
		return Record{}, &StartError{Code: StartNotFound, What: "run", Msg: "run not found", said: ErrNotFound}
	}
	runner, err := d.reg.Get(ctx, prior.RunnerID)
	if err != nil || runner.OwnerID != actorID {
		return Record{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}

	// 4. Prior state: final starts normally; unfinished background is a queued reply;
	// unfinished interactive is refused with RUN_LIVE unless QueuedBehind is set.
	if !runnerwire.RunFinal(prior.Status) && prior.Mode == runnerwire.ModeInteractive && !req.QueuedBehind {
		return Record{}, stageRefusal(CodeRunLive,
			locale.Errorf("this run is interactive and still running; type into its screen instead",
				"這次互動式執行還在進行中；請直接在它的畫面輸入"))
	}

	// 5. The prior is the latest turn: no non-withdrawn run has continue_of = prior,
	// and a withdrawn follow-up is never a prior itself.
	workRoot := prior.WorkRoot
	if workRoot == "" {
		workRoot = prior.ID
	}
	if prior.ContinueOf != "" && IsWithdrawn(prior) {
		latestID, err := d.store.LatestTurnID(ctx, workRoot)
		if err != nil {
			return Record{}, err
		}
		return Record{}, continuedRefusal(latestID)
	}
	if succ, ok, err := d.store.Successor(ctx, prior.ID); err != nil {
		return Record{}, err
	} else if ok {
		latestID, err := d.store.LatestTurnID(ctx, workRoot)
		if err != nil {
			return Record{}, err
		}
		if latestID == "" {
			latestID = succ.ID
		}
		return Record{}, continuedRefusal(latestID)
	}

	// 6. No other unfinished run in the same work_root, apart from prior when
	// this is a queued reply.
	excludeID := ""
	if !runnerwire.RunFinal(prior.Status) {
		excludeID = prior.ID
	}
	if active, err := d.store.ActiveInWork(ctx, workRoot, excludeID); err != nil {
		return Record{}, err
	} else if active {
		return Record{}, stageRefusal(CodeInUse,
			locale.Errorf("another turn of this work is still running", "這份工作還有一輪在執行"))
	}

	// 7. The work is still open: workspace not merged_local or discarded,
	// PRState not merged or closed, and no merge approval pending on any turn.
	turns, err := d.store.Thread(ctx, prior)
	if err != nil {
		return Record{}, err
	}
	if err := checkWorkOpen(append([]Record{prior}, turns...)); err != nil {
		return Record{}, err
	}

	// 8. Message: trimmed, valid UTF-8, no NUL, <= MaxFollowUpBytes; valid mode.
	msg := strings.TrimSpace(req.Body.Message)
	switch {
	case msg == "":
		return Record{}, invalidField("message", "message is required", "請填寫訊息")
	case len(msg) > runnerwire.MaxFollowUpBytes:
		return Record{}, invalidField("message", "message is over %d bytes", "訊息超過 %d 位元組", runnerwire.MaxFollowUpBytes)
	case !utf8.ValidString(msg) || strings.ContainsRune(msg, 0):
		return Record{}, invalidField("message", "message must be valid text", "訊息須為有效的文字")
	}
	if !ValidMode(req.Body.Mode) {
		return Record{}, invalidField("mode", `mode must be "" or %q`, `mode 須為 "" 或 %q`, runnerwire.ModeInteractive)
	}

	// 9. Runner has CapContinue (else RUNNER_OUTDATED); interactive mode also needs ModeSupported.
	if !Headless(runner.Info) {
		return Record{}, refuse(StartPrecondition, "this runner does not take runs; update nodevas on it", "這個 runner 不接受執行；請更新它的 nodevas")
	}
	if !slices.Contains(runner.Info.Capabilities, runnerwire.CapGlobalRuns) ||
		!slices.Contains(runner.Info.Capabilities, runnerwire.CapContinue) {
		said := locale.Errorf("this runner cannot continue work; update nodevas on it", "這個 runner 不能接續工作；請更新它的 nodevas")
		return Record{}, &StartError{Code: StartOutdated, Reason: CodeRunnerOutdated, Msg: said.Error(), said: said}
	}
	if !slices.Contains(runner.Info.Capabilities, runnerwire.CapGlobalRunsOn) {
		return Record{}, stageRefusal(CodeGlobalRunsOff, locale.Errorf(
			"the runner's owner turned global agents off on it (nodevas runner set globalRuns on)",
			"runner 的擁有者在該機關閉了全域 agent（nodevas runner set globalRuns on）"))
	}
	mode := req.Body.Mode
	if !runnerwire.RunFinal(prior.Status) {
		// §8.3: A queued reply is always background.
		mode = runnerwire.ModeHeadless
	}
	if !ModeSupported(runner.Info, mode) {
		return Record{}, refuse(StartPrecondition, "this runner cannot run interactive sessions", "這個 runner 不能執行互動式工作階段")
	}

	// 10. BudgetStatus(owner) has room.
	budget, err := d.store.BudgetStatus(ctx, runner.OwnerID)
	if err != nil {
		return Record{}, err
	}
	if budget.Exceeded != "" {
		return Record{}, &StartError{Code: StartBudget, Msg: BudgetMessage(budget), said: BudgetSay(budget), Budget: budget}
	}

	retryAt := ""
	if prior.ResetAt != "" && resetInFuture(prior.ResetAt, d.clockNow()) {
		retryAt = prior.ResetAt
	}
	title, rest := runnerwire.GlobalTitle(msg)
	slug := prior.BranchSlug
	if slug == "" {
		slug = runnerwire.SlugFrom(title)
	}
	name := actorName
	if name == "" {
		name = actorID
	}
	extra := withSimple(d.overlapNote(ctx, prior), req.Simple)
	jobPrompt := BuildPrompt(title, rest, "", extra)

	createRun := d.createRun
	if createRun == nil {
		createRun = d.store.Create
	}
	record, err := createRun(ctx, New{
		Global:        true,
		NodeTitle:     title,
		BranchSlug:    slug,
		NoPR:          prior.NoPR,
		RunnerID:      prior.RunnerID,
		RunnerName:    runner.Name,
		OwnerID:       prior.OwnerID,
		Agent:         prior.Agent,
		Model:         prior.Model,
		RepoID:        prior.RepoID,
		RepoName:      prior.RepoName,
		Base:          prior.Base,
		RepoKey:       prior.RepoKey,
		JobPrompt:     jobPrompt,
		CreatedBy:     actorID,
		CreatedByName: name,
		SteppedUpAt:   req.SteppedUpAt,
		Mode:          mode,
		RetryAt:       retryAt,
		ContinueOf:    prior.ID,
		WorkRoot:      workRoot,
		GroupID:       prior.GroupID,
		Origin:        runnerwire.OriginPerson,
	})
	if err != nil {
		if db.IsUniqueViolation(err, "runs_one_continuation") {
			if latestID, lErr := d.store.LatestTurnID(ctx, workRoot); lErr == nil && latestID != "" {
				return Record{}, continuedRefusal(latestID)
			}
		}
		return Record{}, err
	}
	if err := d.store.UserFirst(ctx, record.ID, msg); err != nil {
		return Record{}, err
	}
	d.record(AuditEntry{
		ActorID: actorID, ActorName: name, Action: "run.global.followup", Target: record.ID,
		Detail: map[string]any{
			"prior":        prior.ID,
			"root":         workRoot,
			"mode":         record.Mode,
			"messageBytes": len(msg),
		},
	})
	return d.Submit(ctx, record), nil
}

func checkWorkOpen(turns []Record) error {
	for _, t := range turns {
		if t.Workspace == runnerwire.RunWorkspaceMergedLocal || t.Workspace == runnerwire.RunWorkspaceDiscarded {
			return stageRefusal(CodeWorkGone,
				locale.Errorf("this work's folder is gone", "這份工作的資料夾已不在"))
		}
		if t.PRState == runnerwire.PRMerged || t.PRState == runnerwire.PRClosed {
			return stageRefusal(CodePREnded,
				locale.Errorf("its pull request was merged or closed; start new work instead",
					"它的 pull request 已合併或關閉，請開始新工作"))
		}
		if t.Merge != nil && t.Merge.Status == runnerwire.MergePending {
			return stageRefusal(CodeMergePending,
				locale.Errorf("a merge is pending; continue after it", "正在等待合併，完成後再接續"))
		}
	}
	return nil
}

func resetInFuture(resetAt string, now time.Time) bool {
	if at, err := time.Parse(time.RFC3339Nano, resetAt); err == nil {
		return at.After(now)
	}
	return resetAt > formatStamp(now)
}

// End asks the runner of a running interactive run to quit gracefully and keep
// its work (§7.1). Allowed for the MayType set, narrowed to the runner owner on
// a view-only run. Refuses runners without CapContinue with ErrEndOutdated.
func (d *Dispatcher) End(ctx context.Context, runID string, actor identity.Actor, noPR ...bool) (Record, error) {
	if actor.IsAgent() {
		return Record{}, ErrEndForbidden
	}
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	if !CanSee(actor, r) {
		return Record{}, ErrNotFound
	}
	if !MayType(actor, r) {
		return r, ErrEndForbidden
	}
	if r.ViewOnly && actor.ID != r.OwnerID {
		return r, ErrEndViewOnly
	}
	if r.Mode != runnerwire.ModeInteractive || r.Status != runnerwire.RunRunning {
		return r, locale.Errorf("%w: this run is not a running interactive session",
			"%w：這次執行不是進行中的互動式工作階段", ErrPrecondition)
	}
	runner, err := d.reg.Get(ctx, r.RunnerID)
	if err != nil || runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapContinue) {
		return r, ErrEndOutdated
	}
	wantNoPR := len(noPR) > 0 && noPR[0]
	if err := d.reg.Send(r.RunnerID, runnerwire.Envelope{
		Type:  runnerwire.TypeEnd,
		RunID: r.ID,
		NoPR:  wantNoPR,
	}); err != nil {
		if errors.Is(err, runners.ErrOffline) {
			return r, locale.Errorf("%w: the runner is offline", "%w：runner 目前離線", ErrPrecondition)
		}
		return r, err
	}
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	d.record(AuditEntry{
		Project: r.Project, ActorID: actor.ID, ActorName: name,
		Action: "run.end", Target: r.ID,
		Detail: map[string]any{"runner": r.RunnerID, "noPR": wantNoPR},
	})
	return r, nil
}

// Thread returns the non-withdrawn turns in r's work root chain, oldest first
// (at most MaxThreadRuns).
func (s *Store) Thread(ctx context.Context, r Record) ([]Record, error) {
	rootID := r.WorkRoot
	if rootID == "" {
		rootID = r.ID
	}
	if rootID == "" {
		return []Record{}, nil
	}
	query := `SELECT ` + runColumns + ` FROM runs
WHERE is_global AND (id = ? OR work_root = ?)
  AND (continue_of = '' OR ` + notWithdrawnSQL + `)
ORDER BY created_at ASC, id ASC LIMIT ?`
	out, err := s.query(ctx, query, rootID, rootID, MaxThreadRuns)
	if err != nil {
		return nil, err
	}
	refs := make([]*Record, len(out))
	for i := range out {
		refs[i] = &out[i]
	}
	if err := s.attachMerges(ctx, refs); err != nil {
		return nil, err
	}
	if err := s.attachWorkspaceActions(ctx, refs); err != nil {
		return nil, err
	}
	return out, nil
}

// LatestTurnID walks the non-withdrawn continuation chain starting from
// workRoot and returns the latest turn's run ID.
func (s *Store) LatestTurnID(ctx context.Context, workRoot string) (string, error) {
	if workRoot == "" {
		return "", nil
	}
	cur := workRoot
	for range 500 {
		succ, ok, err := s.Successor(ctx, cur)
		if err != nil {
			return "", err
		}
		if !ok {
			return cur, nil
		}
		cur = succ.ID
	}
	return cur, nil
}
