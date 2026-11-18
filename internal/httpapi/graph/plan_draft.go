package graph

import (
	"net/http"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// P6: a plan run the copilot started as a draft does not write its
// create_subgraph to the board; the server records it for the person
// instead (internal/server capturePlanDraft). This package only routes the
// request there and answers; it knows nothing of runs.

// PlanDraftResult is a draft plan run's create_subgraph, recorded: this
// call's keys mapped to "draft:<key>", the check's issues, and how many nodes
// the draft holds now.
type PlanDraftResult struct {
	IDs    map[string]string
	Issues []engine.Issue
	Nodes  int
}

// PlanDraftCapture records a draft plan run's create_subgraph instead of
// writing it. handled false: not a draft run's request, write as usual.
// handled true with err: the refusal, answered as a create's (store errors)
// or as the *PlanDraftRefusal says.
type PlanDraftCapture func(r *http.Request, st *store.Store, spec store.SubgraphSpec) (PlanDraftResult, bool, error)

// PlanDraftRefusal is a refusal only a draft has.
type PlanDraftRefusal struct {
	Status int
	Code   string
	Field  string // extras {"field": Field} when set
	Err    error  // locale.Errorf, both languages
}

func (e *PlanDraftRefusal) Error() string { return e.Err.Error() }
func (e *PlanDraftRefusal) Unwrap() error { return e.Err }

// PlanDraftNote is what the agent reads with a recorded draft.
const PlanDraftNote = "Recorded as a draft for the person to review; nothing was created yet. Do not link to these ids."

// SetPlanDraft routes a draft plan run's create_subgraph to capture.
func (a *API) SetPlanDraft(capture PlanDraftCapture) *API {
	a.planDraft = capture
	return a
}

func fieldExtras(field string) map[string]any {
	if field == "" {
		return nil
	}
	return map[string]any{"field": field}
}
