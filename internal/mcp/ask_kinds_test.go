package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/agents"
)

// pendingAskOver waits for the one pending ask on the live server.
func pendingAskOver(t *testing.T, url string) agents.Ask {
	t.Helper()
	for range 300 {
		resp, err := http.Get(url + "/api/agents/asks?status=pending")
		if err == nil {
			var payload struct {
				Asks []agents.Ask `json:"asks"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if len(payload.Asks) == 1 {
				return payload.Asks[0]
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("no pending ask appeared on /api/agents/asks")
	return agents.Ask{}
}

func answerOver(t *testing.T, url, id, body string) {
	t.Helper()
	resp, err := http.Post(url+"/api/agents/asks/"+id+"/answer", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answer %s = %d", body, resp.StatusCode)
	}
}

func TestAskUserQuestionReturnsTheAnswer(t *testing.T) {
	url, _ := liveServer(t)
	alice := mcpNamedSession(t, url, "mcp:alice", "claude:a1", "api-claude", "claude")
	done := make(chan askUserOutput, 1)
	go func() {
		var out askUserOutput
		call(t, alice, "ask_user", map[string]any{"kind": "question", "question": "Which port?"}, &out)
		done <- out
	}()
	ask := pendingAskOver(t, url)
	if ask.Kind != agents.AskKindQuestion {
		t.Fatalf("pending = %+v", ask)
	}
	answerOver(t, url, ask.ID, `{"decision":"answer","answer":"8080","note":"any is fine"}`)
	select {
	case out := <-done:
		if out.Status != agents.AskAnswered || out.Answer != "8080" || out.Note != "any is fine" {
			t.Fatalf("ask_user = %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask_user did not return")
	}
}

func TestAskUserChoiceReturnsTheOption(t *testing.T) {
	url, _ := liveServer(t)
	alice := mcpNamedSession(t, url, "mcp:alice", "claude:a1", "api-claude", "claude")
	done := make(chan askUserOutput, 1)
	go func() {
		var out askUserOutput
		call(t, alice, "ask_user", map[string]any{"kind": "choice", "question": "Which DB?", "options": []string{"Postgres", "MySQL"}}, &out)
		done <- out
	}()
	ask := pendingAskOver(t, url)
	if ask.Kind != agents.AskKindChoice || len(ask.Options) != 2 {
		t.Fatalf("pending = %+v", ask)
	}
	answerOver(t, url, ask.ID, `{"decision":"answer","answer":"MySQL"}`)
	select {
	case out := <-done:
		if out.Status != agents.AskAnswered || out.Answer != "MySQL" {
			t.Fatalf("ask_user = %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask_user did not return")
	}
	// A choice without options is refused as the agent's mistake.
	result := call(t, alice, "ask_user", map[string]any{"kind": "choice", "question": "Which?"}, nil)
	if !result.IsError {
		t.Fatalf("a choice without options was asked: %s", errorText(result))
	}
}

// approveTool calls approve_tool as Claude Code does and returns its result.
func approveTool(t *testing.T, session *sdk.ClientSession, args map[string]any) chan *sdk.CallToolResult {
	t.Helper()
	done := make(chan *sdk.CallToolResult, 1)
	go func() {
		result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "approve_tool", Arguments: args})
		if err != nil {
			t.Errorf("approve_tool: %v", err)
		}
		done <- result
	}()
	return done
}

// permissionReply is the one text content approve_tool returns, decoded.
func permissionReply(t *testing.T, done chan *sdk.CallToolResult) (map[string]json.RawMessage, string) {
	t.Helper()
	var result *sdk.CallToolResult
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("approve_tool did not return")
	}
	if result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("approve_tool result = %+v", result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("approve_tool content = %T", result.Content[0])
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text.Text), &reply); err != nil {
		t.Fatalf("approve_tool text %q is not JSON: %v", text.Text, err)
	}
	return reply, text.Text
}

// Claude Code's --permission-prompt-tool contract: allow carries the input
// back unchanged, as the one text content of the result, even with mail
// waiting.
func TestApproveToolAllowsWithTheOriginalInput(t *testing.T) {
	url, _ := liveServer(t)
	alice := mcpNamedSession(t, url, "mcp:alice", "claude:a1", "api-claude", "claude")
	bob := mcpNamedSession(t, url, "mcp:bob", "codex:b1", "web-codex", "codex")
	call(t, bob, "send_message", map[string]any{"to": "api-claude", "message": "mail for alice"}, nil)

	input := `{"command":"go test ./...","timeout":9007199254740993,"description":"run the tests"}`
	done := approveTool(t, alice, map[string]any{
		"tool_name": "Bash", "input": json.RawMessage(input), "tool_use_id": "toolu_01",
	})
	ask := pendingAskOver(t, url)
	if ask.Kind != agents.AskKindPermission || ask.Tool != "Bash" || !strings.Contains(ask.ToolInput, "go test ./...") {
		t.Fatalf("pending = %+v", ask)
	}
	answerOver(t, url, ask.ID, `{"decision":"approve"}`)
	reply, text := permissionReply(t, done)
	if string(reply["behavior"]) != `"allow"` {
		t.Fatalf("reply = %s", text)
	}
	if string(reply["updatedInput"]) != input {
		t.Fatalf("updatedInput = %s, want the original %s", reply["updatedInput"], input)
	}
}

func TestApproveToolDeniesWithWhoAndWhy(t *testing.T) {
	url, _ := liveServer(t)
	alice := mcpNamedSession(t, url, "mcp:alice", "claude:a1", "api-claude", "claude")
	done := approveTool(t, alice, map[string]any{"tool_name": "Edit", "input": map[string]any{"file_path": "main.go"}})
	ask := pendingAskOver(t, url)
	answerOver(t, url, ask.ID, `{"decision":"deny","note":"not that file"}`)
	reply, text := permissionReply(t, done)
	var message string
	_ = json.Unmarshal(reply["message"], &message)
	if string(reply["behavior"]) != `"deny"` || !strings.Contains(message, "not that file") || reply["updatedInput"] != nil {
		t.Fatalf("reply = %s", text)
	}
}

func TestPermissionReplyShapes(t *testing.T) {
	input := json.RawMessage(`{"a":1}`)
	for name, tc := range map[string]struct {
		ask       agents.Ask
		behavior  string
		mentioned string
	}{
		"approved":     {agents.Ask{Status: agents.AskApproved, DecidedBy: "alice"}, "allow", ""},
		"run approved": {agents.Ask{Status: agents.AskApproved, Answer: agents.DecisionApproveRun, Note: agents.AllowedForRunNote}, "allow", ""},
		"denied":       {agents.Ask{Status: agents.AskDenied, DecidedBy: "alice", Note: "no"}, "deny", "alice"},
		"expired":      {agents.Ask{Status: agents.AskExpired}, "deny", "5 minutes"},
	} {
		raw := permissionReplyText(tc.ask, input)
		var reply struct {
			Behavior     string          `json:"behavior"`
			UpdatedInput json.RawMessage `json:"updatedInput"`
			Message      string          `json:"message"`
		}
		if err := json.Unmarshal([]byte(raw), &reply); err != nil {
			t.Fatalf("%s: %q: %v", name, raw, err)
		}
		if reply.Behavior != tc.behavior {
			t.Errorf("%s: %s", name, raw)
		}
		if tc.behavior == "allow" && string(reply.UpdatedInput) != string(input) {
			t.Errorf("%s: updatedInput %s", name, reply.UpdatedInput)
		}
		if tc.behavior == "deny" && (reply.Message == "" || !strings.Contains(reply.Message, tc.mentioned)) {
			t.Errorf("%s: message %q, want it to mention %q", name, reply.Message, tc.mentioned)
		}
	}
}
