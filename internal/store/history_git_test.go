package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/gitrepo"
	"nodevas/internal/identity"
)

var (
	ann   = identity.Actor{ID: "u-ann", Name: "ann"}
	bob   = identity.Actor{ID: "u-bob", Name: "bob"}
	carol = identity.Actor{ID: "u-carol", Name: "carol"}
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// seedProject writes a project with two nodes, as it would be on disk before
// the hub ever opened it.
func seedProject(t *testing.T, root string) {
	t.Helper()
	graph, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha", Title: "Alpha"}, {ID: "beta", Title: "Beta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta"} {
		if err := os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// openGitStore opens root with the git backend and the given save window,
// and waits for the first-use import to finish.
func openGitStore(t *testing.T, root string, window time.Duration) *Store {
	t.Helper()
	requireGit(t)
	s := NewStore(root)
	s.historyMu.Lock()
	s.historyImpl = newGitHistory(s, window)
	s.historyMu.Unlock()
	// Registered after t.TempDir, so it runs first: no commit is still
	// writing into the directory when it is removed.
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func newGitStore(t *testing.T, window time.Duration) (*Store, string) {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	seedProject(t, root)
	return openGitStore(t, root, window), root
}

func repoOf(t *testing.T, root string) *gitrepo.Repo {
	t.Helper()
	repo, err := gitrepo.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func commitCount(t *testing.T, root string) int {
	t.Helper()
	out, err := repoOf(t, root).Run(context.Background(), nil, "rev-list", "--count", "HEAD")
	if err != nil {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func headMessage(t *testing.T, root string) (author, message string) {
	t.Helper()
	out, err := repoOf(t, root).Run(context.Background(), nil, "log", "-n1", "--format=%an <%ae>%x00%B")
	if err != nil {
		t.Fatal(err)
	}
	author, message, _ = strings.Cut(string(out), "\x00")
	return author, message
}

func saveNode(t *testing.T, s *Store, actor identity.Actor, id, body string) {
	t.Helper()
	_, rev, err := s.LoadNodeContent(id)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, _, err := s.SaveNodeContent(actor, id, body, rev); err != nil {
		t.Fatal(err)
	}
}

// eventually polls for something a background commit does.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestGitHistorySavesWithinTheWindowAreOneCommit(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	before := commitCount(t, root)
	if before == 0 {
		t.Fatal("first use committed nothing")
	}

	for i := range 5 {
		saveNode(t, s, ann, "alpha", "draft "+strconv.Itoa(i)+"\n")
	}
	if got := commitCount(t, root); got != before {
		t.Fatalf("saves committed before the window closed: %d commits, want %d", got, before)
	}
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := commitCount(t, root); got != before+1 {
		t.Fatalf("got %d commits, want one more than %d", got, before)
	}
	author, message := headMessage(t, root)
	if author != "ann <ann@nodevas.local>" || !strings.HasPrefix(message, "Alpha: edit") {
		t.Fatalf("head = %q / %q", author, message)
	}
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.ReadHistory("nodes/alpha.md", versions[0].Name)
	if err != nil || !strings.Contains(string(data), "draft 4") {
		t.Fatalf("newest version %q, %v; want the last save", data, err)
	}
	if versions[0].Author != "ann" || versions[0].Size != int64(len(data)) || versions[0].At == "" {
		t.Fatalf("version = %+v", versions[0])
	}
}

func TestGitHistoryCommitsWhenTheWindowCloses(t *testing.T) {
	s, root := newGitStore(t, 50*time.Millisecond)
	before := commitCount(t, root)
	saveNode(t, s, ann, "alpha", "hello\n")
	eventually(t, "the window's commit", func() bool { return commitCount(t, root) == before+1 })
}

func TestGitHistoryNamesEveryAuthorOfAWindow(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "ann's\n")
	saveNode(t, s, bob, "beta", "bob's\n")
	saveNode(t, s, ann, "alpha", "ann's again\n")
	agent := carol
	agent.Agent = identity.AgentWorker
	saveNode(t, s, agent, "beta", "carol's agent\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	author, message := headMessage(t, root)
	if author != "ann <ann@nodevas.local>" {
		t.Fatalf("author = %q, want the window's first saver", author)
	}
	for _, want := range []string{
		"Alpha: edit",
		"Beta: edit",
		"\n\nCo-authored-by: bob <bob@nodevas.local>\n",
		"Co-authored-by: carol (worker) <carol+worker@nodevas.local>\n",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q lacks %q", message, want)
		}
	}
	if strings.Count(message, "Co-authored-by:") != 2 {
		t.Fatalf("message %q: ann is the author, not a co-author too", message)
	}
}

func TestGitHistoryRestoreCommitsAtOnceAndKeepsWhatItReplaced(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "version one\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions %+v, %v", versions, err)
	}
	one := versions[0].Name
	if !gitrepo.ValidSHA(one) {
		t.Fatalf("version name %q is not a commit ID", one)
	}

	// Bob's edit is still waiting for the window when carol restores.
	saveNode(t, s, bob, "alpha", "version two\n")
	before := commitCount(t, root)
	if err := s.RestoreHistory(carol, "nodes/alpha.md", one); err != nil {
		t.Fatal(err)
	}
	// No flush: the restore commits on its own.
	eventually(t, "the restore commit", func() bool { return commitCount(t, root) >= before+2 })

	current, _, err := s.LoadNodeContent("alpha")
	if err != nil || !strings.Contains(current, "version one") {
		t.Fatalf("restored file %q, %v", current, err)
	}
	after, err := s.ListHistory("nodes/alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Author != "carol" {
		t.Fatalf("newest version by %q, want the restorer", after[0].Author)
	}
	if data, _ := s.ReadHistory("nodes/alpha.md", after[0].Name); !strings.Contains(string(data), "version one") {
		t.Fatalf("restore commit holds %q", data)
	}
	// Bob's uncommitted edit was committed, as his, before it was replaced.
	if after[1].Author != "bob" {
		t.Fatalf("version before the restore by %q, want bob", after[1].Author)
	}
	if data, _ := s.ReadHistory("nodes/alpha.md", after[1].Name); !strings.Contains(string(data), "version two") {
		t.Fatalf("version before the restore holds %q", data)
	}
	_, message := headMessage(t, root)
	if !strings.HasPrefix(message, "Alpha: restore") {
		t.Fatalf("restore message = %q", message)
	}
}

func TestGitHistoryDeleteCommitsAtOnce(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "doomed\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteNode(bob, "alpha"); err != nil {
		t.Fatal(err)
	}
	repo := repoOf(t, root)
	eventually(t, "the delete commit", func() bool {
		out, err := repo.Run(context.Background(), nil, "ls-tree", "--name-only", "-r", "HEAD")
		return err == nil && !strings.Contains(string(out), "nodes/alpha.md")
	})
	author, message := headMessage(t, root)
	if author != "bob <bob@nodevas.local>" || !strings.Contains(message, "Alpha: delete") {
		t.Fatalf("delete commit %q / %q", author, message)
	}
	// The trash is still the file backend's.
	if items, err := s.ListTrash(); err != nil || len(items) != 1 {
		t.Fatalf("trash %+v, %v", items, err)
	}
}

// Content this store did not write is committed before it is overwritten,
// however recently the store saved: it is the only copy left.
func TestGitHistoryKeepsAnOutsideEdit(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "mine\n")
	theirs := "---\nid: alpha\n---\ntheirs\n"
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	saveNode(t, s, ann, "alpha", "mine again\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil || len(versions) < 2 {
		t.Fatalf("versions %+v, %v", versions, err)
	}
	data, err := s.ReadHistory("nodes/alpha.md", versions[1].Name)
	if err != nil || string(data) != theirs {
		t.Fatalf("version before the save = %q, %v; want the outside edit", data, err)
	}
	if versions[1].Author != gitrepo.Committer.Name {
		t.Fatalf("outside edit attributed to %q", versions[1].Author)
	}
}

