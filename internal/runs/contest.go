package runs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// CodeContestShape is StartError.Reason when a contest request violates the
// plain-task contest shape (staged workflow, pipeline, fallbacks, interactive
// mode, or duplicate entries; §C.4.1, §C.6).
const CodeContestShape = "CONTEST_SHAPE"

var contestEntryLabels = [runnerwire.MaxContestEntries]string{"A", "B", "C"}

// StartContestRequest is one node contest to start (§C.4.1).
type StartContestRequest struct {
	Body        runnerwire.CreateContestRequest
	Project     string
	ActorID     string
	ActorName   string
	IsAdmin     bool
	SteppedUpAt string
	LoadNode    func() (NodeInfo, bool, error)
	// Optional shape-refusal fields when a caller passes forbidden dispatch
	// options (interactive mode, pipeline, fallbacks) to a contest route.
	Mode      string
	Pipeline  *runnerwire.Pipeline
	Fallbacks []runnerwire.AgentModel
}

// StartGlobalContestRequest is one global contest to start (§C.4.1).
type StartGlobalContestRequest struct {
	Body        runnerwire.CreateGlobalContestRequest
	ActorID     string
	ActorName   string
	SteppedUpAt string
	// Optional shape-refusal fields.
	Mode      string
	Pipeline  *runnerwire.Pipeline
	Fallbacks []runnerwire.AgentModel
}

// ContestResult is a contest together with its runs.
type ContestResult struct {
	Contest ContestRecord
	Runs    []Record
}

// ContestDetail is GET /api/contests/:id's backing data.
type ContestDetail struct {
	Contest ContestRecord
	Runs    []Record
	Compare runnerwire.ContestCompare
}

// JudgeContestRequest is POST /api/contests/:id/judge (§C.4.2, §C.6).
type JudgeContestRequest struct {
	ContestID   string
	Body        runnerwire.JudgeContestRequest
	ActorID     string
	ActorName   string
	SteppedUpAt string
}

// PickContestRequest is POST /api/contests/:id/pick (§C.4.4, §C.6).
type PickContestRequest struct {
	ContestID string
	Body      runnerwire.PickRequest
	ActorID   string
	ActorName string
	// Wait optionally bounds how long PickContest waits for pending workspace
	// actions to settle before returning their status. Zero returns immediately.
	Wait time.Duration
}

// PickContestResult is the decided contest and the workspace actions initiated.
type PickContestResult struct {
	Contest ContestRecord
	Actions []runnerwire.PickActionResult
}

func contestShapeRefusal(field, english, chinese string, args ...any) error {
	said := locale.Errorf(english, chinese, args...)
	return &StartError{Code: StartInvalid, Reason: CodeContestShape, Field: field, Msg: said.Error(), said: said}
}

func validContestOnBusy(choice string) bool {
	switch choice {
	case runnerwire.OnBusyAsk, runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver, runnerwire.OnBusyBoth:
		return true
	}
	return false
}

type contestEntryPlan struct {
	label   string
	runner  runnerwire.Runner
	agent   string
	model   string
	repo    runnerwire.Repo
	repoKey string
}

