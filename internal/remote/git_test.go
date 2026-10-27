package remote

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/gitrepo"
	"nodevas/internal/project"
)

// ---------- helpers ----------

func requireGit(t *testing.T) {
	t.Helper()
	if !gitrepo.Available() {
		t.Skip("git is not installed")
	}
}

// useLocalGitURLs opens the test-only seam for the length of one test.
func useLocalGitURLs(t *testing.T) {
	t.Helper()
	allowLocalGitURLs = true
	t.Cleanup(func() { allowLocalGitURLs = false })
}

// plainGit runs the machine's git the way a person working on the remote
// would, isolated from this machine's config.
func plainGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=other", "GIT_AUTHOR_EMAIL=other@example.test",
		"GIT_COMMITTER_NAME=other", "GIT_COMMITTER_EMAIL=other@example.test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newBareRemote(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "backup.git")
	plainGit(t, t.TempDir(), "init", "--quiet", "--bare", bare)
	return bare
}

// remoteBranch is where the bare repository's branch points, or "".
func remoteBranch(t *testing.T, bare, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

type gitWorkspace struct {
	manager *RemoteManager
	pm      *project.ProjectManager
	root    string
}

func newGitWorkspace(t *testing.T) gitWorkspace {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	pm, err := project.NewManagerAt(root, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return gitWorkspace{manager: NewManager(pm), pm: pm, root: pm.Workspace()}
}

// commit writes a file into the project (creating it) and commits it to the
// project's own repository, returning the new HEAD.
func (w gitWorkspace) commit(t *testing.T, name, file, content string) string {
	t.Helper()
	dir := filepath.Join(w.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := filepath.Join(dir, "graph.yaml")
	if _, err := os.Stat(graph); err != nil {
		if err := os.WriteFile(graph, []byte("nodes: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "nodes", file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := repo.Commit(context.Background(), nil, "test: "+file, gitrepo.Person{Name: "ann"}, time.Now())
	if err != nil || sha == "" {
		t.Fatalf("commit %q, %v", sha, err)
	}
	return sha
}

func (w gitWorkspace) useRemote(t *testing.T, bare string) {
	t.Helper()
	useLocalGitURLs(t)
	if err := w.manager.Save(Config{Kind: "git", GitURL: bare}); err != nil {
		t.Fatal(err)
	}
}

func projectStatus(t *testing.T, status SyncStatus, name string) GitProjectStatus {
	t.Helper()
	for _, project := range status.Projects {
		if project.Project == name {
			return project
		}
	}
	t.Fatalf("no status for %q in %+v", name, status.Projects)
	return GitProjectStatus{}
}

// recordGit captures every git command the package runs.
type gitCall struct {
	args []string
	env  []string
}

func recordGit(t *testing.T) func() []gitCall {
	t.Helper()
	var mu sync.Mutex
	var calls []gitCall
	original := runGit
	runGit = func(ctx context.Context, repo *gitrepo.Repo, env []string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, gitCall{args: slices.Clone(args), env: slices.Clone(env)})
		mu.Unlock()
		return original(ctx, repo, env, args...)
	}
	t.Cleanup(func() { runGit = original })
	return func() []gitCall {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// ---------- push ----------

func TestGitBackupPushesEachProjectToItsBranch(t *testing.T) {
	w := newGitWorkspace(t)
	bare := newBareRemote(t)
	w.useRemote(t, bare)
	alpha := w.commit(t, "alpha", "a.md", "one")
	beta := w.commit(t, "beta two", "b.md", "one")
	nested := w.commit(t, "group/inner", "c.md", "one")
	// A project with no history is not pushed, and no repository is made
	// for it.
	if err := os.MkdirAll(filepath.Join(w.root, "fresh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.root, "fresh", "graph.yaml"), []byte("nodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	status, err := w.manager.SyncStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != SyncRemoteMiss {
		t.Fatalf("before the first push: state %s, want %s", status.State, SyncRemoteMiss)
	}
	if _, err := w.manager.FlushBackup(ctx); err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string]string{
		"nodevas/alpha":        alpha,
		"nodevas/beta-two":     beta,
		"nodevas/group--inner": nested,
	} {
		if got := remoteBranch(t, bare, branch); got != want {
			t.Errorf("%s = %q, want %q", branch, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(w.root, "fresh", ".vised", "git")); !os.IsNotExist(err) {
		t.Errorf("a repository was created for a project without history: %v", err)
	}

	status, err = w.manager.SyncStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != SyncSynced {
		t.Fatalf("after the push: state %s, want synced (%+v)", status.State, status.Projects)
	}
	got := projectStatus(t, status, "alpha")
	if got.LastPush == nil || !got.LastPush.OK || got.LastPush.Commit != alpha {
		t.Fatalf("alpha's last push = %+v", got.LastPush)
	}
	if w.manager.LastBackupAt().IsZero() {
		t.Fatal("a completed git backup did not record the backup time")
	}
}

func TestGitSyncClassification(t *testing.T) {
	w := newGitWorkspace(t)
	bare := newBareRemote(t)
	w.useRemote(t, bare)
	ctx := context.Background()
	w.commit(t, "alpha", "a.md", "one")
	if _, err := w.manager.FlushBackup(ctx); err != nil {
		t.Fatal(err)
	}

	state := func() SyncState {
		t.Helper()
		status, err := w.manager.SyncStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return projectStatus(t, status, "alpha").State
	}
	if got := state(); got != SyncSynced {
		t.Fatalf("synced: got %s", got)
	}

	// Local newer: a new commit here, the remote still at the old one.
	local := w.commit(t, "alpha", "a.md", "two")
	if got := state(); got != SyncLocalNewer {
		t.Fatalf("local newer: got %s", got)
	}
	if _, err := w.manager.FlushBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if got := remoteBranch(t, bare, "nodevas/alpha"); got != local {
		t.Fatalf("after pushing local-newer the branch is %q, want %q", got, local)
	}

	// Remote newer: somebody else adds a commit on top of ours.
	other := t.TempDir()
	plainGit(t, other, "clone", "--quiet", "--branch", "nodevas/alpha", bare, "checkout")
	checkout := filepath.Join(other, "checkout")
	if err := os.WriteFile(filepath.Join(checkout, "elsewhere.md"), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	plainGit(t, checkout, "add", "elsewhere.md")
	plainGit(t, checkout, "commit", "--quiet", "-m", "theirs")
	plainGit(t, checkout, "push", "--quiet", "origin", "HEAD:refs/heads/nodevas/alpha")
	theirs := remoteBranch(t, bare, "nodevas/alpha")
	if got := state(); got != SyncRemoteNewer {
		t.Fatalf("remote newer: got %s", got)
	}

	// Diverged: and now a commit here too.
	mine := w.commit(t, "alpha", "a.md", "three")
	if got := state(); got != SyncConflict {
		t.Fatalf("diverged: got %s", got)
	}
	if _, err := w.manager.FlushBackup(ctx); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("flush on divergence = %v, want ErrSyncConflict", err)
	}
	if got := remoteBranch(t, bare, "nodevas/alpha"); got != theirs {
		t.Fatalf("divergence overwrote the remote branch: %q, want %q", got, theirs)
	}
	if _, err := w.manager.PushGitProject(ctx, "alpha"); !errors.Is(err, ErrSyncConflict) {
		t.Fatalf("project push on divergence = %v, want ErrSyncConflict", err)
	}
	if _, err := w.manager.PushGitProject(ctx, "no-such-project"); !errors.Is(err, ErrNoGitHistory) {
		t.Fatalf("push of a project without history = %v, want ErrNoGitHistory", err)
	}

	// Keep local: our history goes to a side branch, theirs stays.
	side, err := w.manager.KeepLocalGit(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(side, "nodevas/alpha-local-") {
		t.Fatalf("side branch %q", side)
	}
	if got := remoteBranch(t, bare, side); got != mine {
		t.Fatalf("side branch at %q, want %q", got, mine)
	}
	if got := remoteBranch(t, bare, "nodevas/alpha"); got != theirs {
		t.Fatalf("keep local moved the main branch to %q", got)
	}
}

func TestGitDivergedProjectHoldsBackOnlyItself(t *testing.T) {
	w := newGitWorkspace(t)
	bare := newBareRemote(t)
	w.useRemote(t, bare)
	ctx := context.Background()
	w.commit(t, "alpha", "a.md", "one")
	if _, err := w.manager.FlushBackup(ctx); err != nil {
		t.Fatal(err)
	}
	// Somebody rewrites alpha's branch to an unrelated commit.
	other := t.TempDir()
	plainGit(t, other, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(other, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plainGit(t, other, "add", "x.md")
	plainGit(t, other, "commit", "--quiet", "-m", "unrelated")
	plainGit(t, other, "push", "--quiet", "--force", bare, "HEAD:refs/heads/nodevas/alpha")
	beta := w.commit(t, "beta", "b.md", "one")

	if _, err := w.manager.FlushBackup(ctx); err != nil {
		t.Fatalf("flush with one diverged project: %v", err)
	}
	if got := remoteBranch(t, bare, "nodevas/beta"); got != beta {
		t.Fatalf("beta was not pushed: %q", got)
	}
	status, err := w.manager.SyncStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != SyncConflict {
		t.Fatalf("aggregate state %s, want conflict", status.State)
	}
}

func TestGitPushNeverForcesAndKeepsCredentialsOffTheCommandLine(t *testing.T) {
	w := newGitWorkspace(t)
	bare := newBareRemote(t)
	useLocalGitURLs(t)
	const token = "ghp_secretTokenValue123"
	if err := w.manager.SaveWithToken(Config{Kind: "git", GitURL: bare}, TokenChange{Set: token}); err != nil {
		t.Fatal(err)
	}
	w.commit(t, "alpha", "a.md", "one")
	calls := recordGit(t)
	if _, err := w.manager.FlushBackup(context.Background()); err != nil {
		t.Fatal(err)
	}

	basic := base64.StdEncoding.EncodeToString([]byte(defaultGitUser + ":" + token))
	header := "Authorization: Basic " + basic
	sawPush := false
	for _, call := range calls() {
		for _, arg := range call.args {
			if strings.Contains(arg, token) || strings.Contains(arg, basic) ||
				strings.Contains(strings.ToLower(arg), "authorization") {
				t.Fatalf("a credential is on the command line: %q", call.args)
			}
		}
		if call.args[0] != "push" {
			continue
		}
		sawPush = true
		for _, arg := range call.args {
			if arg == "--force" || arg == "-f" || strings.HasPrefix(arg, "--force") ||
				strings.HasPrefix(arg, "+") || arg == "--mirror" || arg == "--delete" {
				t.Fatalf("push is forced: %q", call.args)
			}
		}
		if !slices.Contains(call.env, "GIT_CONFIG_KEY_0=credential.helper") ||
			!slices.Contains(call.env, "GIT_CONFIG_VALUE_0=") {
			t.Fatalf("the credential helper is not reset: %q", call.env)
		}
		if !slices.Contains(call.env, "GIT_CONFIG_VALUE_"+keyIndex(call.env, "http.extraHeader")+"="+header) {
			t.Fatalf("the Authorization header is not passed through GIT_CONFIG: %q", call.env)
		}
	}
	if !sawPush {
		t.Fatal("no push was run")
	}
}

// keyIndex is the N of GIT_CONFIG_KEY_N=key.
func keyIndex(env []string, key string) string {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && value == key && strings.HasPrefix(name, "GIT_CONFIG_KEY_") {
			return strings.TrimPrefix(name, "GIT_CONFIG_KEY_")
		}
	}
	return "missing"
}

// ---------- errors ----------

func TestGitErrorsAreRedacted(t *testing.T) {
	const token = "tok_abcdef123456"
	session := newGitSession(gitTarget{url: "https://git.example.test/r.git", host: "git.example.test", port: "443"},
		defaultGitUser, token, []net.IP{net.ParseIP("93.184.216.34")})
	basic := base64.StdEncoding.EncodeToString([]byte(defaultGitUser + ":" + token))
	original := runGit
	runGit = func(context.Context, *gitrepo.Repo, []string, ...string) ([]byte, error) {
		return nil, errors.New("git push: exit status 128: fatal: unable to access " +
			"'https://someone:hunter2@git.example.test/r.git/': token " + token +
			"\n> Authorization: Basic " + basic + "\nbasic " + basic)
	}
	t.Cleanup(func() { runGit = original })

	_, err := session.run(context.Background(), nil, "push", session.target.url, "HEAD:refs/heads/nodevas/x")
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	for _, secret := range []string{token, basic, "hunter2", "someone:"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error still contains %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "unable to access") {
		t.Fatalf("redaction removed the useful part too: %s", message)
	}
}

// ---------- URLs and the address guard ----------

func TestParseGitURL(t *testing.T) {
	accepted := map[string]string{
		"https://github.com/org/repo.git":      "github.com",
		"HTTPS://Git.Example.com:8443/r.git":   "git.example.com",
		"  https://gitlab.com/group/sub/repo ": "gitlab.com",
	}
	for raw, host := range accepted {
		target, err := parseGitURL(raw)
		if err != nil || target.host != host || !strings.HasPrefix(target.url, "https://"+host) {
			t.Errorf("parseGitURL(%q) = %+v, %v", raw, target, err)
		}
	}
	refused := []string{
		"",
		"http://github.com/org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"git://github.com/org/repo.git",
		"file:///srv/repo.git",
		"/srv/repo.git",
		"https://user:secretpass@github.com/org/repo.git",
		"https://token@github.com/org/repo.git",
		"https:///no-host",
		"https://github.com/org/repo.git?x=1",
		"https://github.com/org/repo.git#frag",
		"https://github.com/org/re po.git",
		"ext::sh -c touch% /tmp/pwned",
	}
	for _, raw := range refused {
		_, err := parseGitURL(raw)
		if err == nil {
			t.Errorf("parseGitURL(%q) was accepted", raw)
			continue
		}
		if strings.Contains(err.Error(), "secretpass") {
			t.Errorf("the error repeats the password: %v", err)
		}
	}
}

func TestResolveGitHostRefusesPrivateAddressesOnANetworkedServer(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "::1", "fe80::1", "fd00::1", "0.0.0.0", "224.0.0.1"} {
		if _, err := resolveGitHost(ctx, host, false); err == nil {
			t.Errorf("%s was allowed on a networked server", host)
		}
	}
	if _, err := resolveGitHost(ctx, "140.82.112.3", false); err != nil {
		t.Errorf("a public address was refused: %v", err)
	}
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "fe80::1"} {
		if _, err := resolveGitHost(ctx, host, true); err != nil {
			t.Errorf("%s was refused on a loopback-only server: %v", host, err)
		}
	}
	if _, err := resolveGitHost(ctx, "224.0.0.1", true); err == nil {
		t.Error("a multicast address was allowed")
	}
}

func fakeDNS(t *testing.T, answer *string) {
	t.Helper()
	original := lookupGitHost
	lookupGitHost = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(*answer)}, nil
	}
	t.Cleanup(func() { lookupGitHost = original })
}

func TestGitDestinationIsCheckedAgainAtPushTime(t *testing.T) {
	w := newGitWorkspace(t)
	w.pm.SetRemote(true)
	answer := "140.82.112.3"
	fakeDNS(t, &answer)
	cfg := Config{Kind: "git", GitURL: "https://git.example.test/backup.git"}
	ctx := context.Background()
	if err := w.manager.CheckGitDestination(ctx, cfg); err != nil {
		t.Fatalf("public destination refused: %v", err)
	}
	if err := w.manager.Save(cfg); err != nil {
		t.Fatal(err)
	}
	w.commit(t, "alpha", "a.md", "one")

	// DNS now points the same name inside the network.
	answer = "10.1.2.3"
	if err := w.manager.CheckGitDestination(ctx, cfg); err == nil {
		t.Fatal("a private answer passed the save check")
	}
	calls := recordGit(t)
	_, err := w.manager.FlushBackup(ctx)
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("flush = %v, want the private-address refusal", err)
	}
	for _, call := range calls() {
		if call.args[0] == "push" || call.args[0] == "ls-remote" || call.args[0] == "fetch" {
			t.Fatalf("git reached the network after the refusal: %q", call.args)
		}
	}

	// A loopback-only server may use a private address.
	w.pm.SetRemote(false)
	if err := w.manager.CheckGitDestination(ctx, cfg); err != nil {
		t.Fatalf("loopback-only server: %v", err)
	}
}

func TestGitSessionPinsTheCheckedAddressAndRefusesRedirects(t *testing.T) {
	session := newGitSession(gitTarget{url: "https://git.example.test/r.git", host: "git.example.test", port: "443"},
		defaultGitUser, "", []net.IP{net.ParseIP("140.82.112.3"), net.ParseIP("2606:50c0:8000::153")})
	value := func(key string) string {
		index := keyIndex(session.env, key)
		for _, entry := range session.env {
			if name, v, ok := strings.Cut(entry, "="); ok && name == "GIT_CONFIG_VALUE_"+index {
				return v
			}
		}
		return "missing"
	}
	if got := value("http.curloptResolve"); got != "git.example.test:443:140.82.112.3,[2606:50c0:8000::153]" {
		t.Errorf("curloptResolve = %q", got)
	}
	if got := value("http.followRedirects"); got != "false" {
		t.Errorf("followRedirects = %q", got)
	}
	if got := value("http.extraHeader"); got != "missing" {
		t.Errorf("a header was sent without a token: %q", got)
	}
	if !slices.Contains(session.env, "GIT_ALLOW_PROTOCOL=https") {
		t.Errorf("protocols are not limited to https: %q", session.env)
	}
	// git reads GIT_CURL_VERBOSE as on whatever its value: setting it at all
	// would print every request, headers included, into the error.
	for _, entry := range session.env {
		if strings.HasPrefix(entry, "GIT_CURL_VERBOSE=") {
			t.Errorf("the session turns curl tracing on: %q", entry)
		}
	}
}

// ---------- the token ----------

func TestGitTokenIsEncryptedOutsideTheWorkspace(t *testing.T) {
	w := newGitWorkspace(t)
	const token = "ghp_storedTokenValue987"
	cfg := Config{Kind: "git", GitURL: "https://github.com/org/backup.git", GitUser: "ann"}
	if err := w.manager.SaveWithToken(cfg, TokenChange{Set: token}); err != nil {
		t.Fatal(err)
	}
	if !w.manager.HasGitToken(w.manager.Load()) {
		t.Fatal("the token was not stored")
	}
	for _, path := range []string{w.manager.ConfigPath(), strings.TrimSuffix(w.manager.ConfigPath(), ".json") + ".enc"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), token) {
			t.Fatalf("%s holds the token in the clear", path)
		}
	}
	_ = filepath.WalkDir(w.root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			if data, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(data), token) {
				t.Errorf("the workspace holds the token in %s", path)
			}
		}
		return nil
	})

	// Saving again without a token keeps it.
	cfg.AutoBackup = true
	if err := w.manager.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if !w.manager.HasGitToken(cfg) {
		t.Fatal("an unrelated save dropped the token")
	}
	// It never follows a change of URL or user silently.
	moved := cfg
	moved.GitURL = "https://elsewhere.example.com/backup.git"
	if err := w.manager.Save(moved); !errors.Is(err, ErrGitTokenRequired) {
		t.Fatalf("URL change without a token = %v, want ErrGitTokenRequired", err)
	}
	renamed := cfg
	renamed.GitUser = "bob"
	if err := w.manager.Save(renamed); !errors.Is(err, ErrGitTokenRequired) {
		t.Fatalf("user change without a token = %v, want ErrGitTokenRequired", err)
	}
	// Clearing it is a choice too.
	if err := w.manager.SaveWithToken(moved, TokenChange{Clear: true}); err != nil {
		t.Fatal(err)
	}
	if w.manager.HasGitToken(moved) {
		t.Fatal("the token survived a clear")
	}
	// Leaving the git kind forgets the token.
	if err := w.manager.SaveWithToken(cfg, TokenChange{Set: token}); err != nil {
		t.Fatal(err)
	}
	if err := w.manager.Save(Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSuffix(w.manager.ConfigPath(), ".json") + ".enc"); !os.IsNotExist(err) {
		t.Fatalf("the token file outlived the git backend: %v", err)
	}
}

func TestGitConfigValidation(t *testing.T) {
	manager := NewManager(nil)
	if err := manager.ValidateConfig(Config{Kind: "git", GitURL: "http://github.com/x.git"}); err == nil {
		t.Error("http:// was accepted")
	}
	if err := manager.ValidateConfig(Config{Kind: "git", GitURL: "https://github.com/x.git", GitUser: "a:b"}); err == nil {
		t.Error("a user with a colon was accepted")
	}
	if err := validateGitToken("two words"); err == nil {
		t.Error("a token with a space was accepted")
	}
	if !knownKind("git") {
		t.Error("a saved git config would be dropped on load")
	}
}

// ---------- branch names and the banner ----------

func TestGitBranchComponentIsAValidRef(t *testing.T) {
	requireGit(t)
	cases := map[string]string{
		"alpha":     "alpha",
		"beta two":  "beta-two",
		"group/sub": "group--sub",
		"中文專案":      "中文專案",
		".":         "my-workspace",
		"x.lock":    "x",
		"a..b":      "a.b",
		"_tmp":      "_tmp",
		"trail.":    "trail",
	}
	for project, want := range cases {
		got := gitBranchComponent(project, "my workspace")
		if got != want {
			t.Errorf("gitBranchComponent(%q) = %q, want %q", project, got, want)
		}
		cmd := exec.Command("git", "check-ref-format", "refs/heads/"+gitBranchPrefix+got)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%q is not a valid ref: %v %s", got, err, out)
		}
	}
}

func TestAggregateGitSync(t *testing.T) {
	status := func(states ...SyncState) []GitProjectStatus {
		out := []GitProjectStatus{}
		for _, state := range states {
			out = append(out, GitProjectStatus{State: state})
		}
		return out
	}
	cases := []struct {
		name     string
		projects []GitProjectStatus
		want     SyncState
	}{
		{"nothing", nil, SyncSynced},
		{"all synced", status(SyncSynced, SyncSynced), SyncSynced},
		{"all missing", status(SyncRemoteMiss, SyncRemoteMiss), SyncRemoteMiss},
		{"some missing", status(SyncSynced, SyncRemoteMiss), SyncLocalNewer},
		{"one ahead", status(SyncSynced, SyncLocalNewer), SyncLocalNewer},
		{"remote ahead wins over local", status(SyncLocalNewer, SyncRemoteNewer), SyncRemoteNewer},
		{"conflict wins", status(SyncRemoteNewer, SyncConflict, SyncSynced), SyncConflict},
		{"unknown ignored", append(status(SyncSynced), GitProjectStatus{Error: "offline"}), SyncSynced},
	}
	for _, c := range cases {
		if got := aggregateGitSync(c.projects); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The folder kind is untouched by the git kind's arrival.
func TestFolderKindStillBacksUpBundles(t *testing.T) {
	w := newGitWorkspace(t)
	w.commit(t, "alpha", "a.md", "one")
	folder := filepath.Join(t.TempDir(), "backups")
	if err := w.manager.Save(Config{Kind: "folder", Folder: folder}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.manager.FlushBackup(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), RemoteBundleExt) {
		t.Fatalf("folder backup wrote %v, %v", entries, err)
	}
	status, err := w.manager.SyncStatus(context.Background())
	if err != nil || status.State != SyncSynced || status.Projects != nil {
		t.Fatalf("folder status %+v, %v", status, err)
	}
}
