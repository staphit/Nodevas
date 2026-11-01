package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ask_user's 409 means the session already has a question waiting for a
// person. There is nothing to re-read: the advice is to wait for that answer,
// not to ask again.
func TestAskUserConflictSaysToWaitForThePendingAnswer(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/asks" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"session already has a pending ask_user request","code":"CONFLICT"}`))
			return
		}
		_, _ = w.Write([]byte(`{"session":{"name":"s1"}}`))
	}))
	defer stub.Close()
	client, err := NewClient(ClientOptions{
		Server: stub.URL, Actor: "mcp:ann", AgentKind: "claude", SessionName: "s1",
		HTTPClient: stub.Client(), WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AskUser(context.Background(), askUserInput{Question: "May I?"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("AskUser = %v, want the 409", err)
	}
	message := apiErr.Error()
	if strings.Contains(message, "re-read") {
		t.Errorf("ask_user's 409 = %q: re-reading is advice for a revision conflict", message)
	}
	if !strings.Contains(message, "pending ask_user request") || !strings.Contains(message, "check_inbox") {
		t.Errorf("ask_user's 409 = %q, want the server's reason and to wait for the answer in check_inbox", message)
	}
}
