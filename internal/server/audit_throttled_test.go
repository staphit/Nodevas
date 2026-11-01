package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A refusal for being over a budget is something a stranger can repeat as
// fast as the limiter lets them. Each one a row of its own would grow the
// trail, and the backups, without bound and bury what it is kept for; one
// row per source and window says the same.
func TestThrottledRefusalsDoNotEachWriteARow(t *testing.T) {
	s := newRunnerTestServer(t)
	database := s.server.database
	if database == nil {
		t.Fatal("no database on the test server")
	}
	count := func() int {
		var n int
		if err := database.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	post := func(remote, path, body string) int {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = remote
		response := httptest.NewRecorder()
		s.handler.ServeHTTP(response, request)
		return response.Code
	}

	before := count()
	refused := 0
	for i := 0; i < 120; i++ {
		if post("198.51.100.77:40000", "/api/auth/login", fmt.Sprintf(`{"email":"x%d@nowhere.test","otp":"AAAAAAAA"}`, i)) == http.StatusUnauthorized {
			refused++
		}
	}
	if added := count() - before; added > refused+1 {
		t.Errorf("120 sign-ins (%d judged and refused) wrote %d rows, want at most %d", refused, added, refused+1)
	}

	before = count()
	missed := 0
	for i := 0; i < 80; i++ {
		if post("198.51.100.78:40000", runnerwire.LinkPath, fmt.Sprintf(`{"code":"junk-%d"}`, i)) == http.StatusNotFound {
			missed++
		}
	}
	if added := count() - before; added > missed+1 {
		t.Errorf("80 bad redeems (%d judged) wrote %d rows, want at most %d", missed, added, missed+1)
	}
	if missed == 0 {
		t.Error("no redeem was judged")
	}
}