// StartContest validates a node contest, checks the whole budget (entries +
// optional reviewer) and busy state in one transaction, creates the contest
// and N headless NoPR entries, audits without prompt text, and pumps each
// runner (§C.4.1, §C.2.3).
func (d *Dispatcher) StartContest(ctx context.Context, req StartContestRequest) (ContestResult, error) {
	body := req.Body
	if !engine.ValidNodeID(body.NodeID) {
		return ContestResult{}, invalidField("nodeId", "nodeId is not a valid node id", "nodeId 不是有效的節點代號")
	}
	if len(body.Prompt) > MaxExtraPrompt || !utf8.ValidString(body.Prompt) {
		return ContestResult{}, invalidField("prompt", "prompt must be valid text of at most 8192 bytes", "提示須為最多 8192 位元組的有效文字")
	}
	if !ValidBase(body.Base) {
		return ContestResult{}, invalidField("base", "base is not a valid branch name", "base 不是有效的分支名稱")
	}
	if err := checkContestForbiddenShape(req.Mode, req.Pipeline, req.Fallbacks); err != nil {
		return ContestResult{}, err
	}
	if !validContestOnBusy(body.OnBusy) {
		return ContestResult{}, invalidField("onBusy", "onBusy is not a valid busy choice for a contest", "onBusy 不是比稿有效的忙碌選項")
	}
	if !runnerwire.ValidHolderIDs(body.HolderIDs) {
		return ContestResult{}, invalidField("holderIds", "holderIds is not valid", "holderIds 無效")
	}
	if len(body.Entries) < runnerwire.MinContestEntries || len(body.Entries) > runnerwire.MaxContestEntries {
		return ContestResult{}, invalidField("entries",
			"entries must be %d to %d agents", "entries 須為 %d 到 %d 位 agent",
			runnerwire.MinContestEntries, runnerwire.MaxContestEntries)
	}

	type entryKey struct{ runnerID, agent, model string }
	seen := make(map[entryKey]bool, len(body.Entries))
	plans := make([]contestEntryPlan, 0, len(body.Entries))
	for i, spec := range body.Entries {
		runner, err := d.reg.Get(ctx, spec.RunnerID)
		if err != nil || runner.OwnerID != req.ActorID {
			return ContestResult{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
		}
		model, repo, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
			Agent: spec.Agent, Model: spec.Model, RepoID: body.RepoID,
		})
		if problem != nil {
			field := "entries"
			if runner.Info != nil && !slices.ContainsFunc(runner.Info.Repos, func(r runnerwire.Repo) bool { return r.ID == body.RepoID }) {
				field = "repoId"
			}
			return ContestResult{}, &StartError{Code: StartInvalid, Field: field, Msg: problem.Error(), said: problem}
		}
		if err := checkContestRunnerCaps(runner.Info, false); err != nil {
			return ContestResult{}, err
		}
		key := entryKey{runnerID: runner.ID, agent: spec.Agent, model: model}
		if seen[key] {
			return ContestResult{}, contestShapeRefusal("entries",
				"contest entries must be distinct", "比稿參賽者不能重複")
		}
		seen[key] = true
		plans = append(plans, contestEntryPlan{
			label:   contestEntryLabels[i],
			runner:  runner,
			agent:   spec.Agent,
			model:   model,
			repo:    repo,
			repoKey: runnerwire.RepoKey(runner.ID, repo),
		})
	}

	var resolvedReviewer *runnerwire.ContestEntrySpec
	if body.Reviewer != nil {
		revRunner, err := d.reg.Get(ctx, body.Reviewer.RunnerID)
		if err != nil || revRunner.OwnerID != req.ActorID {
			return ContestResult{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
		}
		revModel, _, problem := CheckAgainstHello(revRunner.Info, runnerwire.CreateRunRequest{
			Agent: body.Reviewer.Agent, Model: body.Reviewer.Model, RepoID: body.RepoID,
		})
		if problem != nil {
			return ContestResult{}, &StartError{Code: StartInvalid, Field: "reviewer", Msg: problem.Error(), said: problem}
		}
		if err := checkContestRunnerCaps(revRunner.Info, false); err != nil {
			return ContestResult{}, err
		}
		resolvedReviewer = &runnerwire.ContestEntrySpec{
			RunnerID: revRunner.ID,
			Agent:    body.Reviewer.Agent,
			Model:    revModel,
		}
	}

	node, found, err := req.LoadNode()
	if err != nil {
		return ContestResult{}, err
	}
	if !found {
		return ContestResult{}, &StartError{Code: StartNotFound, What: "node", Msg: "node not found", said: errNodeNotFound}
	}
	if !AgentModeAllows(node.AgentMode, runnerwire.JobImplement) {
		typeName := node.Type
		if typeName == "" {
			typeName = "task"
		}
		return ContestResult{}, &StartError{
			Code: StartNodeType, NodeType: node.Type, AgentMode: node.AgentMode,
			Msg:  "a " + typeName + " node cannot be dispatched to an agent",
			said: locale.Errorf("a %s node cannot be dispatched to an agent", "%s 節點不能派給 agent", typeName),
		}
	}
	if wf := node.Workflow.Normalized(); wf.Staged() {
		return ContestResult{}, contestShapeRefusal("",
			"compare works on plain tasks only (no stages or pipelines)",
			"比稿只用於一般任務（不含階段或管線）")
	}

	title := node.Title
	if title == "" {
		title = body.NodeID
	}
	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	extra := withSimple(body.Prompt, body.Simple)
	jobPrompt := BuildPrompt(title, node.Content, ReferencesBlock(node.References, node.ReferencesTruncated), extra)
	acceptanceCount := len(acceptanceItems(node.Acceptance))

	needed := len(plans)
	if resolvedReviewer != nil {
		needed++
	}
	lockKeys := make([]string, 0, len(plans)+2)
	lockKeys = append(lockKeys, "budget:"+req.ActorID, "node:"+req.Project+"/"+body.NodeID)
	for _, plan := range plans {
		lockKeys = append(lockKeys, "repo:"+plan.repoKey)
	}

	var contest ContestRecord
	var created []Record
	var outcome BusyOutcome
	err = d.store.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, lockKeys...); err != nil {
			return err
		}
		budget, err := d.store.budgetStatusWith(ctx, tx, req.ActorID)
		if err != nil {
			return err
		}
		if ex := exceededFor(budget, needed); ex != "" {
			budget.Exceeded = ex
			said := BudgetSayFor(budget, needed)
			return &StartError{Code: StartBudget, Msg: said.Error(), said: said, Budget: budget}
		}
		outcome, err = d.applyBusy(ctx, tx, BusyRequest{
			Query: BusyQuery{
				ActorID: req.ActorID,
				IsAdmin: req.IsAdmin,
				Project: req.Project,
				NodeID:  body.NodeID,
				RepoKey: plans[0].repoKey,
			},
			OnBusy:     body.OnBusy,
			HolderIDs:  body.HolderIDs,
			RunnerCaps: plans[0].runner.Info.Capabilities,
		})
		if err != nil {
			return err
		}
		if !outcome.Proceed {
			return nil
		}
		contest, err = d.store.createContestWith(ctx, tx, ContestRecord{
			OwnerID:       req.ActorID,
			CreatedByName: name,
			Contest: runnerwire.Contest{
				Title:      title,
				NodeID:     body.NodeID,
				Project:    req.Project,
				Global:     false,
				State:      runnerwire.ContestRunning,
				EntryCount: len(plans),
				Reviewer:   resolvedReviewer,
				CreatedBy:  req.ActorID,
			},
		})
		if err != nil {
			return err
		}
		created = make([]Record, 0, len(plans))
		for _, plan := range plans {
			r, err := d.store.createWith(ctx, tx, New{
				Project:         req.Project,
				NodeID:          body.NodeID,
				NodeTitle:       title,
				RunnerID:        plan.runner.ID,
				RunnerName:      plan.runner.Name,
				OwnerID:         plan.runner.OwnerID,
				Agent:           plan.agent,
				Model:           plan.model,
				RepoID:          plan.repo.ID,
				RepoName:        plan.repo.Name,
				Base:            body.Base,
				Prompt:          body.Prompt,
				JobPrompt:       jobPrompt,
				CreatedBy:       req.ActorID,
				CreatedByName:   name,
				SteppedUpAt:     req.SteppedUpAt,
				Mode:            runnerwire.ModeHeadless,
				Kind:            runnerwire.JobImplement,
				NoPR:            true,
				Origin:          runnerwire.OriginPerson,
				NodeType:        node.Type,
				AcceptanceCount: acceptanceCount,
				AfterRun:        outcome.AfterRun,
				BusyChoice:      outcome.Choice,
				RepoKey:         plan.repoKey,
				ContestID:       contest.ID,
				ContestLabel:    plan.label,
			})
			if err != nil {
				return err
			}
			created = append(created, r)
		}
		return nil
	})
	if err != nil {
		return ContestResult{}, err
	}

	d.afterBusy(ctx, identity.Actor{ID: req.ActorID, Name: name}, outcome)
	d.record(AuditEntry{
		Project: req.Project, ActorID: req.ActorID, ActorName: name,
		Action: "run.contest.start", Target: contest.ID,
		Detail: map[string]any{
			"contest": contest.ID, "node": body.NodeID, "count": len(created),
			"reviewer": resolvedReviewer != nil, "simple": body.Simple, "global": false,
		},
	})
	pumped := map[string]bool{}
	for _, r := range created {
		d.notifyChanged(r)
	}
	for _, r := range created {
		if !pumped[r.RunnerID] {
			pumped[r.RunnerID] = true
			d.Pump(ctx, r.RunnerID)
		}
	}
	return ContestResult{Contest: contest, Runs: d.reloadRuns(ctx, created)}, nil
}

