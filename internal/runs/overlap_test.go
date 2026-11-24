package runs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

type overlapEvent struct {
	global  bool
	project string
	nodeID  string
	runID   string
	evType  string
}

type overlapMail struct {
	project string
	runID   string
	body    string
}

type overlapFixture struct {
	*busyFixture
	mu     sync.Mutex
	events []overlapEvent
	mails  []overlapMail
	mailFn func(ctx context.Context, project, runID, body string) error
}

func newOverlapFixture(t *testing.T) *overlapFixture {
	t.Helper()
	bf := newBusyFixture(t, 4)
	of := &overlapFixture{busyFixture: bf}
	of.d.changed = func(project, nodeID string) {
		of.mu.Lock()
		of.events = append(of.events, overlapEvent{project: project, nodeID: nodeID})
		of.mu.Unlock()
	}
	of.d.SetGlobalNotify(func(evType string, r Record) {
		of.mu.Lock()
		of.events = append(of.events, overlapEvent{global: true, evType: evType, runID: r.ID})
		of.mu.Unlock()
	})
	of.d.SetCoordinationMail(func(ctx context.Context, project, runID, body string) error {
		of.mu.Lock()
		fn := of.mailFn
		of.mu.Unlock()
		if fn != nil {
			if err := fn(ctx, project, runID, body); err != nil {
				return err
			}
		}
		of.mu.Lock()
		of.mails = append(of.mails, overlapMail{project: project, runID: runID, body: body})
		of.mu.Unlock()
		return nil
	})
	return of
}

func (f *overlapFixture) clearEvents() {
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
}

func (f *overlapFixture) getEvents() []overlapEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]overlapEvent, len(f.events))
	copy(out, f.events)
	return out
}

func (f *overlapFixture) getMails() []overlapMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]overlapMail, len(f.mails))
	copy(out, f.mails)
	return out
}

func (f *overlapFixture) makeRunningNodeRun(t *testing.T, actorID, actorName, runnerID, project, nodeID, title, repoKey string) Record {
	t.Helper()
	ctx := context.Background()
	r, err := f.s.Create(ctx, New{
		Project:       project,
		NodeID:        nodeID,
		NodeTitle:     title,
		RunnerID:      runnerID,
		RunnerName:    "pc",
		OwnerID:       actorID,
		Agent:         "claude",
		Model:         "opus",
		RepoID:        "repo-1",
		RepoName:      "api",
		Base:          "main",
		RepoKey:       repoKey,
		CreatedBy:     actorID,
		CreatedByName: actorName,
		Origin:        runnerwire.OriginPerson,
	})
	if err != nil {
		t.Fatalf("Create node run: %v", err)
	}
	if _, err := f.s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	r, err = f.s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: runnerID, Base: "main", Branch: "agent/" + nodeID})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *overlapFixture) makeRunningGlobalRun(t *testing.T, actorID, actorName, runnerID, title, repoID, repoKey, groupID string) Record {
	t.Helper()
	ctx := context.Background()
	r, err := f.s.Create(ctx, New{
		Global:        true,
		NodeTitle:     title,
		BranchSlug:    "task-" + repoID,
		NoPR:          true,
		RunnerID:      runnerID,
		RunnerName:    "pc",
		OwnerID:       actorID,
		Agent:         "claude",
		Model:         "opus",
		RepoID:        repoID,
		RepoName:      repoID,
		Base:          "main",
		RepoKey:       repoKey,
		GroupID:       groupID,
		CreatedBy:     actorID,
		CreatedByName: actorName,
		Origin:        runnerwire.OriginPerson,
	})
	if err != nil {
		t.Fatalf("Create global run: %v", err)
	}
	if _, err := f.s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	r, err = f.s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: runnerID, Base: "main", Branch: "agent/global"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const testRepoKeyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestTouchedOnlyOwnRunningRun(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Task 1", testRepoKeyA)

	// Another runner cannot report touched files for ann's run.
	if err := f.d.Touched(ctx, f.bob.ID, r.ID, runnerwire.Touched{Files: []string{"src/a.go"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign runner err = %v, want ErrNotFound", err)
	}

	// Queued run is refused with ErrTransition.
	q, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n2", NodeTitle: "Queued", RunnerID: f.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", RepoKey: testRepoKeyA, CreatedBy: "u-ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, q.ID, runnerwire.Touched{Files: []string{"src/a.go"}}); !errors.Is(err, ErrTransition) {
		t.Fatalf("queued run err = %v, want ErrTransition", err)
	}

	// Own running run succeeds.
	if err := f.d.Touched(ctx, f.ann.ID, r.ID, runnerwire.Touched{Files: []string{"src/a.go"}}); err != nil {
		t.Fatalf("own running run err = %v", err)
	}

	// Finished run is refused with ErrTransition.
	if _, err := f.s.Finish(ctx, r.ID, f.ann.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r.ID, runnerwire.Touched{Files: []string{"src/b.go"}}); !errors.Is(err, ErrTransition) {
		t.Fatalf("finished run err = %v, want ErrTransition", err)
	}
}

func TestTouchedDropsBadPathKeepsRest(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Task 1", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n2", "Task 2", testRepoKeyA)

	err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{
		Files: []string{"/etc/passwd", "../escape.go", "bad\x00path", "a\\b.go", strings.Repeat("x", 1025), "src/good.go"},
	})
	if err != nil {
		t.Fatalf("Touched with bad paths should drop them, not fail: %v", err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"src/good.go"}}); err != nil {
		t.Fatal(err)
	}

	overlaps, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(overlaps) != 1 || len(overlaps[0].Paths) != 1 || overlaps[0].Paths[0] != "src/good.go" {
		t.Fatalf("overlaps = %+v, want only src/good.go", overlaps)
	}
}

