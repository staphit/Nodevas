package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/agentsetup"
)

func networkSession(t *testing.T, opts Options) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	if opts.AgentRole == "" {
		opts.AgentRole = "worker"
	}
	server, err := NewServer(ctx, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentsTravelFromWorkspaceToWorkspace(t *testing.T) {
	url, _ := liveServer(t)
	bobDir := t.TempDir()
	aliceDir := t.TempDir()
	// Alice's folder is a git repo, so the saved copy is kept out of it.
	if err := os.MkdirAll(filepath.Join(aliceDir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(aliceDir, ".git", "info", "exclude"), "# git ls-files --others --exclude-from=.git/info/exclude\n*.log")
	writeFile(t, filepath.Join(bobDir, "notes", "plan.md"), "# Plan\n\nShip it.\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "private key")

	bob := networkSession(t, Options{Server: url, Actor: "mcp:bob", SessionID: "codex:b1", SessionName: "web-codex", AgentKind: "codex", WorkDir: bobDir})
	alice := networkSession(t, Options{Server: url, Actor: "mcp:alice", SessionID: "claude:a1", SessionName: "api-claude", AgentKind: "claude", WorkDir: aliceDir})

	// Files must come from inside the sender's working directory.
	for _, path := range []string{"../" + filepath.Base(filepath.Dir(outside)) + "/secret.txt", outside, "notes/missing.md"} {
		res := call(t, bob, "send_message", map[string]any{"to": "api-claude", "message": "x", "files": []string{path}}, nil)
		if !res.IsError {
			t.Fatalf("attaching %s succeeded", path)
		}
	}

	var sent sendMessageOutput
	res := call(t, bob, "send_message", map[string]any{
		"to": "api-claude", "message": "see the plan", "notify": true, "files": []string{"notes/plan.md"},
	}, &sent)
	if res.IsError || len(sent.Deliveries) != 1 {
		t.Fatalf("send = %s / %+v", errorText(res), sent)
	}
	msgID := sent.Deliveries[0].MessageID

	var inbox checkInboxOutput
	call(t, alice, "check_inbox", nil, &inbox)
	if len(inbox.Messages) != 1 || !inbox.Messages[0].Notify || len(inbox.Messages[0].Files) != 1 ||
		inbox.Messages[0].Files[0] != (InboxFile{Name: "plan.md", Size: int64(len("# Plan\n\nShip it.\n"))}) {
		t.Fatalf("inbox = %+v", inbox.Messages)
	}
	if !strings.Contains(inbox.Formatted, "save_attachment") || !strings.Contains(inbox.Formatted, `"plan.md"`) {
		t.Fatalf("formatted inbox does not mention the file: %q", inbox.Formatted)
	}

	// Names and ids that would leave the inbox folder are refused.
	for _, args := range []map[string]any{
		{"message_id": msgID, "name": "../plan.md"},
		{"message_id": msgID, "name": `..\plan.md`},
		{"message_id": msgID, "name": "sub/plan.md"},
		{"message_id": "../..", "name": "plan.md"},
		{"message_id": msgID, "name": ".bashrc"},
		{"message_id": msgID, "name": "con.txt"},
	} {
		if res := call(t, alice, "save_attachment", args, nil); !res.IsError {
			t.Fatalf("save_attachment %v succeeded", args)
		}
	}

	var saved saveAttachmentOutput
	res = call(t, alice, "save_attachment", map[string]any{"message_id": msgID, "name": "plan.md"}, &saved)
	if res.IsError {
		t.Fatalf("save_attachment: %s", errorText(res))
	}
	want := filepath.Join(aliceDir, ".nodevas", "inbox", msgID, "plan.md")
	if saved.Path != want {
		t.Fatalf("saved to %s, want %s", saved.Path, want)
	}
	body, err := os.ReadFile(want)
	if err != nil || string(body) != "# Plan\n\nShip it.\n" {
		t.Fatalf("saved body = %q, %v", body, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(want)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("saved mode = %v, want 0600", info.Mode().Perm())
		}
	}

	// A second save refuses to overwrite, and the exclude line is not repeated.
	if err := os.WriteFile(want, []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := call(t, alice, "save_attachment", map[string]any{"message_id": msgID, "name": "plan.md"}, nil); !res.IsError {
		t.Fatal("save_attachment overwrote an existing file")
	}
	if body, _ := os.ReadFile(want); string(body) != "edited" {
		t.Fatalf("existing file changed to %q", body)
	}
	exclude, _ := os.ReadFile(filepath.Join(aliceDir, ".git", "info", "exclude"))
	if got := strings.Count(string(exclude), inboxExcludeLine); got != 1 || !strings.Contains(string(exclude), "*.log\n.nodevas/inbox/\n") {
		t.Fatalf("exclude = %q, want the inbox line once after the existing lines", exclude)
	}
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	call(t, alice, "save_attachment", map[string]any{"message_id": msgID, "name": "plan.md"}, nil)
	exclude, _ = os.ReadFile(filepath.Join(aliceDir, ".git", "info", "exclude"))
	if got := strings.Count(string(exclude), inboxExcludeLine); got != 1 {
		t.Fatalf("exclude line written %d times", got)
	}

	// The download is a plain-text attachment whatever the name says. (On a
	// loopback server everyone is the local administrator; who may fetch
	// what is exercised in internal/agents and internal/httpapi/agents.)
	response, err := http.Get(url + "/api/agents/messages/" + msgID + "/files/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download = %d %v", response.StatusCode, response.Header)
	}
}

func TestResolveInsideRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "b.txt"), "b")

	if got, err := resolveInside(dir, "a.txt"); err != nil || filepath.Base(got) != "a.txt" {
		t.Fatalf("inside = %q, %v", got, err)
	}
	if _, err := resolveInside(dir, filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("absolute inside: %v", err)
	}
	for _, p := range []string{"../b.txt", filepath.Join(outside, "b.txt"), "sub/../../b.txt", ""} {
		if _, err := resolveInside(dir, p); err == nil {
			t.Errorf("resolveInside(%q) succeeded", p)
		}
	}
	// A link inside that points outside is refused too.
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Join(outside, "b.txt"), link); err == nil {
		if _, err := resolveInside(dir, "link.txt"); err == nil {
			t.Fatal("a symlink out of the working directory was followed")
		}
	}
}

