package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixed(text string) Message {
	return func([]Status) string { return text + "\n" }
}

func TestCommitTrackedCommitsExactlyTheListedFiles(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

	write(t, dir, "graph.yaml", "nodes: []\n")
	write(t, dir, "nodes/n1.md", "one")
	write(t, dir, "nodes/n1.files/big.bin", "attachment")
	var seen []Status
	message := func(changes []Status) string {
		seen = changes
		return "first\n"
	}
	first, err := r.CommitTracked(ctx, []string{"graph.yaml", "nodes/n1.md"}, message, Person{Name: "ann"}, at)
	if err != nil || !ValidSHA(first) {
		t.Fatalf("commit %q, %v", first, err)
	}
	if len(seen) != 2 || seen[0].Code != 'A' {
		t.Fatalf("changes = %+v", seen)
	}
	if again, err := r.CommitTracked(ctx, []string{"graph.yaml", "nodes/n1.md"}, fixed("same"), Person{}, at); err != nil || again != "" {
		t.Fatalf("unchanged tree committed %q, %v", again, err)
	}

	// A file left out of the list is a deletion; a file that vanished after
	// it was listed is too.
	write(t, dir, "nodes/n2.md", "two")
	if err := os.Remove(filepath.Join(dir, "nodes", "n1.md")); err != nil {
		t.Fatal(err)
	}
	second, err := r.CommitTracked(ctx, []string{"nodes/n1.md", "nodes/n2.md"}, message, Person{Name: "bob"}, at.Add(time.Minute))
	if err != nil || second == "" {
		t.Fatalf("second commit %q, %v", second, err)
	}
	codes := map[string]byte{}
	for _, change := range seen {
		codes[change.Path] = change.Code
	}
	if codes["graph.yaml"] != 'D' || codes["nodes/n1.md"] != 'D' || codes["nodes/n2.md"] != 'A' {
		t.Fatalf("changes = %+v", seen)
	}
	tracked, err := r.Run(ctx, nil, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(tracked)); got != "nodes/n2.md" {
		t.Fatalf("tracked = %q", got)
	}
}

