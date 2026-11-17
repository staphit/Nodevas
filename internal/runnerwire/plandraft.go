package runnerwire

// PlanDraft is what a draft plan run's create_subgraph calls recorded on the
// hub instead of writing (runs.Store.UpdatePlanDraft), in first-given order.
type PlanDraft struct {
	Calls int             `json:"calls"`
	Nodes []PlanDraftNode `json:"nodes"`
}

// PlanDraftNode is store.SubgraphNode without assignee, deadline and
// writeAccess: the card neither shows nor edits them, so the person's Create
// must not carry them. A body's YAML front matter would set assignee,
// requires, effects, priority and tags all the same (store
// hydrateNodeFromMeta), so a body that opens with a front-matter delimiter is
// refused at capture (server capturePlanDraft) and stripped again when the
// draft is built (copilot planDraftBody).
type PlanDraftNode struct {
	Key        string   `json:"key"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind,omitempty"`
	Body       string   `json:"body,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	DependsOn  []string `json:"dependsOn,omitempty"`
	Refs       []string `json:"refs,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
}

const (
	// MaxPlanDraftNodes is the store's per-call cap: the merged draft is
	// checked as one call.
	MaxPlanDraftNodes = 100
	// MaxPlanDraftCalls is how many create_subgraph calls one draft run may
	// have recorded.
	MaxPlanDraftCalls = 8
	// MaxPlanDraftBytes caps the encoded draft; bodies are already at most
	// 1 MiB by the check.
	MaxPlanDraftBytes = 2 << 20
	// PlanDraftIDPrefix is the provisional id the hub answers per key.
	PlanDraftIDPrefix = "draft:"
)
