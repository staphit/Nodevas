package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func TestWorkRegistryPersistAndPrune(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now().UTC()
	old := now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Format(time.RFC3339)

	if err := registerWork(dataDir, workEntry{
		Root:       "run-old",
		RepoID:     "r1",
		Branch:     "agent/tidy-run-old",
		Base:       "main",
		Agent:      "claude",
		LastRunID:  "run-old",
		LastStatus: runnerwire.RunSucceeded,
		At:         old,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxWorks+5; i++ {
		id := fmt.Sprintf("run-%03d", i)
		if err := registerWork(dataDir, workEntry{
			Root:       id,
			RepoID:     "r1",
			Branch:     "agent/tidy-" + id,
			Head:       strings.Repeat("a", 40),
			Base:       "main",
			Agent:      "claude",
			LastRunID:  id,
			LastStatus: runnerwire.RunSucceeded,
			At:         recent,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dataDir, workFile))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("work.json mode = %04o, want 0600", got)
		}
	}

	entries, err := loadWorks(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxWorks {
		t.Fatalf("len(entries) = %d, want %d", len(entries), maxWorks)
	}
	if _, ok, _ := lookupWork(dataDir, "run-old"); ok {
		t.Error("expired entry run-old was kept")
	}
	if _, ok, _ := lookupWork(dataDir, "run-000"); ok {
		t.Error("oldest entry past maxWorks was kept")
	}
	if got, ok, err := lookupWork(dataDir, fmt.Sprintf("run-%03d", maxWorks+4)); err != nil || !ok || got.LastRunID != fmt.Sprintf("run-%03d", maxWorks+4) {
		t.Errorf("newest entry lookup = %+v, %v, %v", got, ok, err)
	}
}

func TestResumeOnlyItsOwnSession(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	// Turn 1: headless Claude emits session_id from claude_stream.jsonl.
	first := globalTestJob("run-s1", "edit: tidy the README")
	env.hub.offer(first)
	if res := env.hub.wait("run-s1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 result = %+v", res)
	}

	entry, ok, err := lookupWork(env.client.Config.DataDir, "run-s1")
	if err != nil || !ok {
		t.Fatalf("lookupWork = %+v, %v, %v", entry, ok, err)
	}
	const claudeFixtureSession = "fc97caf9-629c-414a-8b84-67ec6e88bd98"
	if entry.Session != claudeFixtureSession {
		t.Fatalf("recorded session = %q, want %q", entry.Session, claudeFixtureSession)
	}

	// Turn 2: follow-up with the same agent (and a different model) resumes the
	// runner's own recorded session from work.json.
	second := globalTestJob("run-s2", "continue: add more.txt")
	second.ContinueOf = "run-s1"
	second.WorkRoot = "run-s1"
	env.hub.offer(second)
	if res := env.hub.wait("run-s2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 result = %+v", res)
	}

	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-s2", "args")), &args); err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || i+1 >= len(args) || args[i+1] != claudeFixtureSession {
		t.Fatalf("turn 2 args = %q, want --resume %s", args, claudeFixtureSession)
	}
	stdin := env.recorded(t, "run-s2", "stdin")
	if !strings.Contains(stdin, "Reminder: same worktree") || !strings.Contains(stdin, "continue: add more.txt") {
		t.Errorf("turn 2 stdin = %q", stdin)
	}
	if strings.Contains(stdin, "<prior-task>") {
		t.Errorf("resumed turn used fallback ContinueSection:\n%s", stdin)
	}

	// If work.json has no session recorded, the runner does not pass --resume.
	entry, ok, err = lookupWork(env.client.Config.DataDir, "run-s1")
	if err != nil || !ok {
		t.Fatalf("lookupWork after turn 2 = %+v, %v, %v", entry, ok, err)
	}
	entry.Session = ""
	if err := registerWork(env.client.Config.DataDir, entry); err != nil {
		t.Fatal(err)
	}
	third := globalTestJob("run-s3", "continue: another change")
	third.ContinueOf = "run-s2"
	third.WorkRoot = "run-s1"
	third.ContinueTask = "tidy the README"
	third.ContinueSummary = "added more.txt"
	env.hub.offer(third)
	if res := env.hub.wait("run-s3", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 3 result = %+v", res)
	}
	var args3 []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-s3", "args")), &args3); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args3, "--resume") {
		t.Fatalf("turn 3 args unexpectedly contained --resume: %q", args3)
	}
	stdin3 := env.recorded(t, "run-s3", "stdin")
	if !strings.Contains(stdin3, "<prior-task>") || !strings.Contains(stdin3, "<prior-summary>") {
		t.Errorf("turn 3 stdin missing fallback ContinueSection:\n%s", stdin3)
	}
}