// Content that is no longer on disk can still be committed, and the work
// tree is not touched doing it.
func TestCommitChangesLeavesTheWorkTreeAlone(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	write(t, dir, "nodes/n1.md", "live")
	if _, err := r.CommitTracked(ctx, []string{"nodes/n1.md"}, fixed("live"), Person{}, at); err != nil {
		t.Fatal(err)
	}

	sha, err := r.CommitChanges(ctx, []Change{{Path: "nodes/n1.md", Data: []byte("outside")}}, fixed("outside edit"), Person{}, at.Add(time.Minute))
	if err != nil || sha == "" {
		t.Fatalf("commit %q, %v", sha, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nodes", "n1.md")); string(got) != "live" {
		t.Fatalf("work tree = %q", got)
	}
	if got, err := r.Show(ctx, sha, "nodes/n1.md"); err != nil || string(got) != "outside" {
		t.Fatalf("show %q, %v", got, err)
	}
	if again, err := r.CommitChanges(ctx, []Change{{Path: "nodes/n1.md", Data: []byte("outside")}}, fixed("same"), Person{}, at); err != nil || again != "" {
		t.Fatalf("unchanged content committed %q, %v", again, err)
	}
	// The next tracked commit puts the live file back.
	back, err := r.CommitTracked(ctx, []string{"nodes/n1.md"}, fixed("live again"), Person{}, at.Add(2*time.Minute))
	if err != nil || back == "" {
		t.Fatalf("tracked commit %q, %v", back, err)
	}
	if got, _ := r.Show(ctx, back, "nodes/n1.md"); string(got) != "live" {
		t.Fatalf("tracked content = %q", got)
	}
	if _, err := r.CommitChanges(ctx, []Change{{Path: "../escape", Data: nil}}, fixed("x"), Person{}, at); err == nil {
		t.Fatal("a path outside the work tree was accepted")
	}
}

func TestImportAppendsBackdatedCommitsInOrder(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	base := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	write(t, dir, "graph.yaml", "start")
	if _, err := r.CommitTracked(ctx, []string{"graph.yaml"}, fixed("start"), Person{}, base); err != nil {
		t.Fatal(err)
	}
	err := r.Import(ctx, []Import{
		{Path: "nodes/n1.md", Data: []byte("v1"), Message: "import 1\n", At: base.Add(time.Hour)},
		{Path: "nodes/n1.md", Data: []byte("v2"), Message: "import 2\n", At: base.Add(2 * time.Hour),
			Author: Person{Name: "ann", Email: "ann@example.test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := r.Log(ctx, "nodes/n1.md", 0)
	if err != nil || len(log) != 2 {
		t.Fatalf("log %+v, %v", log, err)
	}
	if log[0].Subject != "import 2" || !log[0].At.Equal(base.Add(2*time.Hour)) || log[0].Author != "ann" ||
		log[1].Author != Committer.Name {
		t.Fatalf("log = %+v", log)
	}
	if got, _ := r.Show(ctx, log[1].SHA, "nodes/n1.md"); string(got) != "v1" {
		t.Fatalf("first import = %q", got)
	}
	// Imports build on HEAD rather than replacing it.
	if got, err := r.Show(ctx, log[0].SHA, "graph.yaml"); err != nil || string(got) != "start" {
		t.Fatalf("graph after import %q, %v", got, err)
	}
	found, err := r.LastMessageContaining(ctx, "import 1")
	if err != nil || !strings.HasPrefix(found, "import 1") {
		t.Fatalf("message %q, %v", found, err)
	}
}

func TestImportIntoAnEmptyRepository(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := r.Import(ctx, []Import{{Path: "graph.yaml", Data: []byte("g"), Message: "first\n", At: at}}); err != nil {
		t.Fatal(err)
	}
	if log, err := r.Log(ctx, "graph.yaml", 0); err != nil || len(log) != 1 {
		t.Fatalf("log %+v, %v", log, err)
	}
}

// A node moved into a folder keeps its history, and every version reports
// the name it had and its size.
func TestHistoryFollowsRenamesAndSkipsDeletions(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	content := strings.Repeat("a long enough body for rename detection\n", 4)
	write(t, dir, "nodes/n1.md", content)
	first, err := r.CommitTracked(ctx, []string{"nodes/n1.md"}, fixed("create"), Person{}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nodes", "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "nodes", "n1.md"), filepath.Join(dir, "nodes", "folder", "n1.md")); err != nil {
		t.Fatal(err)
	}
	moved, err := r.CommitTracked(ctx, []string{"nodes/folder/n1.md"}, fixed("move"), Person{}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	versions, err := r.History(ctx, "nodes/folder/n1.md", 10)
	if err != nil || len(versions) != 2 {
		t.Fatalf("history %+v, %v", versions, err)
	}
	if versions[0].SHA != moved || versions[0].Path != "nodes/folder/n1.md" ||
		versions[1].SHA != first || versions[1].Path != "nodes/n1.md" ||
		versions[1].Size != int64(len(content)) {
		t.Fatalf("history = %+v", versions)
	}

	if err := os.Remove(filepath.Join(dir, "nodes", "folder", "n1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitTracked(ctx, nil, fixed("delete"), Person{}, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	versions, err = r.History(ctx, "nodes/folder/n1.md", 10)
	if err != nil || len(versions) != 2 {
		t.Fatalf("history after delete %+v, %v", versions, err)
	}
}

// A path kept stays as HEAD has it, whatever the work tree holds now: neither
// committed from disk nor dropped as a deletion. A kept path HEAD lacks stays
// out of the commit.
func TestCommitTrackedKeepingLeavesKeptPathsAtHead(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)

	if _, err := r.CommitChanges(ctx, []Change{{Path: "nodes/n1.md", Data: []byte("start")}},
		fixed("start"), Person{}, at); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "graph.yaml", "nodes: []\n")
	write(t, dir, "nodes/n1.md", "halfway")
	write(t, dir, "nodes/n2.md", "new")
	commit, err := r.CommitTrackedKeeping(ctx,
		[]string{"graph.yaml", "nodes/n1.md", "nodes/n2.md"}, []string{"nodes/n1.md", "nodes/n2.md"},
		fixed("baseline"), Person{}, at.Add(time.Minute))
	if err != nil || commit == "" {
		t.Fatalf("commit %q, %v", commit, err)
	}
	tracked, err := r.Run(ctx, nil, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(tracked)); strings.Join(got, ",") != "graph.yaml,nodes/n1.md" {
		t.Fatalf("HEAD tracks %v, want graph.yaml and the kept nodes/n1.md only", got)
	}
	kept, err := r.Run(ctx, nil, "show", "HEAD:nodes/n1.md")
	if err != nil || string(kept) != "start" {
		t.Fatalf("kept path is %q, %v; want HEAD's %q", kept, err, "start")
	}
}
