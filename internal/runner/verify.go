package runner

// Verify jobs (agent roles, spec section 4): no agent, no MCP, no model. The
// runner runs the repository's own test command, configured here and never
// sent by the hub, in a throwaway worktree at the PR head, and reports the
// exit code. Red passes when the tests fail; green when they pass.
//
// The command runs code an agent wrote, outside that agent's sandbox, as the
// owner. That is why it is opt-in per repository (the owner sets the command
// here), why auto-accepting a verify job needs its own switch
// (`nodevas runner autoaccept verify on`), and why it runs with no git hooks,
// an environment without the runner's or GitHub's credentials, a timeout
// that kills the whole process tree, and in a worktree that is thrown away.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// DefaultTestTimeout bounds a test command without its own timeout.
const DefaultTestTimeout = 20 * time.Minute

// MaxTestTimeoutMinutes bounds a repo's test timeout.
const MaxTestTimeoutMinutes = 180

// maxTestCommandBytes bounds a test command line.
const maxTestCommandBytes = 2 << 10

// verifyCaptureBytes is how much of a test run's output is kept before it is
// redacted and cut to runnerwire.MaxVerifyOutput (the tail).
const verifyCaptureBytes = 256 << 10

// VerifyRequest is one verify run, built from this runner's own config and
// the job's phase and head only.
type VerifyRequest struct {
	// RepoPath is the owner's checkout; WorkRoot where the throwaway
	// worktree goes.
	RepoPath, WorkRoot string
	Phase              string
	// Head is the commit the job names; PRHead what the PR's head is now.
	// They must agree.
	Head, PRHead string
	// Command is the repo's test command from runner.json, argv-style.
	Command []string
	Timeout time.Duration
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// RunVerify runs req's command at req.Head and reports the result. An error
// is a verify that could not run (the head moved, the commit is unknown);
// a test run that failed is a result.
func RunVerify(ctx context.Context, req VerifyRequest) (runnerwire.VerifyResult, error) {
	result := runnerwire.VerifyResult{Phase: req.Phase, Head: req.Head}
	if len(req.Command) == 0 {
		result.NoCommand, result.ExitCode = true, -1
		result.Output = "no test command is configured for this repository on the runner"
		return result, nil
	}
	if req.PRHead != "" && req.PRHead != req.Head {
		return result, fmt.Errorf("the pull request's head moved since the job was offered (job %s, now %s)",
			short(req.Head), short(req.PRHead))
	}
	if !shaPattern.MatchString(req.Head) {
		return result, fmt.Errorf("verify needs a commit sha, not %q", req.Head)
	}
	if err := os.MkdirAll(req.WorkRoot, 0o700); err != nil {
		return result, err
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return result, err
	}
	dir := filepath.Join(req.WorkRoot, "verify-"+hex.EncodeToString(raw[:]))
	if _, err := git(ctx, req.RepoPath, "worktree", "add", "--detach", dir, req.Head); err != nil {
		return result, fmt.Errorf("check out %s: %w", short(req.Head), err)
	}
	defer func() {
		// Background: the run's context may be done, the worktree still
		// has to go.
		cleanup := context.Background()
		if _, err := git(cleanup, req.RepoPath, "worktree", "remove", "--force", dir); err != nil {
			_ = os.RemoveAll(dir)
			_, _ = git(cleanup, req.RepoPath, "worktree", "prune")
		}
	}()
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTestTimeout
	}
	exit, output, timedOut, duration := runTestCommand(ctx, dir, req.Command, timeout)
	result.ExitCode = exit
	result.DurationMs = max(duration.Milliseconds(), 1)
	text := redact.String(output)
	note := ""
	if timedOut {
		note = fmt.Sprintf("\n[verify timed out after %s; the test command and everything it started were stopped]", timeout)
	}
	result.Output = tailString(text, runnerwire.MaxVerifyOutput-len(note)) + note
	switch req.Phase {
	case runnerwire.VerifyRed:
		result.Passed = exit != 0 && !timedOut
	default:
		result.Passed = exit == 0 && !timedOut
	}
	return result, nil
}

// tailWriter keeps the last n bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - w.n; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.ToValidUTF8(string(w.buf), "")
}

// tailString keeps the last n bytes of s on a rune boundary.
func tailString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return s[start:]
}

// scrubbedEnv is the runner's environment without its credentials or
// GitHub's: the test command runs code an agent wrote.
func scrubbedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(key)
		switch {
		case strings.HasPrefix(upper, "NODEVAS_"),
			upper == "GH_TOKEN", upper == "GITHUB_TOKEN", upper == "GH_ENTERPRISE_TOKEN",
			upper == "GITHUB_ENTERPRISE_TOKEN", upper == "GIT_ASKPASS", upper == "SSH_AUTH_SOCK":
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	return append(out, gitConfigEnv()...)
}

