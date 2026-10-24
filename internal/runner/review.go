package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Review and revise jobs (W5): the two halves of a pipeline's later rounds,
// both about a pull request that already exists.
//
// Review: the PR's head is checked out detached in its own worktree, the
// agent gets the PR's diff in its prompt and is told to change nothing, and
// its final message is posted on the PR as a comment (GitHub refuses an
// approve or request-changes review from the PR's own author, which these
// PRs are). The verdict is its last "VERDICT: approve|changes" line. Nothing
// is committed or pushed and the worktree is removed, whatever the agent did.
//
// Revise: the PR's branch is checked out detached (the implement run's own
// worktree may still hold the branch itself, while its PR is watched), the
// agent addresses the review, and its commit is pushed to the branch as a
// fast-forward: the open PR picks it up, and no new PR is opened. The revise
// run watches the PR too, so a merge approval naming it works; its worktree
// is removed once pushed, since the commit lives on the branch.

const (
	// maxReviewDiff caps the diff a review prompt carries.
	maxReviewDiff = 200 << 10
	// maxReviewComment caps the PR comment (GitHub's limit is 65536 chars).
	maxReviewComment = 64000
	// revisePrefix is the only kind of branch a revise pushes to: the ones
	// this runner's own runs create (see BranchName).
	revisePrefix = "agent/"
	// prCheckFields is what is read about a PR before a review or revise.
	prCheckFields = "number,state,headRefName,baseRefName,isCrossRepository,headRefOid"
)

// prURLExact is a whole PR URL as an offer may name it.
var prURLExact = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:\d+)?(/[A-Za-z0-9._~-]+){2,}/pull/\d{1,12}$`)

// checkKind vets an offer's W5 fields; "" means fine.
func checkKind(job *runnerwire.Job, cfg *Config) string {
	switch job.Kind {
	case runnerwire.JobImplement:
		return ""
	case runnerwire.JobPlan:
		// A plan names no pull request and pushes nothing.
		if job.Branch != "" || job.PRURL != "" || job.Review != "" {
			return "a plan job names no pull request"
		}
		return ""
	case runnerwire.JobReview, runnerwire.JobRevise:
	default:
		return fmt.Sprintf("job kind %q is not one this runner knows", job.Kind)
	}
	if job.Branch == "" || checkBase(job.Branch) != nil {
		return fmt.Sprintf("pull request branch %q is not a plain branch name", job.Branch)
	}
	if len(job.PRURL) > 512 || !prURLExact.MatchString(job.PRURL) {
		return fmt.Sprintf("%q is not a pull request URL", job.PRURL)
	}
	if job.Kind == runnerwire.JobReview {
		return ""
	}
	if strings.TrimSpace(job.Review) == "" {
		return "a revise job needs the review to address"
	}
	if len(job.Review) > runnerwire.MaxPromptBytes {
		return "review is too long"
	}
	if !strings.HasPrefix(job.Branch, revisePrefix) {
		return fmt.Sprintf("this runner pushes revisions only to its own %s* branches, not %s", revisePrefix, job.Branch)
	}
	if !cfg.AutoPROn() {
		return "autoPR is off on this runner, so it does not push revisions"
	}
	return ""
}

// prCheck is what checkPR learned about the job's pull request.
type prCheck struct {
	// Base is the branch the PR merges into.
	Base string
	// Head is the PR's head commit when it was checked.
	Head string
}

// ghPRCheck is `gh pr view --json prCheckFields`.
type ghPRCheck struct {
	Number            int    `json:"number"`
	State             string `json:"state"`
	HeadRefName       string `json:"headRefName"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	HeadRefOid        string `json:"headRefOid"`
}

