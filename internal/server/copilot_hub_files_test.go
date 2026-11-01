package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/copilot/toolbridge"
)

// The copilot's tools run inside the hub, whose working directory is the
// workspace in the Docker image (WORKDIR /data): every project's files, the
// restricted ones too. A member's copilot attaches and saves only what the
// API hands it, never a file of the hub's own.
func TestCopilotBridgeReadsAndWritesNoFilesOfTheHub(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea restricting other: %d %s", response.Code, response.Body)
	}
	workspace := filepath.Dir(f.otherRoot)
	t.Chdir(workspace)
	bridge := openBridge(t, toolbridge.Options{
		Handler: f.handler, Cookie: cookieHeader(f.dan.cookies), Project: "main", Actor: "copilot:dan",
	})

	sent := callTool(t, bridge, "send_message", `{"to":"x","message":"x","files":["other/graph.yaml"]}`)
	if !sent.IsError || !strings.Contains(sent.Output, "working directory") {
		t.Fatalf("dan's copilot attached a restricted project's graph from the hub's disk: %+v", sent)
	}
	saved := callTool(t, bridge, "save_attachment", `{"message_id":"abc","name":"graph.yaml"}`)
	if !saved.IsError {
		t.Fatalf("save_attachment = %+v, want refused", saved)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".nodevas")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the copilot wrote into the hub's folder: %v", err)
	}
}
