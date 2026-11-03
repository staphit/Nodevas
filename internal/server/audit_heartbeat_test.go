package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
)

// An agent session says it is still there every 30 seconds. That is
// presence, not a change anybody made: a row per heartbeat would bury the
// writes the audit trail is for.
func TestAgentHeartbeatsWriteNoAuditRows(t *testing.T) {
	server, pm, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)
	handler := server.Handler()
	post := func(path, body string, want int) {
		t.Helper()
		request := withCookies(httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), cookies)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(auth.CSRFHeaderName, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("POST %s = %d %s", path, response.Code, response.Body)
		}
	}
	for range 3 {
		post("/api/agents/heartbeat", `{"sessionId":"claude:s1","agent":"claude","state":"idle"}`, http.StatusOK)
	}
	post("/api/nodes", `{"id":"h1","title":"A write","body":""}`, http.StatusCreated)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	heartbeats, err := server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "POST agents/heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(heartbeats) != 0 {
		t.Fatalf("%d audit rows for heartbeats, want none", len(heartbeats))
	}
	writes, err := server.audit.Query(context.Background(), audit.Filter{Project: pm.Store().Root(), ActionPrefix: "POST nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 {
		t.Fatalf("%d audit rows for the write, want 1", len(writes))
	}
}