// checkPR makes sure a review or revise is about a PR of this repository:
// its URL names the repository origin points at, and gh says it is open,
// from a branch of that repository, the branch the job names. reason is ""
// when it is, else why the offer is declined.
func (j *jobs) checkPR(ctx context.Context, r *run) (prCheck, string) {
	job := r.job
	raw, err := git(ctx, r.repo.Path, "config", "--get", "remote.origin.url")
	if err != nil || strings.TrimSpace(raw) == "" {
		return prCheck{}, "the repository has no origin remote"
	}
	origin, prRepo := repoKey(strings.TrimSpace(raw)), prRepoKey(job.PRURL)
	if origin == "" || prRepo == "" || origin != prRepo {
		return prCheck{}, fmt.Sprintf("pull request %s is not in this repository (origin is %s)", job.PRURL,
			orDash(origin))
	}
	gh, err := j.c.gh()
	if err != nil {
		return prCheck{}, err.Error()
	}
	out, err := gh.call(ctx, r.repo.Path, "", "pr", "view", job.PRURL, "--json", prCheckFields)
	if err != nil {
		return prCheck{}, ghError("gh pr view", err).Error()
	}
	var v ghPRCheck
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return prCheck{}, "gh pr view printed something other than JSON"
	}
	switch {
	case !strings.EqualFold(v.State, "OPEN"):
		return prCheck{}, fmt.Sprintf("pull request %s is %s, not open", job.PRURL, strings.ToLower(orDash(v.State)))
	case v.IsCrossRepository:
		return prCheck{}, fmt.Sprintf("pull request %s comes from a fork", job.PRURL)
	case v.HeadRefName != job.Branch:
		return prCheck{}, fmt.Sprintf("pull request %s is on branch %s, not %s", job.PRURL, orDash(v.HeadRefName), job.Branch)
	case checkBase(v.BaseRefName) != nil:
		return prCheck{}, fmt.Sprintf("pull request %s merges into %q, which is not a plain branch name", job.PRURL,
			v.BaseRefName)
	}
	return prCheck{Base: v.BaseRefName, Head: v.HeadRefOid}, ""
}

// repoKey is a remote URL as host/owner/repo, lowercase: https, ssh:// and
// scp-like (git@host:owner/repo.git) forms alike. "" when it is none of them.
func repoKey(remote string) string {
	var host, path string
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if at, rest, ok := strings.Cut(remote, ":"); ok && !strings.Contains(at, "/") && len(at) > 1 {
		// scp-like: [user@]host:path. (One letter is a Windows drive: C:/x.)
		if i := strings.LastIndexByte(at, '@'); i >= 0 {
			at = at[i+1:]
		}
		host, path = at, rest
	} else {
		return ""
	}
	host = strings.ToLower(host)
	if host == "ssh.github.com" {
		host = "github.com" // GitHub's ssh-over-443 host
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || strings.Count(path, "/") < 1 {
		return ""
	}
	return host + "/" + strings.ToLower(path)
}

// prRepoKey is the repository a PR URL belongs to, as repoKey writes it.
func prRepoKey(prURL string) string {
	u, err := url.Parse(prURL)
	if err != nil || u.Host == "" {
		return ""
	}
	path, _, ok := strings.Cut(strings.Trim(u.Path, "/"), "/pull/")
	if !ok {
		return ""
	}
	return repoKey("https://" + u.Host + "/" + path)
}

// fetchBranch brings origin's branch into refs/remotes/origin/<branch> and
// returns that ref.
func fetchBranch(ctx context.Context, repoPath, branch string) (string, error) {
	ref := "refs/remotes/origin/" + branch
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if _, err := git(fetchCtx, repoPath, "fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	return ref, nil
}

// createPRWorktree checks out origin's <branch> detached in
// <dataDir>/worktrees/<runID>. Unlike CreateWorktree it never falls back to
// a local branch: a review or revise must see the PR as it is now.
func createPRWorktree(ctx context.Context, repoPath, dataDir, runID, branch, base string) (Worktree, error) {
	if !validRunID.MatchString(runID) {
		return Worktree{}, fmt.Errorf("run id %q is not usable as a directory name", runID)
	}
	if err := checkBase(branch); err != nil {
		return Worktree{}, err
	}
	ref, err := fetchBranch(ctx, repoPath, branch)
	if err != nil {
		return Worktree{}, fmt.Errorf("could not fetch %s from origin: %w", branch, err)
	}
	wt := Worktree{Path: filepath.Join(dataDir, "worktrees", runID), Branch: branch, Base: base, BaseRef: ref}
	if err := os.MkdirAll(filepath.Dir(wt.Path), 0o700); err != nil {
		return Worktree{}, err
	}
	if _, err := git(ctx, repoPath, "worktree", "add", "--quiet", "--detach", "--", wt.Path, ref); err != nil {
		return Worktree{}, err
	}
	if err := recordGitDir(ctx, &wt); err != nil {
		return Worktree{}, err
	}
	return wt, nil
}

// startPRRun makes a review's or revise's worktree and reports the accept.
// ok is false when the run already finished (failed or cancelled).
func (j *jobs) startPRRun(ctx context.Context, r *run, pr prCheck) (Worktree, bool) {
	job := r.job
	dataDir, err := j.c.Config.RunDataDir()
	var wt Worktree
	if err == nil {
		wt, err = createPRWorktree(ctx, r.repo.Path, dataDir, job.RunID, job.Branch, pr.Base)
	}
	if err != nil {
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return wt, false
		}
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate("could not create the worktree: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return wt, false
	}
	j.mu.Lock()
	r.accepted = true
	r.started = time.Now()
	r.accept = &runnerwire.Accept{RunID: job.RunID, Base: pr.Base, Branch: job.Branch}
	j.mu.Unlock()
	j.wake()
	j.c.logf("run %s: accepted (%s of %s); worktree %s at origin/%s", job.RunID, job.Kind, job.PRURL, wt.Path, job.Branch)
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: fmt.Sprintf("%s of %s: worktree %s at origin/%s (detached)", job.Kind, job.PRURL, wt.Path, job.Branch)})
	return wt, true
}