// runTestCommand runs command in dir within timeout, killing its whole
// process tree on timeout or cancel.
func runTestCommand(ctx context.Context, dir string, command []string, timeout time.Duration) (int, string, bool, time.Duration) {
	start := time.Now()
	out := &tailWriter{n: verifyCaptureBytes}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = scrubbedEnv()
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		return -1, "the test command did not start: " + err.Error(), false, time.Since(start)
	}
	tree := attachTree(cmd)
	defer tree.close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-timer.C:
		timedOut = true
		tree.kill()
		err = <-done
	case <-ctx.Done():
		tree.kill()
		err = <-done
		_, _ = out.Write([]byte("\n[verify cancelled]"))
	}
	exit := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			exit = exitErr.ExitCode()
		} else {
			exit = -1
		}
	}
	if timedOut && exit == 0 {
		exit = -1
	}
	return exit, out.String(), timedOut, time.Since(start)
}

// verifyRequestFor builds a verify request for job from this runner's config.
// Nothing the job carries but its phase and head is used. reason is why the
// job cannot run ("" when it can).
func verifyRequestFor(cfg *Config, job runnerwire.Job, repo runnerwire.Repo, prHead, workRoot string) (VerifyRequest, string) {
	spec := job.Verify
	if spec == nil {
		return VerifyRequest{}, "the verify job names no phase or head"
	}
	switch spec.Phase {
	case runnerwire.VerifyRed, runnerwire.VerifyGreen, runnerwire.VerifyGoal:
	default:
		return VerifyRequest{}, fmt.Sprintf("unknown verify phase %q", spec.Phase)
	}
	if !shaPattern.MatchString(spec.Head) {
		return VerifyRequest{}, fmt.Sprintf("the verify job's head %q is not a commit sha", spec.Head)
	}
	command, timeout := cfg.RepoTestCommand(repo.ID)
	return VerifyRequest{
		RepoPath: repo.Path, WorkRoot: workRoot, Phase: spec.Phase,
		Head: spec.Head, PRHead: prHead, Command: command, Timeout: timeout,
	}, ""
}

// repoTest is a repo's test command and timeout: runner-local settings that
// never go into the hello (runnerwire.Repo carries only Verify).
type repoTest struct {
	Command        []string
	TimeoutMinutes int
}

// SplitCommandLine splits a command line argv-style: whitespace separates
// words, single and double quotes group them, a backslash escapes the next
// character outside single quotes. Nothing is expanded: there is no shell
// unless the owner writes one (sh -c "...").
func SplitCommandLine(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case unicode.IsSpace(r):
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unterminated quote or escape in the command line")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// SetRepoTestCommand sets (or, with command "", clears) a repo's test command
// and timeout: `nodevas runner repo set <id|name> --test-command "..."`.
// The command is split argv-style; no shell unless the owner writes one.
func (c *Config) SetRepoTestCommand(arg, command string, timeoutMinutes int) (runnerwire.Repo, error) {
	index := -1
	for i, repo := range c.Repos {
		if repo.ID == arg || repo.Name == arg {
			index = i
			break
		}
	}
	if index < 0 {
		return runnerwire.Repo{}, fmt.Errorf("no repository %q on this runner (nodevas runner repo list)", arg)
	}
	if timeoutMinutes < 0 || timeoutMinutes > MaxTestTimeoutMinutes {
		return runnerwire.Repo{}, fmt.Errorf("--test-timeout must be 1 to %d minutes (0 is the default, %d)",
			MaxTestTimeoutMinutes, int(DefaultTestTimeout/time.Minute))
	}
	repo := c.Repos[index]
	if strings.TrimSpace(command) == "" {
		delete(c.repoTests, repo.ID)
		return repo, nil
	}
	if len(command) > maxTestCommandBytes || strings.ContainsAny(command, "\x00\r\n") {
		return runnerwire.Repo{}, fmt.Errorf("the test command must be one line of at most %d bytes", maxTestCommandBytes)
	}
	argv, err := SplitCommandLine(command)
	if err != nil {
		return runnerwire.Repo{}, err
	}
	if len(argv) == 0 {
		return runnerwire.Repo{}, errors.New("the test command is empty")
	}
	if c.repoTests == nil {
		c.repoTests = map[string]repoTest{}
	}
	c.repoTests[repo.ID] = repoTest{Command: argv, TimeoutMinutes: timeoutMinutes}
	return repo, nil
}

// RepoTestCommand is a repo's test command and timeout, as loaded at start.
func (c *Config) RepoTestCommand(repoID string) ([]string, time.Duration) {
	t, ok := c.repoTests[repoID]
	if !ok || len(t.Command) == 0 {
		return nil, 0
	}
	timeout := DefaultTestTimeout
	if t.TimeoutMinutes > 0 {
		timeout = time.Duration(t.TimeoutMinutes) * time.Minute
	}
	return append([]string(nil), t.Command...), timeout
}

// SetAllowVerify is `runner autoaccept verify on|off`.
func (c *Config) SetAllowVerify(on bool) {
	if c.AutoAccept == nil {
		c.AutoAccept = &runnerwire.AutoAcceptPolicy{RequireStepUp: true}
	}
	c.AutoAccept.AllowVerify = on
}

// repoEntry is a runner.json repository: the hello's fields and the
// runner-local test settings beside them.
type repoEntry struct {
	runnerwire.Repo
	TestCommand        []string `json:"testCommand,omitempty"`
	TestTimeoutMinutes int      `json:"testTimeoutMinutes,omitempty"`
}

// MarshalJSON writes runner.json with each repository's test settings in its
// entry. The hello never goes through here: it copies Config.Repos.
func (c Config) MarshalJSON() ([]byte, error) {
	type plain Config
	entries := make([]repoEntry, 0, len(c.Repos))
	for _, repo := range c.Repos {
		repo.Verify = false // derived from the command at hello time
		t := c.repoTests[repo.ID]
		entries = append(entries, repoEntry{Repo: repo, TestCommand: t.Command, TestTimeoutMinutes: t.TimeoutMinutes})
	}
	aux := struct {
		plain
		Repos []repoEntry `json:"repos,omitempty"`
	}{plain: plain(c), Repos: entries}
	return json.Marshal(aux)
}

// executeVerify runs a verify job: the repo's test command at the PR head the
// job names, in a throwaway worktree. No agent, no MCP, no model.
func (j *jobs) executeVerify(ctx context.Context, r *run, pr prCheck) {
	job := r.job
	dataDir, err := j.c.Config.RunDataDir()
	if err != nil {
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, Error: err.Error()})
		return
	}
	req, reason := verifyRequestFor(j.c.Config, job, r.repo, pr.Head, filepath.Join(dataDir, "verify"))
	if reason != "" {
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: runnerwire.FailureTask, Error: reason})
		return
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: pr.Base, Branch: job.Branch}
	j.mu.Unlock()
	j.wake()
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("verify %s: running the repository's test command at %s", req.Phase, short(req.Head))})
	res, err := RunVerify(ctx, req)
	if ctx.Err() != nil {
		j.finish(r, cancelled(ctx))
		return
	}
	if err != nil {
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: runnerwire.FailureTask,
			Error: adapter.Truncate("verify: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return
	}
	summary := fmt.Sprintf("verify %s at %s: exit %d in %.1fs", res.Phase, short(res.Head), res.ExitCode, float64(res.DurationMs)/1000)
	if res.NoCommand {
		summary = "verify: no test command is configured for this repository (nodevas runner repo set " + r.repo.Name + " --test-command \"...\")"
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: summary})
	j.finish(r, runnerwire.RunResult{Status: runnerwire.RunSucceeded, ExitCode: res.ExitCode, Summary: summary, Verify: &res})
}

