package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testPanel(t *testing.T) http.Handler {
	t.Helper()
	sup := newSupervisor(supervisorConfig{
		Binary: "nodevas", Workspace: t.TempDir(), ServerHost: "127.0.0.1", ServerPort: 5666,
	})
	return panelHandler(sup, newBuilder(t.TempDir(), sup.logs))
}

// A page on another site can rebind its own name to 127.0.0.1 and then read
// the panel same-origin: the status names the workspace, the binary and the
// child's recent log lines. The panel only answers to this machine's names.
func TestPanelAnswersOnlyToThisMachinesNames(t *testing.T) {
	panel := testPanel(t)
	for _, path := range []string{"/", "/api/status"} {
		for _, host := range []string{"rebind.attacker.example:5667", "attacker.example", "192.168.1.20:5667"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Host = host
			response := httptest.NewRecorder()
			panel.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Errorf("GET %s with Host %q = %d, want 403", path, host, response.Code)
			}
			if strings.Contains(response.Body.String(), "workspace") {
				t.Errorf("GET %s with Host %q leaked the status: %s", path, host, response.Body)
			}
		}
		for _, host := range []string{"127.0.0.1:5667", "localhost:5667", "[::1]:5667", "127.0.0.53:5667", "localhost"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Host = host
			response := httptest.NewRecorder()
			panel.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("GET %s with Host %q = %d, want 200", path, host, response.Code)
			}
		}
	}
}

// Its buttons start, stop and kill processes, so no other page may frame it
// and steer a click onto them.
func TestPanelCannotBeFramed(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "127.0.0.1:5667"
	response := httptest.NewRecorder()
	testPanel(t).ServeHTTP(response, request)
	if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
	}
}
