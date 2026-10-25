package runners

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

// Copilot model calls made on a runner (runnerwire/llm.go). The hub sends a
// call to the person's own runner and relays the answer back to the copilot
// turn waiting on it. The runner holds the API key; the hub only ever holds
// the conversation it already had.

// ErrNoLLMRunner: the person has no online runner that answers copilot model
// calls.
var ErrNoLLMRunner = errors.New("your runner is offline or has no copilot key: start `nodevas runner` on your PC " +
	"(set one up with `nodevas runner set copilotProvider anthropic` and `nodevas runner copilot-key`)")

// ErrNoAdvisorRunner: the person has no online runner with an autopilot
// advisor model (W7).
var ErrNoAdvisorRunner = errors.New("no online runner of yours has an advisor model: set one with " +
	"`nodevas runner set advisorProvider anthropic` (and advisorModel), then restart the runner")

// ErrNoDecideRunner: the person has no online runner with a System One
// advisor (W7).
var ErrNoDecideRunner = errors.New("no online runner of yours has a System One advisor: run Laya (`laya-serve`) " +
	"or use Jev, then `nodevas runner set advisorProvider systemone` and restart the runner")

// DefaultDecideTimeout bounds one System One decision end to end: the runner
// gives the endpoint 10s.
const DefaultDecideTimeout = 15 * time.Second

// ErrLLMRunnerGone: the runner a call was sent to disconnected before it
// answered.
var ErrLLMRunnerGone = errors.New("your runner disconnected before the model answered")

// DefaultLLMTimeout bounds one model call end to end.
const DefaultLLMTimeout = 10 * time.Minute

// maxQueuedLLMText bounds the streamed text a call holds before its turn
// reads it; past it, further deltas are dropped (the final answer still has
// the whole text).
const maxQueuedLLMText = 4 << 20

// LLMRelay carries copilot model calls to runners and their answers back.
type LLMRelay struct {
	reg *Registry
	// Timeout overrides DefaultLLMTimeout; tests shorten it.
	Timeout time.Duration
	// OfflineCheck is how often a waiting call checks its runner is still
	// connected; zero means two seconds.
	OfflineCheck time.Duration

	mu    sync.Mutex
	calls map[string]*llmCall
}

type llmCall struct {
	runnerID string

	mu      sync.Mutex
	queued  []string
	size    int
	done    *runnerwire.LLMDone
	decided *runnerwire.DecideDone
	signal  chan struct{}
}

