package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/db"
)

func TestAuditHealthEndpointReportsDatabaseFallback(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	trail := audit.New(database)
	api := &API{audit: trail}

	response := httptest.NewRecorder()
	api.getAuditHealth(notifySecurityContext(response,
		httptest.NewRequest(http.MethodGet, "/api/audit/health", nil)))
	if response.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body = %s", response.Code, response.Body)
	}
	var health audit.Health
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != audit.HealthHealthy || health.WriteStatus != audit.HealthHealthy ||
		health.FallbackEvents != 0 || health.UnreconciledEvents != 0 {
		t.Fatalf("healthy response = %+v", health)
	}

	if _, err := database.Writer().Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	trail.RecordOrLog(context.Background(), audit.Event{Action: "POST nodes"})
	response = httptest.NewRecorder()
	api.getAuditHealth(notifySecurityContext(response,
		httptest.NewRequest(http.MethodGet, "/api/audit/health", nil)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded status = %d, body = %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != audit.HealthDegraded || health.WriteStatus != audit.HealthDegraded ||
		health.FallbackEvents != 1 || health.UnreconciledEvents != 1 || health.LastFailureAt == nil {
		t.Fatalf("degraded response = %+v", health)
	}

	acknowledge := func(expected string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/audit/health/acknowledge",
			strings.NewReader(`{"expectedFallbackEvents":`+expected+`}`))
		api.postAuditHealthAcknowledge(notifySecurityContext(response, request))
		return response
	}
	response = acknowledge("1")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"audit_writes_degraded"`) {
		t.Fatalf("acknowledge while writes degraded status = %d, body = %s", response.Code, response.Body)
	}

	if _, err := database.Writer().Exec("PRAGMA query_only = OFF"); err != nil {
		t.Fatal(err)
	}
	trail.RecordOrLog(context.Background(), audit.Event{Action: "audit recovery probe"})
	health = trail.Health()
	if health.Status != audit.HealthDegraded || health.WriteStatus != audit.HealthHealthy || health.UnreconciledEvents != 1 {
		t.Fatalf("write-recovered health = %+v", health)
	}

	response = acknowledge("0")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"audit_fallback_count_changed"`) {
		t.Fatalf("stale acknowledgement status = %d, body = %s", response.Code, response.Body)
	}
	response = acknowledge("1")
	if response.Code != http.StatusOK {
		t.Fatalf("acknowledgement status = %d, body = %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != audit.HealthHealthy || health.WriteStatus != audit.HealthHealthy ||
		health.FallbackEvents != 1 || health.AcknowledgedFallbackEvents != 1 ||
		health.UnreconciledEvents != 0 || health.LastFailureAt == nil {
		t.Fatalf("acknowledged response = %+v", health)
	}
}