// StartGlobalContest validates a global contest on one runner and repository,
// checks the whole budget (entries + optional reviewer) and busy state in one
// transaction, creates the contest and N headless NoPR global entries, writes
// each entry's seq-0 user event, audits without task text, and pumps the
// runner (§C.4.1, §C.2.3).
func (d *Dispatcher) StartGlobalContest(ctx context.Context, req StartGlobalContestRequest) (ContestResult, error) {
	body := req.Body
	if err := checkContestForbiddenShape(req.Mode, req.Pipeline, req.Fallbacks); err != nil {
		return ContestResult{}, err
	}
	if !validContestOnBusy(body.OnBusy) {
		return ContestResult{}, invalidField("onBusy", "onBusy is not a valid busy choice for a contest", "onBusy 不是比稿有效的忙碌選項")
	}
	if len(body.Entries) < runnerwire.MinContestEntries || len(body.Entries) > runnerwire.MaxContestEntries {
		return ContestResult{}, invalidField("entries",
			"entries must be %d to %d agents", "entries 須為 %d 到 %d 位 agent",
			runnerwire.MinContestEntries, runnerwire.MaxContestEntries)
	}
	v, err := d.validateGlobal(ctx, body.Task, body.Slug, body.Base, runnerwire.ModeHeadless,
		body.OnBusy, body.HolderIDs, body.RunnerID, req.ActorID, req.ActorName, false)
	if err != nil {
		return ContestResult{}, err
	}
	runner := v.runner
	if err := checkContestRunnerCaps(runner.Info, true); err != nil {
		return ContestResult{}, err
	}

	type entryKey struct{ agent, model string }
	seen := make(map[entryKey]bool, len(body.Entries))
	plans := make([]contestEntryPlan, 0, len(body.Entries))
	var repo runnerwire.Repo
	for i, spec := range body.Entries {
		if spec.RunnerID != "" && spec.RunnerID != runner.ID {
			return ContestResult{}, invalidField("entries",
				"global contest entries must all use the contest runner",
				"全域比稿的所有參賽者須使用同一個 runner")
		}
		model, rRepo, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
			Agent: spec.Agent, Model: spec.Model, RepoID: body.RepoID,
		})
		if problem != nil {
			field := "entries"
			if runner.Info != nil && !slices.ContainsFunc(runner.Info.Repos, func(r runnerwire.Repo) bool { return r.ID == body.RepoID }) {
				field = "repoId"
			}
			return ContestResult{}, &StartError{Code: StartInvalid, Field: field, Msg: problem.Error(), said: problem}
		}
		repo = rRepo
		key := entryKey{agent: spec.Agent, model: model}
		if seen[key] {
			return ContestResult{}, contestShapeRefusal("entries",
				"contest entries must be distinct", "比稿參賽者不能重複")
		}
		seen[key] = true
		plans = append(plans, contestEntryPlan{
			label:   contestEntryLabels[i],
			runner:  runner,
			agent:   spec.Agent,
			model:   model,
			repo:    rRepo,
			repoKey: runnerwire.RepoKey(runner.ID, rRepo),
		})
	}

	var resolvedReviewer *runnerwire.ContestEntrySpec
	if body.Reviewer != nil {
		if body.Reviewer.RunnerID != "" && body.Reviewer.RunnerID != runner.ID {
			return ContestResult{}, invalidField("reviewer",
				"global contest reviewer must use the contest runner",
				"全域比稿的審查者須使用同一個 runner")
		}
		revModel, _, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
			Agent: body.Reviewer.Agent, Model: body.Reviewer.Model, RepoID: body.RepoID,
		})
		if problem != nil {
			return ContestResult{}, &StartError{Code: StartInvalid, Field: "reviewer", Msg: problem.Error(), said: problem}
		}
		resolvedReviewer = &runnerwire.ContestEntrySpec{
			RunnerID: runner.ID,
			Agent:    body.Reviewer.Agent,
			Model:    revModel,
		}
	}

	repoKey := runnerwire.RepoKey(runner.ID, repo)
	jobPrompt := BuildPrompt(v.title, v.rest, "", withSimple("", body.Simple))
	needed := len(plans)
	if resolvedReviewer != nil {
		needed++
	}

	var contest ContestRecord
	var created []Record
	var outcome BusyOutcome
	err = d.store.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, "budget:"+runner.OwnerID, "repo:"+repoKey); err != nil {
			return err
		}
		budget, err := d.store.budgetStatusWith(ctx, tx, runner.OwnerID)
		if err != nil {
			return err
		}
		if ex := exceededFor(budget, needed); ex != "" {
			budget.Exceeded = ex
			said := BudgetSayFor(budget, needed)
			return &StartError{Code: StartBudget, Msg: said.Error(), said: said, Budget: budget}
		}
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
		contest, err = d.store.createContestWith(ctx, tx, ContestRecord{
			OwnerID:       runner.OwnerID,
			CreatedByName: v.actorName,
			Contest: runnerwire.Contest{
				Title:      v.title,
				Global:     true,
				State:      runnerwire.ContestRunning,
				EntryCount: len(plans),
				Reviewer:   resolvedReviewer,
				CreatedBy:  req.ActorID,
			},
		})
		if err != nil {
			return err
		}
		created = make([]Record, 0, len(plans))
		for _, plan := range plans {
			r, err := d.store.createWith(ctx, tx, New{
				Global:        true,
				NodeTitle:     v.title,
				BranchSlug:    v.slug,
				NoPR:          true,
				RunnerID:      runner.ID,
				RunnerName:    runner.Name,
				OwnerID:       runner.OwnerID,
				Agent:         plan.agent,
				Model:         plan.model,
				RepoID:        repo.ID,
				RepoName:      repo.Name,
				Base:          body.Base,
				JobPrompt:     jobPrompt,
				CreatedBy:     req.ActorID,
				CreatedByName: v.actorName,
				SteppedUpAt:   req.SteppedUpAt,
				Mode:          runnerwire.ModeHeadless,
				Kind:          runnerwire.JobImplement,
				Origin:        runnerwire.OriginPerson,
				AfterRun:      outcome.AfterRun,
				BusyChoice:    outcome.Choice,
				RepoKey:       repoKey,
				ContestID:     contest.ID,
				ContestLabel:  plan.label,
			})
			if err != nil {
				return err
			}
			created = append(created, r)
		}
		return nil
	})
	if err != nil {
		return ContestResult{}, err
	}

	d.afterBusy(ctx, identity.Actor{ID: req.ActorID, Name: v.actorName}, outcome)
	for _, r := range created {
		if err := d.store.UserFirst(ctx, r.ID, v.task); err != nil {
			return ContestResult{}, err
		}
	}
	d.record(AuditEntry{
		ActorID: req.ActorID, ActorName: v.actorName,
		Action: "run.contest.start", Target: contest.ID,
		Detail: map[string]any{
			"contest": contest.ID, "runner": runner.ID, "repo": repo.ID, "slug": v.slug,
			"count": len(created), "reviewer": resolvedReviewer != nil,
			"taskBytes": len(v.task), "simple": body.Simple, "global": true,
		},
	})
	for _, r := range created {
		d.notifyChanged(r)
	}
	d.Pump(ctx, runner.ID)
	return ContestResult{Contest: contest, Runs: d.reloadRuns(ctx, created)}, nil
}

