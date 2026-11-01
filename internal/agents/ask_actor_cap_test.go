package agents

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// One session waits on one question at a time, but session ids are the
// caller's to pick: an account (or a run acting for it) asking from fresh
// ids each time must still not pile up pending asks without end, since every
// browser in the project lists them all.
func TestPendingAsksAreBoundedPerActorAcrossSessions(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const extra = 20
		n := maxPendingAsksPerActor + extra
		results := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(i int) {
				_, err := hub.CreateAndWait(ctx, AskInput{
					Project: "alpha", SessionID: fmt.Sprintf("s-%d", i), ActorID: "mallory",
					Kind: AskKindQuestion, Question: "q",
				}, time.Minute)
				results <- err
			}(i)
		}
		refused, pending := 0, 0
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && pending+refused < n; time.Sleep(20 * time.Millisecond) {
			for len(results) > 0 {
				if err := <-results; errors.Is(err, ErrAskBusy) {
					refused++
				} else {
					t.Errorf("an ask returned early: %v", err)
				}
			}
			asks, err := hub.ListAsks(context.Background(), "alpha", AskPending)
			if err != nil {
				t.Fatal(err)
			}
			pending = len(asks)
		}
		if pending != maxPendingAsksPerActor || refused != extra {
			t.Fatalf("one actor holds %d pending asks over %d session ids (%d refused), want %d and %d refused",
				pending, n, refused, maxPendingAsksPerActor, extra)
		}

		// The cap is the actor's: another account still asks.
		done := make(chan error, 1)
		go func() {
			_, err := hub.CreateAndWait(ctx, AskInput{
				Project: "alpha", SessionID: "alice-1", ActorID: "alice", Kind: AskKindQuestion, Question: "mine",
			}, time.Minute)
			done <- err
		}()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			select {
			case err := <-done:
				t.Fatalf("alice's ask returned early: %v", err)
			default:
			}
			if asks, _ := hub.ListAsks(context.Background(), "alpha", AskPending); len(asks) == maxPendingAsksPerActor+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("alice's ask never became pending")
			}
		}
	})
}
