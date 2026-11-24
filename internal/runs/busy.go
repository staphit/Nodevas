package runs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/redact"
	"nodevas/internal/runnerwire"
)

// CodeNotYoursToStop is StartError.Reason when onBusy == "takeOver" encounters
// a hidden or unstoppable holder (§C.2.2, §C.6).
const CodeNotYoursToStop = "NOT_YOURS_TO_STOP"

// maxAfterSummaryBytes caps the carry-over summary appended to a queued run's
// prompt (§C.2.3).
const maxAfterSummaryBytes = 2 << 10

// hiddenAfterNotice is the sole carry-over line given to a queued run whose
// prior belonged to another member (§C.2.3, §C.10).
const hiddenAfterNotice = "Another run on this task finished before yours; check the base branch and open pull requests for its work."

// SetNodeCoordCheck registers a callback that reports whether (project, nodeID)
// is currently done or holds a live board claim, so coordJob marks queued and
// take-over runs as Job.Coordinated at offer time (§C.2.5). Call it before
// serving.
func (d *Dispatcher) SetNodeCoordCheck(fn func(ctx context.Context, project, nodeID string) bool) {
	if d == nil {
		return
	}
	d.coord.nodeDoneOrClaimed = fn
}

func (d *Dispatcher) nodeDoneOrClaimed(ctx context.Context, project, nodeID string) bool {
	if d == nil || project == "" || nodeID == "" || d.coord.nodeDoneOrClaimed == nil {
		return false
	}
	return d.coord.nodeDoneOrClaimed(ctx, project, nodeID)
}

// Lineage identifies runs that belong to the same lineage and never block each
// other in Layer 1 busy checks (§C.2.1).
type Lineage struct {
	PipelineID string
	WorkRoot   string
	GroupID    string
	ContestID  string
}

// BusyQuery describes the node and/or repository a starting run would hold.
type BusyQuery struct {
	ActorID  string
	IsAdmin  bool
	Project  string
	NodeID   string
	RepoKey  string
	Global   bool
	Exclude  Lineage
	RunnerID string // optional; used to check global "continue" eligibility
	RepoID   string // optional; used to check global "continue" eligibility
}

// Holder is one unfinished run currently holding a node or repository.
type Holder struct {
	Run     Record
	Hidden  bool
	Waiting bool
}

// BusyRequest is the input to applyBusy inside a start transaction (§C.2.3).
type BusyRequest struct {
	Query      BusyQuery
	OnBusy     string
	HolderIDs  []string
	RunnerCaps []string
	Group      bool
	// Automatic is a start nobody clicked (the autopilot). Asked to queue on
	// a node whose runner lacks CapCoord, it still waits behind the holder
	// in Pump order rather than failing as a person's start does.
	Automatic bool
}

// BusyOutcome is the result of applyBusy.
type BusyOutcome struct {
	Proceed  bool
	AfterRun string
	Choice   string
	Stop     []Holder
	Audit    *AuditEntry
}

func (d *Dispatcher) holders(ctx context.Context, q querier, bq BusyQuery) ([]Holder, error) {
	records, err := d.store.busyHolders(ctx, q, bq)
	if err != nil {
		return nil, err
	}
	out := make([]Holder, 0, len(records))
	for _, r := range records {
		out = append(out, Holder{
			Run:     r,
			Hidden:  !canSeeQuery(bq, r),
			Waiting: r.Status == runnerwire.RunQueued && r.AfterRun != "",
		})
	}
	return out, nil
}

// PreflightBusy reads the current busy state for a form preflight route on the
// reader pool without taking any lock or writing anything (§C.2.2).
func (d *Dispatcher) PreflightBusy(ctx context.Context, bq BusyQuery, runnerCaps []string, group bool) (runnerwire.BusyResponse, error) {
	holders, err := d.holders(ctx, d.store.db, bq)
	if err != nil {
		return runnerwire.BusyResponse{}, err
	}
	return d.buildBusyResponse(ctx, d.store.db, BusyRequest{
		Query:      bq,
		RunnerCaps: runnerCaps,
		Group:      group,
	}, holders), nil
}

