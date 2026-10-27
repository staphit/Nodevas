package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func browseHello() runnerwire.Envelope {
	env := runsHello(1)
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapRepoBrowse)
	return env
}

// B-48: repo browsing goes to the asker's own runner, and only there.
func TestExplorerRepoRoutes(t *testing.T) {
	f := newRunsFixture(t)
	runner, repo := f.runner.Runner.ID, "repo-1"
	tree := "/api/explorer/repos/" + runner + "/" + repo + "/tree?path=src"

	// Offline first.
	response := f.do(t, &f.bea, http.MethodGet, tree, "", nil)
	if response.Code != http.StatusServiceUnavailable || errorCode(t, response) != "RUNNER_OFFLINE" {
		t.Fatalf("offline runner: %d %s", response.Code, response.Body)
	}
	conn := f.connect(t, browseHello())

	list := f.do(t, &f.bea, http.MethodGet, "/api/explorer/repos", "", nil)
	var repos struct {
		Repos []struct {
			RunnerID   string `json:"runnerId"`
			RunnerName string `json:"runnerName"`
			Online     bool   `json:"online"`
			RepoID     string `json:"repoId"`
			Name       string `json:"name"`
			Browsable  bool   `json:"browsable"`
		} `json:"repos"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &repos)
	if list.Code != http.StatusOK || len(repos.Repos) != 1 || !repos.Repos[0].Online || !repos.Repos[0].Browsable ||
		repos.Repos[0].RepoID != repo || repos.Repos[0].Name != "visual-editor" || repos.Repos[0].RunnerName != "bea-pc" {
		t.Fatalf("GET repos = %d %s", list.Code, list.Body)
	}
	if calList := f.do(t, &f.cal, http.MethodGet, "/api/explorer/repos", "", nil); calList.Code != http.StatusOK || strings.Contains(calList.Body.String(), runner) {
		t.Fatalf("cal sees bea's runner: %d %s", calList.Code, calList.Body)
	}

	// An agent never browses.
	response = f.do(t, &f.bea, http.MethodGet, tree, "", map[string]string{auth.AgentRoleHeaderName: "worker"})
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PERMISSION_DENIED" {
		t.Fatalf("agent browsing: %d %s", response.Code, response.Body)
	}
	// Another person's runner does not exist for them.
	response = f.do(t, &f.cal, http.MethodGet, tree, "", nil)
	if response.Code != http.StatusNotFound || errorCode(t, response) != "NOT_FOUND" {
		t.Fatalf("cal browsing bea's runner: %d %s", response.Code, response.Body)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/explorer/repos/"+runner+"/nope/tree", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("unknown repo: %d", code)
	}

	// The runner answers; the hub passes the listing through.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.do(t, &f.bea, http.MethodGet, tree, "", nil) }()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeRepoRequest || env.Repo == nil || env.Repo.Op != runnerwire.RepoOpList || env.Repo.Path != "src" ||
		env.Repo.RepoID != repo || env.Repo.AccountID != f.runner.Runner.OwnerID {
		t.Fatalf("runner got %+v", env)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		Listing: &runnerwire.RepoListing{Path: "src", Entries: []runnerwire.RepoEntry{{Name: "math.js", Type: "file", Size: 40}}, Hidden: 1}}})
	response = <-done
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "math.js") || !strings.Contains(response.Body.String(), `"hidden":1`) {
		t.Fatalf("listing = %d %s", response.Code, response.Body)
	}

	// A runner's coded refusal maps to its status.
	go func() {
		done <- f.do(t, &f.bea, http.MethodGet, "/api/explorer/repos/"+runner+"/"+repo+"/file?path=.env", "", nil)
	}()
	env = readEnvelopeForTest(t, conn)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID, Code: runnerwire.RepoErrHidden, Error: "hidden"}})
	if response = <-done; response.Code != http.StatusForbidden || errorCode(t, response) != "HIDDEN_FILE" {
		t.Fatalf("hidden file: %d %s", response.Code, response.Body)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/explorer/repos/"+runner+"/"+repo+"/git/diff?ref=HEAD", "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("diff ref HEAD: %d", code)
	}
}

// B-48: the repo routes are behind the project gate.
func TestExplorerRepoRoutesNeedProjectAccess(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict: %d %s", response.Code, response.Body)
	}
	response := f.do(t, &f.dan, http.MethodGet, "/api/explorer/repos?project=other", "", nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_ACCESS_DENIED" {
		t.Fatalf("non-member: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.cal, http.MethodGet, "/api/explorer/repos?project=other", ""); code != http.StatusOK {
		t.Fatalf("viewer: %d", code)
	}
}

// B-49 and B-50: the workspace's own files and history.
func TestExplorerWorkspace(t *testing.T) {
	f := newRunsFixture(t)
	root := f.server.pm.Store().Root()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/files", "", nil)
	if files.Code != http.StatusOK || !strings.Contains(files.Body.String(), `"nodeId":"`+runsTestNode+`"`) ||
		strings.Contains(files.Body.String(), `".env"`) || strings.Contains(files.Body.String(), `".vised`) {
		t.Fatalf("workspace files = %d %s", files.Code, files.Body)
	}
	file := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/file?path=nodes/"+runsTestNode+".md", "", nil)
	if file.Code != http.StatusOK || !strings.Contains(file.Body.String(), "Safari") || !strings.Contains(file.Body.String(), `"kind":"text"`) {
		t.Fatalf("node file = %d %s", file.Code, file.Body)
	}
	for path, want := range map[string]string{"../outside": "BAD_PATH", ".env": "HIDDEN_FILE"} {
		response := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/file?path="+path, "", nil)
		if errorCode(t, response) != want {
			t.Errorf("file %s = %d %s, want %s", path, response.Code, response.Body, want)
		}
	}
	git := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/git", "", nil)
	if git.Code != http.StatusOK || !strings.Contains(git.Body.String(), `"backend":"files"`) {
		t.Fatalf("workspace git = %d %s", git.Code, git.Body)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/git/commit?sha=HEAD", "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("commit sha HEAD = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/explorer/workspace/files", "", map[string]string{auth.AgentRoleHeaderName: "worker"}).Code; code != http.StatusForbidden {
		t.Fatalf("agent reading workspace files = %d", code)
	}
}

type agentItem struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	RunID         string `json:"runId"`
	SessionID     string `json:"sessionId"`
	Agent         string `json:"agent"`
	Role          string `json:"role"`
	State         string `json:"state"`
	NodeID        string `json:"nodeId"`
	CostUSD       float64
	CostEstimated bool `json:"costEstimated"`
	Verdict       *struct {
		Outcome  string   `json:"outcome"`
		Severity string   `json:"severity"`
		Reasons  []string `json:"reasons"`
	} `json:"verdict"`
	Files []struct {
		Path string `json:"path"`
		Mark string `json:"mark"`
	} `json:"files"`
}