func checkContestForbiddenShape(mode string, pipeline *runnerwire.Pipeline, fallbacks []runnerwire.AgentModel) error {
	if mode != "" {
		return contestShapeRefusal("mode",
			"contests run in the background only", "比稿只能在背景執行")
	}
	if pipeline != nil {
		return contestShapeRefusal("pipeline",
			"compare works on plain tasks only (no stages or pipelines)",
			"比稿只用於一般任務（不含階段或管線）")
	}
	if len(fallbacks) > 0 {
		return contestShapeRefusal("fallbacks",
			"contest entries cannot use fallbacks", "比稿參賽者不能設定備援")
	}
	return nil
}

func checkContestRunnerCaps(info *runnerwire.Info, global bool) error {
	if global {
		if err := checkGlobalRunnerCaps(info, runnerwire.ModeHeadless); err != nil {
			return err
		}
	} else if !Headless(info) {
		return refuse(StartPrecondition, "this runner does not take runs; update nodevas on it", "這個 runner 不接受執行；請更新它的 nodevas")
	}
	if info == nil || !slices.Contains(info.Capabilities, runnerwire.CapCoord) {
		return refuse(StartOutdated, "update nodevas on the runner to compare agents", "請更新 runner 的 nodevas 才能比稿")
	}
	return nil
}