// A node moved into a folder is still nodes/<id>.md to the UI, and its
// versions from before the move stay readable.
func TestGitHistoryFollowsANodeIntoAFolder(t *testing.T) {
	s, _ := newGitStore(t, time.Hour)
	body := strings.Repeat("a paragraph that survives the move intact\n", 4)
	saveNode(t, s, ann, "alpha", body)
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNodeFolder("folder"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveNodesToFolder([]string{"alpha"}, "folder"); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil || len(versions) < 2 {
		t.Fatalf("versions %+v, %v", versions, err)
	}
	oldest := versions[len(versions)-1]
	if _, err := s.ReadHistory("nodes/alpha.md", oldest.Name); err != nil {
		t.Fatalf("read a version from before the move: %v", err)
	}
	if err := s.RestoreHistory(ann, "nodes/alpha.md", versions[1].Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "nodes", "alpha.md")); !os.IsNotExist(err) {
		t.Fatal("restore wrote outside the node's folder")
	}
}

func TestGitHistoryRefusesFileVersionNames(t *testing.T) {
	s, _ := newGitStore(t, time.Hour)
	for _, version := range []string{"20200101-000000.000000000.yaml", "HEAD", "main", "--all"} {
		if _, err := s.ReadHistory("graph.yaml", version); err == nil {
			t.Errorf("version %q accepted", version)
		}
	}
	if _, err := s.ReadHistory("run/state.json", strings.Repeat("a", 40)); err == nil {
		t.Error("an untracked path was accepted")
	}
}