// executeReview is a review job after its accept.
func (j *jobs) executeReview(ctx context.Context, r *run, pr prCheck) {
	job := r.job
	wt, ok := j.startPRRun(ctx, r, pr)
	if !ok {
		return
	}
	// Whatever happens, nothing of a review stays: not a file, not the
	// worktree. (Detached: there is no branch of its own to drop.)
	defer func() {
		if err := removeWorktree(j.ctx, r.repo.Path, wt, false); err != nil {
			j.c.logf("run %s: removing its worktree: %v", job.RunID, err)
		}
	}()

	diff, err := reviewDiff(ctx, r.repo.Path, wt, pr.Base)
	if err != nil {
		if ctx.Err() != nil {
			j.finish(r, cancelled(ctx))
			return
		}
		j.finish(r, runnerwire.RunResult{Status: runnerwire.RunFailed, ExitCode: -1,
			Error: adapter.Truncate("could not read the pull request's diff: "+err.Error(), runnerwire.MaxSummaryBytes)})
		return
	}
	reviewJob := job
	reviewJob.Prompt = strings.TrimRight(job.Prompt, "\n") + "\n\n" + diff
	result, text := j.runAgent(ctx, r, wt, reviewJob)
	if result.Status == runnerwire.RunCancelled {
		j.finish(r, result)
		return
	}
	if !j.guardGitDir(r, wt) {
		return
	}
	j.mu.Lock()
	r.publishing = true
	j.mu.Unlock()

	if dirty, _ := changedFiles(j.ctx, wt.Path); strings.TrimSpace(dirty) != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "the reviewer changed files; discarded"})
	}
	if result.Status != runnerwire.RunSucceeded {
		j.finish(r, result)
		return
	}
	verdict := parseVerdict(text)
	if strings.TrimSpace(text) == "" {
		text = result.Summary
	}
	if strings.TrimSpace(text) != "" {
		if err := j.postReview(r, text, verdict); err != nil {
			// The verdict stands without the comment; the hub has the summary.
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not post the review: " + err.Error()})
			j.c.logf("run %s: could not post the review on %s: %v", job.RunID, job.PRURL, err)
		} else {
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "posted the review on " + job.PRURL})
		}
	}
	result.Summary = reviewSummary(text, verdict)
	if verdict == "" {
		result.Status = runnerwire.RunFailed
		result.FailureKind = runnerwire.FailureTask
		result.Error = `review gave no verdict (no final "VERDICT: approve" or "VERDICT: changes" line)`
	} else {
		result.Verdict = verdict
	}
	j.finish(r, result)
}