func TestOverlapPairOnceAndCounts(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Fix login", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n2", "Fix session", testRepoKeyA)

	paths := make([]string, 25)
	for i := range paths {
		paths[i] = "src/file_" + string(rune('a'+i)) + ".go"
	}
	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: paths}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: paths}); err != nil {
		t.Fatal(err)
	}

	// Stored once in run_overlaps (run_a < run_b).
	var rowCount int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_overlaps`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("run_overlaps rows = %d, want 1", rowCount)
	}

	// Run.OverlapCount is 1 on both runs.
	got1, err := f.s.Get(ctx, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := f.s.Get(ctx, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.OverlapCount != 1 || got2.OverlapCount != 1 {
		t.Fatalf("OverlapCount = (%d, %d), want (1, 1)", got1.OverlapCount, got2.OverlapCount)
	}

	// Paths capped at 20, pathCount and strongCount are 25.
	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 || len(ov[0].Paths) != runnerwire.MaxOverlapPaths || ov[0].PathCount != 25 || ov[0].StrongCount != 25 {
		t.Fatalf("overlaps = %+v, want 20 paths, pathCount=25, strongCount=25", ov)
	}
}

func TestOverlapNotifiesOnFirstStrongPath(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Task 1", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n2", "Task 2", testRepoKeyA)
	f.clearEvents()

	// 1. Add-only on both sides -> weak, no notification.
	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"i18n.ts"}, AddOnly: []string{"i18n.ts"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"i18n.ts"}, AddOnly: []string{"i18n.ts"}}); err != nil {
		t.Fatal(err)
	}
	if evs := f.getEvents(); len(evs) != 0 {
		t.Fatalf("weak overlap emitted %d events, want 0", len(evs))
	}

	// 2. First strong path -> notifies!
	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	if evs := f.getEvents(); len(evs) == 0 {
		t.Fatalf("first strong path emitted 0 events, want notification")
	}

	// 3. 2nd, 3rd, 4th strong paths -> no new notification until crossing 5.
	f.clearEvents()
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"b.go", "c.go", "d.go"}}); err != nil {
		t.Fatal(err)
	}
	if evs := f.getEvents(); len(evs) != 0 {
		t.Fatalf("paths 2..4 emitted %d events, want 0", len(evs))
	}

	// 4. Crossing 5 -> notifies again!
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"e.go"}}); err != nil {
		t.Fatal(err)
	}
	if evs := f.getEvents(); len(evs) == 0 {
		t.Fatalf("crossing 5 strong paths emitted 0 events, want notification")
	}
}

func TestOverlapAddOnlyIsWeak(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Task 1", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n2", "Task 2", testRepoKeyA)
	f.clearEvents()

	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"web/src/i18n.ts"}, AddOnly: []string{"web/src/i18n.ts"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"web/src/i18n.ts"}, AddOnly: []string{"web/src/i18n.ts"}}); err != nil {
		t.Fatal(err)
	}

	// Shown in list with strongCount=0, OverlapCount=0, no notice, no mail.
	got1, _ := f.s.Get(ctx, r1.ID)
	if got1.OverlapCount != 0 {
		t.Fatalf("weak overlap OverlapCount = %d, want 0", got1.OverlapCount)
	}
	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 || ov[0].PathCount != 1 || ov[0].StrongCount != 0 {
		t.Fatalf("weak overlaps = %+v, want pathCount=1, strongCount=0", ov)
	}
	if len(f.getEvents()) != 0 || len(f.getMails()) != 0 {
		t.Fatalf("weak overlap should not notify or mail: events=%v mails=%v", f.getEvents(), f.getMails())
	}

	// Once r2 modifies/deletes lines in i18n.ts (add_only becomes false), it upgrades to strong!
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"web/src/i18n.ts"}}); err != nil {
		t.Fatal(err)
	}
	got1, _ = f.s.Get(ctx, r1.ID)
	if got1.OverlapCount != 1 {
		t.Fatalf("upgraded OverlapCount = %d, want 1", got1.OverlapCount)
	}
	if len(f.getEvents()) == 0 || len(f.getMails()) == 0 {
		t.Fatalf("upgraded overlap should notify and mail: events=%d mails=%d", len(f.getEvents()), len(f.getMails()))
	}
}

func TestOverlapScopeProjectOrOwn(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	// Two different members' global runs sharing the same repo_key must NEVER pair.
	gAnn := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Ann global", "repo-1", testRepoKeyA, "")
	gBob := f.makeRunningGlobalRun(t, "u-bob", "bob", f.bob.ID, "Bob global", "repo-1", testRepoKeyA, "")

	// Two different members' node runs in DIFFERENT projects must NEVER pair.
	nAnnP1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, "proj-1", "n1", "Ann P1", testRepoKeyA)
	nBobP2 := f.makeRunningNodeRun(t, "u-bob", "bob", f.bob.ID, "proj-2", "n2", "Bob P2", testRepoKeyA)

	for _, pair := range []struct {
		runner string
		runID  string
	}{
		{f.ann.ID, gAnn.ID},
		{f.bob.ID, gBob.ID},
		{f.ann.ID, nAnnP1.ID},
		{f.bob.ID, nBobP2.ID},
	} {
		if err := f.d.Touched(ctx, pair.runner, pair.runID, runnerwire.Touched{Files: []string{"shared.go"}}); err != nil {
			t.Fatal(err)
		}
	}

	// Bob's global run must have 0 overlaps (neither with Ann's global nor Ann's node run).
	ovBobG, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-bob"}, gBob)
	if err != nil {
		t.Fatal(err)
	}
	if len(ovBobG) != 1 || ovBobG[0].RunID != nBobP2.ID {
		t.Fatalf("Bob global should pair ONLY with Bob's own node run, got %+v", ovBobG)
	}

	// Ann's global run should pair ONLY with Ann's own node run, never with Bob's runs.
	ovAnnG, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, gAnn)
	if err != nil {
		t.Fatal(err)
	}
	if len(ovAnnG) != 1 || ovAnnG[0].RunID != nAnnP1.ID {
		t.Fatalf("Ann global should pair ONLY with Ann's own node run, got %+v", ovAnnG)
	}
}

func TestOverlapCrossMemberHasNoPathsOrCount(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	nAnn := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, "proj-1", "n1", "Ann secret title", testRepoKeyA)
	nBob := f.makeRunningNodeRun(t, "u-bob", "bob", f.bob.ID, "proj-1", "n2", "Bob secret title", testRepoKeyA)
	f.clearEvents()

	if err := f.d.Touched(ctx, f.ann.ID, nAnn.ID, runnerwire.Touched{Files: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.bob.ID, nBob.ID, runnerwire.Touched{Files: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}

	// Announced once on first strong path, never again even when crossing 5.
	if len(f.getEvents()) == 0 {
		t.Fatalf("expected initial announcement for cross-member overlap")
	}
	f.clearEvents()
	if err := f.d.Touched(ctx, f.bob.ID, nBob.ID, runnerwire.Touched{Files: []string{"b.go", "c.go", "d.go", "e.go"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.getEvents()) != 0 {
		t.Fatalf("cross-member overlap must NOT announce again on thresholds, got %+v", f.getEvents())
	}

	// Check for Ann, Bob, AND an admin: cross-member overlap is ALWAYS hidden with no paths, count, or run details.
	for _, viewer := range []identity.Actor{
		{ID: "u-ann"},
		{ID: "u-admin", Role: identity.RoleAdmin},
	} {
		ov, err := f.d.Overlaps(ctx, viewer, nAnn)
		if err != nil {
			t.Fatal(err)
		}
		if len(ov) != 1 {
			t.Fatalf("viewer %v overlaps len = %d, want 1", viewer, len(ov))
		}
		got := ov[0]
		if !got.Hidden || got.Since == "" {
			t.Fatalf("viewer %v: want hidden=true with since, got %+v", viewer, got)
		}
		if got.RunID != "" || got.Title != "" || got.NodeID != "" || got.Agent != "" || got.Status != "" ||
			got.Mine || len(got.Paths) != 0 || got.PathCount != 0 || got.StrongCount != 0 {
			t.Fatalf("viewer %v leaked cross-member fields: %+v", viewer, got)
		}
	}
}

func TestOverlapCrossMemberNeverMails(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	nAnn := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, "proj-1", "n1", "Ann task", testRepoKeyA)
	nBob := f.makeRunningNodeRun(t, "u-bob", "bob", f.bob.ID, "proj-1", "n2", "Bob task", testRepoKeyA)

	if err := f.d.Touched(ctx, f.ann.ID, nAnn.ID, runnerwire.Touched{Files: []string{"src/auth.ts"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.bob.ID, nBob.ID, runnerwire.Touched{Files: []string{"src/auth.ts"}}); err != nil {
		t.Fatal(err)
	}
	if mails := f.getMails(); len(mails) != 0 {
		t.Fatalf("cross-member overlap mailed agents: %+v", mails)
	}
}

func TestOverlapIncludesKeptUnappliedResult(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	// Kept unapplied result: succeeded within 24h, workspace="", pr_url="".
	kept := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Kept work", "repo-1", testRepoKeyA, "")
	if err := f.d.Touched(ctx, f.ann.ID, kept.ID, runnerwire.Touched{Files: []string{"src/shared.go"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Finish(ctx, kept.ID, f.ann.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	// New active run touches the same file -> pairs with kept result.
	cur := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Current work", "repo-1", testRepoKeyA, "")
	if err := f.d.Touched(ctx, f.ann.ID, cur.ID, runnerwire.Touched{Files: []string{"src/shared.go"}}); err != nil {
		t.Fatal(err)
	}

	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 || ov[0].RunID != kept.ID {
		t.Fatalf("overlaps = %+v, want kept run %s", ov, kept.ID)
	}
}

func TestOverlapSkipsKeptResultWithPR(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	// Succeeded run WITH a PR is left out (the PR is its signal).
	withPR := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "PR work", "repo-1", testRepoKeyA, "")
	if err := f.d.Touched(ctx, f.ann.ID, withPR.ID, runnerwire.Touched{Files: []string{"src/shared.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.PR(ctx, f.ann.ID, withPR.ID, runnerwire.PRInfo{
		URL: "https://github.com/org/api/pull/42", Number: 42, State: runnerwire.PROpen,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Finish(ctx, withPR.ID, f.ann.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	cur := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Current work", "repo-1", testRepoKeyA, "")
	if err := f.d.Touched(ctx, f.ann.ID, cur.ID, runnerwire.Touched{Files: []string{"src/shared.go"}}); err != nil {
		t.Fatal(err)
	}

	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 0 {
		t.Fatalf("expected 0 overlaps with PR-backed run, got %+v", ov)
	}
}

func TestOverlapGroupSiblingsSharingRemotePair(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	g1 := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Group repo 1", "repo-1", testRepoKeyA, "grp-1")
	g2 := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Group repo 2", "repo-2", testRepoKeyA, "grp-1")

	if err := f.d.Touched(ctx, f.ann.ID, g1.ID, runnerwire.Touched{Files: []string{"shared.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, g2.ID, runnerwire.Touched{Files: []string{"shared.go"}}); err != nil {
		t.Fatal(err)
	}

	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, g1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 || ov[0].RunID != g2.ID {
		t.Fatalf("group siblings sharing remote should pair: got %+v", ov)
	}
}

func TestOverlapMailsNodeRunsOncePerPair(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "node-0001", "Fix auth", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "node-0002", "Fix login", testRepoKeyA)

	// Simulate r2 having no session on first touch (ErrNoSession), then connecting on second touch.
	r2Ready := false
	f.mu.Lock()
	f.mailFn = func(_ context.Context, _, runID, _ string) error {
		if runID == r2.ID && !r2Ready {
			return agents.ErrNoSession
		}
		return nil
	}
	f.mu.Unlock()

	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"src/auth.ts"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"src/auth.ts"}}); err != nil {
		t.Fatal(err)
	}

	// Only r1 should have been mailed so far (r2 returned ErrNoSession).
	mails := f.getMails()
	if len(mails) != 1 || mails[0].runID != r1.ID {
		t.Fatalf("mails after first touch = %+v, want only r1", mails)
	}
	if !strings.Contains(mails[0].body, "node-0002") || !strings.Contains(mails[0].body, "src/auth.ts") ||
		!strings.Contains(mails[0].body, "send_message node:node-0002") {
		t.Fatalf("unexpected mail body: %q", mails[0].body)
	}

	// Now r2's session comes online and r2 sends another touched message -> r2 gets mailed, r1 is NOT mailed again.
	r2Ready = true
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"src/other.ts"}}); err != nil {
		t.Fatal(err)
	}
	mails = f.getMails()
	if len(mails) != 2 || mails[1].runID != r2.ID {
		t.Fatalf("mails after r2 retry = %+v, want r1 then r2", mails)
	}

	// Further shared paths on the same pair do NOT re-mail either run.
	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"src/api.ts"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, r2.ID, runnerwire.Touched{Files: []string{"src/api.ts"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.getMails()) != 2 {
		t.Fatalf("pair mailed more than once per run: %+v", f.getMails())
	}
}

func TestFollowUpPromptCarriesOverlapNote(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	prior := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Prior task", "repo-1", testRepoKeyA, "")
	other := f.makeRunningGlobalRun(t, "u-ann", "ann", f.ann.ID, "Other task", "repo-1", testRepoKeyA, "")

	if err := f.d.Touched(ctx, f.ann.ID, prior.ID, runnerwire.Touched{
		Files:   []string{"src/auth.ts", "i18n.ts"},
		AddOnly: []string{"i18n.ts"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Touched(ctx, f.ann.ID, other.ID, runnerwire.Touched{
		Files:   []string{"src/auth.ts", "i18n.ts"},
		AddOnly: []string{"i18n.ts"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Finish(ctx, prior.ID, f.ann.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}

	next, err := f.d.FollowUp(ctx, FollowUpRequest{
		PriorID:   prior.ID,
		Body:      runnerwire.FollowUpRequest{Message: "please add unit tests"},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatalf("FollowUp: %v", err)
	}
	if !strings.Contains(next.JobPrompt, "Note from Nodevas:") {
		t.Fatalf("JobPrompt missing 'Note from Nodevas:': %q", next.JobPrompt)
	}
	if !strings.Contains(next.JobPrompt, "src/auth.ts") {
		t.Fatalf("JobPrompt missing strong overlap path src/auth.ts: %q", next.JobPrompt)
	}
	if strings.Contains(next.JobPrompt, "i18n.ts") {
		t.Fatalf("JobPrompt should not include weak-only path i18n.ts: %q", next.JobPrompt)
	}
	if note := f.d.overlapNote(ctx, prior); len(note) > 1024 {
		t.Fatalf("overlapNote length = %d, want <= 1024", len(note))
	}
}

func TestChangesAddsCommittedFiles(t *testing.T) {
	ctx := context.Background()
	f := newOverlapFixture(t)

	r1 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n1", "Task 1", testRepoKeyA)
	r2 := f.makeRunningNodeRun(t, "u-ann", "ann", f.ann.ID, ".", "n2", "Task 2", testRepoKeyA)

	// r1 touched committed.go via normal Touched; r2 only reports it at Changes.
	if err := f.d.Touched(ctx, f.ann.ID, r1.ID, runnerwire.Touched{Files: []string{"pkg/committed.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Changes(ctx, f.ann.ID, r2.ID, runnerwire.RunChanges{
		Commit: "0123456789abcdef0123456789abcdef01234567",
		Base:   "main",
		Files: []runnerwire.FileChange{
			{Path: "pkg/committed.go", Status: runnerwire.FileModified, Additions: 5, Deletions: 2},
		},
		Additions: 5,
		Deletions: 2,
	}); err != nil {
		t.Fatalf("Changes: %v", err)
	}

	ov, err := f.d.Overlaps(ctx, identity.Actor{ID: "u-ann"}, r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 || ov[0].RunID != r2.ID || len(ov[0].Paths) != 1 || ov[0].Paths[0] != "pkg/committed.go" {
		t.Fatalf("overlaps after Changes = %+v, want pkg/committed.go with %s", ov, r2.ID)
	}
	_ = time.Now()
}
