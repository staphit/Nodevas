package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"nodevas/internal/store"
)

// A project made from a template was written twice: the template's graph,
// then again with the new project's default workflow. When the second write
// beat the history repository's first open, history recorded the project's
// creation and then an "outside edit" taking the workflow away, though
// nobody edited anything. A new project is written once, workflow included,
// and its history is that one commit.
func TestNewProjectHistoryIsOneCreate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	store.SetDefaultHistoryMode(store.HistoryGit)
	t.Cleanup(func() { store.SetDefaultHistoryMode(store.HistoryFiles) })

	for _, template := range []string{"eisenhower", ""} {
		t.Run("template "+template, func(t *testing.T) {
			pm := securityTestManager(t)
			body := `{"name":"m","create":true,"template":"` + template + `","language":"zh-TW"}`
			request := httptest.NewRequest(http.MethodPost, "/api/projects/open", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
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
			if g.Workflow == nil {
				t.Fatal("new project has no workflow")
			}
			if err := st.FlushHistory(context.Background()); err != nil {
				t.Fatal(err)
			}
			history, err := st.HistoryGit(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if history.Backend != string(store.HistoryGit) {
				t.Skipf("history backend is %s", history.Backend)
			}
			subjects := make([]string, 0, len(history.Commits))
			for _, commit := range history.Commits {
				subjects = append(subjects, commit.Subject)
			}
			if len(subjects) != 1 || subjects[0] != "graph: create" {
				t.Fatalf("new project's history = %q, want one \"graph: create\"", subjects)
			}
		})
	}
}
