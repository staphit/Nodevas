package project

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/engine"
)

// Every search reads up to searchindex's per-project budget; how many run
// at once is bounded. One past the bound waits its turn (it is an
// interactive read: it queues, it is not refused).
func TestSearchesBeyondTheBoundQueue(t *testing.T) {
	root := writeSearchProject(t, &engine.Node{ID: "lighthouse", Title: "Lighthouse"})
	pm := &ProjectManager{}
	info := ProjectInfo{Name: "main", Path: root}

	var running, most atomic.Int32
	release := make(chan struct{})
	hook := func() {
		now := running.Add(1)
		for {
			seen := most.Load()
			if now <= seen || most.CompareAndSwap(seen, now) {
				break
			}
		}
		<-release
		running.Add(-1)
	}
	searchRunning.Store(&hook)
	t.Cleanup(func() { searchRunning.Store(nil) })

	const searches = maxConcurrentSearches + 6
	var wg sync.WaitGroup
	errs := make(chan error, searches)
	for range searches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pm.RankProjectNodes(context.Background(), info, "lighthouse", 10)
			errs <- err
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for running.Load() < maxConcurrentSearches && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if got := most.Load(); got > maxConcurrentSearches {
		t.Errorf("%d searches ran at once, bound %d", got, maxConcurrentSearches)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a queued search failed: %v", err)
		}
	}
}

// A search whose request is gone stops instead of reading on for nobody,
// and one waiting for a turn stops waiting.
func TestASearchStopsWithItsRequest(t *testing.T) {
	root := writeSearchProject(t, &engine.Node{ID: "lighthouse", Title: "Lighthouse"})
	pm := &ProjectManager{}
	info := ProjectInfo{Name: "main", Path: root}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pm.RankProjectNodes(gone, info, "lighthouse", 10); !errors.Is(err, context.Canceled) {
		t.Errorf("ranked search of a cancelled request: %v", err)
	}
	if _, err := pm.SearchProjectNodes(gone, info, "lighthouse"); !errors.Is(err, context.Canceled) {
		t.Errorf("search of a cancelled request: %v", err)
	}
}
