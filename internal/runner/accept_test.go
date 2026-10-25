package runner

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// syncBuffer is a bytes.Buffer safe to read while another goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var testOffer = Offer{
	Job: runnerwire.Job{RunID: "run-1", Project: "demo", NodeID: "n-1", NodeTitle: "Fix \x1b[2Jit",
		Prompt: strings.Repeat("x", 600), Agent: "claude", Model: "sonnet", RequestedBy: "patrick"},
	Repo: runnerwire.Repo{ID: "r1", Name: "demo-repo", Path: "/src/demo"},
}

func TestTerminalAccepterNotInteractive(t *testing.T) {
	a := &TerminalAccepter{In: strings.NewReader("y\n"), Out: io.Discard}
	if ok, reason := a.Accept(context.Background(), testOffer); ok || reason != ReasonNoTerminal {
		t.Fatalf("Accept = %v, %q", ok, reason)
	}
}

func TestTerminalAccepterAnswers(t *testing.T) {
	in, w := io.Pipe()
	out := &syncBuffer{}
	a := &TerminalAccepter{In: in, Out: out, Interactive: true, Timeout: 5 * time.Second}

	asked := 0
	answer := func(line string) (bool, string) {
		done := make(chan struct{})
		var ok bool
		var reason string
		go func() {
			ok, reason = a.Accept(context.Background(), testOffer)
			close(done)
		}()
		asked++
		deadline := time.Now().Add(5 * time.Second)
		for strings.Count(out.String(), "Accept? [y/N]: ") < asked && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		_, _ = io.WriteString(w, line+"\n")
		<-done
		return ok, reason
	}
	if ok, _ := answer("Y"); !ok {
		t.Fatal("y was not a yes")
	}
	shown := out.String()
	for _, want := range []string{"patrick", "demo", "n-1", "demo-repo", "/src/demo", "claude, model sonnet",
		"(the repository's default branch)", strings.Repeat("x", 500) + " …"} {
		if !strings.Contains(shown, want) {
			t.Errorf("offer text missing %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "\x1b") || strings.Contains(shown, strings.Repeat("x", 501)) {
		t.Errorf("offer text has an escape or the whole prompt:\n%q", shown)
	}
	if ok, reason := answer(""); ok || reason != ReasonDeclined {
		t.Fatalf("empty answer = %v, %q", ok, reason)
	}

	// Timeout, and a cancelled offer, both decline without an answer.
	a.Timeout = 30 * time.Millisecond
	if ok, reason := a.Accept(context.Background(), testOffer); ok || reason != ReasonTimedOut {
		t.Fatalf("timeout = %v, %q", ok, reason)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Timeout = time.Minute
	if ok, reason := a.Accept(ctx, testOffer); ok || reason != ReasonCancelled {
		t.Fatalf("cancelled = %v, %q", ok, reason)
	}

	// Input closing is a no, not a hang.
	w.Close()
	if ok, reason := a.Accept(context.Background(), testOffer); ok || reason != ReasonInputGone {
		t.Fatalf("closed input = %v, %q", ok, reason)
	}
}

func TestTerminalAccepterOneQuestionAtATime(t *testing.T) {
	in, w := io.Pipe()
	defer w.Close()
	out := &syncBuffer{}
	a := &TerminalAccepter{In: in, Out: out, Interactive: true, Timeout: 5 * time.Second}
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ok, _ := a.Accept(context.Background(), testOffer)
			results <- ok
		}()
	}
	waitFor := func(n int) {
		deadline := time.Now().Add(5 * time.Second)
		for strings.Count(out.String(), "Accept? [y/N]: ") < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		if got := strings.Count(out.String(), "Accept? [y/N]: "); got != n {
			t.Fatalf("%d questions on screen, want %d", got, n)
		}
	}
	waitFor(1)
	_, _ = io.WriteString(w, "y\n")
	waitFor(2)
	_, _ = io.WriteString(w, "n\n")
	got := []bool{<-results, <-results}
	if got[0] == got[1] {
		t.Fatalf("answers = %v, want one yes and one no", got)
	}
}

func TestConfigSet(t *testing.T) {
	cfg := &Config{}
	if cfg.MaxConcurrentRuns() != 1 || cfg.RunTimeoutMins() != 60 || cfg.ClaudePermissionMode() != "acceptEdits" || cfg.AutoAcceptOn() {
		t.Fatalf("defaults: %+v", cfg)
	}
	if dir, err := cfg.RunDataDir(); err != nil || !strings.HasSuffix(dir, "runner-data") {
		t.Fatalf("data dir %q, %v", dir, err)
	}
	ok := []struct {
		key    string
		values []string
	}{
		{"maxConcurrent", []string{"3"}},
		{"RUNTIMEOUTMINUTES", []string{"90"}},
		{"permissionMode", []string{"dontask"}},
		{"allowedTools", []string{"Edit, Bash(git diff, git log),Read"}},
		{"autoAccept", []string{"true"}},
		{"dataDir", []string{"rel/dir"}},
	}
	for _, c := range ok {
		if err := cfg.Set(c.key, c.values); err != nil {
			t.Fatalf("Set(%s, %v) = %v", c.key, c.values, err)
		}
	}
	if cfg.MaxConcurrentRuns() != 3 || cfg.RunTimeout() != 90*time.Minute || cfg.PermissionMode != "dontAsk" ||
		!slices.Equal(cfg.AllowedTools, []string{"Edit", "Bash(git diff, git log)", "Read"}) || !cfg.AutoAcceptOn() ||
		!strings.HasSuffix(cfg.DataDir, "dir") || !filepath.IsAbs(cfg.DataDir) {
		t.Fatalf("after set: %+v", cfg)
	}
	if err := cfg.Set("allowedTools", []string{"Edit", "Bash(go test *)"}); err != nil ||
		!slices.Equal(cfg.AllowedTools, []string{"Edit", "Bash(go test *)"}) {
		t.Fatalf("tools as arguments: %v %v", cfg.AllowedTools, err)
	}
	for _, key := range SettingKeys {
		if err := cfg.Set(key, []string{"default"}); err != nil {
			t.Fatalf("reset %s: %v", key, err)
		}
	}
	if cfg.MaxConcurrent != 0 || cfg.RunTimeoutMinutes != 0 || cfg.PermissionMode != "" || cfg.AllowedTools != nil ||
		cfg.AutoAccept != nil || cfg.DataDir != "" {
		t.Fatalf("after reset: %+v", cfg)
	}
	bad := []struct {
		key    string
		values []string
	}{
		{"nope", []string{"1"}},
		{"maxConcurrent", []string{"0"}},
		{"maxConcurrent", []string{"99"}},
		{"runTimeoutMinutes", []string{"x"}},
		{"permissionMode", []string{"yolo"}},
		{"autoAccept", []string{"maybe"}},
		{"autoAccept", []string{"true", "false"}},
		{"autoAccept", nil},
	}
	for _, c := range bad {
		if err := cfg.Set(c.key, c.values); err == nil {
			t.Errorf("Set(%s, %v) accepted", c.key, c.values)
		}
	}
}

func TestTerminalAccepterRemoteTyping(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "yes\n": true, "\n": false, "n\n": false, "": false} {
		var out strings.Builder
		a := &TerminalAccepter{In: strings.NewReader(in), Out: &out, Interactive: true, Timeout: 5 * time.Second}
		if got := a.AllowRemoteTyping(context.Background(), Offer{}); got != want {
			t.Errorf("answer %q: %t", in, got)
		}
		if !strings.Contains(out.String(), "Allow remote typing? [y/N]") {
			t.Errorf("question: %q", out.String())
		}
	}
	if (&TerminalAccepter{In: strings.NewReader("y\n"), Out: io.Discard}).AllowRemoteTyping(context.Background(), Offer{}) {
		t.Error("no terminal, yet typing allowed")
	}
}

func TestRunTimeoutFor(t *testing.T) {
	cfg := &Config{}
	if cfg.RunTimeoutFor(runnerwire.JobReview) != 20*time.Minute || cfg.RunTimeoutFor(runnerwire.JobPlan) != 30*time.Minute ||
		cfg.RunTimeoutFor("") != 60*time.Minute || cfg.RunTimeoutFor(runnerwire.JobRevise) != 60*time.Minute {
		t.Fatal("defaults")
	}
	cfg.RunTimeoutMinutes = 15
	if cfg.RunTimeoutFor(runnerwire.JobReview) != 15*time.Minute {
		t.Fatal("a review's default outlasts the run timeout")
	}
	if err := cfg.Set("reviewTimeoutMinutes", []string{"45"}); err != nil || cfg.RunTimeoutFor(runnerwire.JobReview) != 45*time.Minute {
		t.Fatalf("set review: %v", err)
	}
	if err := cfg.Set("planTimeoutMinutes", []string{"0"}); err == nil {
		t.Fatal("0 accepted")
	}
	if err := cfg.Set("reviewTimeoutMinutes", []string{"default"}); err != nil || cfg.ReviewTimeoutMinutes != 0 {
		t.Fatalf("reset: %v", err)
	}
}