func (d *Dispatcher) reloadRuns(ctx context.Context, list []Record) []Record {
	out := make([]Record, len(list))
	for i, r := range list {
		if latest, err := d.store.Get(ctx, r.ID); err == nil {
			out[i] = latest
		} else {
			out[i] = r
		}
	}
	return out
}

// GetContestDetail loads a contest, all of its runs, and the comparison view
// (entries with changed files and reviewer scores) for GET /api/contests/:id.
func (d *Dispatcher) GetContestDetail(ctx context.Context, contestID string) (ContestDetail, error) {
	contest, err := d.store.GetContest(ctx, contestID)
	if err != nil {
		return ContestDetail{}, err
	}
	allRuns, err := d.store.ContestRuns(ctx, contestID)
	if err != nil {
		return ContestDetail{}, err
	}
	entries := make([]runnerwire.ContestCompareEntry, 0, contest.EntryCount)
	for _, r := range allRuns {
		if r.ContestLabel == "J" {
			continue
		}
		files := []runnerwire.FileChange{}
		if ch, err := d.store.Changes(ctx, r.ID); err == nil && ch != nil && len(ch.Files) > 0 {
			files = ch.Files
		}
		entries = append(entries, runnerwire.ContestCompareEntry{
			Label:     r.ContestLabel,
			RunID:     r.ID,
			Agent:     r.Agent,
			Model:     r.Model,
			Status:    r.Status,
			Files:     files,
			Additions: r.Additions,
			Deletions: r.Deletions,
			Summary:   r.Summary,
		})
	}
	return ContestDetail{
		Contest: contest,
		Runs:    allRuns,
		Compare: runnerwire.ContestCompare{
			Entries: entries,
			Scores:  contest.Scores,
		},
	}, nil
}

// contestEnded advances a contest when an entry or reviewer run finishes
// (§C.4.2, §C.4.3). Called from coordhooks.go's deferred ended hook.
func (d *Dispatcher) contestEnded(ctx context.Context, r Record) {
	if r.ContestID == "" || d.store == nil {
		return
	}
	contest, err := d.store.GetContest(ctx, r.ContestID)
	if err != nil {
		slog.Warn("runs: load contest on run end", slog.String("contest", r.ContestID), slog.Any("error", err))
		return
	}
	if contest.State == runnerwire.ContestDecided || contest.State == runnerwire.ContestCancelled {
		return
	}
	allRuns, err := d.store.ContestRuns(ctx, contest.ID)
	if err != nil {
		slog.Warn("runs: load contest runs on end", slog.String("contest", contest.ID), slog.Any("error", err))
		return
	}

	if r.ContestLabel == "J" {
		if contest.State != runnerwire.ContestJudging || contest.JudgeRunID != r.ID {
			return
		}
		var scores *runnerwire.ContestScores
		if r.Status == runnerwire.RunSucceeded {
			var labels []string
			for _, e := range allRuns {
				if e.ContestLabel != "J" && e.Status == runnerwire.RunSucceeded && e.Commit != "" {
					labels = append(labels, e.ContestLabel)
				}
			}
			scores = ParseScores(r.Summary, labels)
		}
		if _, changed, err := d.store.finishContestJudging(ctx, contest.ID, r.ID, scores); err != nil {
			slog.Warn("runs: finish contest judging", slog.String("contest", contest.ID), slog.Any("error", err))
		} else if changed {
			d.announceChanged(r)
		}
		return
	}

	if contest.State != runnerwire.ContestRunning {
		return
	}
	var entries []Record
	var succeeded []Record
	for _, e := range allRuns {
		if e.ContestLabel == "J" {
			continue
		}
		entries = append(entries, e)
		if !runnerwire.RunFinal(e.Status) {
			return
		}
		if e.Status == runnerwire.RunSucceeded && e.Commit != "" {
			succeeded = append(succeeded, e)
		}
	}
	if len(entries) < contest.EntryCount {
		return
	}

	if contest.Reviewer == nil || len(succeeded) < 2 {
		if _, changed, err := d.store.markContestReady(ctx, contest.ID); err != nil {
			slog.Warn("runs: mark contest ready", slog.String("contest", contest.ID), slog.Any("error", err))
		} else if changed {
			d.announceChanged(r)
		}
		return
	}

	_, judgeRun, err := d.startContestReviewer(ctx, contest, entries, succeeded, *contest.Reviewer, runnerwire.ContestRunning, entries[0].SteppedUpAt)
	if err != nil {
		if errors.Is(err, errContestAlreadyJudged) {
			return
		}
		slog.Warn("runs: start contest reviewer, moving to ready", slog.String("contest", contest.ID), slog.Any("error", err))
		if _, changed, merr := d.store.markContestReady(ctx, contest.ID); merr == nil && changed {
			d.announceChanged(r)
		}
		return
	}
	d.notifyChanged(judgeRun)
	d.Pump(ctx, judgeRun.RunnerID)
}

var errContestAlreadyJudged = locale.Errorf("contest already judged or not in expected state", "比稿已經評分過或不在預期狀態")

