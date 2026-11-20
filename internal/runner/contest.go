package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Contests and coordinated runs (§C.2.5, §C.4):
//
//   - Coordinated runs drop board claim/status tools (`--toolset coordinated`)
//     so concurrent runs on the same node never fight over claims.
//   - Compare jobs (`JobCompare`) run read-only at base, strip repo-local
//     agent config, discard any edits and remove their throwaway worktree.
//   - `open_pr` workspace actions push a contest winner's own branch without
//     force and open a pull request, refusing non-owners, non-owned branches
//     and branches that already have an open PR.

const workspaceOpenPRTimeout = pushTimeout + ghTimeout

// coordinatedDroppedTools are the board claim/status tools withheld from
// coordinated runs (§C.2.5).
var coordinatedDroppedTools = []string{
	"claim_task",
	"release_task",
	"set_node_status",
	"create_subgraph",
}

func validContestLabel(label string) bool {
	switch label {
	case "A", "B", "C", "J":
		return true
	}
	return false
}

// checkCompareJob vets a JobCompare offer; "" means fine.
func checkCompareJob(job *runnerwire.Job) string {
	if job.Mode != runnerwire.ModeHeadless {
		return "a compare job is headless"
	}
	if job.Branch != "" || job.PRURL != "" {
		return "a compare job names no pull request"
	}
	if job.Contest == nil || job.Contest.ID == "" || job.Contest.Label != "J" {
		return "a compare job must belong to a contest reviewer"
	}
	return ""
}

// filterCoordinatedTools removes claim/status/subgraph tools from a role job's
// hub-resolved tool allowlist when the job is coordinated.
func filterCoordinatedTools(tools []string) []string {
	return slices.DeleteFunc(slices.Clone(tools), func(name string) bool {
		return slices.Contains(coordinatedDroppedTools, name)
	})
}

// executeCompare runs a read-only contest reviewer job at base (§C.4.2).
// Nothing it changes is committed or pushed; its worktree and branch are
// removed before finish goes out.
func (j *jobs) executeCompare(ctx context.Context, r *run) {
	job := r.job
	dataDir, err := j.runCfg(r).RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = CreateWorktree(ctx, r.repo.Path, dataDir, job.RunID, branchKey(job.NodeID, job.Slug), job.Base)
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
	unmark := func() {}
	if job.Global {
		if unmark, err = markNoBoard(wt, job.RunID); err != nil {
			_ = removeWorktree(j.ctx, r.repo.Path, wt, true)
			j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
				Error: adapter.Truncate("could not mark the worktree as a global agent's (no board): "+err.Error(),
					runnerwire.MaxSummaryBytes)})
			return
		}
	}
	cleanup := func() {
		unmark()
		if err := removeWorktree(j.ctx, r.repo.Path, wt, true); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	// No branch to report: nothing of a compare run is pushed.
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: wt.Base}
	j.mu.Unlock()
	j.wake()
	j.c.logf("run %s: accepted (compare); worktree %s from %s, thrown away after", job.RunID, wt.Path, wt.BaseRef)
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("compare: worktree %s from %s (read only; thrown away after)", wt.Path, wt.BaseRef)})

	result, text := j.runAgent(ctx, r, wt, job)
	if strings.TrimSpace(text) != "" {
		result.Summary = compareSummary(text, result.Summary)
	}
	if err := checkGitDir(j.ctx, wt); err != nil {
		cleanup()
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			FailureKind: runnerwire.FailureTask, Error: err.Error()})
		return
	}
	if dirty, _ := changedFiles(j.ctx, wt.Path); strings.TrimSpace(dirty) != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "the reviewer changed files; discarded"})
	}
	cleanup()
	j.finish(r, result)
}

// compareSummary fits a contest reviewer's final message into MaxSummaryBytes,
// preserving the trailing fenced JSON block so the hub's ParseScores reads it.
func compareSummary(text, fallback string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = strings.TrimSpace(fallback)
	}
	if len(text) <= runnerwire.MaxSummaryBytes {
		return text
	}
	if start := strings.LastIndex(text, "```"); start > 0 {
		// Look for the opening fence of the last fenced block.
		if open := strings.LastIndex(text[:start], "```"); open >= 0 {
			block := strings.TrimSpace(text[open : start+3])
			const gap = "\n…\n\n"
			if len(block)+len(gap) <= runnerwire.MaxSummaryBytes {
				prefix := strings.TrimSpace(text[:open])
				return adapter.Truncate(prefix, runnerwire.MaxSummaryBytes-len(block)-len(gap)) + gap + block
			}
		}
	}
	return adapter.Truncate(text, runnerwire.MaxSummaryBytes)
}

