package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nodevas/internal/redact"
	"nodevas/internal/runnerwire"
)

// publish is what happens to a run's work once its agent exits, for a run
// that succeeded or failed (a cancelled one is left as it is):
//
//  1. Everything in the worktree is committed, with the agent's summary as the
//     message and Nodevas-Node / Nodevas-Run trailers.
//  2. What the run changed goes to the hub (changes), even when it is nothing.
//  3. With autoPR on, the branch is pushed and a pull request opened; the hub
//     hears how that went (pr), success or not, and the PR is watched.
//
// Worktrees: one whose run changed nothing is removed at once, with its
// branch. One with a PR stays until the PR is merged or closed. Every other
// one stays for the owner to look at: a cancelled run, a commit git
// refused, a push or PR that failed, autoPR off.
func (j *jobs) publish(r *run, wt Worktree, result runnerwire.RunResult) (*runnerwire.RunChanges, *runnerwire.PRInfo) {
	ctx := j.ctx // a cancel from the hub no longer applies: the agent is done
	job := r.job
	cfg := j.c.Config

	message := commitMessage(result.Summary, job)
	committed := commitRun(ctx, wt, message)
	if committed.Err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not commit the changes: " + committed.Err.Error()})
	}
	changes, err := describeChanges(ctx, wt.Path, committed, committed.Base)
	if err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not describe the changes: " + err.Error()})
	}
	fitChanges(job.RunID, &changes)

	switch {
	case !committed.Changed && committed.Err == nil:
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no changes; worktree removed"})
		if err := removeWorktree(ctx, r.repo.Path, wt, true); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
		return &changes, nil
	case changes.Commit != "":
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("committed %s on %s: %d files, +%d −%d",
			short(changes.Commit), wt.Branch, len(changes.Files), changes.Additions, changes.Deletions)})
	}
	if !cfg.AutoPROn() {
		if changes.Commit != "" {
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
				Text: fmt.Sprintf("autoPR is off: branch %s stays in %s", wt.Branch, wt.Path)})
		}
		return &changes, nil
	}
	var pr runnerwire.PRInfo
	if changes.Commit == "" {
		pr.Error = "not committed, so not pushed: " + errText(committed.Err)
	} else {
		pr = j.openPR(ctx, r, wt, result, message)
	}
	if pr.Error != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no pull request: " + pr.Error})
		j.c.logf("run %s: no pull request: %s; worktree kept at %s", job.RunID, firstLine(pr.Error), wt.Path)
	} else {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "opened " + pr.URL})
		j.c.logf("run %s: opened %s", job.RunID, pr.URL)
		j.watch(watchedPR{RunID: job.RunID, NodeID: job.NodeID, URL: pr.URL, Worktree: wt.Path, Repo: r.repo.Path,
			Branch: wt.Branch, PR: pr})
	}
	return &changes, &pr
}

// openPR pushes the run's branch and opens its pull request. Whatever goes
// wrong ends up in PRInfo.Error, for the hub to show on the run.
func (j *jobs) openPR(ctx context.Context, r *run, wt Worktree, result runnerwire.RunResult, message string) runnerwire.PRInfo {
	job := r.job
	cfg := j.c.Config
	fail := func(format string, args ...any) runnerwire.PRInfo {
		return runnerwire.PRInfo{Error: truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)}
	}
	if _, err := git(ctx, wt.Path, "remote", "get-url", "origin"); err != nil {
		return fail("the repository has no origin remote to push to")
	}
	gh, err := j.c.gh()
	if err != nil {
		return fail("%v", err)
	}
	pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
	_, err = git(pushCtx, wt.Path, "push", "--no-verify", "--set-upstream", "origin", "HEAD:refs/heads/"+wt.Branch)
	cancel()
	if err != nil {
		return fail("push to origin refused: %v", err)
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "pushed " + wt.Branch + " to origin"})

	req := prRequest{
		Dir:    wt.Path,
		Base:   wt.Base,
		Branch: wt.Branch,
		Title:  prTitle(job, message),
		Body:   redact.String(prBody(prInfo{job: job, result: result, runnerName: j.runnerName(), hub: cfg.Hub})),
		// A failed run's PR is never ready for review, nor a Tester's: its
		// tests fail by design until the Executor's work lands.
		Draft:  prDraft(cfg, job, result),
		Labels: cfg.PRLabelsFor(job.Agent, job.Model),
	}
	prURL, err := gh.create(ctx, req, func(format string, args ...any) {
		text := fmt.Sprintf(format, args...)
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
		j.c.logf("run %s: %s", job.RunID, text)
	})
	if err != nil {
		return fail("%v", err)
	}
	info, err := gh.view(ctx, wt.Path, prURL)
	if err != nil {
		// Opened, just not read back yet; the watcher fills in the rest.
		j.c.logf("run %s: %v", job.RunID, err)
		info = runnerwire.PRInfo{URL: prURL, Number: prNumber(prURL), State: runnerwire.PROpen, Draft: req.Draft}
	}
	return info
}

// runnerName is how a PR names this runner: its own label, else the hub's
// name for it, else the machine's.
func (j *jobs) runnerName() string {
	if name := j.c.Config.Name; name != "" {
		return name
	}
	j.mu.Lock()
	name := j.hubName
	j.mu.Unlock()
	if name != "" {
		return name
	}
	host, _ := os.Hostname()
	return host
}

func (j *jobs) setHubName(name string) {
	j.mu.Lock()
	j.hubName = name
	j.mu.Unlock()
}

// maxFilePath is the longest path the hub takes in a changes message.
const maxFilePath = 1024

// fitChanges makes a changes message one the hub takes: paths valid UTF-8 and
// at most maxFilePath bytes, and the whole message under
// MaxChangesMessageBytes, dropping the patch first (it is the big part), then
// files from the end of the list.
func fitChanges(runID string, changes *runnerwire.RunChanges) {
	files := changes.Files[:0]
	for _, f := range changes.Files {
		f.Path = hubPath(f.Path)
		f.OldPath = hubPath(f.OldPath)
		if f.Path != "" {
			files = append(files, f)
		}
	}
	changes.Files = files
	fits := func() bool {
		raw, err := json.Marshal(runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: runID, Changes: changes})
		return err == nil && len(raw) <= runnerwire.MaxChangesMessageBytes-4<<10
	}
	if fits() {
		return
	}
	if changes.Patch != "" {
		changes.Patch, changes.PatchTruncated = "", true
	}
	for !fits() && len(changes.Files) > 0 {
		changes.Files = changes.Files[:len(changes.Files)/2]
	}
}

// hubPath is a path as the hub stores it: valid UTF-8, no NUL, at most
// maxFilePath bytes.
func hubPath(p string) string {
	p = strings.ReplaceAll(strings.ToValidUTF8(p, "�"), "\x00", "")
	return truncateUTF8(p, maxFilePath)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
