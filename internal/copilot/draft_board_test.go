package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	nodevasmcp "nodevas/internal/mcp"
)

// boardBridge answers get_graph_outline and get_node from a fixture board,
// per input, the way the MCP tools do.
type boardBridge struct {
	mu      sync.Mutex
	outline nodevasmcp.Outline
	// whole, when false, answers an unfiltered outline with total only
	// (a board too large to list whole) and filtered ones by status.
	whole bool
	nodes map[string]nodevasmcp.OutlineNode
	calls []string
}

func (b *boardBridge) Tools() []toolbridge.Tool { return nil }
func (b *boardBridge) Close() error             { return nil }

func (b *boardBridge) Call(_ context.Context, name string, input json.RawMessage) (toolbridge.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var in struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(input, &in)
	b.calls = append(b.calls, name+":"+in.ID+in.Status)
	switch name {
	case "get_graph_outline":
		out := b.outline
		if !b.whole {
			out.Nodes, out.Edges = nil, nil
			if in.Status != "" {
				for _, node := range b.outline.Nodes {
					if node.Status == in.Status {
						out.Nodes = append(out.Nodes, node)
					}
				}
			}
		}
		raw, _ := json.Marshal(out)
		return toolbridge.Result{Output: string(raw)}, nil
	case "get_node":
		node, ok := b.nodes[in.ID]
		if !ok {
			return toolbridge.Result{Output: "not found", IsError: true}, nil
		}
		var up, down []string
		for _, edge := range b.outline.Edges {
			if edge.To == in.ID {
				up = append(up, edge.From)
			}
			if edge.From == in.ID {
				down = append(down, edge.To)
			}
		}
		raw, _ := json.Marshal(map[string]any{"id": node.ID, "title": node.Title, "kind": node.Kind,
			"status": node.Status, "upstream": up, "downstream": down})
		return toolbridge.Result{Output: string(raw)}, nil
	}
	return toolbridge.Result{Output: "unknown", IsError: true}, nil
}

// bigBoard is n nodes in a chain, n-0 → n-1 → …, every third one done; the
// selected node sits in the middle, far past the listing cap in outline
// order.
func bigBoard(n int) *boardBridge {
	b := &boardBridge{whole: true, nodes: map[string]nodevasmcp.OutlineNode{}}
	b.outline.Layout = "workflow"
	b.outline.Total = n
	for i := 0; i < n; i++ {
		status := "ready"
		if i%3 == 0 {
			status = "done"
		}
		node := nodevasmcp.OutlineNode{ID: fmt.Sprintf("n-%d", i), Title: fmt.Sprintf("step %d", i), Kind: "task", Status: status}
		b.outline.Nodes = append(b.outline.Nodes, node)
		b.nodes[node.ID] = node
		if i > 0 {
			b.outline.Edges = append(b.outline.Edges, nodevasmcp.OutlineEdge{From: fmt.Sprintf("n-%d", i-1), To: node.ID})
		}
	}
	return b
}

func listedIDs(c draftContext) []string {
	ids := make([]string, 0, len(c.Listed))
	for _, node := range c.Listed {
		ids = append(ids, node.ID)
	}
	return ids
}

// The listing is capped at 150 (30 for an on-device model) minus the
// selected nodes, puts the selection's neighbours first and open nodes
// before closed ones, and links only nodes it lists.
func TestReadBoardClipsTheListing(t *testing.T) {
	for _, small := range []bool{false, true} {
		b := bigBoard(260)
		selected := []string{"n-201"} // after n-200 (ready), before n-202 (ready); n-200's upstream n-199 is done
		c, board, err := readBoard(context.Background(), b, selected, small)
		if err != nil {
			t.Fatal(err)
		}
		limit := maxOutlineNodes
		if small {
			limit = smallOutlineNodes
		}
		if len(c.Selected) != 1 || len(c.Listed) != limit-1 {
			t.Fatalf("small=%v: selected %d, listed %d", small, len(c.Selected), len(c.Listed))
		}
		ids := listedIDs(c)
		if !slices.Contains(ids[:2], "n-200") || !slices.Contains(ids[:2], "n-202") {
			t.Errorf("small=%v: the selection's neighbours are not listed first: %v", small, ids[:5])
		}
		// After the neighbours, open nodes come before done ones.
		seenDone := false
		for _, node := range c.Listed[2:] {
			if node.Status == "done" {
				seenDone = true
			} else if seenDone {
				t.Fatalf("small=%v: open node %s listed after a done one", small, node.ID)
			}
		}
		for _, edge := range c.Edges {
			for _, id := range edge {
				if id != "n-201" && !slices.Contains(ids, id) {
					t.Errorf("small=%v: edge %v names an unlisted node", small, edge)
				}
			}
		}
		if !slices.ContainsFunc(c.Edges, func(e [2]string) bool { return e == [2]string{"n-202", "n-201"} }) {
			t.Errorf("small=%v: the selection's downstream link is missing: %v", small, c.Edges)
		}
		if _, known := board.known["n-200"]; !known || c.Total != 260 {
			t.Errorf("small=%v: known n-200 %v, total %d", small, known, c.Total)
		}
	}
}