// openPRForWorkspace handles WorkspaceOpenPR (§C.4.4): pushes the winner's own
// branch without force, opens its pull request, starts watching it and returns
// its URL in WorkspaceOutcome.PRURL. It refuses non-owned branches, existing
// PRs, foreign worktrees and non-fast-forward pushes.
func (j *jobs) openPRForWorkspace(ctx context.Context, job runnerwire.WorkspaceActionJob, repo, dataDir string) runnerwire.WorkspaceOutcome {
	root := job.RunID
	if job.WorkRoot != "" {
		root = job.WorkRoot
	}
	if !ownBranch(job) || checkBase(job.Branch) != nil {
		return refusedWith(runnerwire.WorkspaceErrNotOwnBranch, "%s is not the branch run %s made", job.Branch, job.RunID)
	}
	if w := j.openWatchForJob(job); w != nil {
		return refusedWith(runnerwire.WorkspaceErrHasPR, "the run already has an open pull request (%s)", w.URL)
	}
	if entry, ok, _ := lookupWork(dataDir, root); ok && entry.PRURL != "" {
		return refusedWith(runnerwire.WorkspaceErrHasPR, "the run already has a pull request (%s)", entry.PRURL)
	}
	if job.Base == "" || checkBase(job.Base) != nil ||
		(!refExists(ctx, repo, "refs/heads/"+job.Base) && !refExists(ctx, repo, "refs/remotes/origin/"+job.Base)) {
		return refusedWith(runnerwire.WorkspaceErrNoBase, "there is no branch %q", job.Base)
	}
	tip, err := revParse(ctx, repo, "refs/heads/"+job.Branch)
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrNoBranch, "the run's branch %s is gone", job.Branch)
	}
	if job.HeadSHA != "" && !strings.EqualFold(tip, job.HeadSHA) {
		return refusedWith(runnerwire.WorkspaceErrHeadMoved, "%s is at %s, not %s", job.Branch, short(tip), short(job.HeadSHA))
	}

	wt, err := findRunWorktreeDirect(ctx, dataDir, repo, root)
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrForeign, "%v", err)
	}
	if wt.Exists {
		if !wt.Listed || !wt.GitFile || wt.Branch != job.Branch {
			return refusedWith(runnerwire.WorkspaceErrForeign, "the run's worktree is not on %s as the run left it", job.Branch)
		}
		if !strings.EqualFold(wt.Head, tip) {
			return refusedWith(runnerwire.WorkspaceErrHeadMoved, "the run's worktree is at %s, not %s", short(wt.Head), short(tip))
		}
	}

	workDir := repo
	wtPath := ""
	if wt.Exists {
		workDir = wt.Path
		wtPath = wt.Path
	}
	if _, err := git(ctx, workDir, "remote", "get-url", "origin"); err != nil {
		return refusedWith(runnerwire.WorkspaceErrGit, "the repository has no origin remote to push to")
	}
	gh, err := j.c.gh()
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrGit, "%v", err)
	}
	if existing := existingOpenPR(ctx, gh, workDir, job.Branch); existing != "" {
		return refusedWith(runnerwire.WorkspaceErrHasPR, "an open pull request already exists for %s (%s)", job.Branch, existing)
	}

	rawMsg, _ := git(ctx, repo, "log", "-1", "--format=%B", tip)
	subject, summary := splitCommitMessage(rawMsg)
	cfg := j.c.cfg()
	synJob := runnerwire.Job{
		RunID:     job.RunID,
		NodeID:    job.NodeID,
		NodeTitle: subject,
		Global:    job.NodeID == "",
		Slug:      job.Slug,
		Kind:      job.Kind,
		Base:      job.Base,
		Branch:    job.Branch,
	}
	synRun := &run{
		job:  synJob,
		repo: runnerwire.Repo{ID: job.RepoID, Path: repo},
		cfg:  cfg,
	}
	synResult := runnerwire.RunResult{
		Status:  runnerwire.RunSucceeded,
		Summary: summary,
	}

	var pr runnerwire.PRInfo
	if wt.Exists {
		pr = j.openPR(ctx, synRun, Worktree{Path: wt.Path, Branch: job.Branch, Base: job.Base}, synResult, rawMsg)
	} else {
		pr = j.pushAndOpenPRFromRepo(ctx, gh, synRun, repo, job.Branch, job.Base, synResult, rawMsg)
	}
	if pr.Error != "" {
		return refusedWith(runnerwire.WorkspaceErrGit, "%s", pr.Error)
	}
	if pr.URL == "" {
		return refusedWith(runnerwire.WorkspaceErrGit, "pull request was created but returned no URL")
	}

	j.watch(watchedPR{
		RunID:    root,
		NodeID:   job.NodeID,
		URL:      pr.URL,
		Worktree: wtPath,
		Repo:     repo,
		Branch:   job.Branch,
		PR:       pr,
	})
	if entry, ok, _ := lookupWork(dataDir, root); ok {
		entry.PRURL = pr.URL
		entry.PushedHead = tip
		_ = registerWork(dataDir, entry)
	}
	return runnerwire.WorkspaceOutcome{PRURL: pr.URL}
}