func (d *Dispatcher) buildBusyResponse(ctx context.Context, q querier, br BusyRequest, holders []Holder) runnerwire.BusyResponse {
	resp := runnerwire.BusyResponse{
		Holders: []runnerwire.RunHolder{},
		Choices: []string{},
	}
	if len(holders) == 0 {
		return resp
	}

	totalVisible := 0
	allMayStop := true
	for _, h := range holders {
		if h.Hidden {
			resp.Others++
			continue
		}
		totalVisible++
		if len(resp.Holders) >= runnerwire.MaxHolderIDs {
			continue
		}
		r := h.Run
		title := r.Title
		if title == "" {
			title = r.NodeTitle
		}
		if title == "" {
			title = r.NodeID
		}
		since := r.StartedAt
		if since == "" {
			since = r.CreatedAt
		}
		by := r.CreatedByName
		if by == "" {
			by = r.CreatedBy
		}
		mayStop := true
		sameNode := !br.Query.Global && !r.Global && r.Project == br.Query.Project && r.NodeID == br.Query.NodeID
		resp.Holders = append(resp.Holders, runnerwire.RunHolder{
			RunID:    r.ID,
			Title:    title,
			Agent:    r.Agent,
			Model:    r.Model,
			Mode:     r.Mode,
			Status:   r.Status,
			Waiting:  h.Waiting,
			Since:    since,
			By:       by,
			NodeID:   r.NodeID,
			RepoName: r.RepoName,
			MayStop:  mayStop,
			SameNode: sameNode,
		})
	}

	if br.Group {
		resp.Choices = []string{runnerwire.OnBusyBoth, runnerwire.OnBusySkip}
		return resp
	}

	canTakeOver := resp.Others == 0 && allMayStop && totalVisible > 0 && totalVisible <= runnerwire.MaxHolderIDs
	if !br.Query.Global {
		// Node start: queue and both require CapCoord; older runners offer only takeOver (§C.2.5).
		hasCoord := slices.Contains(br.RunnerCaps, runnerwire.CapCoord)
		if hasCoord {
			resp.Choices = append(resp.Choices, runnerwire.OnBusyQueue)
		}
		if canTakeOver {
			resp.Choices = append(resp.Choices, runnerwire.OnBusyTakeOver)
		}
		if hasCoord {
			resp.Choices = append(resp.Choices, runnerwire.OnBusyBoth)
		}
		return resp
	}

	// Global start: queue and both always; takeOver when all visible & stoppable;
	// continue when all holders are the actor's own global runs of one work_root
	// on the same runner and repo (§C.2.2).
	resp.Choices = append(resp.Choices, runnerwire.OnBusyQueue)
	if canTakeOver {
		resp.Choices = append(resp.Choices, runnerwire.OnBusyTakeOver)
	}
	resp.Choices = append(resp.Choices, runnerwire.OnBusyBoth)
	if cont := d.continueTarget(ctx, q, br.Query, holders); cont != nil {
		resp.Choices = append(resp.Choices, runnerwire.OnBusyContinue)
		resp.Continue = cont
	}
	return resp
}

