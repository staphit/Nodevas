package gitrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// B-50: the project's newest commits and one commit's capped patch.
func TestRecentCommitsAndPatch(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	ann := Person{Name: "ann", Email: "ann@example.test"}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	write(t, dir, "nodes/n1.md", "one\n")
	first, err := r.Commit(ctx, nil, "n1: create", ann, at)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "nodes/n2.md", strings.Repeat("line of text\n", 30000))
	second, err := r.Commit(ctx, nil, "n2: create", Person{Name: "run:claude"}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	commits, err := r.RecentCommits(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].SHA != second || commits[0].Author != "run:claude" || commits[0].Subject != "n2: create" ||
		!commits[0].At.Equal(at.Add(time.Minute)) || commits[1].SHA != first {
		t.Fatalf("commits = %+v", commits)
	}
	if one, err := r.RecentCommits(ctx, 1); err != nil || len(one) != 1 {
		t.Fatalf("limit 1 = %+v, %v", one, err)
	}

	patch, truncated, err := r.CommitPatch(ctx, first, MaxPatchBytes)
	if err != nil || truncated || !strings.Contains(patch, "nodes/n1.md") || !strings.Contains(patch, "+one") {
		t.Fatalf("first patch = %q truncated %v err %v", patch, truncated, err)
	}
	patch, truncated, err = r.CommitPatch(ctx, second, MaxPatchBytes)
	if err != nil || !truncated || len(patch) > MaxPatchBytes {
		t.Fatalf("big patch: %d bytes truncated %v err %v", len(patch), truncated, err)
	}
	for _, bad := range []string{"HEAD", "--output=x", "abc", strings.Repeat("z", 40)} {
		if _, _, err := r.CommitPatch(ctx, bad, MaxPatchBytes); err == nil {
			t.Errorf("CommitPatch(%q) accepted", bad)
		}
	}
}