// JudgeContest starts a compare reviewer run on a ready, unjudged contest
// (§C.4.2, §C.6). Caller must already have verified the actor is the creator.
func (d *Dispatcher) JudgeContest(ctx context.Context, req JudgeContestRequest) (ContestRecord, Record, error) {
	contest, err := d.store.GetContest(ctx, req.ContestID)
	if err != nil {
		return ContestRecord{}, Record{}, err
	}
	if contest.State != runnerwire.ContestReady || contest.JudgeRunID != "" {
		return ContestRecord{}, Record{}, locale.Errorf("%w: contest is not ready for judging or already judged", "%w：比稿尚未準備好評分或已經評分過", ErrTransition)
	}
	allRuns, err := d.store.ContestRuns(ctx, contest.ID)
	if err != nil {
		return ContestRecord{}, Record{}, err
	}
	var entries, succeeded []Record
	for _, r := range allRuns {
		if r.ContestLabel == "J" {
			continue
		}
		entries = append(entries, r)
		if r.Status == runnerwire.RunSucceeded && r.Commit != "" {
			succeeded = append(succeeded, r)
		}
	}
	if len(entries) == 0 || len(succeeded) < 2 {
		return ContestRecord{}, Record{}, locale.Errorf("%w: at least two entries must succeed with changes to compare", "%w：至少需要兩份成功且有變更的結果才能評分", ErrTransition)
	}

	runnerID := strings.TrimSpace(req.Body.RunnerID)
	if runnerID == "" {
		runnerID = entries[0].RunnerID
	}
	if contest.Global && runnerID != entries[0].RunnerID {
		return ContestRecord{}, Record{}, invalidField("runnerId",
			"global contest reviewer must use the contest runner",
			"全域比稿的審查者須使用同一個 runner")
	}
	runner, err := d.reg.Get(ctx, runnerID)
	if err != nil || runner.OwnerID != req.ActorID {
		return ContestRecord{}, Record{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}
	model, _, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
		Agent: req.Body.Agent, Model: req.Body.Model, RepoID: entries[0].RepoID,
	})
	if problem != nil {
		return ContestResult{}.Contest, Record{}, &StartError{Code: StartInvalid, Field: "agent", Msg: problem.Error(), said: problem}
	}
	if err := checkContestRunnerCaps(runner.Info, contest.Global); err != nil {
		return ContestRecord{}, Record{}, err
	}

	spec := runnerwire.ContestEntrySpec{RunnerID: runner.ID, Agent: req.Body.Agent, Model: model}
	steppedUpAt := req.SteppedUpAt
	if steppedUpAt == "" {
		steppedUpAt = entries[0].SteppedUpAt
	}
	updated, judgeRun, err := d.startContestReviewer(ctx, contest, entries, succeeded, spec, runnerwire.ContestReady, steppedUpAt)
	if err != nil {
		if errors.Is(err, errContestAlreadyJudged) {
			return ContestRecord{}, Record{}, locale.Errorf("%w: contest is not ready for judging or already judged", "%w：比稿尚未準備好評分或已經評分過", ErrTransition)
		}
		return ContestRecord{}, Record{}, err
	}

	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	d.record(AuditEntry{
		Project: contest.Project, ActorID: req.ActorID, ActorName: name,
		Action: "run.contest.judge", Target: contest.ID,
		Detail: map[string]any{
			"contest": contest.ID, "judgeRun": judgeRun.ID, "runner": runner.ID,
			"agent": judgeRun.Agent, "model": judgeRun.Model,
		},
	})
	d.notifyChanged(judgeRun)
	d.Pump(ctx, judgeRun.RunnerID)
	if latest, err := d.store.Get(ctx, judgeRun.ID); err == nil {
		judgeRun = latest
	}
	return updated, judgeRun, nil
}