// B-51: the Agent tab: live runs, external sessions, recent runs; a role
// only from the run's record.
func TestExplorerAgents(t *testing.T) {
	f := newRunsFixture(t)
	database := f.analyticsDB(t)
	live := f.runningRoleRun(t, runnerwire.JobImplement, engine.RoleExecutor, "")
	if _, err := database.Exec(`INSERT INTO run_changes (run_id, files, created_at) VALUES (?, '[{"path":"src/login.js","status":"modified","additions":3}]', '2026-10-28T00:00:00.000000Z')`, live.ID); err != nil {
		t.Fatal(err)
	}
	review := f.runningRoleRun(t, runnerwire.JobReview, engine.RoleReviewer, "")
	if _, _, err := f.server.runs.Store().SubmitVerdict(t.Context(), review, runs.VerdictInput{Outcome: "fail", Severity: "high", Reasons: []string{"regression"}}); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	usage := runnerwire.Usage{InputTokens: 1_000_000, OutputTokens: 100_000}
	if _, err := f.server.runs.Store().Finish(t.Context(), review.ID, f.runner.Runner.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: &usage}); err != nil {
		t.Fatal(err)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/agents/heartbeat", `{"sessionId":"claude:ext","agent":"claude","role":"reviewer","doing":"reading"}`, nil).Code; code != http.StatusOK {
		t.Fatalf("heartbeat = %d", code)
	}

	response := f.do(t, &f.bea, http.MethodGet, "/api/explorer/agents", "", nil)
	var got struct {
		Live     []agentItem `json:"live"`
		External []agentItem `json:"external"`
		Recent   []agentItem `json:"recent"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &got)
	if response.Code != http.StatusOK || len(got.Live) != 1 || len(got.Recent) != 1 || len(got.External) != 1 {
		t.Fatalf("agents = %d %s", response.Code, response.Body)
	}
	if l := got.Live[0]; l.Kind != "run" || l.RunID != live.ID || l.Role != engine.RoleExecutor || l.State != "running" ||
		len(l.Files) != 1 || l.Files[0].Path != "src/login.js" || l.Files[0].Mark != "M" {
		t.Fatalf("live = %+v", l)
	}
	if r := got.Recent[0]; r.RunID != review.ID || r.Role != engine.RoleReviewer || !r.CostEstimated ||
		r.Verdict == nil || r.Verdict.Outcome != "fail" || r.Verdict.Severity != "high" || r.Verdict.Reasons[0] != "regression" {
		t.Fatalf("recent = %+v", r)
	}
	if e := got.External[0]; e.Kind != "session" || e.SessionID != "claude:ext" || e.Role != "" {
		t.Fatalf("external = %+v, want no role (a session's claim is not a role)", e)
	}
	actions := f.do(t, &f.bea, http.MethodGet, "/api/explorer/sessions/claude:ext/actions?limit=200", "", nil)
	if actions.Code != http.StatusOK || !strings.Contains(actions.Body.String(), `"action":"joined"`) {
		t.Fatalf("session actions = %d %s", actions.Code, actions.Body)
	}
}