// continueTarget returns the BusyContinue target when every holder is the
// actor's own global run of one work_root on the same runner and repo, and its
// latest turn can be continued (§C.2.2).
func (d *Dispatcher) continueTarget(ctx context.Context, q querier, bq BusyQuery, holders []Holder) *runnerwire.BusyContinue {
	if !bq.Global || len(holders) == 0 || bq.ActorID == "" {
		return nil
	}
	var root, runnerID, repoID string
	for _, h := range holders {
		r := h.Run
		if h.Hidden || !r.Global || r.ContestID != "" {
			return nil
		}
		if r.CreatedBy != bq.ActorID || r.OwnerID != bq.ActorID {
			return nil
		}
		if bq.RunnerID != "" && r.RunnerID != bq.RunnerID {
			return nil
		}
		if bq.RepoID != "" && r.RepoID != bq.RepoID {
			return nil
		}
		wr := workRootOf(r)
		if wr == "" {
			return nil
		}
		if root == "" {
			root, runnerID, repoID = wr, r.RunnerID, r.RepoID
		} else if wr != root || r.RunnerID != runnerID || r.RepoID != repoID {
			return nil
		}
	}
	// FollowUp requires no other unfinished turn in work_root besides the prior itself.
	if len(holders) > 1 {
		return nil
	}
	latest, ok, err := d.store.latestTurnWith(ctx, q, root)
	if err != nil || !ok {
		return nil
	}
	if latest.ID != holders[0].Run.ID {
		return nil
	}
	if latest.Workspace == runnerwire.RunWorkspaceMergedLocal || latest.Workspace == runnerwire.RunWorkspaceDiscarded ||
		latest.PRState == runnerwire.PRMerged || latest.PRState == runnerwire.PRClosed {
		return nil
	}
	return &runnerwire.BusyContinue{
		RunID:  latest.ID,
		Agent:  latest.Agent,
		Model:  latest.Model,
		OpenPR: !latest.NoPR,
	}
}

func busyError(resp runnerwire.BusyResponse) error {
	said := locale.Errorf("someone is already working on this", "已經有人在處理這項工作")
	cp := resp
	return &StartError{Code: StartBusy, Msg: said.Error(), said: said, Busy: &cp}
}

func notYoursToStopError() error {
	said := locale.Errorf("only the run's owner or an admin can stop it", "只有這次執行的擁有者或管理員可以停止它")
	return &StartError{Code: StartForbidden, Reason: CodeNotYoursToStop, Msg: said.Error(), said: said}
}

