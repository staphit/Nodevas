package copilot

import (
	"context"
	"errors"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
)

// On a server with accounts, the server's key pays for every member's turns.
// One account runs at most maxServerTurnsPerAccount of them at once; the next
// is refused with a clear busy answer instead of running up the bill, and
// other accounts are not held up by it.
func TestServerKeyTurnsAreCappedPerAccount(t *testing.T) {
	h := newHarness(t, nil)
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	h.model.repeat = func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return provider.Response{}, ctx.Err()
		}
		return answer("done")(t, ctx, req, onDelta)
	}
	waitEntered := func(what string) {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never reached the model", what)
		}
	}

	var running []*running
	for i := range maxServerTurnsPerAccount {
		running = append(running, h.start(h.turn(h.conversation(alice, ModeDefault), ModeDefault)))
		waitEntered("alice's turn " + string(rune('1'+i)))
	}

	extra := h.start(h.turn(h.conversation(alice, ModeDefault), ModeDefault))
	select {
	case <-entered:
		t.Errorf("alice's turn %d ran on the server's key alongside %d others", maxServerTurnsPerAccount+1, maxServerTurnsPerAccount)
	case err := <-extra.done:
		if !errors.Is(err, ErrTurnLimit) {
			t.Errorf("alice's extra turn = %v, want ErrTurnLimit", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("alice's extra turn neither ran nor was refused")
	}

	// Another account is not held up by alice's turns.
	bobs := h.start(TurnRequest{Actor: bob, ConversationID: h.conversation(bob, ModeDefault).ID, Text: "hi", Mode: ModeDefault})
	waitEntered("bob's turn")
	running = append(running, bobs)

	close(release)
	for _, turn := range running {
		turn.finish()
	}
	// Finished turns free their places.
	h.start(h.turn(h.conversation(alice, ModeDefault), ModeDefault)).finish()
}