func (c *llmCall) wake() {
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// NewLLMRelay builds a relay over the registry's live connections.
func NewLLMRelay(reg *Registry) *LLMRelay {
	return &LLMRelay{reg: reg, calls: map[string]*llmCall{}}
}

// Target returns the runner that answers accountID's copilot: one of that
// account's own online runners whose hello offers model calls, the most
// recently connected if there are several. Another account's runner is never
// a target, administrators' included: the key on it is its owner's.
func (l *LLMRelay) Target(ctx context.Context, accountID string) (runnerwire.Runner, error) {
	return l.TargetFor(ctx, accountID, runnerwire.LLMPurposeCopilot)
}

// TargetFor is Target for a model call's purpose: a copilot call goes to a
// runner whose hello offers copilot calls (Info.LLM), an advisor call to one
// that offers advisor calls (Info.Advisor). The same ownership rule holds.
func (l *LLMRelay) TargetFor(ctx context.Context, accountID, purpose string) (runnerwire.Runner, error) {
	none := ErrNoLLMRunner
	offers := func(info *runnerwire.Info) bool {
		return info.LLM != nil && slices.Contains(info.Capabilities, runnerwire.CapLLM)
	}
	switch purpose {
	case runnerwire.LLMPurposeCopilot:
	case runnerwire.LLMPurposeAdvisor:
		none = ErrNoAdvisorRunner
		offers = func(info *runnerwire.Info) bool {
			return info.Advisor != nil && slices.Contains(info.Capabilities, runnerwire.CapLLMAdvisor)
		}
	case decidePurpose:
		none = ErrNoDecideRunner
		offers = func(info *runnerwire.Info) bool {
			return info.Advisor != nil && info.Advisor.Provider == runnerwire.ProviderSystemOne &&
				slices.Contains(info.Capabilities, runnerwire.CapDecide)
		}
	default:
		return runnerwire.Runner{}, fmt.Errorf("%w: unknown model call purpose %q", ErrInvalid, purpose)
	}
	if accountID == "" {
		return runnerwire.Runner{}, none
	}
	rows, err := l.reg.db.QueryContext(ctx,
		`SELECT `+runnerColumns+` FROM runners WHERE revoked_at = '' AND owner_id = ?`, accountID)
	if err != nil {
		return runnerwire.Runner{}, err
	}
	defer rows.Close()
	var best runnerwire.Runner
	var bestAt time.Time
	found := false
	for rows.Next() {
		runner, err := l.reg.scan(rows)
		if err != nil {
			return runnerwire.Runner{}, err
		}
		if !runner.Online || runner.Info == nil || !offers(runner.Info) {
			continue
		}
		at := l.reg.attachedAt(runner.ID)
		if !found || at.After(bestAt) {
			best, bestAt, found = runner, at, true
		}
	}
	if err := rows.Err(); err != nil {
		return runnerwire.Runner{}, err
	}
	if !found {
		return runnerwire.Runner{}, none
	}
	return best, nil
}

// Call sends req to runnerID and waits for its answer, handing each streamed
// fragment to onDelta on the calling goroutine, in order. A cancelled ctx
// tells the runner to stop. The returned error is set when there is no
// answer: the runner refused or failed (its reason), went away, or the call
// timed out.
func (l *LLMRelay) Call(ctx context.Context, runnerID string, req runnerwire.LLMRequest, onDelta func(string)) (runnerwire.LLMDone, error) {
	if req.ID == "" {
		id, err := newCallID()
		if err != nil {
			return runnerwire.LLMDone{}, err
		}
		req.ID = id
	}
	call := &llmCall{runnerID: runnerID, signal: make(chan struct{}, 1)}
	l.mu.Lock()
	if _, taken := l.calls[req.ID]; taken {
		l.mu.Unlock()
		return runnerwire.LLMDone{}, fmt.Errorf("%w: model call id %q is in use", ErrInvalid, req.ID)
	}
	l.calls[req.ID] = call
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.calls, req.ID)
		l.mu.Unlock()
	}()

	if err := l.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeLLMRequest, LLM: &req}); err != nil {
		if errors.Is(err, ErrOffline) {
			if req.Purpose == runnerwire.LLMPurposeAdvisor {
				return runnerwire.LLMDone{}, ErrNoAdvisorRunner
			}
			return runnerwire.LLMDone{}, ErrNoLLMRunner
		}
		return runnerwire.LLMDone{}, fmt.Errorf("sending the model call to your runner: %w", err)
	}
	cancelRemote := func() {
		_ = l.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeLLMCancel, LLMID: req.ID})
	}

	timeout := l.Timeout
	if timeout <= 0 {
		timeout = DefaultLLMTimeout
	}
	check := l.OfflineCheck
	if check <= 0 {
		check = 2 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	offline := time.NewTicker(check)
	defer offline.Stop()
	for {
		select {
		case <-ctx.Done():
			cancelRemote()
			return runnerwire.LLMDone{}, ctx.Err()
		case <-deadline.C:
			cancelRemote()
			return runnerwire.LLMDone{}, fmt.Errorf("your runner did not answer within %s", timeout)
		case <-offline.C:
			if !l.reg.Online(runnerID) {
				return runnerwire.LLMDone{}, ErrLLMRunnerGone
			}
		case <-call.signal:
			call.mu.Lock()
			texts := call.queued
			call.queued, call.size = nil, 0
			done := call.done
			call.mu.Unlock()
			if onDelta != nil {
				for _, text := range texts {
					onDelta(text)
				}
			}
			if done != nil {
				if done.Error != "" {
					return *done, fmt.Errorf("your runner: %s", done.Error)
				}
				return *done, nil
			}
		}
	}
}

// decidePurpose is TargetFor's key for a System One decision (never on the
// wire: a decision is its own message type).
const decidePurpose = "decide"

