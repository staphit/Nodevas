// P6: a plan run the copilot started as a draft (runs.Record.Draft) writes
// nothing to the board. Its create_subgraph is checked as if it were made and
// recorded for the person (run_plan_drafts); every other graph or node write
// it tries is refused, so the plan reaches the person whole, as one draft.

package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/graph"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// CodePlanRunDrafts refuses a draft plan run's board write other than its
// create_subgraph.
const CodePlanRunDrafts = "PLAN_RUN_DRAFTS"

var errPlanRunDrafts = locale.Errorf(
	"this plan run is a draft: nothing is written to the board. Put every task in one create_subgraph call "+
		"(keys, dependsOn, tags, acceptance); do not create, link, edit, claim or set acceptance on nodes one by "+
		"one. The person reviews the draft and creates it, and the goal is linked then",
	"這次計畫執行是草稿：不會寫入看板。請把所有任務放進一次 create_subgraph 呼叫（key、dependsOn、標籤、驗收條件）；"+
		"不要逐一建立、連結、編輯、認領節點或設定驗收條件。由使用者檢查草稿後建立，屆時會連到目標")

// planDraftRefuses: a graph or node write from a draft run, other than the
// create_subgraph it records. Prefix-based on purpose: a write route added to
// the proxy allowlist later is refused by default.
func planDraftRefuses(method, target string) bool {
	if method == http.MethodGet || (method == http.MethodPost && target == "/api/graph/subgraph") {
		return false
	}
	return target == "/api/nodes" || strings.HasPrefix(target, "/api/nodes/") || strings.HasPrefix(target, "/api/graph/")
}

// capturePlanDraft is graph.PlanDraftCapture (wired in routes.go): a draft
// plan run's create_subgraph, checked as the run's agent and recorded on the
// run instead of written. Only the context marker serveRunnerProxy set says
// whether the request is such a run's; nothing the agent sends does.
func (s *Server) capturePlanDraft(r *http.Request, st *store.Store, spec store.SubgraphSpec) (graph.PlanDraftResult, bool, error) {
	proxied, ok := runnerProxiedFrom(r.Context())
	if !ok || !proxied.draft || s.runs == nil {
		return graph.PlanDraftResult{}, false, nil
	}
	if st == nil || runsapi.ProjectName(s.pm, st) != proxied.runProject {
		return graph.PlanDraftResult{}, true, &graph.PlanDraftRefusal{Status: http.StatusForbidden, Code: "PERMISSION_DENIED",
			Err: locale.Errorf("a draft plan run drafts for its own project only", "草稿計畫執行只能替自己的專案草擬")}
	}
	// The goal is linked when the person creates the draft; an edge to it is
	// dropped, any other would make an existing node wait for a new one.
	for i, edge := range spec.Edges {
		if edge.To != proxied.goalID {
			return graph.PlanDraftResult{}, true, &graph.PlanDraftRefusal{Status: http.StatusBadRequest, Code: "INVALID_SUBGRAPH",
				Field: fmt.Sprintf("edges[%d].to", i),
				Err: locale.Errorf("a draft plan cannot make existing nodes wait for new ones; leave edges out (the goal is linked when the person creates the draft)",
					"草稿計畫不能讓既有節點等待新節點；請不要給 edges（使用者建立草稿時會連到目標）")}
		}
	}
	// A body's front matter would set fields the card never shows
	// (assignee, requires, effects, priority, tags). Checked on the trimmed
	// body: the draft keeps the trimmed body, so what the store reads as
	// text now would be front matter at Create.
	for i, node := range spec.Nodes {
		if engine.OpensFrontMatter(node.Body) {
			return graph.PlanDraftResult{}, true, &graph.PlanDraftRefusal{Status: http.StatusBadRequest, Code: "INVALID_SUBGRAPH",
				Field: fmt.Sprintf("nodes[%d].body", i),
				Err: locale.Errorf("no front matter in a draft plan: put the fields in the node (title, kind, tags, priority, acceptance) and start the body with text",
					"草稿計畫的內容不能有前置資料：請把欄位放在節點上（title、kind、tags、priority、acceptance），內容以文字開頭")}
		}
	}
	actor := auth.ActorFrom(r)
	if len(spec.Nodes) == 0 {
		// Refused as a create would be, and no call spent on it.
		_, err := st.CheckSubgraph(actor, store.SubgraphSpec{})
		return graph.PlanDraftResult{}, true, err
	}
	add := planDraftNodes(spec.Nodes)
	var result graph.PlanDraftResult
	// refused: the error came from the update (the draft's or the check's
	// refusal), not from storing it.
	refused := false
	err := s.runs.Store().UpdatePlanDraft(r.Context(), proxied.runID, func(held runnerwire.PlanDraft) (runnerwire.PlanDraft, error) {
		refused = true
		if held.Calls >= runnerwire.MaxPlanDraftCalls {
			return held, &graph.PlanDraftRefusal{Status: http.StatusRequestEntityTooLarge, Code: "SUBGRAPH_TOO_LARGE",
				Err: locale.Errorf("a draft plan run takes at most %d create_subgraph calls; send the whole plan in one call",
					"草稿計畫執行最多 %d 次 create_subgraph 呼叫；請一次送出整個計畫", runnerwire.MaxPlanDraftCalls)}
		}
		merged := runs.MergePlanDraft(held, add)
		checked, err := st.CheckSubgraph(actor, planDraftSpec(merged, add))
		if err != nil {
			return held, err
		}
		encoded, err := json.Marshal(merged)
		if err != nil {
			return held, err
		}
		if len(encoded) > runnerwire.MaxPlanDraftBytes {
			return held, &graph.PlanDraftRefusal{Status: http.StatusRequestEntityTooLarge, Code: "SUBGRAPH_TOO_LARGE",
				Err: locale.Errorf("the draft plan is over %d MiB; send shorter bodies",
					"草稿計畫超過 %d MiB；請縮短內容", runnerwire.MaxPlanDraftBytes>>20)}
		}
		result = graph.PlanDraftResult{IDs: make(map[string]string, len(add)), Issues: checked.Issues, Nodes: len(merged.Nodes)}
		for _, n := range add {
			result.IDs[n.Key] = runnerwire.PlanDraftIDPrefix + n.Key
		}
		if result.Issues == nil {
			result.Issues = []engine.Issue{}
		}
		refused = false
		return merged, nil
	})
	if err != nil {
		if refused {
			return graph.PlanDraftResult{}, true, err
		}
		slog.Warn("plan draft: record", slog.String("run", proxied.runID), slog.Any("error", err))
		return graph.PlanDraftResult{}, true, &graph.PlanDraftRefusal{Status: http.StatusServiceUnavailable, Code: "PLAN_DRAFT_UNAVAILABLE",
			Err: locale.Errorf("the draft could not be recorded; try again", "無法記錄草稿；請再試一次")}
	}
	return result, true, nil
}

