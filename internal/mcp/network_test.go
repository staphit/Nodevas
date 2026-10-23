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
)

func mcpNamedSession(t *testing.T, url, actor, sessionID, sessionName, agentKind string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server, err := NewServer(ctx, Options{
		Server:      url,
		Actor:       actor,
		SessionID:   sessionID,
		SessionName: sessionName,
		AgentKind:   agentKind,
		Repo:        "nodevas",
		Branch:      "main",
		AgentRole:   "worker",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: agentKind, Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAgentNetworkMultiAgentCollaborationAndAskUser(t *testing.T) {
	url, _ := liveServer(t)

	alice := mcpNamedSession(t, url, "mcp:alice", "claude:a1", "api-claude", "claude")
	bob := mcpNamedSession(t, url, "mcp:bob", "codex:b1", "web-codex", "codex")

	// 1. Alice claims the "design" task; list_team from Bob shows Alice busy on "design".
	var claimed claimTaskOutput
	call(t, alice, "claim_task", map[string]any{"id": "design"}, &claimed)

	var team listTeamOutput
	call(t, bob, "list_team", nil, &team)
	if len(team.Sessions) != 2 {
		t.Fatalf("team sessions = %+v, want 2", team.Sessions)
	}
	foundAlice := false
	for _, s := range team.Sessions {
		if strings.HasSuffix(s.Address, "api-claude") {
			foundAlice = true
			if s.State != "busy" || s.NodeID != "design" {
				t.Fatalf("alice session = %+v, want busy on design", s)
			}
		}
	}
	if !foundAlice {
		t.Fatalf("api-claude not found in %+v", team.Sessions)
	}

	// 2. Bob sends a message to the agent holding node:design.
	var sent sendMessageOutput
	call(t, bob, "send_message", map[string]any{
		"to":      "node:design",
		"message": "Should we expose a REST endpoint for asks?",
		"nodeId":  "design",
	}, &sent)
	if len(sent.Deliveries) != 1 || sent.Deliveries[0].Status != "delivered" {
		t.Fatalf("deliveries = %+v, want delivered", sent.Deliveries)
	}
	msgID := sent.Deliveries[0].MessageID

	// 3. Alice calls list_team (which refreshes unreadCount) and sees the unread notice.
	res := call(t, alice, "list_team", nil, &team)
	if team.Unread != 1 || !strings.Contains(errorText(res), "Unread mail: 1") {
		t.Fatalf("expected unread mail notice on Alice's tool call; unread=%d, text=%q", team.Unread, errorText(res))
	}

	// 4. Alice reads her inbox and gets the <nodevas-message> framing.
	var inbox checkInboxOutput
	call(t, alice, "check_inbox", nil, &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].ID != msgID {
		t.Fatalf("inbox = %+v, want 1 message %s", inbox, msgID)
	}
	if !strings.Contains(inbox.Formatted, "<nodevas-message") {
		t.Fatalf("formatted = %q, want <nodevas-message> tag", inbox.Formatted)
	}

	// 5. Alice calls ask_user referencing Bob's message (`about`), and a human approves via HTTP.
	askDone := make(chan askUserOutput, 1)
	go func() {
		var askOut askUserOutput
		call(t, alice, "ask_user", map[string]any{
			"question": "Approve exposing REST endpoint for asks?",
			"about":    msgID,
			"nodeId":   "design",
		}, &askOut)
		askDone <- askOut
	}()

	var askID string
	for range 30 {
		resp, err := http.Get(url + "/api/agents/asks?status=pending")
		if err == nil {
			var payload struct {
				Asks []struct {
					ID string `json:"id"`
				} `json:"asks"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if len(payload.Asks) == 1 {
				askID = payload.Asks[0].ID
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	if askID == "" {
		t.Fatal("pending ask did not appear on /api/agents/asks")
	}

	// An agent request with X-Nodevas-Agent-Role cannot approve an ask_user request.
	agentReq, _ := http.NewRequest(http.MethodPost, url+"/api/agents/asks/"+askID+"/answer",
		bytes.NewBufferString(`{"decision":"approved"}`))
	agentReq.Header.Set("Content-Type", "application/json")
	agentReq.Header.Set("X-Nodevas-Agent-Role", "worker")
	agentResp, err := http.DefaultClient.Do(agentReq)
	if err != nil {
		t.Fatalf("agent answer request: %v", err)
	}
	agentResp.Body.Close()
	if agentResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent answer status = %d, want 403 Forbidden", agentResp.StatusCode)
	}

	// Human approves the ask.
	humanResp, err := http.Post(url+"/api/agents/asks/"+askID+"/answer", "application/json",
		bytes.NewBufferString(`{"decision":"approved","note":"Use /api/agents/asks"}`))
	if err != nil {
		t.Fatalf("human answer: %v", err)
	}
	humanResp.Body.Close()
	if humanResp.StatusCode != http.StatusOK {
		t.Fatalf("human answer status = %d, want 200", humanResp.StatusCode)
	}

	select {
	case decided := <-askDone:
		if decided.Status != "approved" || decided.Note != "Use /api/agents/asks" {
			t.Fatalf("ask_user output = %+v", decided)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user did not return after human approval")
	}

	// 6. Bob's inbox receives the verified <nodevas-approval> forwarded from the human decision.
	var bobInbox checkInboxOutput
	call(t, bob, "check_inbox", nil, &bobInbox)
	if len(bobInbox.Messages) != 1 || !strings.Contains(bobInbox.Messages[0].Body, "<nodevas-approval") {
		t.Fatalf("bob inbox = %+v, want <nodevas-approval>", bobInbox)
	}
}
