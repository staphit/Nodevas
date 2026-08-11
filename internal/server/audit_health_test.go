package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
)

func TestAuditFallbackAcknowledgementRequiresCSRFAndAuditsItsOwnOutcome(t *testing.T) {
	server, _, inbox, database := accountServerWithAuditDBForTest(t)
	cookies, csrf := signIn(t, server, inbox, testPin)
	handler := server.Handler()
	t.Cleanup(func() { _, _ = database.Writer().Exec("PRAGMA query_only = OFF") })

	acknowledge := func(expected uint64, token string) *httptest.ResponseRecorder {
		t.Helper()
		body := strings.NewReader(`{"expectedFallbackEvents":` + strconv.FormatUint(expected, 10) + `}`)
		request := withCookies(httptest.NewRequest(http.MethodPost,
			"/api/audit/health/acknowledge", body), cookies)
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set(auth.CSRFHeaderName, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := acknowledge(0, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("acknowledgement without CSRF status = %d, want 403; body = %s", response.Code, response.Body)
	}

	// Produce a deterministic fallback, then prove writes have recovered. The
	// overall status must stay degraded until the operator acknowledges count 1.
	if _, err := database.Writer().Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	server.audit.RecordOrLog(context.Background(), audit.Event{Action: "test initial fallback"})
	if _, err := database.Writer().Exec("PRAGMA query_only = OFF"); err != nil {
		t.Fatal(err)
	}
	server.audit.RecordOrLog(context.Background(), audit.Event{Action: "test write recovery"})

	response = acknowledge(1, csrf)
	if response.Code != http.StatusOK {
		t.Fatalf("acknowledgement status = %d, want 200; body = %s", response.Code, response.Body)
	}
	health := server.audit.Health()
	if health.Status != audit.HealthHealthy || health.AcknowledgedFallbackEvents != 1 {
		t.Fatalf("health after acknowledgement = %+v", health)
	}
	entries, err := server.audit.Query(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == "POST audit/health/acknowledge" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("successful acknowledgement left no audit event: %+v", entries)
	}

	// Reconcile a second observed fallback, but make the acknowledgement
	// request's own middleware write fail. The HTTP action completed, yet the
	// newly lost audit event must immediately create count 3 and re-degrade.
	if _, err := database.Writer().Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	server.audit.RecordOrLog(context.Background(), audit.Event{Action: "test second fallback"})
	if _, err := database.Writer().Exec("PRAGMA query_only = OFF"); err != nil {
		t.Fatal(err)
	}
	server.audit.RecordOrLog(context.Background(), audit.Event{Action: "test second recovery"})
	if _, err := database.Writer().Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	response = acknowledge(2, csrf)
	if response.Code != http.StatusOK {
		t.Fatalf("acknowledgement before middleware fallback status = %d, want 200; body = %s", response.Code, response.Body)
	}
	health = server.audit.Health()
	if health.Status != audit.HealthDegraded || health.WriteStatus != audit.HealthDegraded ||
		health.FallbackEvents != 3 || health.AcknowledgedFallbackEvents != 2 ||
		health.UnreconciledEvents != 1 {
		t.Fatalf("failed acknowledgement audit did not re-degrade health: %+v", health)
	}
}