// planDraftNodes is the call's nodes as a draft keeps them: no assignee,
// deadline or write access (the card neither shows nor edits them).
func planDraftNodes(nodes []store.SubgraphNode) []runnerwire.PlanDraftNode {
	out := make([]runnerwire.PlanDraftNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, runnerwire.PlanDraftNode{
			Key: n.Key, Title: n.Title, Kind: n.Kind, Body: n.Body, Priority: n.Priority,
			Tags: n.Tags, DependsOn: n.DependsOn, Refs: n.Refs, Acceptance: n.Acceptance,
		})
	}
	return out
}

// planDraftSpec is the merged draft as one create_subgraph, for the check:
// this call's nodes first, in the call's order (as merged, so "draft:<key>"
// targets are keys again; a key given twice stays twice, so the check
// refuses it as a create would), then the held nodes it did not replace. So
// nodes[i] in a refusal is the agent's own index.
func planDraftSpec(merged runnerwire.PlanDraft, add []runnerwire.PlanDraftNode) store.SubgraphSpec {
	byKey := make(map[string]runnerwire.PlanDraftNode, len(merged.Nodes))
	for _, n := range merged.Nodes {
		byKey[n.Key] = n
	}
	inCall := make(map[string]bool, len(add))
	spec := store.SubgraphSpec{Nodes: make([]store.SubgraphNode, 0, len(merged.Nodes)+len(add))}
	for _, n := range add {
		inCall[n.Key] = true
		spec.Nodes = append(spec.Nodes, subgraphNodeOf(byKey[n.Key]))
	}
	for _, n := range merged.Nodes {
		if !inCall[n.Key] {
			spec.Nodes = append(spec.Nodes, subgraphNodeOf(n))
		}
	}
	return spec
}

func subgraphNodeOf(n runnerwire.PlanDraftNode) store.SubgraphNode {
	return store.SubgraphNode{
		Key: n.Key, Title: n.Title, Kind: n.Kind, Body: n.Body, Priority: n.Priority,
		Tags: n.Tags, DependsOn: n.DependsOn, Refs: n.Refs, Acceptance: n.Acceptance,
	}
}