// reviewDiff is the PR's diff against its base, as the review prompt
// carries it: a stat, then the diff, cut at maxReviewDiff.
func reviewDiff(ctx context.Context, repoPath string, wt Worktree, base string) (string, error) {
	baseRef, err := fetchBranch(ctx, repoPath, base)
	if err != nil {
		// Offline after the PR fetch worked: an older copy of the base still
		// gives the right merge base unless the base moved a lot.
		baseRef = "refs/remotes/origin/" + base
		if !refExists(ctx, repoPath, baseRef) {
			return "", err
		}
	}
	span := baseRef + "...HEAD"
	stat, err := git(ctx, wt.Path, "diff", "--no-color", "--no-ext-diff", "-M", "--stat", span)
	if err != nil {
		return "", err
	}
	diffCtx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	patch, over, err := gitCapped(diffCtx, wt.Path, maxReviewDiff, "diff", "--no-color", "--no-ext-diff", "-M", span)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The pull request's changes (git diff origin/%s...HEAD):\n\n%s\n", base, strings.TrimRight(stat, "\n"))
	b.WriteString("\n----- BEGIN DIFF -----\n")
	b.WriteString(strings.ToValidUTF8(patch, "�"))
	if over {
		fmt.Fprintf(&b, "\n----- DIFF TRUNCATED at %d KB -----\n", maxReviewDiff>>10)
		fmt.Fprintf(&b, "(The diff is longer than that. Run `git diff origin/%s...HEAD -- <path>` for the files "+
			"cut off, if your tools allow it.)\n", base)
	} else {
		b.WriteString("\n----- END DIFF -----\n")
	}
	return b.String(), nil
}

// verdictLine is a "VERDICT: approve|changes" line, allowing the markdown an
// agent tends to wrap it in, and nothing else.
var verdictLine = regexp.MustCompile("(?i)^[\\s*_`>#-]*verdict[\\s*_`]*[:：][\\s*_`]*(approve|changes)[\\s*_`.]*$")

// parseVerdict is the verdict on the final message's last non-empty line, ""
// when that line is not a verdict. Only that line counts: a verdict quoted
// from the diff or the repository, earlier in the message, never does.
func parseVerdict(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if m := verdictLine.FindStringSubmatch(lines[i]); m != nil {
			return strings.ToLower(m[1])
		}
		return ""
	}
	return ""
}

// reviewSummary is the review as RunResult.Summary carries it: cut to its
// cap, but keeping the verdict line at the end where the cut would lose it.
func reviewSummary(text, verdict string) string {
	text = strings.TrimSpace(text)
	if len(text) <= runnerwire.MaxSummaryBytes || verdict == "" {
		return adapter.Truncate(text, runnerwire.MaxSummaryBytes)
	}
	tail := "\n…\nVERDICT: " + verdict
	return adapter.Truncate(text, runnerwire.MaxSummaryBytes-len(tail)) + tail
}

