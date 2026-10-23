package runs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// startRun makes a run and takes it to running on runner r1.
func startRun(t *testing.T, s *Store) Record {
	t.Helper()
	ctx := context.Background()
	r := createRun(t, s, "n1")
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	running, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1", Branch: "agent/n1"})
	if err != nil {
		t.Fatal(err)
	}
	return running
}

func sampleChanges() runnerwire.RunChanges {
	return runnerwire.RunChanges{
		Commit: "0123456789abcdef0123456789abcdef01234567", Base: "main",
		Files: []runnerwire.FileChange{
			{Path: "web/login.ts", Status: runnerwire.FileModified, Additions: 3, Deletions: 1},
			{Path: "web/new.ts", OldPath: "web/old.ts", Status: runnerwire.FileRenamed},
			{Path: "logo.png", Status: runnerwire.FileAdded, Binary: true},
		},
		Additions: 3, Deletions: 1,
		Patch: "diff --git a/web/login.ts b/web/login.ts\n+fixed\n",
	}
}

func TestSetChangesStoresOnceAndCopiesCounts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)

	if got, err := s.Changes(ctx, r.ID); err != nil || got != nil {
		t.Fatalf("changes before any report = %+v, %v", got, err)
	}
	updated, stored, err := s.SetChanges(ctx, r.ID, "r1", sampleChanges())
	if err != nil || !stored {
		t.Fatalf("SetChanges = %v, stored %v", err, stored)
	}
	if updated.Commit != sampleChanges().Commit || updated.FilesChanged != 3 || updated.Additions != 3 || updated.Deletions != 1 {
		t.Fatalf("run after changes = %+v", updated.Run)
	}

	// A resend, even a different one, changes nothing.
	again := sampleChanges()
	again.Commit = "ffff"
	again.Files = again.Files[:1]
	if _, stored, err := s.SetChanges(ctx, r.ID, "r1", again); err != nil || stored {
		t.Fatalf("duplicate SetChanges = %v, stored %v", err, stored)
	}
	got, err := s.Changes(ctx, r.ID)
	if err != nil || got == nil {
		t.Fatalf("Changes = %+v, %v", got, err)
	}
	if got.Commit != sampleChanges().Commit || len(got.Files) != 3 || got.Files[1].OldPath != "web/old.ts" ||
		!got.Files[2].Binary || got.Patch != sampleChanges().Patch || got.PatchTruncated || got.Base != "main" {
		t.Fatalf("stored changes = %+v", got)
	}
	if latest, _ := s.Get(ctx, r.ID); latest.FilesChanged != 3 || latest.Commit != sampleChanges().Commit {
		t.Fatalf("run after the resend = %+v", latest.Run)
	}
}

func TestSetChangesChecksRunnerStatusAndLimits(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// Never ran: nothing to report.
	queued := createRun(t, s, "n1")
	if _, _, err := s.SetChanges(ctx, queued.ID, "r1", sampleChanges()); !errors.Is(err, ErrTransition) {
		t.Fatalf("changes for a queued run: %v", err)
	}

	r := startRun(t, s)
	if _, _, err := s.SetChanges(ctx, r.ID, "r2", sampleChanges()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changes from another runner: %v", err)
	}
	if _, _, err := s.SetChanges(ctx, "nope", "r1", sampleChanges()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changes for no run: %v", err)
	}

	bad := map[string]func(*runnerwire.RunChanges){
		"too many files": func(c *runnerwire.RunChanges) {
			c.Files = make([]runnerwire.FileChange, runnerwire.MaxChangedFiles+1)
			for i := range c.Files {
				c.Files[i] = runnerwire.FileChange{Path: "f", Status: runnerwire.FileAdded}
			}
		},
		"patch too big":    func(c *runnerwire.RunChanges) { c.Patch = strings.Repeat("x", runnerwire.MaxPatchBytes+1) },
		"long path":        func(c *runnerwire.RunChanges) { c.Files[0].Path = strings.Repeat("a", MaxPathBytes+1) },
		"long old path":    func(c *runnerwire.RunChanges) { c.Files[1].OldPath = strings.Repeat("a", MaxPathBytes+1) },
		"empty path":       func(c *runnerwire.RunChanges) { c.Files[0].Path = "" },
		"NUL in path":      func(c *runnerwire.RunChanges) { c.Files[0].Path = "a\x00b" },
		"unknown status":   func(c *runnerwire.RunChanges) { c.Files[0].Status = "copied" },
		"negative counts":  func(c *runnerwire.RunChanges) { c.Additions = -1 },
		"negative in file": func(c *runnerwire.RunChanges) { c.Files[0].Deletions = -1 },
		"commit not hex":   func(c *runnerwire.RunChanges) { c.Commit = "HEAD; rm -rf" },
	}
	for name, mutate := range bad {
		ch := sampleChanges()
		mutate(&ch)
		if _, _, err := s.SetChanges(ctx, r.ID, "r1", ch); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ := s.Changes(ctx, r.ID); got != nil {
		t.Fatalf("an invalid report was stored: %+v", got)
	}

	// At the limits exactly is fine; "no changes" (no commit) is a report too.
	edge := sampleChanges()
	edge.Patch = strings.Repeat("x", runnerwire.MaxPatchBytes)
	edge.Files[0].Path = strings.Repeat("a", MaxPathBytes)
	if _, stored, err := s.SetChanges(ctx, r.ID, "r1", edge); err != nil || !stored {
		t.Fatalf("changes at the limits: %v %v", err, stored)
	}
	empty := startRun(t, s)
	if updated, stored, err := s.SetChanges(ctx, empty.ID, "r1", runnerwire.RunChanges{Base: "main"}); err != nil || !stored ||
		updated.Commit != "" || updated.FilesChanged != 0 {
		t.Fatalf("empty changes: %+v %v %v", updated.Run, stored, err)
	}
	if got, _ := s.Changes(ctx, empty.ID); got == nil || got.Files == nil || len(got.Files) != 0 {
		t.Fatalf("empty changes read back as %+v", got)
	}
}

func TestSetChangesAfterFinishWithinGrace(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)
	if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	late := startRun(t, s)
	if _, err := s.Finish(ctx, late.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunFailed}); err != nil {
		t.Fatal(err)
	}
	if _, stored, err := s.SetChanges(ctx, r.ID, "r1", sampleChanges()); err != nil || !stored {
		t.Fatalf("changes just after the finish: %v %v", err, stored)
	}
	base := time.Now()
	s.now = func() time.Time { return base.Add(ChangesGrace + time.Minute) }
	if _, _, err := s.SetChanges(ctx, late.ID, "r1", sampleChanges()); !errors.Is(err, ErrTransition) {
		t.Fatalf("changes long after the finish: %v", err)
	}
}

