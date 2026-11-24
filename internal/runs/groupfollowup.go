package runs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// GroupFollowUpRequest is a follow-up message sent to eligible latest member
// turns of a multi-repo global run group (§10.2).
type GroupFollowUpRequest struct {
	GroupID            string
	Body               runnerwire.GroupFollowUpRequest
	Actor              identity.Actor
	ActorID, ActorName string
	IsAgent            bool
	SteppedUpAt        string
	Simple             bool
}

// GroupFollowUpResult holds the follow-up runs started for a group and the
// eligible latest turns skipped with their refusal codes.
type GroupFollowUpResult struct {
	Started []Record
	Skipped []runnerwire.GroupFollowUpSkipped
}

// FollowUpGroup sends a background follow-up to each eligible latest member
// turn of groupID (§10.2). Non-latest turns and withdrawn continuations are
// ignored; per-member follow-up refusals (e.g. WORK_GONE, PR_ENDED, RUN_LIVE,
// BUDGET_EXCEEDED) are returned in Skipped while remaining members continue.
func (d *Dispatcher) FollowUpGroup(ctx context.Context, req GroupFollowUpRequest) (GroupFollowUpResult, error) {
	actorID := req.ActorID
	if actorID == "" {
		actorID = req.Actor.ID
	}
	actorName := req.ActorName
	if actorName == "" {
		actorName = req.Actor.Name
	}
	isAgent := req.IsAgent || req.Actor.IsAgent()

	if isAgent {
		return GroupFollowUpResult{}, stageRefusal("PERMISSION_DENIED",
			locale.Errorf("agents cannot use global agents", "agent 不能使用全域 agent"))
	}
	g, err := d.store.GetGroup(ctx, req.GroupID)
	if errors.Is(err, ErrNotFound) || req.GroupID == "" {
		said := locale.Errorf("group not found", "找不到群組")
		return GroupFollowUpResult{}, &StartError{Code: StartNotFound, What: "group", Msg: said.Error(), said: said}
	}
	if err != nil {
		return GroupFollowUpResult{}, err
	}

	// Own-runner-only and creator-only, matching FollowUp (§6.3, §14).
	if actorID == "" || actorID != g.CreatedBy || actorID != g.OwnerID {
		said := locale.Errorf("group not found", "找不到群組")
		return GroupFollowUpResult{}, &StartError{Code: StartNotFound, What: "group", Msg: said.Error(), said: said}
	}
	runner, err := d.reg.Get(ctx, g.RunnerID)
	if err != nil || runner.OwnerID != actorID {
		return GroupFollowUpResult{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found", said: errRunnerNotFound}
	}

	msg := strings.TrimSpace(req.Body.Message)
	switch {
	case msg == "":
		return GroupFollowUpResult{}, invalidField("message", "message is required", "請填寫訊息")
	case len(msg) > runnerwire.MaxFollowUpBytes:
		return GroupFollowUpResult{}, invalidField("message", "message is over %d bytes", "訊息超過 %d 位元組", runnerwire.MaxFollowUpBytes)
	case !utf8.ValidString(msg) || strings.ContainsRune(msg, 0):
		return GroupFollowUpResult{}, invalidField("message", "message must be valid text", "訊息須為有效的文字")
	}
	only := strings.TrimSpace(req.Body.Only)
	if only == "" {
		only = "all"
	}
	if only != "all" && only != "failed" {
		return GroupFollowUpResult{}, invalidField("only", `only must be "all" or "failed"`, `only 須為 "all" 或 "failed"`)
	}

	// Every member of a group lives on g.RunnerID, so check runner caps up front.
	if !Headless(runner.Info) {
		return GroupFollowUpResult{}, refuse(StartPrecondition, "this runner does not take runs; update nodevas on it", "這個 runner 不接受執行；請更新它的 nodevas")
	}
	if !slices.Contains(runner.Info.Capabilities, runnerwire.CapGlobalRuns) ||
		!slices.Contains(runner.Info.Capabilities, runnerwire.CapContinue) {
		said := locale.Errorf("this runner cannot continue work; update nodevas on it", "這個 runner 不能接續工作；請更新它的 nodevas")
		return GroupFollowUpResult{}, &StartError{Code: StartOutdated, Reason: CodeRunnerOutdated, Msg: said.Error(), said: said}
	}
	if !slices.Contains(runner.Info.Capabilities, runnerwire.CapGlobalRunsOn) {
		return GroupFollowUpResult{}, stageRefusal(CodeGlobalRunsOff, locale.Errorf(
			"the runner's owner turned global agents off on it (nodevas runner set globalRuns on)",
			"runner 的擁有者在該機關閉了全域 agent（nodevas runner set globalRuns on）"))
	}

	members, err := d.store.GroupRuns(ctx, g.ID)
	if err != nil {
		return GroupFollowUpResult{}, err
	}
	res := GroupFollowUpResult{
		Started: []Record{},
		Skipped: []runnerwire.GroupFollowUpSkipped{},
	}
	for _, r := range members {
		// Withdrawn follow-ups and superseded turns are never latest turns.
		if r.ContinueOf != "" && IsWithdrawn(r) {
			continue
		}
		if r.ContinuedBy != "" {
			continue
		}
		if only == "failed" && r.Status != runnerwire.RunFailed {
			continue
		}
		next, err := d.FollowUp(ctx, FollowUpRequest{
			PriorID:     r.ID,
			Body:        runnerwire.FollowUpRequest{Message: msg, Mode: runnerwire.ModeHeadless},
			Actor:       req.Actor,
			ActorID:     actorID,
			ActorName:   actorName,
			IsAgent:     false,
			SteppedUpAt: req.SteppedUpAt,
			Simple:      req.Simple,
		})
		if err != nil {
			var refused *StartError
			if errors.As(err, &refused) {
				res.Skipped = append(res.Skipped, runnerwire.GroupFollowUpSkipped{
					RunID: r.ID,
					Code:  groupFollowUpSkipCode(refused),
				})
				continue
			}
			return res, err
		}
		res.Started = append(res.Started, next)
	}
	return res, nil
}

func groupFollowUpSkipCode(se *StartError) string {
	if se.Reason != "" {
		return se.Reason
	}
	switch se.Code {
	case StartBudget:
		return "BUDGET_EXCEEDED"
	case StartOutdated:
		return CodeRunnerOutdated
	case StartPrecondition:
		return "FAILED_PRECONDITION"
	case StartNotFound:
		return "NOT_FOUND"
	default:
		return "INVALID_ARGUMENT"
	}
}