// postReview comments the review on the PR, with the owner's gh login.
func (j *jobs) postReview(r *run, text, verdict string) error {
	job := r.job
	gh, err := j.c.gh()
	if err != nil {
		return err
	}
	model := job.Model
	if model == "" {
		model = "default"
	}
	shown := verdict
	if shown == "" {
		shown = "none given"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Nodevas review** (%s/%s) — verdict: %s\n\n", job.Agent, model, shown)
	b.WriteString(strings.TrimSpace(text))
	fmt.Fprintf(&b, "\n\n---\n\nNodevas-Node: %s\nNodevas-Run: %s\n", oneLine(job.NodeID), oneLine(job.RunID))
	body := truncateUTF8(redact.String(b.String()), maxReviewComment)
	if _, err := gh.call(j.ctx, r.repo.Path, body, "pr", "comment", job.PRURL, "--body-file", "-"); err != nil {
		return ghError("gh pr comment", err)
	}
	return nil
}

// executeRevise is a revise job after its accept.
func (j *jobs) executeRevise(ctx context.Context, r *run, pr prCheck) {
	wt, ok := j.startPRRun(ctx, r, pr)
	if !ok {
		return
	}
	result, _ := j.runAgent(ctx, r, wt, r.job)
	if result.Status == runnerwire.RunCancelled {
		// As for an implement run: nothing pushed, the worktree kept.
		j.finish(r, result)
		return
	}
	if !j.guardGitDir(r, wt) {
		return
	}
	j.mu.Lock()
	r.publishing = true
	j.mu.Unlock()
	changes, info, result := j.publishRevise(r, wt, result)
	j.finishWith(r, result, changes, info)
}

// publishRevise commits a revise run's work and pushes it to the PR's branch,
// as a fast-forward only. A failed agent's work is committed but not pushed:
// the PR under review keeps its last good head, and the worktree stays.
func (j *jobs) publishRevise(r *run, wt Worktree, result runnerwire.RunResult) (*runnerwire.RunChanges, *runnerwire.PRInfo, runnerwire.RunResult) {
	ctx := j.ctx
	job := r.job
	titled := job
	if title := oneLine(job.NodeTitle); title != "" {
		titled.NodeTitle = "Address review: " + title
	}
	committed := commitRun(ctx, wt, commitMessage(result.Summary, titled))
	if committed.Err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not commit the changes: " + committed.Err.Error()})
	}
	// Measured from the PR head the run started at (wt.BaseRef was fetched
	// then): what this revise added, not the whole PR.
	changes, err := describeChanges(ctx, wt.Path, committed, committed.Base)
	if err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not describe the changes: " + err.Error()})
	}
	fitChanges(job.RunID, &changes)
	fail := func(format string, args ...any) (*runnerwire.RunChanges, *runnerwire.PRInfo, runnerwire.RunResult) {
		result.Status = runnerwire.RunFailed
		result.FailureKind = runnerwire.FailureTask
		result.Error = truncate(fmt.Sprintf(format, args...), runnerwire.MaxSummaryBytes)
		j.c.logf("run %s: %s; worktree kept at %s", job.RunID, firstLine(result.Error), wt.Path)
		return &changes, nil, result
	}

	switch {
	case !committed.Changed && committed.Err == nil:
		if result.Status != runnerwire.RunSucceeded {
			// Nothing to keep; the PR stays as it was.
			j.removePRWorktree(r, wt)
			return &changes, nil, result
		}
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no changes; nothing pushed; worktree removed"})
		j.removePRWorktree(r, wt)
		info := j.attachPR(r, wt, "")
		return &changes, &info, result
	case changes.Commit == "":
		return fail("not committed, so not pushed: %s", errText(committed.Err))
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("committed %s on top of %s: %d files, +%d −%d",
		short(changes.Commit), short(committed.Base), len(changes.Files), changes.Additions, changes.Deletions)})
	if result.Status != runnerwire.RunSucceeded {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: fmt.Sprintf("the run failed: not pushed to %s; worktree kept at %s", wt.Branch, wt.Path)})
		return &changes, nil, result
	}
	pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
	// No + and no --force: a branch that moved on origin since the run
	// started is refused, never overwritten.
	_, err = git(pushCtx, wt.Path, "push", "--no-verify", "origin", "HEAD:refs/heads/"+wt.Branch)
	cancel()
	if err != nil {
		if pushRejected(err) {
			return fail("push to %s refused: the branch moved on origin since this run started (not a fast-forward): %v",
				wt.Branch, err)
		}
		return fail("push to origin refused: %v", err)
	}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("pushed %s to %s", short(changes.Commit), wt.Branch)})
	j.c.logf("run %s: pushed %s to %s (%s)", job.RunID, short(changes.Commit), wt.Branch, job.PRURL)
	j.removePRWorktree(r, wt)
	info := j.attachPR(r, wt, changes.Commit)
	return &changes, &info, result
}

func pushRejected(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first") ||
		strings.Contains(msg, "[rejected]") || strings.Contains(msg, "stale info")
}

func (j *jobs) removePRWorktree(r *run, wt Worktree) {
	// Detached: the branch is the PR's, and stays.
	if err := removeWorktree(j.ctx, r.repo.Path, wt, false); err != nil {
		j.c.logf("run %s: removing its worktree: %v", r.job.RunID, err)
	}
}

// attachPR reads the revised PR back and watches it under this run as well,
// so the hub sees its new head and CI on this run and can approve its merge
// by this run. head is the commit just pushed, for when gh cannot be read.
func (j *jobs) attachPR(r *run, wt Worktree, head string) runnerwire.PRInfo {
	job := r.job
	info := runnerwire.PRInfo{URL: job.PRURL, Number: prNumber(job.PRURL), State: runnerwire.PROpen, HeadSHA: head}
	gh, err := j.c.gh()
	if err == nil {
		var viewed runnerwire.PRInfo
		if viewed, err = gh.view(j.ctx, r.repo.Path, job.PRURL); err == nil {
			info = viewed
		}
	}
	if err != nil {
		j.c.logf("run %s: %v", job.RunID, err)
	}
	// No worktree: it is gone already. The implement run's watch, if there
	// is one, still owns its own.
	j.watch(watchedPR{RunID: job.RunID, NodeID: job.NodeID, URL: job.PRURL, Repo: r.repo.Path, Branch: wt.Branch, PR: info})
	return info
}