func TestFallbackWhenAgentChanged(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	// Turn 1: Claude.
	first := globalTestJob("run-a1", "edit: initial work")
	env.hub.offer(first)
	if res := env.hub.wait("run-a1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 result = %+v", res)
	}
	entry, ok, err := lookupWork(env.client.Config.DataDir, "run-a1")
	if err != nil || !ok || entry.Agent != "claude" || entry.Session == "" {
		t.Fatalf("turn 1 entry = %+v, %v, %v", entry, ok, err)
	}
	claudeSession := entry.Session

	// Turn 2: switch agent to Codex. It must NOT resume Claude's session ID; it
	// must use the carry-over fallback prompt (ContinueSection).
	second := globalTestJob("run-a2", "continue-codex: finish with codex")
	second.Agent = "codex"
	second.Model = "gpt-5.5"
	second.ContinueOf = "run-a1"
	second.WorkRoot = "run-a1"
	second.ContinueTask = "initial work"
	second.ContinueSummary = "added hello.txt"
	env.hub.offer(second)
	if res := env.hub.wait("run-a2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 result = %+v", res)
	}

	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-a2", "args")), &args); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "resume") || slices.Contains(args, claudeSession) {
		t.Fatalf("turn 2 with different agent tried to resume %s: args = %q", claudeSession, args)
	}
	stdin := env.recorded(t, "run-a2", "stdin")
	for _, want := range []string{"You are continuing your earlier work in this folder on branch agent/tidy-readme-run-a1.",
		"<prior-task>\ninitial work\n</prior-task>",
		"<prior-summary>\nadded hello.txt\n</prior-summary>",
		"continue-codex: finish with codex"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("turn 2 stdin missing %q:\n%s", want, stdin)
		}
	}

	entry, ok, err = lookupWork(env.client.Config.DataDir, "run-a1")
	if err != nil || !ok {
		t.Fatalf("lookupWork after turn 2 = %+v, %v, %v", entry, ok, err)
	}
	const codexFixtureSession = "01a169d3-4de5-7b12-b230-46d24af8c811"
	if entry.Agent != "codex" || entry.Session != codexFixtureSession {
		t.Errorf("entry after turn 2 = %+v, want agent=codex session=%s", entry, codexFixtureSession)
	}
}

func TestResumeFastFailRetriesFresh(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	// Turn 1: Claude records its session ID.
	first := globalTestJob("run-ff1", "edit: initial work")
	env.hub.offer(first)
	if res := env.hub.wait("run-ff1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 result = %+v", res)
	}

	// Turn 2: "resume-fail" mode exits non-zero with no text event when
	// --resume is present, and succeeds when retried without --resume.
	second := globalTestJob("run-ff2", "resume-fail: retry fresh")
	second.ContinueOf = "run-ff1"
	second.WorkRoot = "run-ff1"
	second.ContinueTask = "initial work"
	second.ContinueSummary = "added hello.txt"
	env.hub.offer(second)
	if res := env.hub.wait("run-ff2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 result = %+v", res)
	}

	// The first attempt passed --resume.
	var resumeArgs []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-ff2", "resume-args")), &resumeArgs); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resumeArgs, "--resume") {
		t.Fatalf("first attempt args lacked --resume: %q", resumeArgs)
	}

	// The retry dropped --resume and sent the fallback ContinueSection prompt.
	var retryArgs []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-ff2", "args")), &retryArgs); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(retryArgs, "--resume") {
		t.Fatalf("retry args still contained --resume: %q", retryArgs)
	}
	stdin := env.recorded(t, "run-ff2", "stdin")
	if !strings.Contains(stdin, "<prior-task>\ninitial work\n</prior-task>") || !strings.Contains(stdin, "resume-fail: retry fresh") {
		t.Errorf("retry stdin missing ContinueSection:\n%s", stdin)
	}
	if !hubHasEvent(env, "run-ff2", "resume failed; continuing without the agent's memory") {
		t.Errorf("missing fast-fail retry log event in %+v", env.hub.messages("run-ff2"))
	}
}
