package runners

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// llmConn records what the hub sends a runner.
type llmConn struct {
	mu   sync.Mutex
	sent []runnerwire.Envelope
	got  chan runnerwire.Envelope
}

func newLLMConn() *llmConn { return &llmConn{got: make(chan runnerwire.Envelope, 16)} }

func (c *llmConn) Bye(string) {}
func (c *llmConn) Send(env runnerwire.Envelope) error {
	c.mu.Lock()
	c.sent = append(c.sent, env)
	c.mu.Unlock()
	c.got <- env
	return nil
}

func (c *llmConn) next(t *testing.T) runnerwire.Envelope {
	t.Helper()
	select {
	case env := <-c.got:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("nothing sent to the runner")
		return runnerwire.Envelope{}
	}
}

func llmInfo() runnerwire.Info {
	return runnerwire.Info{Version: "v", Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapLLM},
		LLM: &runnerwire.LLMInfo{Provider: "anthropic", Model: "claude-test"}}
}

// online registers a runner for owner, says hello and attaches conn.
func online(t *testing.T, reg *Registry, ownerName string, info runnerwire.Info, conn Conn) runnerwire.Runner {
	t.Helper()
	owner := ann
	if ownerName == "bob" {
		owner = bob
	}
	runner, _, err := reg.Create(context.Background(), owner, ownerName+" pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(context.Background(), runner.ID, info); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Attach(runner.ID, conn))
	return runner
}

func TestLLMTargetIsTheOwnersNewestCopilotRunner(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	ctx := context.Background()
	if _, err := relay.Target(ctx, ann.ID); !errors.Is(err, ErrNoLLMRunner) {
		t.Fatalf("no runners: %v", err)
	}
	plain := llmInfo()
	plain.Capabilities, plain.LLM = []string{runnerwire.CapHeadless}, nil
	online(t, reg, "ann", plain, newLLMConn())
	online(t, reg, "bob", llmInfo(), newLLMConn())
	if _, err := relay.Target(ctx, ann.ID); !errors.Is(err, ErrNoLLMRunner) {
		t.Fatalf("ann has no copilot runner, bob's must not count: %v", err)
	}
	first := online(t, reg, "ann", llmInfo(), newLLMConn())
	reg.now = func() time.Time { return time.Now().Add(time.Minute) }
	second := online(t, reg, "ann", llmInfo(), newLLMConn())
	got, err := relay.Target(ctx, ann.ID)
	if err != nil || got.ID != second.ID {
		t.Fatalf("target = %v, %v; want the newest, %s (not %s)", got.ID, err, second.ID, first.ID)
	}
	if err := reg.Revoke(ctx, ann, second.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := relay.Target(ctx, ann.ID); err != nil || got.ID != first.ID {
		t.Fatalf("after revoking the newest: %v, %v", got.ID, err)
	}
}

func TestLLMCallRelaysInOrder(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	conn := newLLMConn()
	r := online(t, reg, "ann", llmInfo(), conn)
	other := online(t, reg, "bob", llmInfo(), newLLMConn())

	type result struct {
		done runnerwire.LLMDone
		err  error
	}
	var deltas []string
	results := make(chan result, 1)
	go func() {
		done, err := relay.Call(context.Background(), r.ID, runnerwire.LLMRequest{AccountID: ann.ID, Provider: "anthropic"},
			func(text string) { deltas = append(deltas, text) })
		results <- result{done, err}
	}()
	sent := conn.next(t)
	if sent.Type != runnerwire.TypeLLMRequest || sent.LLM == nil || !strings.HasPrefix(sent.LLM.ID, "llm-") {
		t.Fatalf("sent = %+v", sent)
	}
	id := sent.LLM.ID
	// Another runner cannot answer this call.
	if err := relay.Deliver(other.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDone,
		LLMDone: &runnerwire.LLMDone{ID: id, Error: "hijack"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other runner's answer: %v", err)
	}
	for _, text := range []string{"a", "b", "c"} {
		if err := relay.Deliver(r.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDelta,
			LLMDelta: &runnerwire.LLMDelta{ID: id, Text: text}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := relay.Deliver(r.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDone,
		LLMDone: &runnerwire.LLMDone{ID: id, Message: []byte(`{"role":"assistant","blocks":[]}`), Stop: "end_turn"}}); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.done.Stop != "end_turn" || strings.Join(deltas, "") != "abc" {
		t.Fatalf("call = %+v, %v, deltas %q", got.done, got.err, deltas)
	}
	// The call is over: a late answer is dropped.
	if err := relay.Deliver(r.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDelta,
		LLMDelta: &runnerwire.LLMDelta{ID: id, Text: "late"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late delta: %v", err)
	}
}

func TestLLMCallErrors(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	relay.OfflineCheck = 10 * time.Millisecond
	conn := newLLMConn()
	r := online(t, reg, "ann", llmInfo(), conn)

	// The runner's refusal is the call's error.
	errs := make(chan error, 1)
	go func() {
		_, err := relay.Call(context.Background(), r.ID, runnerwire.LLMRequest{}, nil)
		errs <- err
	}()
	id := conn.next(t).LLM.ID
	_ = relay.Deliver(r.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDone, LLMDone: &runnerwire.LLMDone{ID: id, Error: "no key"}})
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "no key") {
		t.Fatalf("refusal = %v", err)
	}

	// Cancelling the turn tells the runner to stop.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := relay.Call(ctx, r.ID, runnerwire.LLMRequest{}, nil)
		errs <- err
	}()
	id = conn.next(t).LLM.ID
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
	if sent := conn.next(t); sent.Type != runnerwire.TypeLLMCancel || sent.LLMID != id {
		t.Fatalf("cancel sent = %+v", sent)
	}

	// A runner that goes away fails the call.
	go func() {
		_, err := relay.Call(context.Background(), r.ID, runnerwire.LLMRequest{}, nil)
		errs <- err
	}()
	conn.next(t)
	reg.kick(r.ID, "test")
	if err := <-errs; !errors.Is(err, ErrLLMRunnerGone) {
		t.Fatalf("runner gone = %v", err)
	}
	if _, err := relay.Call(context.Background(), r.ID, runnerwire.LLMRequest{}, nil); !errors.Is(err, ErrNoLLMRunner) {
		t.Fatalf("offline runner = %v", err)
	}

	// Oversized answers are refused at the door.
	if err := relay.Deliver(r.ID, runnerwire.Envelope{Type: runnerwire.TypeLLMDelta,
		LLMDelta: &runnerwire.LLMDelta{ID: "x", Text: strings.Repeat("x", runnerwire.MaxLLMDeltaBytes+1)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge delta = %v", err)
	}
}

func TestHelloLLMFieldsAreBounded(t *testing.T) {
	info := llmInfo()
	info.LLM.Model = strings.Repeat("m", 10000)
	if err := ValidateInfo(info); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge model name = %v", err)
	}
}
