package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The realtime socket is the API's live half: it reads documents as they are
// typed and writes edits back. A page from another site must not open it,
// even when a proxy in between (the Vite dev server) has rewritten its Origin
// to the hub's own, so the browser's Sec-Fetch-Site is checked on the upgrade
// as it is on an unsafe API call.
func TestRealtimeSocketRefusesACrossSiteUpgrade(t *testing.T) {
	httpServer, _ := hubTestServer(t)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	cases := []struct {
		fetchSite string
		reject    bool
	}{
		{fetchSite: "cross-site", reject: true},
		{fetchSite: "same-site", reject: true},
		{fetchSite: "same-origin"},
		{fetchSite: "none"},
		{fetchSite: ""},
	}
	for _, testCase := range cases {
		t.Run("Sec-Fetch-Site "+testCase.fetchSite, func(t *testing.T) {
			header := http.Header{"Origin": {httpServer.URL}}
			if testCase.fetchSite != "" {
				header.Set("Sec-Fetch-Site", testCase.fetchSite)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
			if conn != nil {
				_ = conn.CloseNow()
			}
			if testCase.reject {
				if err == nil {
					t.Fatal("a cross-site page opened the realtime socket")
				}
				if response == nil || response.StatusCode != http.StatusForbidden {
					t.Fatalf("response = %+v, err = %v; want 403", response, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("our own page could not open the socket: %v", err)
			}
		})
	}
}