// Decide asks accountID's own runner with a System One advisor for a
// decision and waits for it. The runner fills in nothing: the model and base
// URL are its hello's, sent back so a runner whose settings changed refuses
// rather than asks somewhere the hub did not expect.
func (l *LLMRelay) Decide(ctx context.Context, accountID string, req runnerwire.DecisionRequest) (runnerwire.DecisionResponse, error) {
	if len(req.State) > runnerwire.MaxDecideStateBytes || len(req.Questions) > runnerwire.MaxDecideQuestionsBytes {
		return runnerwire.DecisionResponse{}, fmt.Errorf("%w: the decision is too large", ErrInvalid)
	}
	runner, err := l.TargetFor(ctx, accountID, decidePurpose)
	if err != nil {
		return runnerwire.DecisionResponse{}, err
	}
	id, err := newCallID()
	if err != nil {
		return runnerwire.DecisionResponse{}, err
	}
	req.Model = runner.Info.Advisor.Model
	call := &llmCall{runnerID: runner.ID, signal: make(chan struct{}, 1)}
	l.mu.Lock()
	l.calls[id] = call
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.calls, id)
		l.mu.Unlock()
	}()
	err = l.reg.Send(runner.ID, runnerwire.Envelope{Type: runnerwire.TypeDecideRequest, Decide: &runnerwire.DecideRequest{
		ID: id, AccountID: accountID, Purpose: runnerwire.LLMPurposeAdvisor,
		Model: runner.Info.Advisor.Model, BaseURL: runner.Info.Advisor.BaseURL, Request: req,
	}})
	if err != nil {
		if errors.Is(err, ErrOffline) {
			return runnerwire.DecisionResponse{}, ErrNoDecideRunner
		}
		return runnerwire.DecisionResponse{}, fmt.Errorf("sending the decision to your runner: %w", err)
	}
	timeout := l.Timeout
	if timeout <= 0 || timeout > DefaultDecideTimeout {
		timeout = DefaultDecideTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return runnerwire.DecisionResponse{}, ctx.Err()
		case <-deadline.C:
			return runnerwire.DecisionResponse{}, fmt.Errorf("your runner's System One advisor did not answer within %s", timeout)
		case <-call.signal:
			call.mu.Lock()
			done := call.decided
			call.mu.Unlock()
			if done == nil {
				continue
			}
			if done.Error != "" || done.Response == nil {
				return runnerwire.DecisionResponse{}, fmt.Errorf("your runner: %s", firstNonEmpty(done.Error, "no decision"))
			}
			return *done.Response, nil
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Deliver applies a runner's llm_delta, llm_done or decide_done. runnerID comes from the
// authenticated socket: only the runner a call was sent to can answer it, and
// an answer to a call nobody waits for any more is dropped.
func (l *LLMRelay) Deliver(runnerID string, env runnerwire.Envelope) error {
	var id string
	switch env.Type {
	case runnerwire.TypeLLMDelta:
		if env.LLMDelta == nil {
			return fmt.Errorf("%w: empty llm_delta", ErrInvalid)
		}
		id = env.LLMDelta.ID
		if len(env.LLMDelta.Text) > runnerwire.MaxLLMDeltaBytes {
			return fmt.Errorf("%w: llm_delta too large", ErrInvalid)
		}
	case runnerwire.TypeLLMDone:
		if env.LLMDone == nil {
			return fmt.Errorf("%w: empty llm_done", ErrInvalid)
		}
		id = env.LLMDone.ID
		if len(env.LLMDone.Message) > runnerwire.MaxLLMDoneBytes || len(env.LLMDone.Error) > runnerwire.MaxLLMDeltaBytes {
			return fmt.Errorf("%w: llm_done too large", ErrInvalid)
		}
	case runnerwire.TypeDecideDone:
		if env.DecideDone == nil {
			return fmt.Errorf("%w: empty decide_done", ErrInvalid)
		}
		id = env.DecideDone.ID
		if len(env.DecideDone.Error) > runnerwire.MaxLLMDeltaBytes ||
			(env.DecideDone.Response != nil && len(env.DecideDone.Response.Answers) > runnerwire.MaxDecideQuestions) {
			return fmt.Errorf("%w: decide_done too large", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: not a model call answer", ErrInvalid)
	}
	l.mu.Lock()
	call := l.calls[id]
	l.mu.Unlock()
	if call == nil || call.runnerID != runnerID {
		return ErrNotFound
	}
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.done != nil || call.decided != nil {
		return nil
	}
	if env.Type == runnerwire.TypeDecideDone {
		done := *env.DecideDone
		call.decided = &done
		call.wake()
		return nil
	}
	if env.Type == runnerwire.TypeLLMDelta {
		if text := env.LLMDelta.Text; text != "" && call.size+len(text) <= maxQueuedLLMText {
			call.queued = append(call.queued, text)
			call.size += len(text)
		}
	} else {
		done := *env.LLMDone
		call.done = &done
	}
	call.wake()
	return nil
}

func newCallID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "llm-" + hex.EncodeToString(b[:]), nil
}

// attachedAt is when the runner's live connection was attached, zero if none.
func (r *Registry) attachedAt(id string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attached[id]
}
