package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// A roled run works its own project: a request it sends at another project
// (here the one the owner has open) is refused, whatever node ids match.
func TestProxiedRunStaysInItsProject(t *testing.T) {
	f := newRunsFixture(t)
	other := filepath.Join(f.server.pm.Workspace(), "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewStore(other).SaveGraph(identity.Local, &engine.Graph{Version: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.pm.StoreFor("elsewhere"); err != nil {
		t.Fatalf("second project: %v", err)
	}
	rec, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: "elsewhere", NodeID: runsTestNode, NodeTitle: "Fix the login bug",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Fix", CreatedBy: f.runner.Runner.OwnerID, CreatedByName: "bea", Role: engine.RoleExecutor,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body := f.proxied(t, rec.ID, http.MethodPost, "nodes/"+runsTestNode+"/claim", `{"owner":"run"}`, nil)
	if code != http.StatusForbidden || !strings.Contains(body, "ROLE_DENIED") || !strings.Contains(body, "elsewhere") {
		t.Fatalf("executor of another project claiming here: %d %s", code, body)
	}
}