// pushAndOpenPRFromRepo is openPR's non-force push + PR creation when the run's
// worktree was already removed and only its local branch remains in repo.
func (j *jobs) pushAndOpenPRFromRepo(ctx context.Context, gh *ghCLI, r *run, repo, branch, base string, result runnerwire.RunResult, message string) runnerwire.PRInfo {
	fail := func(format string, args ...any) runnerwire.PRInfo {
		return runnerwire.PRInfo{Error: truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)}
	}
	pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
	_, err := git(pushCtx, repo, "push", "--no-verify", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	cancel()
	if err != nil {
		return fail("push to origin refused: %v", err)
	}
	cfg := j.runCfg(r)
	req := prRequest{
		Dir:    repo,
		Base:   base,
		Branch: branch,
		Title:  prTitle(r.job, message),
		Body:   redact.Output(prBody(prInfo{job: r.job, result: result, runnerName: j.runnerName(), hub: cfg.Hub})),
		Draft:  prDraft(cfg, r.job, result),
		Labels: cfg.PRLabelsFor(r.job.Agent, r.job.Model),
	}
	prURL, err := gh.create(ctx, req, func(format string, args ...any) {
		j.c.logf("run %s: %s", r.job.RunID, fmt.Sprintf(format, args...))
	})
	if err != nil {
		return fail("%v", err)
	}
	info, err := gh.view(ctx, repo, prURL)
	if err != nil {
		j.c.logf("run %s: %v", r.job.RunID, err)
		info = runnerwire.PRInfo{URL: prURL, Number: prNumber(prURL), State: runnerwire.PROpen, Draft: req.Draft}
	}
	return info
}

// existingOpenPR asks GitHub whether branch already has an open pull request.
// Returns its URL when one exists, or "" otherwise.
func existingOpenPR(ctx context.Context, gh *ghCLI, dir, branch string) string {
	out, err := gh.call(ctx, dir, "", "pr", "list", "--head", branch, "--state", "open", "--json", "url")
	if err != nil {
		return ""
	}
	var prs []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &prs); err != nil || len(prs) == 0 {
		return ""
	}
	return strings.TrimSpace(prs[0].URL)
}

// splitCommitMessage separates a commit message into its subject line and its
// body (with Nodevas trailers stripped), for building a PR opened later.
func splitCommitMessage(msg string) (subject, body string) {
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\r\n", "\n"))
	if msg == "" {
		return "", ""
	}
	head, rest, ok := strings.Cut(msg, "\n")
	subject = strings.TrimSpace(head)
	if !ok {
		return subject, ""
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(rest), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Nodevas-Node:") || strings.HasPrefix(trimmed, "Nodevas-Run:") {
			continue
		}
		lines = append(lines, line)
	}
	return subject, strings.TrimSpace(strings.Join(lines, "\n"))
}