func TestGitHistoryCloseCommitsWhatIsPending(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	before := commitCount(t, root)
	saveNode(t, s, ann, "alpha", "last words\n")
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := commitCount(t, root); got != before+1 {
		t.Fatalf("got %d commits, want %d", got, before+1)
	}
}

// writeCopy puts a file-backend copy where the old history kept it.
func writeCopy(t *testing.T, root, rel string, at time.Time, content string) string {
	t.Helper()
	dir := filepath.Join(root, DataDir, "history", filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := at.UTC().Format("20060102-150405.000000000") + filepath.Ext(rel)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel + "/" + name
}

func importTrailers(t *testing.T, root string) []string {
	t.Helper()
	out, err := repoOf(t, root).Run(context.Background(), nil, "log", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(line, importTrailer); ok {
			found = append(found, value)
		}
	}
	return found
}

func TestGitHistoryImportsOldCopiesOnce(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	seedProject(t, root)
	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	writeCopy(t, root, "nodes/alpha.md", base, "alpha one\n")
	writeCopy(t, root, "graph.yaml", base.Add(time.Minute), "version: 1\n")
	writeCopy(t, root, "nodes/alpha.md", base.Add(2*time.Minute), "alpha two\n")
	// Same second, different nanoseconds: the order still holds.
	writeCopy(t, root, "nodes/alpha.md", base.Add(2*time.Minute+time.Millisecond), "alpha three\n")
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("alpha live\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := openGitStore(t, root, time.Hour)
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, version := range versions {
		data, err := s.ReadHistory("nodes/alpha.md", version.Name)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, string(data))
	}
	want := []string{"alpha live\n", "alpha three\n", "alpha two\n", "alpha one\n"}
	if strings.Join(contents, "|") != strings.Join(want, "|") {
		t.Fatalf("versions = %q, want %q", contents, want)
	}
	if versions[3].At != base.Format(time.RFC3339) || versions[3].Author != gitrepo.Committer.Name {
		t.Fatalf("oldest import = %+v, want dated from its copy and by Nodevas", versions[3])
	}
	if _, err := os.Stat(filepath.Join(root, DataDir, historyImportMarker)); err != nil {
		t.Fatalf("no import marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, DataDir, "history", "nodes", "alpha.md")); err != nil {
		t.Fatalf("the old copies were not left in place: %v", err)
	}
	commits := commitCount(t, root)

	// A second store over the same folder, with the marker: nothing again.
	_ = s.Close(context.Background())
	openGitStore(t, root, time.Hour)
	if got := commitCount(t, root); got != commits {
		t.Fatalf("reopening committed again: %d, was %d", got, commits)
	}
	// Without the marker (a crash just before it was written): still nothing.
	if err := os.Remove(filepath.Join(root, DataDir, historyImportMarker)); err != nil {
		t.Fatal(err)
	}
	openGitStore(t, root, time.Hour)
	if got := commitCount(t, root); got != commits {
		t.Fatalf("an import without its marker repeated itself: %d, was %d", got, commits)
	}
}

// An import that stopped partway resumes after the last copy it committed.
func TestGitHistoryImportResumesAfterACrash(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	seedProject(t, root)
	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	var sources []string
	for i := range 5 {
		sources = append(sources, writeCopy(t, root, "nodes/beta.md", base.Add(time.Duration(i)*time.Second), "beta "+strconv.Itoa(i)+"\n"))
	}

	// The first two copies made it in before the hub died.
	repo := repoOf(t, root)
	var partial []gitrepo.Import
	for i, source := range sources[:2] {
		partial = append(partial, gitrepo.Import{
			Path:    "nodes/beta.md",
			Data:    []byte("beta " + strconv.Itoa(i) + "\n"),
			Message: "Beta: earlier version\n\n" + importTrailer + source + "\n",
			At:      base.Add(time.Duration(i) * time.Second),
		})
	}
	if err := repo.Import(context.Background(), partial); err != nil {
		t.Fatal(err)
	}

	openGitStore(t, root, time.Hour)
	trailers := importTrailers(t, root)
	if len(trailers) != len(sources) {
		t.Fatalf("imported %d copies, want %d: %q", len(trailers), len(sources), trailers)
	}
	seen := map[string]int{}
	for _, trailer := range trailers {
		seen[trailer]++
	}
	for _, source := range sources {
		if seen[source] != 1 {
			t.Fatalf("copy %s imported %d times", source, seen[source])
		}
	}
}

func TestHistoryFallsBackToFilesWithoutGit(t *testing.T) {
	saved := gitAvailable
	gitAvailable = func() bool { return false }
	t.Cleanup(func() { gitAvailable = saved })

	root := t.TempDir()
	seedProject(t, root)
	s := NewStore(root)
	s.UseHistory(HistoryGit)
	if got := s.HistoryBackend(); got != HistoryFiles {
		t.Fatalf("backend = %q without git, want files", got)
	}
	saveNode(t, s, ann, "alpha", "first\n")
	saveNode(t, s, ann, "alpha", "second\n")
	versions, err := s.ListHistory("nodes/alpha.md")
	if err != nil || len(versions) != 1 || !historyVersionPattern.MatchString(versions[0].Name) {
		t.Fatalf("versions %+v, %v", versions, err)
	}
	// The one copy is the seeded file, which the first save replaced.
	kept, err := s.ReadHistory("nodes/alpha.md", versions[0].Name)
	if err != nil || string(kept) != "alpha\n" {
		t.Fatalf("copy %q, %v", kept, err)
	}
	if err := s.RestoreHistory(ann, "nodes/alpha.md", versions[0].Name); err != nil {
		t.Fatal(err)
	}
	if current, _, _ := s.LoadNodeContent("alpha"); current != "alpha\n" {
		t.Fatalf("restored = %q", current)
	}
	if _, err := os.Stat(filepath.Join(root, ".vised", "git")); !os.IsNotExist(err) {
		t.Fatal("a repository was created without git")
	}
	if _, err := s.ReadHistory("nodes/alpha.md", strings.Repeat("a", 40)); err == nil {
		t.Fatal("the file backend accepted a commit ID")
	}
}

func TestParseHistoryMode(t *testing.T) {
	for value, want := range map[string]HistoryMode{"": HistoryAuto, "GIT": HistoryGit, " files ": HistoryFiles, "auto": HistoryAuto} {
		if got, err := ParseHistoryMode(value); err != nil || got != want {
			t.Errorf("ParseHistoryMode(%q) = %q, %v", value, got, err)
		}
	}
	if _, err := ParseHistoryMode("svn"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestCommitMessageComesFromPathsNotContent(t *testing.T) {
	titles := map[string]string{"n1": "Auth API", "n2": "Billing\nteam"}
	message := commitMessage(titles, map[string]string{"nodes/n2.md": "restore"}, "", nil)([]gitrepo.Status{
		{Code: 'M', Path: "nodes/n1.pages/p1.md"},
		{Code: 'M', Path: "nodes/n1.md"},
		{Code: 'M', Path: "nodes/n2.md"},
		{Code: 'D', Path: "nodes/n3.md"},
		{Code: 'A', Path: "nodes/folder/n3.md"},
		{Code: 'M', Path: "graph.yaml"},
	})
	want := "Auth API: edit page, edit; Billing team: restore; n3: move; graph: edit\n"
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

// A session's starting version is committed at once, before the window
// closes: the history panel lists it straight away, and content written and
// then edited within one window is not lost to the window's single commit.
func TestGitHistoryKeepsWhereASessionStartedAtOnce(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "starter\n")
	// Let the store's own first write settle as a session of its own.
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for path, mark := range s.historyMarks {
		mark.snapshotAt = time.Time{}
		s.historyMarks[path] = mark
	}
	s.mu.Unlock()
	saveNode(t, s, ann, "alpha", "first edit\n")
	saveNode(t, s, ann, "alpha", "second edit\n")

	// No flush, the window is an hour: the start is there anyway.
	eventually(t, "the session's start", func() bool {
		versions, err := s.ListHistory("nodes/alpha.md")
		if err != nil || len(versions) == 0 {
			return false
		}
		data, err := s.ReadHistory("nodes/alpha.md", versions[0].Name)
		return err == nil && strings.Contains(string(data), "starter")
	})
	// The later saves in the session wait for the window.
	out, err := repoOf(t, root).Run(context.Background(), nil, "log", "--format=%s", "-p", "-n1")
	if err != nil || strings.Contains(string(out), "second edit") {
		t.Fatalf("the session's later saves committed early: %s %v", out, err)
	}
}