func holderIDsMatch(requested []string, visible []runnerwire.RunHolder) bool {
	if len(requested) != len(visible) {
		return false
	}
	want := make(map[string]bool, len(visible))
	for _, h := range visible {
		want[h.RunID] = true
	}
	seen := make(map[string]bool, len(requested))
	for _, raw := range requested {
		id := strings.TrimSpace(raw)
		if !want[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return len(seen) == len(want)
}

func busyAudit(bq BusyQuery, choice string, resp runnerwire.BusyResponse) *AuditEntry {
	ids := make([]string, 0, len(resp.Holders))
	for _, h := range resp.Holders {
		ids = append(ids, h.RunID)
	}
	target := bq.NodeID
	if target == "" {
		target = bq.RepoKey
	}
	return &AuditEntry{
		Project: bq.Project,
		ActorID: bq.ActorID,
		Action:  "run.busy",
		Target:  target,
		Detail: map[string]any{
			"choice":  choice,
			"holders": ids,
			"others":  resp.Others,
		},
	}
}

// applyBusy checks for active holders inside tx and decides whether to proceed,
// skip, refuse BUSY, or stop superseded holders after commit (§C.2.3).
func (d *Dispatcher) applyBusy(ctx context.Context, tx *sql.Tx, br BusyRequest) (BusyOutcome, error) {
	if !runnerwire.ValidOnBusy(br.OnBusy, br.Group) {
		return BusyOutcome{}, invalidField("onBusy", "onBusy is not a valid busy choice", "onBusy 不是有效的忙碌選項")
	}
	if !runnerwire.ValidHolderIDs(br.HolderIDs) {
		return BusyOutcome{}, invalidField("holderIds", "holderIds is not valid", "holderIds 無效")
	}

	holders, err := d.holders(ctx, tx, br.Query)
	if err != nil {
		return BusyOutcome{}, err
	}
	if len(holders) == 0 {
		return BusyOutcome{Proceed: true}, nil
	}

	resp := d.buildBusyResponse(ctx, tx, br, holders)
	switch br.OnBusy {
	case runnerwire.OnBusyAsk:
		return BusyOutcome{}, busyError(resp)

	case runnerwire.OnBusySkip:
		if !br.Group {
			return BusyOutcome{}, invalidField("onBusy", "skip is only for group starts", "skip 只用於群組啟動")
		}
		return BusyOutcome{Proceed: false}, nil

	case runnerwire.OnBusyBoth:
		if !slices.Contains(resp.Choices, runnerwire.OnBusyBoth) {
			return BusyOutcome{}, invalidField("onBusy",
				"update nodevas on this computer to run both or wait in line",
				"請更新這台電腦上的 nodevas，才能同時執行或排隊")
		}
		return BusyOutcome{
			Proceed: true,
			Choice:  runnerwire.OnBusyBoth,
			Audit:   busyAudit(br.Query, runnerwire.OnBusyBoth, resp),
		}, nil

	case runnerwire.OnBusyQueue:
		// The autopilot always asks to queue. On an older runner (no
		// CapCoord) a person is offered only take-over, but an automatic
		// start must not fail where it used to start: it waits behind the
		// newest holder like any queued run. Waiting is the hub's alone; the
		// runner sees an ordinary offer once the holder has ended.
		if !slices.Contains(resp.Choices, runnerwire.OnBusyQueue) && !(br.Automatic && !br.Query.Global && !br.Group) {
			return BusyOutcome{}, invalidField("onBusy",
				"update nodevas on this computer to run both or wait in line",
				"請更新這台電腦上的 nodevas，才能同時執行或排隊")
		}
		return BusyOutcome{
			Proceed:  true,
			AfterRun: holders[len(holders)-1].Run.ID,
			Choice:   runnerwire.OnBusyQueue,
			Audit:    busyAudit(br.Query, runnerwire.OnBusyQueue, resp),
		}, nil

	case runnerwire.OnBusyTakeOver:
		if resp.Others > 0 {
			return BusyOutcome{}, notYoursToStopError()
		}
		for _, h := range resp.Holders {
			if !h.MayStop {
				return BusyOutcome{}, notYoursToStopError()
			}
		}
		if len(holders) > runnerwire.MaxHolderIDs || !holderIDsMatch(br.HolderIDs, resp.Holders) {
			return BusyOutcome{}, busyError(resp)
		}
		stop := make([]Holder, 0, len(holders))
		for _, h := range holders {
			if !h.Hidden {
				stop = append(stop, h)
			}
		}
		return BusyOutcome{
			Proceed:  true,
			AfterRun: holders[len(holders)-1].Run.ID,
			Choice:   runnerwire.OnBusyTakeOver,
			Stop:     stop,
			Audit:    busyAudit(br.Query, runnerwire.OnBusyTakeOver, resp),
		}, nil

	default:
		return BusyOutcome{}, invalidField("onBusy", "onBusy is not a valid busy choice", "onBusy 不是有效的忙碌選項")
	}
}

// afterBusy stops superseded holders and records busy/takeover audit entries
// after the start transaction commits (§C.2.3).
func (d *Dispatcher) afterBusy(ctx context.Context, actor identity.Actor, o BusyOutcome) {
	actorName := actor.Name
	if actorName == "" {
		actorName = actor.ID
	}
	if o.Audit != nil {
		entry := *o.Audit
		if entry.ActorID == "" {
			entry.ActorID = actor.ID
		}
		if entry.ActorName == "" {
			entry.ActorName = actorName
		}
		d.record(entry)
	}
	if len(o.Stop) == 0 {
		return
	}

	stopped := make([]string, 0, len(o.Stop))
	seenPipelines := map[string]bool{}
	reason := "taken over by " + actorName
	project := ""
	for _, h := range o.Stop {
		cur, err := d.store.Get(ctx, h.Run.ID)
		if err != nil {
			cur = h.Run
		}
		if project == "" && !cur.Global {
			project = cur.Project
		}
		stopped = append(stopped, cur.ID)

		// Running interactive run the actor may End -> graceful End (with noPR on node runs).
		if cur.Status == runnerwire.RunRunning && cur.Mode == runnerwire.ModeInteractive && MayEnd(actor, cur) {
			if _, err := d.End(ctx, cur.ID, actor, !cur.Global); err == nil {
				continue
			} else if !errors.Is(err, ErrEndOutdated) && !errors.Is(err, ErrPrecondition) {
				slog.Warn("runs: takeover end", slog.String("run", cur.ID), slog.Any("error", err))
			}
		}

		// Otherwise Cancel, plus CancelPipeline when the holder belongs to a pipeline.
		if cur.PipelineID != "" && !seenPipelines[cur.PipelineID] {
			seenPipelines[cur.PipelineID] = true
			if err := d.CancelPipeline(ctx, cur.PipelineID, reason); err != nil {
				slog.Warn("runs: takeover cancel pipeline", slog.String("pipeline", cur.PipelineID), slog.Any("error", err))
			}
		}
		if !runnerwire.RunFinal(cur.Status) {
			if _, err := d.Cancel(ctx, cur.ID, reason); err != nil {
				slog.Warn("runs: takeover cancel", slog.String("run", cur.ID), slog.Any("error", err))
			}
		}
	}

	target := o.AfterRun
	if target == "" && len(stopped) > 0 {
		target = stopped[len(stopped)-1]
	}
	d.record(AuditEntry{
		Project:   project,
		ActorID:   actor.ID,
		ActorName: actorName,
		Action:    "run.takeover",
		Target:    target,
		Detail:    map[string]any{"stopped": stopped},
	})
}

// continueBusy delegates onBusy=="continue" on a global start to G1's FollowUp
// with QueuedBehind: true, before any busy transaction is opened (§C.2.3).
func (d *Dispatcher) continueBusy(ctx context.Context, req GlobalStartRequest) (Record, error) {
	body := req.Body
	runner, err := d.reg.Get(ctx, body.RunnerID)
	if err != nil || runner.OwnerID != req.ActorID {
		return Record{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}
	_, repo, problem := CheckAgainstHello(runner.Info, runnerwire.CreateRunRequest{
		Agent: body.Agent, Model: body.Model, RepoID: body.RepoID,
	})
	if problem != nil {
		return Record{}, refused(StartInvalid, problem)
	}
	repoKey := runnerwire.RepoKey(runner.ID, repo)
	bq := BusyQuery{
		ActorID:  req.ActorID,
		RepoKey:  repoKey,
		Global:   true,
		RunnerID: runner.ID,
		RepoID:   repo.ID,
	}
	holders, err := d.holders(ctx, d.store.db, bq)
	if err != nil {
		return Record{}, err
	}
	if len(holders) == 0 {
		return Record{}, invalidField("onBusy", "there is no active run to continue", "目前沒有可接續的執行")
	}
	resp := d.buildBusyResponse(ctx, d.store.db, BusyRequest{
		Query:      bq,
		OnBusy:     body.OnBusy,
		HolderIDs:  body.HolderIDs,
		RunnerCaps: runner.Info.Capabilities,
	}, holders)
	if resp.Continue == nil || !holderIDsMatch(body.HolderIDs, resp.Holders) {
		return Record{}, busyError(resp)
	}

	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	rec, err := d.FollowUp(ctx, FollowUpRequest{
		PriorID: resp.Continue.RunID,
		Body: runnerwire.FollowUpRequest{
			Message: body.Task,
			Mode:    body.Mode,
		},
		Actor:        identity.Actor{ID: req.ActorID, Name: name},
		ActorID:      req.ActorID,
		ActorName:    name,
		SteppedUpAt:  req.SteppedUpAt,
		Simple:       body.Simple,
		QueuedBehind: true,
	})
	if err != nil {
		return Record{}, err
	}
	entry := busyAudit(bq, runnerwire.OnBusyContinue, resp)
	entry.ActorName = name
	d.record(*entry)
	return rec, nil
}

// waitsBehind reports whether a queued busy run must still wait for an active
// or older holder (§C.2.3). Strict lineage-age order guarantees no cycle.
func (d *Dispatcher) waitsBehind(ctx context.Context, r Record) bool {
	if d == nil || d.store == nil || r.Status != runnerwire.RunQueued {
		return false
	}
	if r.BusyChoice != runnerwire.OnBusyQueue && r.BusyChoice != runnerwire.OnBusyTakeOver {
		return false
	}
	if !isWritingKind(r.Kind, !r.Global) {
		return false
	}

	bq := BusyQuery{
		ActorID: r.CreatedBy,
		Project: r.Project,
		NodeID:  r.NodeID,
		RepoKey: r.RepoKey,
		Global:  r.Global,
		Exclude: Lineage{
			PipelineID: r.PipelineID,
			WorkRoot:   workRootOf(r),
			GroupID:    r.GroupID,
			ContestID:  r.ContestID,
		},
	}
	records, err := d.store.busyHolders(ctx, d.store.db, bq)
	if err != nil {
		slog.Warn("runs: waitsBehind holders", slog.String("run", r.ID), slog.Any("error", err))
		return false
	}
	rKey := d.store.lineageKeyOf(ctx, d.store.db, r)
	for _, h := range records {
		if h.ID == r.ID {
			continue
		}
		hKey := d.store.lineageKeyOf(ctx, d.store.db, h)
		if hKey.rootID == rKey.rootID {
			continue
		}
		// Offered or running holder always blocks.
		if h.Status == runnerwire.RunOffered || h.Status == runnerwire.RunRunning {
			return true
		}
		// Queued holder blocks only if its lineage started strictly before r's.
		if h.Status == runnerwire.RunQueued && hKey.before(rKey) {
			return true
		}
		// A final representative of a running pipeline (between steps) is handled below.
	}
	if !r.Global {
		heldByPipe, err := d.store.runningPipelinesBefore(ctx, d.store.db, r.Project, r.NodeID, r.PipelineID, rKey)
		if err != nil {
			slog.Warn("runs: waitsBehind pipelines", slog.String("run", r.ID), slog.Any("error", err))
			return false
		}
		if heldByPipe {
			return true
		}
	}
	return false
}

// heldByOther reports whether a queued writing pipeline/handoff run is held by
// an active (offered or running) writing run of another lineage on its node (§C.2.3).
func (d *Dispatcher) heldByOther(ctx context.Context, r Record) bool {
	if d == nil || d.store == nil || r.Global || r.Status != runnerwire.RunQueued {
		return false
	}
	if r.Origin != runnerwire.OriginPipeline && r.Origin != runnerwire.OriginHandoff {
		return false
	}
	if !isWritingKind(r.Kind, true) {
		return false
	}
	held, err := d.store.activeOtherLineageOnNode(ctx, r)
	if err != nil {
		slog.Warn("runs: heldByOther", slog.String("run", r.ID), slog.Any("error", err))
		return false
	}
	return held
}

// coordJob appends carry-over context for runs with after_run and sets
// Job.Coordinated at offer time (§C.2.3, §C.2.5).
func (d *Dispatcher) coordJob(ctx context.Context, r Record, job *runnerwire.Job) {
	if d == nil || d.store == nil || job == nil {
		return
	}
	if r.AfterRun != "" {
		if prior, err := d.store.Get(ctx, r.AfterRun); err == nil {
			own := r.CreatedBy != "" && (prior.CreatedBy == r.CreatedBy || prior.OwnerID == r.CreatedBy)
			if section := afterSection(prior, own); section != "" {
				room := runnerwire.MaxPromptBytes - len(section)
				if room < 0 {
					section = Truncate(section, runnerwire.MaxPromptBytes)
					job.Prompt = section
				} else if len(job.Prompt) > room {
					job.Prompt = Truncate(job.Prompt, room) + section
				} else {
					job.Prompt += section
				}
			}
		}
	}

	if !r.Global {
		switch {
		case r.ContestID != "":
			job.Coordinated = true
		case r.BusyChoice == runnerwire.OnBusyBoth:
			job.Coordinated = true
		case r.BusyChoice == runnerwire.OnBusyQueue || r.BusyChoice == runnerwire.OnBusyTakeOver:
			if d.nodeDoneOrClaimed(ctx, r.Project, r.NodeID) {
				job.Coordinated = true
			}
		}
	}
}

// afterSection builds the carry-over prompt note for a run queued behind prior.
// Only the actor's own prior shares summary/branch/PR details (§C.2.3, §C.10).
func afterSection(prior Record, own bool) string {
	var b strings.Builder
	b.WriteString("\n\n## Previous run\n")
	if !own {
		b.WriteString(hiddenAfterNotice)
		return b.String()
	}
	var wrote bool
	if branch := strings.TrimSpace(prior.Branch); branch != "" {
		b.WriteString("Branch: " + branch + "\n")
		wrote = true
	}
	if prURL := strings.TrimSpace(prior.PRURL); prURL != "" {
		b.WriteString("Pull request: " + prURL + "\n")
		wrote = true
	}
	if summary := strings.TrimSpace(redact.Output(prior.Summary)); summary != "" {
		b.WriteString("Summary:\n" + Truncate(summary, maxAfterSummaryBytes) + "\n")
		wrote = true
	}
	if !wrote {
		b.WriteString("Your earlier run finished before this one.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// busyEnded releases board claims (when stopped by take-over or waited on) and
// pumps other runners whose queued busy runs can now proceed (§C.2.3, §C.2.5).
func (d *Dispatcher) busyEnded(ctx context.Context, r Record) {
	if d == nil || d.store == nil {
		return
	}
	runnersToPump, waitingOnNode, err := d.store.waitingRunnersFor(ctx, r)
	if err != nil {
		slog.Warn("runs: busyEnded lookup", slog.String("run", r.ID), slog.Any("error", err))
		return
	}
	takenOver := false
	if !r.Global && r.NodeID != "" && !waitingOnNode {
		if takenOver, err = d.store.takenOver(ctx, r); err != nil {
			slog.Warn("runs: busyEnded take-over lookup", slog.String("run", r.ID), slog.Any("error", err))
		}
	}
	if !r.Global && r.NodeID != "" && (takenOver || waitingOnNode) {
		d.releaseClaim(ctx, r.Project, r.NodeID, r.ID)
	}
	for _, runnerID := range runnersToPump {
		// The caller of afterEnd pumps r.RunnerID itself (Finish, Cancel, or
		// Pump after a budget cancel). No caller holds a runner lock here:
		// Pump cancels only after releasing its own (dispatcher.go).
		if runnerID == r.RunnerID {
			continue
		}
		d.Pump(ctx, runnerID)
	}
}

// releaseWaiting pumps runners that have queued coordinated runs whose wait has
// ended, closing the restart gap between a holder ending and Pump (§C.2.3).
func (d *Dispatcher) releaseWaiting(ctx context.Context) {
	if d == nil || d.store == nil {
		return
	}
	queued, err := d.store.queuedCoordinatedRuns(ctx)
	if err != nil {
		slog.Warn("runs: releaseWaiting", slog.Any("error", err))
		return
	}
	pumped := map[string]bool{}
	for _, r := range queued {
		if pumped[r.RunnerID] {
			continue
		}
		if d.waitsBehind(ctx, r) || d.heldByOther(ctx, r) {
			continue
		}
		pumped[r.RunnerID] = true
		d.Pump(ctx, r.RunnerID)
	}
}
