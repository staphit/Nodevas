package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A hub without accounts treats every request as the person at this
// machine's keyboard. A request a proxy on this machine relays for somebody
// else (a tunnel, a reverse proxy, the Vite dev server opened to the LAN)
// is not that person, and must not get their administrator rights.
func TestLocalHubRefusesRequestsRelayedForAnotherMachine(t *testing.T) {
	server, _ := twoProjectServer(t)
	if err := server.UseListenAddress("127.0.0.1", 5666, []string{"nodevas.example.com"}); err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	get := func(host string, header map[string]string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
		request.Host = host
		request.RemoteAddr = "127.0.0.1:50000"
		for key, value := range header {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}

	relayed := []map[string]string{
		// A tunnel or reverse proxy on this machine, under the public name.
		{"X-Forwarded-For": "203.0.113.9"},
		{"Cf-Connecting-Ip": "203.0.113.9"},
		{"X-Real-Ip": "203.0.113.9"},
		{"True-Client-Ip": "203.0.113.9"},
		{"Forwarded": "for=203.0.113.9;proto=https"},
		{"Via": "1.1 proxy"},
		{"X-Forwarded-Host": "nodevas.example.com"},
		// A LAN peer through the Vite dev proxy (xfwd appends the real peer).
		{"X-Forwarded-For": "192.168.1.50", "X-Forwarded-Host": "192.168.1.10:5173"},
		{"X-Forwarded-For": "127.0.0.1, 192.168.1.50", "X-Forwarded-Host": "localhost:5173"},
	}
	for _, header := range relayed {
		for _, host := range []string{"nodevas.example.com", "127.0.0.1:5666"} {
			if code := get(host, header); code != http.StatusUnauthorized {
				t.Errorf("Host %s with %v = %d, want 401", host, header, code)
			}
		}
	}

	// The person's own browser, directly or through the Vite dev proxy on
	// this machine, keeps working with no sign-in.
	local := []map[string]string{
		nil,
		{"X-Forwarded-For": "127.0.0.1", "X-Forwarded-Host": "localhost:5173", "X-Forwarded-Proto": "http", "X-Forwarded-Port": "5173"},
		{"X-Forwarded-For": "::1", "X-Forwarded-Host": "[::1]:5173"},
		{"X-Forwarded-For": "::ffff:127.0.0.1", "X-Forwarded-Host": "127.0.0.1:5173"},
		{"Forwarded": `for="[::1]:51000";host=localhost:5173`},
	}
	for _, header := range local {
		if code := get("127.0.0.1:5666", header); code != http.StatusOK {
			t.Errorf("local request with %v = %d, want 200", header, code)
		}
	}
	// A page whose DNS name rebinds to 127.0.0.1 still names itself.
	if code := get("rebind.attacker.test:5666", nil); code != http.StatusForbidden {
		t.Errorf("rebound Host = %d, want 403", code)
	}
}
