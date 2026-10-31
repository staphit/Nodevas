package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// pinRepoSlotWait sets repoSlotWait for the length of one test.
func pinRepoSlotWait(t *testing.T, wait time.Duration) {
	t.Helper()
	previous := repoSlotWait
	repoSlotWait = wait
	t.Cleanup(func() { repoSlotWait = previous })
}

// untilTestDeadline is how long t may still wait: the rest of its go test
// -timeout, less a moment to report a failure.
func untilTestDeadline(t *testing.T) time.Duration {
	deadline, ok := t.Deadline()
	if !ok {
		return 24 * time.Hour
	}
	return time.Until(deadline) - 5*time.Second
}

// Clicking through folders fast is normal use, not abuse: a burst far past
// the concurrency limit queues and every request is answered.
//
// The burst's callers listen until the test's deadline: how fast 200 reads
// get through 4 slots is the machine's load (beside a full parallel test run
// the last ones waited past repoSlotWait), and what is checked here is that
// they queue, not how fast. TestRepoBrowseWaitEndsAtRepoSlotWait checks the
// bound.
func TestRepoBrowseBurstQueuesInsteadOfRefusing(t *testing.T) {
	cfg, _ := browseRepo(t)
	pinRepoSlotWait(t, untilTestDeadline(t))
	const n = 200
	var wg sync.WaitGroup
	codes := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- browse(cfg, runnerwire.RepoOpList, "src").Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != "" {
			t.Fatalf("a request in a burst of %d was refused with %q", n, code)
		}
	}
}

// A request still waiting for a slot when its caller gives up ends as busy
// instead of holding the queue.
func TestRepoBrowseWaitEndsWithTheCaller(t *testing.T) {
	cfg, _ := browseRepo(t)
	for range repoBrowseConcurrent {
		if err := acquireRepoSlot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for range repoBrowseConcurrent {
			releaseRepoSlot()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := BrowseRepo(ctx, cfg, browseOwner, runnerwire.RepoRequest{
		ID: "q", AccountID: browseOwner, RepoID: "r1", Op: runnerwire.RepoOpList,
	})
	if done.Code != runnerwire.RepoErrBusy {
		t.Fatalf("code = %q, want busy once the caller's context ended", done.Code)
	}
}

// A request nobody has told to stop still stops waiting at repoSlotWait.
func TestRepoBrowseWaitEndsAtRepoSlotWait(t *testing.T) {
	cfg, _ := browseRepo(t)
	pinRepoSlotWait(t, 50*time.Millisecond)
	for range repoBrowseConcurrent {
		if err := acquireRepoSlot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for range repoBrowseConcurrent {
			releaseRepoSlot()
		}
	}()
	if done := browse(cfg, runnerwire.RepoOpList, "src"); done.Code != runnerwire.RepoErrBusy {
		t.Fatalf("code = %q, want busy once repoSlotWait passed", done.Code)
	}
}
