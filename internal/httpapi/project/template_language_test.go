package project

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Eisenhower template's quadrant titles are written into the new
// project's graph, so they must be in the interface language of whoever
// created it: an English interface must not get a board of Chinese titles.
func TestEisenhowerTemplateTitlesFollowLanguage(t *testing.T) {
	cases := []struct {
		name, body, acceptLanguage, want, notWant string
	}{
		{"english body", `{"name":"m","create":true,"template":"eisenhower","language":"en"}`, "zh-TW,zh;q=0.9", "Important and urgent", "重要且緊急"},
		{"chinese body", `{"name":"m","create":true,"template":"eisenhower","language":"zh-TW"}`, "en-US", "重要且緊急", "Important and urgent"},
		{"no language, chinese browser", `{"name":"m","create":true,"template":"eisenhower"}`, "zh-TW,zh;q=0.9,en;q=0.8", "重要且緊急", "Important and urgent"},
		{"no language at all", `{"name":"m","create":true,"template":"eisenhower"}`, "", "Important and urgent", "重要且緊急"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := securityTestManager(t)
			request := httptest.NewRequest(http.MethodPost, "/api/projects/open", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if tc.acceptLanguage != "" {
				request.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			response := httptest.NewRecorder()
			New(pm).PostProjectOpen(securityTestContext(response, request))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			st, err := pm.StoreFor("m")
			if err != nil {
				t.Fatal(err)
			}
			g, _, err := st.LoadGraph()
			if err != nil {
				t.Fatal(err)
			}
			if g.UI == nil || len(g.UI.Groups) != 4 {
				t.Fatalf("want 4 quadrant groups, got %+v", g.UI)
			}
			titles := make([]string, 0, len(g.UI.Groups))
			for _, group := range g.UI.Groups {
				titles = append(titles, group.Title)
			}
			joined := strings.Join(titles, " | ")
			if !strings.Contains(joined, tc.want) || strings.Contains(joined, tc.notWant) {
				t.Fatalf("quadrant titles = %q, want %q and not %q", joined, tc.want, tc.notWant)
			}
		})
	}
}
