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

// The audit row of every write the middleware sees is queued, not written
// on the request's own time, and Shutdown drains the queue before the
// caller closes the database.
func TestMiddlewareAuditRowsAreQueuedAndShutdownDrainsThem(t *testing.T) {
	server, pm, inbox := accountServerForTest(t)
	if server.auditQueue == nil {
		t.Fatal("UseAudit made no audit queue")
	}
	cookies, csrf := signIn(t, server, inbox, testEmail)
	handler := server.Handler()
	for _, id := range []string{"q1", "q2", "q3"} {
		request := withCookies(httptest.NewRequest(http.MethodPost, "/api/nodes",
			strings.NewReader(`{"id":"`+id+`","title":"Queued","body":""}`)), cookies)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(auth.CSRFHeaderName, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", id, response.Code, response.Body)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stats := server.auditQueue.Stats()
	if !stats.Closed || stats.Written < 3 || stats.Depth != 0 {
		t.Fatalf("audit queue after Shutdown = %+v, want closed, drained, and the three writes", stats)
	}
	rows, err := server.audit.Query(context.Background(), audit.Filter{Project: pm.Store().Root(), ActionPrefix: "POST nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d audit rows for the three writes, want 3", len(rows))
	}
}
