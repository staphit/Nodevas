package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// Clicking through folders fast is normal use, not abuse: a burst far past
// the concurrency limit queues and every request is answered.
func TestRepoBrowseBurstQueuesInsteadOfRefusing(t *testing.T) {
	cfg, _ := browseRepo(t)
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