// executeJudge runs a judge job: a read-only agent run, like a plan, that
// judges the acceptance items (CHECK lines) and ends with VERDICT: pass or
// fail. A goal judge runs the repo's tests first and is given their result.
func (j *jobs) executeJudge(ctx context.Context, r *run) {
	job := r.job
	dataDir, err := j.c.Config.RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = CreateWorktree(ctx, r.repo.Path, dataDir, job.RunID, job.NodeID, job.Base)
	}
	if err != nil {
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate("could not create the worktree: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return
	}
	cleanup := func() {
		if err := removeWorktree(j.ctx, r.repo.Path, wt, true); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: wt.Base}
	j.mu.Unlock()
	j.wake()
	var tests *runnerwire.VerifyResult
	judgeJob := job
	if job.Verify != nil && job.Verify.Phase == runnerwire.VerifyGoal {
		head, err := git(ctx, wt.Path, "rev-parse", "HEAD")
		head = strings.TrimSpace(head)
		command, timeout := j.c.Config.RepoTestCommand(r.repo.ID)
		if err == nil {
			res, verr := RunVerify(ctx, VerifyRequest{RepoPath: r.repo.Path, WorkRoot: filepath.Join(dataDir, "verify"),
				Phase: runnerwire.VerifyGoal, Head: head, PRHead: head, Command: command, Timeout: timeout})
			if verr == nil {
				tests = &res
				judgeJob.Prompt = strings.TrimRight(job.Prompt, "\n") + "\n\n" + adapter.GoalTestBlock(res.ExitCode, res.NoCommand, res.Output)
			}
		}
	}
	result, text := j.runAgent(ctx, r, wt, judgeJob)
	if err := checkGitDir(j.ctx, wt); err != nil {
		cleanup()
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1, FailureKind: runnerwire.FailureTask, Error: err.Error()})
		return
	}
	if dirty, _ := changedFiles(j.ctx, wt.Path); strings.TrimSpace(dirty) != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "the judge changed files; discarded"})
	}
	cleanup()
	result.Verify = tests
	if result.Status == runnerwire.RunSucceeded {
		if strings.TrimSpace(text) == "" {
			text = result.Summary
		}
		result.Checks = parseChecks(text)
		result.Verdict = parseJudgeVerdict(text)
		result.Summary = adapter.Truncate(strings.TrimSpace(text), runnerwire.MaxSummaryBytes)
		if result.Verdict == "" {
			result.Status, result.FailureKind = runnerwire.RunFailed, runnerwire.FailureTask
			result.Error = `the judge gave no verdict (no final "VERDICT: pass" or "VERDICT: fail" line)`
		}
	}
	result.ClampRoleFields()
	j.finish(r, result)
}
