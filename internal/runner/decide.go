package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// System One decisions (runnerwire/decide.go): the hub's autopilot asks which
// agent and model should implement a task, and this runner asks its owner's
// System One endpoint — Laya on this machine, or TypeSafe's hosted Jev — with
// a key (if any) that never leaves this machine. The same rules as the
// copilot's model calls: the owner's settings decide where the request goes,
// the hub only says what to ask, and only for this runner's owner.

// decideTimeout bounds one decision. System One models answer in a fraction
// of a second; a cold Laya loading its checkpoint takes longer.
const decideTimeout = 10 * time.Second

// decide vets a decision and starts it. It returns at once; the answer goes
// out through out.
func (s *llmSession) decide(req *runnerwire.DecideRequest) {
	if req == nil || req.ID == "" || len(req.ID) > runnerwire.MaxLLMIDBytes {
		return
	}
	refuse := func(reason string) {
		s.c.logf("advisor: refused decision %s: %s", shortID(req.ID), reason)
		go s.send(runnerwire.Envelope{Type: runnerwire.TypeDecideDone,
			DecideDone: &runnerwire.DecideDone{ID: req.ID, Error: reason}})
	}
	info, key, reason := s.c.Config.AdvisorLLM()
	switch {
	case req.Purpose != runnerwire.LLMPurposeAdvisor:
		refuse("this runner answers decisions for the autopilot's advisor only")
		return
	case reason != "":
		refuse("this runner has no System One advisor: " + reason)
		return
	case info.Provider != runnerwire.ProviderSystemOne:
		refuse("this runner's advisor is a model, not a System One endpoint (nodevas runner set advisorProvider systemone)")
		return
	case s.ownerID != "" && req.AccountID != s.ownerID:
		refuse("this runner answers its owner's advisor only")
		return
	case req.Model != info.Model || strings.TrimRight(req.BaseURL, "/") != info.BaseURL ||
		req.Request.Model != info.Model:
		refuse("the request does not match this runner's advisor settings (restart the runner after changing them)")
		return
	case len(req.Request.State) == 0 || len(req.Request.State) > runnerwire.MaxDecideStateBytes:
		refuse("the decision's state is empty or too large")
		return
	case len(req.Request.Questions) == 0 || len(req.Request.Questions) > runnerwire.MaxDecideQuestionsBytes:
		refuse("the decision's questions are empty or too large")
		return
	}
	var questions map[string]json.RawMessage
	if err := json.Unmarshal(req.Request.Questions, &questions); err != nil || len(questions) == 0 {
		refuse("the decision's questions are not an object of questions")
		return
	}
	if len(questions) > runnerwire.MaxDecideQuestions {
		refuse(fmt.Sprintf("a decision asks at most %d questions", runnerwire.MaxDecideQuestions))
		return
	}

	s.mu.Lock()
	if _, dup := s.running[req.ID]; dup {
		s.mu.Unlock()
		return
	}
	if len(s.running) >= maxConcurrentLLM {
		s.mu.Unlock()
		refuse("this runner is already making as many advisor calls as it will at once")
		return
	}
	if !s.c.llmLimiter().allow(time.Now()) {
		s.mu.Unlock()
		refuse("this runner's advisor is over its limit of calls per minute")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, decideTimeout)
	s.running[req.ID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, req.ID)
			s.mu.Unlock()
			cancel()
		}()
		done := runnerwire.DecideDone{ID: req.ID}
		started := time.Now()
		resp, err := callSystemOne(ctx, info.BaseURL, key, req.Request)
		if err != nil {
			s.c.logf("advisor: decision %s failed: %v", shortID(req.ID), err)
			done.Error = truncate(err.Error(), 2000)
		} else {
			s.c.logf("advisor: decision %s by %s in %s (%d questions)", shortID(req.ID), orDash(resp.Model),
				time.Since(started).Round(10*time.Millisecond), len(questions))
			done.Response = &resp
		}
		s.send(runnerwire.Envelope{Type: runnerwire.TypeDecideDone, DecideDone: &done})
	}()
}

// callSystemOne asks the System One endpoint at base: POST /v1/systemone,
// the key (if any) as a bearer token. Redirects are not followed, so the key
// goes nowhere but the configured endpoint.
func callSystemOne(ctx context.Context, base, key string, req runnerwire.DecisionRequest) (runnerwire.DecisionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return runnerwire.DecisionResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return runnerwire.DecisionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{
		Timeout: decideTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return runnerwire.DecisionResponse{}, fmt.Errorf("the System One endpoint at %s did not answer in time", base)
		}
		return runnerwire.DecisionResponse{}, fmt.Errorf("the System One endpoint at %s: %w", base, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, runnerwire.MaxDecideAnswerBytes+1))
	if err != nil {
		return runnerwire.DecisionResponse{}, fmt.Errorf("reading the System One answer: %w", err)
	}
	if len(raw) > runnerwire.MaxDecideAnswerBytes {
		return runnerwire.DecisionResponse{}, errors.New("the System One answer is too large")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return runnerwire.DecisionResponse{}, fmt.Errorf("System One %d: %s", res.StatusCode, systemOneError(raw))
	}
	var resp runnerwire.DecisionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return runnerwire.DecisionResponse{}, errors.New("the System One endpoint answered something other than a decision")
	}
	if len(resp.Answers) == 0 {
		return runnerwire.DecisionResponse{}, errors.New("the System One endpoint answered no questions")
	}
	return resp, nil
}

// systemOneError is an error body's message: Laya's {"detail": "…"} (or
// FastAPI's list of details), {"error": {"message": …}} or {"error": "…"},
// else the start of the body.
func systemOneError(raw []byte) string {
	var v struct {
		Detail json.RawMessage `json:"detail"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &v) == nil {
		for _, field := range []json.RawMessage{v.Detail, v.Error} {
			if len(field) == 0 {
				continue
			}
			var text string
			if json.Unmarshal(field, &text) == nil && text != "" {
				return truncate(text, 500)
			}
			var obj struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(field, &obj) == nil && obj.Message != "" {
				return truncate(obj.Message, 500)
			}
			return truncate(string(field), 500)
		}
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "(no message)"
	}
	return truncate(text, 300)
}