func (d *Dispatcher) startContestReviewer(
	ctx context.Context,
	contest ContestRecord,
	entries []Record,
	succeeded []Record,
	spec runnerwire.ContestEntrySpec,
	fromState string,
	steppedUpAt string,
) (ContestRecord, Record, error) {
	runnerID := spec.RunnerID
	if runnerID == "" && len(entries) > 0 {
		runnerID = entries[0].RunnerID
	}
	runner, err := d.reg.Get(ctx, runnerID)
	if err != nil || runner.OwnerID != contest.OwnerID {
		return ContestRecord{}, Record{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}
	model, repo, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
		Agent: spec.Agent, Model: spec.Model, RepoID: entries[0].RepoID,
	})
	if problem != nil {
		return ContestRecord{}, Record{}, &StartError{Code: StartInvalid, Field: "reviewer", Msg: problem.Error(), said: problem}
	}
	if err := checkContestRunnerCaps(runner.Info, contest.Global); err != nil {
		return ContestRecord{}, Record{}, err
	}

	compareEntries := make([]CompareEntry, 0, len(succeeded))
	for _, e := range succeeded {
		ch, _ := d.store.Changes(ctx, e.ID)
		compareEntries = append(compareEntries, CompareEntry{
			Label:   e.ContestLabel,
			Branch:  e.Branch,
			Summary: e.Summary,
			Changes: ch,
		})
	}
	prompt := BuildComparePrompt(contest.ID, entries[0].JobPrompt, compareEntries)

	slug := ""
	if contest.Global {
		slug = entries[0].BranchSlug
		if !runnerwire.ValidSlug(slug) {
			slug = runnerwire.SlugFrom(contest.Title)
		}
	}
	resolvedSpec := &runnerwire.ContestEntrySpec{RunnerID: runner.ID, Agent: spec.Agent, Model: model}
	in := New{
		Project:       contest.Project,
		NodeID:        contest.NodeID,
		NodeTitle:     contest.Title,
		Global:        contest.Global,
		BranchSlug:    slug,
		NoPR:          true,
		RunnerID:      runner.ID,
		RunnerName:    runner.Name,
		OwnerID:       runner.OwnerID,
		Agent:         spec.Agent,
		Model:         model,
		RepoID:        repo.ID,
		RepoName:      repo.Name,
		Base:          entries[0].Base,
		JobPrompt:     prompt,
		CreatedBy:     contest.CreatedBy,
		CreatedByName: contest.CreatedByName,
		SteppedUpAt:   steppedUpAt,
		Mode:          runnerwire.ModeHeadless,
		Kind:          runnerwire.JobCompare,
		Origin:        runnerwire.OriginPipeline,
		RepoKey:       runnerwire.RepoKey(runner.ID, repo),
		ContestID:     contest.ID,
		ContestLabel:  "J",
	}

	var updated ContestRecord
	var judgeRun Record
	err = d.store.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockBusy(ctx, tx, "contest:"+contest.ID, "budget:"+contest.OwnerID); err != nil {
			return err
		}
		cur, err := d.store.getContestWith(ctx, tx, contest.ID)
		if err != nil {
			return err
		}
		if cur.State != fromState || cur.JudgeRunID != "" {
			return errContestAlreadyJudged
		}
		budget, err := d.store.budgetStatusWith(ctx, tx, contest.OwnerID)
		if err != nil {
			return err
		}
		if ex := exceededFor(budget, 1); ex != "" {
			budget.Exceeded = ex
			said := BudgetSayFor(budget, 1)
			return &StartError{Code: StartBudget, Msg: said.Error(), said: said, Budget: budget}
		}
		judgeRun, err = d.store.createWith(ctx, tx, in)
		if err != nil {
			return err
		}
		var ok bool
		updated, ok, err = d.store.startContestJudgingWith(ctx, tx, contest.ID, fromState, judgeRun.ID, resolvedSpec)
		if err != nil {
			return err
		}
		if !ok {
			return errContestAlreadyJudged
		}
		return nil
	})
	if err != nil {
		return ContestRecord{}, Record{}, err
	}
	return updated, judgeRun, nil
}