func TestReadAttachmentRefusesBinaryAndHugeFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bin.dat"), "\x00\x01\xff")
	writeFile(t, filepath.Join(dir, "big.txt"), strings.Repeat("a", 256<<10+1))
	writeFile(t, filepath.Join(dir, ".env"), "KEY=1")
	for _, name := range []string{"bin.dat", "big.txt", ".env"} {
		if _, err := ReadAttachment(filepath.Join(dir, name)); err == nil {
			t.Errorf("ReadAttachment(%s) succeeded", name)
		}
	}
	if _, err := ReadAttachment(dir); err == nil {
		t.Error("a folder was attached")
	}
}

func TestExcludeLineForASubfolderIsAnchoredToIt(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "svc", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := excludeInboxFromGit(sub); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if string(raw) != "svc/api/.nodevas/inbox/\n" {
		t.Fatalf("exclude = %q", raw)
	}
}

func TestExcludedFolderStaysOffTheNetwork(t *testing.T) {
	url, _ := liveServer(t)
	private := t.TempDir()
	config := filepath.Join(t.TempDir(), "agent-network.json")
	cfg := agentsetup.NetworkConfig{ExcludeFolders: []string{private}}
	if err := agentsetup.SaveNetworkConfig(config, cfg); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(private, "x.txt"), "x")

	off := networkSession(t, Options{Server: url, Actor: "mcp:hidden", SessionID: "claude:h1", SessionName: "hidden", AgentKind: "claude",
		WorkDir: filepath.Join(private, "repo"), ExclusionFile: config})
	roster := func() []string {
		response, err := http.Get(url + "/api/agents?all=true")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var payload struct {
			Sessions []struct {
				Name string `json:"name"`
			} `json:"sessions"`
		}
		_ = json.NewDecoder(response.Body).Decode(&payload)
		var names []string
		for _, s := range payload.Sessions {
			names = append(names, s.Name)
		}
		return names
	}
	if names := roster(); len(names) != 0 {
		t.Fatalf("excluded session on the roster: %v", names)
	}
	// Tools still work; sending says why it does not.
	var team listTeamOutput
	if res := call(t, off, "list_team", nil, &team); res.IsError {
		t.Fatalf("list_team off the network: %s", errorText(res))
	}
	res := call(t, off, "send_message", map[string]any{"to": "anyone", "message": "hi"}, nil)
	if !res.IsError || !strings.Contains(errorText(res), "off the Nodevas agent network") {
		t.Fatalf("send_message off the network = %s", errorText(res))
	}
	if names := roster(); len(names) != 0 {
		t.Fatalf("tool calls put the excluded session on the roster: %v", names)
	}

	// A runner's run ignores the exclusion.
	networkSession(t, Options{Server: url, Actor: "run:claude", SessionID: "claude:r1", SessionName: "run-1", AgentKind: "claude",
		WorkDir: private, ExclusionFile: config, IgnoreExclusion: true})
	if names := roster(); len(names) != 1 || names[0] != "run-1" {
		t.Fatalf("roster with a run = %v", names)
	}
}