// A board too large to list whole is read by open status, page by page;
// the selection's closed neighbour is read on its own so it can be linked.
func TestReadBoardPagesALargeBoardByStatus(t *testing.T) {
	b := bigBoard(30)
	b.whole = false
	c, board, err := readBoard(context.Background(), b, []string{"n-4"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var pages []string
	for _, call := range b.calls {
		if strings.HasPrefix(call, "get_graph_outline:") {
			pages = append(pages, strings.TrimPrefix(call, "get_graph_outline:"))
		}
	}
	if !slices.Equal(pages, []string{"", "ready", "in_progress", "started", "failed"}) {
		t.Fatalf("outline calls = %v", pages)
	}
	ids := listedIDs(c)
	if len(ids) == 0 || ids[0] != "n-3" || ids[1] != "n-5" {
		t.Fatalf("listed = %v", ids)
	}
	if _, known := board.known["n-3"]; !known {
		t.Fatal("the selection's done upstream node is not known")
	}
	for _, node := range c.Listed[2:] {
		if node.Status == "done" {
			t.Fatalf("a done node came from the open pages: %+v", node)
		}
	}
}

// The words name code (a path, a file, a function): the draft is marked as
// needing code knowledge whatever the model said.
func TestMentionsCode(t *testing.T) {
	for text, want := range map[string]bool{
		"Refactor internal/auth/session.go to rotate CSRF tokens": true,
		"fix the bug in session.go":                               true,
		"call rotateTokens() on login":                            true,
		"rename `userId` everywhere":                              true,
		"規劃產品上線：寫文件、測試、發布":                                        false,
		"Plan the product launch: write docs, test, release":      false,
		"寄喜帖、找攝影師":                                                false,
	} {
		if got := mentionsCode(text); got != want {
			t.Errorf("mentionsCode(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestDraftMarksCodeKnowledgeFromTheRequest(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answerJSON(goodDraft, provider.StopEndTurn, func(t *testing.T, req provider.Request) {
		// The drafter samples cool (C5): 0.2.
		if req.Temperature == nil || *req.Temperature != draftTemperature {
			t.Errorf("temperature = %v", req.Temperature)
		}
	})}
	request := draftTurn(conversation)
	request.Text = "Refactor internal/auth/session.go to rotate CSRF tokens"
	h.start(request).finish()
	if _, data := draftEntry(t, h, conversation); !data.Draft.NeedsCodeKnowledge {
		t.Fatal("a request naming a file is not marked as needing code knowledge")
	}
}

// On the browser's model a message is bounded; the person's instruction is
// never what gets cut: the previous draft's bodies and the board listing
// give way first.
func TestDraftUserMessageKeepsTheInstructionWithinABudget(t *testing.T) {
	previous := &Draft{Summary: "s"}
	for i := 0; i < 40; i++ {
		previous.Nodes = append(previous.Nodes, DraftNode{Key: fmt.Sprintf("n%d", i+1), Title: "步驟",
			Kind: "task", Body: strings.Repeat("說明", 300)})
	}
	listed := make([]boardNode, smallOutlineNodes)
	for i := range listed {
		listed[i] = boardNode{ID: fmt.Sprintf("n-%d", i), Title: strings.Repeat("標題", 60), Kind: "task", Status: "ready"}
	}
	instruction := "發布前加一個主管審查" + strings.Repeat("。", 100)
	message := draftUserMessage(draftContext{Text: instruction, Listed: listed, Previous: previous,
		MaxBytes: maxBrowserStructuredMessageBytes})
	if len(message) > maxBrowserStructuredMessageBytes {
		t.Fatalf("message is %d bytes, over %d", len(message), maxBrowserStructuredMessageBytes)
	}
	if !strings.HasSuffix(message, "Change it as the person asks:\n"+instruction+"\n") {
		t.Fatalf("the instruction was cut:\n…%s", message[max(0, len(message)-300):])
	}
	if !strings.Contains(message, `"key":"n40"`) || !strings.Contains(message, "DATA>>>") {
		t.Fatalf("the previous draft's nodes or the DATA block's end are gone")
	}
	// Without a budget nothing is trimmed.
	if whole := draftUserMessage(draftContext{Text: instruction, Listed: listed, Previous: previous}); len(whole) <= maxBrowserStructuredMessageBytes {
		t.Fatalf("the fixture is too small to test the budget: %d bytes", len(whole))
	}
}

// The browser host passes the drafter's message through whole: the drafter
// already budgeted it, and cutting its end would drop the instruction.
func TestBrowserDraftCarriesTheInstruction(t *testing.T) {
	h := browserHarness(t, nil)
	h.bridge.outputs = map[string]string{"get_graph_outline": draftOutline}
	conversation := h.conversation(alice, ModeDefault)
	previous := &Draft{Summary: "s"}
	for i := 0; i < 30; i++ {
		previous.Nodes = append(previous.Nodes, DraftNode{Key: fmt.Sprintf("n%d", i+1), Title: "步驟",
			Kind: "task", Body: strings.Repeat("說明", 280)})
	}
	request := browserTurn(conversation)
	request.Intent, request.Previous, request.Text = IntentDraft, previous, "發布前加一個主管審查"
	r := h.start(request)
	model := h.answerModel(t, r, conversation.ID, goodDraft)
	messages := model.ModelRequest.Messages
	if len(messages) != 1 || !strings.HasSuffix(messages[0].Content, "發布前加一個主管審查") ||
		len(messages[0].Content) > maxBrowserStructuredMessageBytes {
		t.Fatalf("browser draft message (%d bytes) ends …%q", len(messages[0].Content),
			messages[0].Content[max(0, len(messages[0].Content)-80):])
	}
	r.finish()
}

// What the P6 real-model run showed: an approval step typed task, and a
// listed step dropped.
func TestDraftPromptGatesApprovalsAndKeepsEveryListedStep(t *testing.T) {
	system := draftSystemPrompt(draftContext{})
	for _, want := range []string{"Every step the person listed becomes its own node", "approves", "signs off", `is "gate"`} {
		if !strings.Contains(system, want) {
			t.Errorf("draft prompt lacks %q:\n%s", want, system)
		}
	}
}
