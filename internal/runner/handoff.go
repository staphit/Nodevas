package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Handoff (runnerwire/handoff.go): an implement run that stops at a usage
// limit keeps its partial work on a local branch, nodevas/handoff/<run ID>,
// and leaves a note saying where it got to. The next run of the task (the
// next model in the fallback chain, or the same one once the limit resets)
// starts its worktree from that branch.
//
// The branch is the runner's own: it is made here, recorded in
// handoffs.json in the runner's data directory with its repository and
// commit, never pushed by this step, and a job may only name one that is in
// that record, for the job's own repository, still at the recorded commit.
// The hub only passes the name along; it cannot make the runner check out
// any other ref, or a branch of another repository.

const (
	handoffFile = "handoffs.json"
	// handoffKeep is how long a handoff branch is kept for a next run.
	handoffKeep = 30 * 24 * time.Hour
	// maxHandoffs bounds the record; the oldest go first.
	maxHandoffs = 200

	// Parts of a note, each capped so one long part cannot crowd out the rest.
	noteTask    = 1536
	noteWords   = 2048
	noteStat    = 1536
	noteCommand = 1536
	notePlan    = 1024
)

// handoffEntry is one handoff branch this runner made.
type handoffEntry struct {
	Branch    string `json:"branch"`
	RepoID    string `json:"repoId"`
	RepoPath  string `json:"repoPath"`
	Commit    string `json:"commit"`
	RunID     string `json:"runId"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}

// handoffMu serialises the record's reads and writes.
var handoffMu sync.Mutex

func handoffPath(dataDir string) string { return filepath.Join(dataDir, handoffFile) }

func loadHandoffs(dataDir string) ([]handoffEntry, error) {
	raw, err := os.ReadFile(handoffPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []handoffEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", handoffFile, err)
	}
	return entries, nil
}

func saveHandoffs(dataDir string, entries []handoffEntry) error {
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, handoffFile+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), handoffPath(dataDir))
}

// registerHandoff records a branch the runner just made, dropping (and
// deleting) the ones past handoffKeep or past maxHandoffs.
func registerHandoff(ctx context.Context, dataDir string, entry handoffEntry) error {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	entries, err := loadHandoffs(dataDir)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-handoffKeep).UTC().Format(time.RFC3339)
	var kept, dropped []handoffEntry
	for _, e := range entries {
		if e.Branch == entry.Branch && e.RepoID == entry.RepoID {
			continue // replaced
		}
		if e.CreatedAt < cutoff {
			dropped = append(dropped, e)
			continue
		}
		kept = append(kept, e)
	}
	kept = append(kept, entry)
	if over := len(kept) - maxHandoffs; over > 0 {
		dropped = append(dropped, kept[:over]...)
		kept = kept[over:]
	}
	if err := saveHandoffs(dataDir, kept); err != nil {
		return err
	}
	for _, e := range dropped {
		// Only a branch still where this runner left it.
		if head, err := revParse(ctx, e.RepoPath, "refs/heads/"+e.Branch); err == nil && head == e.Commit {
			_, _ = git(ctx, e.RepoPath, "branch", "-D", e.Branch)
		}
	}
	return nil
}

// lookupHandoff checks a job's handoff branch: a handoff branch name, made
// by this runner for this repository, still at the commit it was made at.
func lookupHandoff(ctx context.Context, dataDir string, repo runnerwire.Repo, branch string) (handoffEntry, error) {
	if !runnerwire.ValidHandoffBranch(branch) {
		return handoffEntry{}, fmt.Errorf("handoff branch %q is not a handoff branch name", branch)
	}
	handoffMu.Lock()
	entries, err := loadHandoffs(dataDir)
	handoffMu.Unlock()
	if err != nil {
		return handoffEntry{}, err
	}
	for _, e := range entries {
		if e.Branch != branch || e.RepoID != repo.ID {
			continue
		}
		head, err := revParse(ctx, repo.Path, "refs/heads/"+branch)
		if err != nil {
			return handoffEntry{}, fmt.Errorf("handoff branch %s is gone from %s", branch, repo.Name)
		}
		if head != e.Commit {
			return handoffEntry{}, fmt.Errorf("handoff branch %s moved since this runner made it", branch)
		}
		return e, nil
	}
	return handoffEntry{}, fmt.Errorf("handoff branch %s is not one this runner made for %s", branch, repo.Name)
}

// prepareHandoff vets a job that continues a handoff, before anyone is asked
// to accept it: the reason is "" when it may go on, and r.job then carries
// the runner's own copy of the note.
func (j *jobs) prepareHandoff(ctx context.Context, r *run) string {
	job := &r.job
	if job.HandoffBranch == "" {
		job.HandoffNote = ""
		return ""
	}
	if job.Kind != runnerwire.JobImplement || job.Mode == runnerwire.ModeInteractive {
		return "a handoff is only continued by a headless implement run"
	}
	dataDir, err := j.c.Config.RunDataDir()
	if err != nil {
		return "cannot read the handoff record: " + err.Error()
	}
	entry, err := lookupHandoff(ctx, dataDir, r.repo, job.HandoffBranch)
	if err != nil {
		return err.Error()
	}
	// The note this runner wrote, not the hub's copy of it.
	if entry.Note != "" {
		job.HandoffNote = entry.Note
	}
	job.HandoffNote = adapter.Truncate(redact.String(job.HandoffNote), runnerwire.MaxHandoffNoteBytes)
	return ""
}

// handOff keeps a failed run's partial work: it commits the worktree, puts
// the commit on the run's handoff branch, writes the note and removes the
// worktree. With nothing changed there is no branch. result gets the branch
// and note; the changes are reported as any run's are.
func (j *jobs) handOff(r *run, wt Worktree, result *runnerwire.RunResult) *runnerwire.RunChanges {
	ctx := j.ctx
	job := r.job
	message := commitMessage(fmt.Sprintf("Partial work: the agent stopped (%s) before finishing.", result.FailureKind), job)
	committed := commitRun(ctx, wt, message)
	if committed.Err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not commit the partial work: " + committed.Err.Error()})
	}
	changes, err := describeChanges(ctx, wt.Path, committed, committed.Base)
	if err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not describe the changes: " + err.Error()})
	}
	fitChanges(job.RunID, &changes)
	switch {
	case committed.Err != nil:
		j.c.logf("run %s: partial work not kept for a handoff; worktree kept at %s", job.RunID, wt.Path)
		return &changes
	case !committed.Changed || changes.Commit == "":
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no changes to hand off; worktree removed"})
		if err := removeWorktree(ctx, r.repo.Path, wt, true); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
		return &changes
	}
	branch := runnerwire.HandoffBranchName(job.RunID)
	if _, err := git(ctx, r.repo.Path, "branch", "--force", branch, changes.Commit); err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not keep the partial work: " + err.Error()})
		return &changes
	}
	note := j.handoffNote(ctx, r, wt, committed, changes, *result)
	dataDir, err := j.c.Config.RunDataDir()
	if err == nil {
		err = registerHandoff(ctx, dataDir, handoffEntry{
			Branch: branch, RepoID: r.repo.ID, RepoPath: r.repo.Path, Commit: changes.Commit,
			RunID: job.RunID, Note: note, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if err != nil {
		_, _ = git(ctx, r.repo.Path, "branch", "-D", branch)
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not record the handoff: " + err.Error()})
		j.c.logf("run %s: handoff not recorded: %v; worktree kept at %s", job.RunID, err, wt.Path)
		return &changes
	}
	if err := removeWorktree(ctx, r.repo.Path, wt, true); err != nil {
		j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf(
		"kept the partial work on local branch %s (%s: %d files, +%d −%d) for the next run; not pushed",
		branch, short(changes.Commit), len(changes.Files), changes.Additions, changes.Deletions)})
	j.c.logf("run %s: partial work kept on %s for a handoff", job.RunID, branch)
	result.HandoffBranch, result.HandoffNote = branch, note
	return &changes
}

// handoffNote is what the next run is told about this one: what it was
// asked, its last words, what it changed, its last command and what it
// meant to do next. Redacted, and capped at MaxHandoffNoteBytes.
func (j *jobs) handoffNote(ctx context.Context, r *run, wt Worktree, committed commitResult, changes runnerwire.RunChanges, result runnerwire.RunResult) string {
	job := r.job
	j.mu.Lock()
	trail := r.trail
	j.mu.Unlock()

	var b strings.Builder
	model := job.Model
	if model == "" {
		model = "default"
	}
	fmt.Fprintf(&b, "Run %s (%s/%s) stopped before finishing: %s", job.RunID, job.Agent, model, result.FailureKind)
	if line := firstLine(result.Error); line != "" {
		fmt.Fprintf(&b, " (%s)", adapter.Truncate(line, 300))
	}
	b.WriteString(".\n")
	if job.HandoffBranch != "" {
		fmt.Fprintf(&b, "It had itself continued from %s.\n", job.HandoffBranch)
	}
	section := func(title, body string, limit int) {
		if body = strings.TrimSpace(body); body == "" {
			return
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", title, tail(body, limit))
	}
	section("What it was asked", adapter.Truncate(job.Prompt, noteTask), noteTask+1)
	words := strings.Join(trail.texts, "\n\n")
	if words == "" {
		words = result.Summary
	}
	section("Its last messages", words, noteWords)
	stat, _ := git(ctx, wt.Path, "diff", "--stat", "--no-color", committed.Base, changes.Commit)
	section(fmt.Sprintf("Files changed against the base (%s)", short(committed.Base)), stat, noteStat)
	if trail.command != "" {
		command := "$ " + adapter.Truncate(trail.command, 400)
		if trail.output != "" {
			command += "\n" + tail(trail.output, noteCommand-400)
		}
		section("Its last command", command, noteCommand)
	}
	section("What it still meant to do (its own plan)", trail.plan, notePlan)
	return adapter.Truncate(redactNote(b.String()), runnerwire.MaxHandoffNoteBytes)
}

// secretAssignment is an environment-style assignment to a secret-sounding
// name (an `env` dump, a .env file printed): its value goes.
var secretAssignment = regexp.MustCompile(`(?i)\b([A-Z_][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|AUTH)[A-Z0-9_]*)(\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|\S+)`)

// redactNote is redact.String plus secret-looking assignments.
func redactNote(s string) string {
	s = redact.String(s)
	return secretAssignment.ReplaceAllString(s, "${1}${2}"+redact.Placeholder)
}

// tail keeps the last n bytes of s, at a line start when one is near.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut++
	}
	out := s[cut:]
	if i := strings.IndexByte(out, '\n'); i >= 0 && i < 200 {
		out = out[i+1:]
	}
	return "…\n" + out
}

// handoffTrail is what a run's events said that a handoff note needs: the
// agent's last messages, its last shell command and output, and its plan.
type handoffTrail struct {
	texts     []string
	command   string
	commandID string
	output    string
	plan      string
}

const (
	trailTexts    = 3
	trailTextSize = 1024
)

// see takes in one (already redacted) event.
func (t *handoffTrail) see(ev runnerwire.RunEvent) {
	switch ev.Type {
	case runnerwire.EventText:
		text := strings.TrimSpace(ev.Text)
		if text == "" {
			return
		}
		t.texts = append(t.texts, tail(text, trailTextSize))
		if len(t.texts) > trailTexts {
			t.texts = t.texts[len(t.texts)-trailTexts:]
		}
	case runnerwire.EventToolCall:
		name := strings.ToLower(ev.Tool)
		switch {
		case strings.Contains(name, "todo") || name == "update_plan" || name == "plan":
			if plan := planText(ev.Input); plan != "" {
				t.plan = plan
			}
		case strings.Contains(name, "bash") || strings.Contains(name, "shell") ||
			strings.Contains(name, "command") || strings.Contains(name, "terminal"):
			t.command, t.commandID, t.output = shellCommand(ev.Input), ev.ToolID, ""
		}
	case runnerwire.EventToolResult:
		if t.command != "" && (ev.ToolID == t.commandID || t.commandID == "") {
			t.output = tail(ev.Text, noteCommand)
		}
	}
}

// shellCommand is the command in a shell tool's input: {"command": "…"} as
// most CLIs send it, or the input itself.
func shellCommand(input string) string {
	var parsed struct {
		Command json.RawMessage `json:"command"`
		Cmd     json.RawMessage `json:"cmd"`
	}
	if json.Unmarshal([]byte(input), &parsed) == nil {
		for _, raw := range []json.RawMessage{parsed.Command, parsed.Cmd} {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
			var list []string
			if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
				return strings.Join(list, " ")
			}
		}
	}
	return strings.TrimSpace(input)
}

// planText lists the unfinished items of a to-do or plan tool's input
// (Claude Code's TodoWrite, OpenCode's todowrite, Gemini CLI's write_todos:
// {"todos":[{content|description, status}]}; Codex's update_plan:
// {"plan":[{step, status}]}).
func planText(input string) string {
	type item struct {
		Content     string `json:"content"`
		Description string `json:"description"`
		Step        string `json:"step"`
		Status      string `json:"status"`
	}
	var parsed struct {
		Todos []item `json:"todos"`
		Plan  []item `json:"plan"`
	}
	if json.Unmarshal([]byte(input), &parsed) != nil {
		return ""
	}
	var b strings.Builder
	for _, it := range append(parsed.Todos, parsed.Plan...) {
		text := strings.TrimSpace(it.Content + it.Description + it.Step)
		status := strings.ToLower(it.Status)
		if text == "" || status == "completed" || status == "done" || status == "cancelled" {
			continue
		}
		if status == "" {
			status = "pending"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", status, oneLine(text))
	}
	return b.String()
}
