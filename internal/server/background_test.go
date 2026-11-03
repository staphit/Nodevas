package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/realtime"
)

// Shutdown returns once the server's background work has: serve closes the
// database right after it, and a prune or a cost settlement cut off half
// way would leave its batch half written.
func TestShutdownWaitsForBackgroundWork(t *testing.T) {
	pm := projectManagerForTest(t)
	server := New(pm, realtime.NewHub(), nil)
	var finished atomic.Bool
	server.goBackground("slow job", func() {
		<-server.stop
		time.Sleep(150 * time.Millisecond)
		finished.Store(true)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !finished.Load() {
		t.Fatal("Shutdown returned before the background work finished")
	}
	if n := server.backgroundRunning.Load(); n != 0 {
		t.Fatalf("%d background goroutines still running after Shutdown", n)
	}
	// Work asked for after Shutdown is not started.
	var late atomic.Bool
	server.goBackground("late job", func() { late.Store(true) })
	time.Sleep(20 * time.Millisecond)
	if late.Load() {
		t.Fatal("background work started after Shutdown")
	}
}

// A goroutine that will not stop does not hang Shutdown past its deadline.
func TestShutdownGivesUpOnBackgroundWorkAtItsDeadline(t *testing.T) {
	pm := projectManagerForTest(t)
	server := New(pm, realtime.NewHub(), nil)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	server.goBackground("stuck job", func() { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := server.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown with stuck background work = %v, want the deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %v", elapsed)
	}
}

// A panic in background work is logged and ends that goroutine, not the
// process.
func TestBackgroundPanicIsRecovered(t *testing.T) {
	pm := projectManagerForTest(t)
	server := New(pm, realtime.NewHub(), nil)
	server.goBackground("exploding job", func() { panic("analytics exploded") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n := server.backgroundRunning.Load(); n != 0 {
		t.Fatalf("%d background goroutines still running", n)
	}
}

// The analytics loops (cost settlement and retention) and the run retries
// are background work Shutdown waits for.
func TestAnalyticsLoopsAreBackgroundWork(t *testing.T) {
	pm := projectManagerForTest(t)
	server := New(pm, realtime.NewHub(), nil)
	database := dbtestOpenForServer(t, pm.Workspace())
	server.UseRunnersDB(database)
	if n := server.backgroundRunning.Load(); n < 2 {
		t.Fatalf("%d background goroutines after UseRunnersDB, want the analytics loop and the run retries", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n := server.backgroundRunning.Load(); n != 0 {
		t.Fatalf("%d background goroutines still running after Shutdown", n)
	}
}