func TestSetPRInAnyStatus(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)

	failed := runnerwire.PRInfo{Error: "gh is not signed in"}
	updated, changed, err := s.SetPR(ctx, r.ID, "r1", failed)
	if err != nil || !changed || updated.PRError != "gh is not signed in" || updated.PRURL != "" {
		t.Fatalf("pr error: %+v %v %v", updated.Run, changed, err)
	}

	open := runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: runnerwire.PROpen, CI: runnerwire.CIPending}
	updated, changed, err = s.SetPR(ctx, r.ID, "r1", open)
	if err != nil || !changed || updated.PRURL != open.URL || updated.PRNumber != 7 ||
		updated.PRState != runnerwire.PROpen || updated.CIStatus != runnerwire.CIPending || updated.PRError != "" {
		t.Fatalf("pr open: %+v %v %v", updated.Run, changed, err)
	}
	// The same report again is not a change. HeadSHA and Draft are not kept.
	open.HeadSHA = "abc"
	if _, changed, err := s.SetPR(ctx, r.ID, "r1", open); err != nil || changed {
		t.Fatalf("repeat pr: %v %v", changed, err)
	}

	if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	// Long after the finish, the PR still moves.
	s.now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	merged := runnerwire.PRInfo{URL: open.URL, Number: 7, State: runnerwire.PRMerged, CI: runnerwire.CISuccess}
	updated, changed, err = s.SetPR(ctx, r.ID, "r1", merged)
	if err != nil || !changed || updated.PRState != runnerwire.PRMerged || updated.CIStatus != runnerwire.CISuccess ||
		updated.Status != runnerwire.RunSucceeded {
		t.Fatalf("pr merged after finish: %+v %v %v", updated.Run, changed, err)
	}

	if _, _, err := s.SetPR(ctx, r.ID, "r2", merged); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pr from another runner: %v", err)
	}
	for name, pr := range map[string]runnerwire.PRInfo{
		"http":          {URL: "http://github.com/o/r/pull/7", State: runnerwire.PROpen},
		"javascript":    {URL: "javascript:alert(1)", State: runnerwire.PROpen},
		"too long":      {URL: "https://github.com/" + strings.Repeat("a", MaxPRURLBytes), State: runnerwire.PROpen},
		"no host":       {URL: "https:///pull/7", State: runnerwire.PROpen},
		"space":         {URL: "https://github.com/o r", State: runnerwire.PROpen},
		"userinfo":      {URL: "https://x@evil.test/", State: runnerwire.PROpen},
		"no state":      {URL: "https://github.com/o/r/pull/7"},
		"bad state":     {URL: "https://github.com/o/r/pull/7", State: "draft"},
		"bad ci":        {URL: "https://github.com/o/r/pull/7", State: runnerwire.PROpen, CI: "green"},
		"negative num":  {URL: "https://github.com/o/r/pull/7", State: runnerwire.PROpen, Number: -1},
		"no url, state": {State: "weird"},
	} {
		if _, _, err := s.SetPR(ctx, r.ID, "r1", pr); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if latest, _ := s.Get(ctx, r.ID); latest.PRState != runnerwire.PRMerged {
		t.Fatalf("an invalid report changed the run: %+v", latest.Run)
	}
}

// runs-changed fires for a stored report and a PR that moved, and for nothing
// else.
func TestDispatcherAnnouncesChangesAndPRMoves(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)
	var announced []string
	d := NewDispatcher(s, nil, func(project, nodeID string) { announced = append(announced, project+"/"+nodeID) }, nil)

	if err := d.Changes(ctx, "r1", r.ID, sampleChanges()); err != nil {
		t.Fatal(err)
	}
	if err := d.Changes(ctx, "r1", r.ID, sampleChanges()); err != nil {
		t.Fatalf("a resend is not an error: %v", err)
	}
	if err := d.Changes(ctx, "r2", r.ID, sampleChanges()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another runner's changes: %v", err)
	}
	pr := runnerwire.PRInfo{URL: "https://github.com/o/r/pull/1", Number: 1, State: runnerwire.PROpen}
	for i := 0; i < 2; i++ {
		if err := d.PR(ctx, "r1", r.ID, pr); err != nil {
			t.Fatal(err)
		}
	}
	pr.CI = runnerwire.CIFailure
	if err := d.PR(ctx, "r1", r.ID, pr); err != nil {
		t.Fatal(err)
	}
	if err := d.PR(ctx, "r1", r.ID, runnerwire.PRInfo{URL: "http://x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid pr: %v", err)
	}
	if len(announced) != 3 || announced[0] != "./n1" {
		t.Fatalf("announced %v, want three runs-changed for ./n1", announced)
	}
}