// PickContest marks the winning entry, applies it (merge_local, open_pr, or
// keep), optionally discards the other entries, reports every action's status
// (including refused discards), and records the audit entry (§C.4.4, §C.6).
// Caller must already have verified the actor is the contest creator.
func (d *Dispatcher) PickContest(ctx context.Context, req PickContestRequest) (PickContestResult, error) {
	runID := strings.TrimSpace(req.Body.RunID)
	if runID == "" {
		return PickContestResult{}, invalidField("runId", "runId is required", "請指定勝出的 runId")
	}
	switch req.Body.Apply {
	case runnerwire.ContestApplyLocal, runnerwire.ContestApplyPR, runnerwire.ContestApplyKeep:
	default:
		return PickContestResult{}, invalidField("apply", `apply must be "local", "pr" or "keep"`, `apply 須為 "local"、"pr" 或 "keep"`)
	}

	contest, err := d.store.GetContest(ctx, req.ContestID)
	if err != nil {
		return PickContestResult{}, err
	}
	if contest.State != runnerwire.ContestReady && contest.State != runnerwire.ContestJudging {
		return PickContestResult{}, locale.Errorf("%w: contest cannot be decided in state %s", "%w：比稿在 %s 狀態下無法決定", ErrTransition, contest.State)
	}
	allRuns, err := d.store.ContestRuns(ctx, contest.ID)
	if err != nil {
		return PickContestResult{}, err
	}

	var winner *Record
	var others []Record
	for i := range allRuns {
		r := allRuns[i]
		if r.ContestLabel == "J" {
			continue
		}
		if r.ID == runID {
			copyR := r
			winner = &copyR
		} else {
			others = append(others, r)
		}
	}
	if winner == nil {
		return PickContestResult{}, invalidField("runId", "runId is not an entry of this contest", "runId 不是這場比稿的參賽者")
	}
	if winner.Status != runnerwire.RunSucceeded || winner.Commit == "" {
		return PickContestResult{}, locale.Errorf("%w: the chosen entry must have succeeded with a commit", "%w：選擇的參賽者必須成功並有提交", ErrTransition)
	}

	updated, ok, err := d.store.decideContest(ctx, contest.ID, winner.ID, req.ActorID)
	if err != nil {
		return PickContestResult{}, err
	}
	if !ok {
		return PickContestResult{}, locale.Errorf("%w: contest was already decided or cancelled", "%w：比稿已經決定或取消", ErrTransition)
	}

	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	// If the person decided while a reviewer was still running, cancel it.
	if contest.State == runnerwire.ContestJudging && contest.JudgeRunID != "" {
		_, _ = d.Cancel(ctx, contest.JudgeRunID, "contest decided by "+name)
	}

	approver := Approver{ID: req.ActorID, Name: name}
	actions := make([]runnerwire.PickActionResult, 0, 1+len(others))
	type pendingWait struct {
		idx      int
		actionID string
	}
	var pending []pendingWait

	runAction := func(target Record, action, headSHA string) {
		res, aerr := d.WorkspaceAction(ctx, target.ID, approver, action, headSHA)
		if aerr != nil {
			code := "failed"
			var wb *WorkspaceBlocked
			if errors.As(aerr, &wb) && wb.Code != "" {
				code = wb.Code
			}
			actions = append(actions, runnerwire.PickActionResult{
				RunID:  target.ID,
				Action: action,
				Status: runnerwire.WorkspaceFailed,
				Code:   code,
			})
			return
		}
		status := runnerwire.WorkspaceDone
		code := ""
		actionID := ""
		if res.WorkspaceAction != nil && res.WorkspaceAction.Action == action {
			status = res.WorkspaceAction.Status
			code = res.WorkspaceAction.Code
			if status == runnerwire.WorkspacePending {
				actionID = res.WorkspaceAction.ID
			}
		}
		idx := len(actions)
		actions = append(actions, runnerwire.PickActionResult{
			RunID:  target.ID,
			Action: action,
			Status: status,
			Code:   code,
		})
		if actionID != "" {
			pending = append(pending, pendingWait{idx: idx, actionID: actionID})
		}
	}

	switch req.Body.Apply {
	case runnerwire.ContestApplyLocal:
		runAction(*winner, runnerwire.WorkspaceMergeLocal, winner.Commit)
	case runnerwire.ContestApplyPR:
		runAction(*winner, runnerwire.WorkspaceOpenPR, winner.Commit)
	case runnerwire.ContestApplyKeep:
		// Keep for now: no workspace action; ResultActions stay available.
	}

	if req.Body.DiscardOthers {
		for _, other := range others {
			runAction(other, runnerwire.WorkspaceDiscard, "")
		}
	}

	if req.Wait > 0 && len(pending) > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, req.Wait)
		var wg sync.WaitGroup
		for _, pw := range pending {
			wg.Add(1)
			go func(p pendingWait) {
				defer wg.Done()
				d.AwaitWorkspaceAction(waitCtx, p.actionID, req.Wait)
			}(pw)
		}
		wg.Wait()
		cancel()
		for _, pw := range pending {
			if latest, err := d.store.WorkspaceAction(ctx, pw.actionID); err == nil {
				actions[pw.idx].Status = latest.Status
				actions[pw.idx].Code = latest.Code
			}
		}
	}

	discarded := []string{}
	for _, a := range actions {
		if a.Action == runnerwire.WorkspaceDiscard && a.Status != runnerwire.WorkspaceFailed {
			discarded = append(discarded, a.RunID)
		}
	}
	d.record(AuditEntry{
		Project: contest.Project, ActorID: req.ActorID, ActorName: name,
		Action: "run.contest.pick", Target: contest.ID,
		Detail: map[string]any{
			"contest":   contest.ID,
			"winner":    winner.ID,
			"apply":     req.Body.Apply,
			"discarded": discarded,
		},
	})
	d.announceChanged(*winner)
	return PickContestResult{Contest: updated, Actions: actions}, nil
}

// CancelContest cancels an undecided contest and every unfinished run in it
// (§C.4.3, §C.6). Caller must already have verified the actor is creator or admin.
func (d *Dispatcher) CancelContest(ctx context.Context, contestID, actorID, actorName string) (ContestResult, error) {
	contest, err := d.store.GetContest(ctx, contestID)
	if err != nil {
		return ContestResult{}, err
	}
	if contest.State == runnerwire.ContestDecided {
		return ContestResult{}, locale.Errorf("%w: a decided contest cannot be cancelled", "%w：已決定的比稿無法取消", ErrTransition)
	}
	updated := contest
	if contest.State != runnerwire.ContestCancelled {
		var ok bool
		updated, ok, err = d.store.cancelContest(ctx, contestID)
		if err != nil {
			return ContestResult{}, err
		}
		if !ok && updated.State == runnerwire.ContestDecided {
			return ContestResult{}, locale.Errorf("%w: a decided contest cannot be cancelled", "%w：已決定的比稿無法取消", ErrTransition)
		}
	}
	name := actorName
	if name == "" {
		name = actorID
	}
	allRuns, err := d.store.ContestRuns(ctx, contestID)
	if err != nil {
		return ContestResult{}, err
	}
	for _, r := range allRuns {
		if runnerwire.RunFinal(r.Status) {
			continue
		}
		if _, err := d.Cancel(ctx, r.ID, "cancelled by "+name); err != nil && !errors.Is(err, ErrTransition) {
			return ContestResult{}, err
		}
	}
	d.record(AuditEntry{
		Project: contest.Project, ActorID: actorID, ActorName: name,
		Action: "run.contest.cancel", Target: contest.ID,
		Detail: map[string]any{"contest": contest.ID},
	})
	latestRuns, err := d.store.ContestRuns(ctx, contestID)
	if err != nil {
		return ContestResult{}, err
	}
	if len(latestRuns) > 0 {
		d.announceChanged(latestRuns[0])
	} else if !contest.Global && d.changed != nil {
		d.changed(contest.Project, contest.NodeID)
	}
	return ContestResult{Contest: updated, Runs: latestRuns}, nil
}
