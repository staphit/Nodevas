package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Remote merge (W4): someone approves a run's diff on the hub and the hub
// asks this runner to merge its pull request. The runner decides, from its
// own settings and a fresh look at the PR, never from the request:
//
//   - remoteMerge must be on (the default);
//   - the PR must be one this runner opened for that run and still watches;
//   - gh must say it is open, not a draft, at exactly the head the approver
//     saw, with green CI and nothing GitHub says blocks it.
//
// Then `gh pr merge --<mergeMethod> --match-head-commit <head>`, never with
// --admin or --auto. Each request is answered once with merge_result; the
// answer is kept a day in <dataDir>/merges.json, so a request the hub sends
// again after a reconnect (or a runner restart) gets the same answer and
// never a second merge.

// mergeRecord is one answered merge request.
type mergeRecord struct {
	ID      string    `json:"id"`
	RunID   string    `json:"runId"`
	HeadSHA string    `json:"headSha"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

type mergesFile struct {
	Merges []mergeRecord `json:"merges"`
}

const (
	mergesFileName = "merges.json"
	// mergeMemory is how long an answer is kept for re-sent requests: far
	// longer than the hub keeps a request (MergeRequestTTLSeconds).
	mergeMemory = 24 * time.Hour
	// maxMergeRecords bounds merges.json whatever the clock does.
	maxMergeRecords = 1000
	// maxMergeError is how much of gh's complaint goes to the hub.
	maxMergeError = 1024
	// maxMergeIDLen refuses ids no hub would send.
	maxMergeIDLen = 128
	// prMergeFields is what is read about a PR right before merging it.
	prMergeFields = prViewFields + ",mergeable,mergeStateStatus"
)

// ErrRemoteMergeOff is the answer to every approval while remoteMerge is off.
const ErrRemoteMergeOff = "this runner does not accept remote merges"

// loadMerges reads merges.json, dropping answers older than a day.
func (j *jobs) loadMerges() {
	dir, err := j.c.Config.RunDataDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, mergesFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var file mergesFile
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		j.c.logf("merges: cannot read %s: %v", path, err)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, rec := range file.Merges {
		if rec.ID != "" {
			j.merges[rec.ID] = rec
		}
	}
	j.pruneMergesLocked(time.Now())
}

// pruneMergesLocked forgets answers older than mergeMemory, and the oldest
// beyond maxMergeRecords.
func (j *jobs) pruneMergesLocked(now time.Time) {
	for id, rec := range j.merges {
		if now.Sub(rec.At) > mergeMemory {
			delete(j.merges, id)
		}
	}
	if over := len(j.merges) - maxMergeRecords; over > 0 {
		for _, rec := range j.sortedMergesLocked()[:over] {
			delete(j.merges, rec.ID)
		}
	}
}

func (j *jobs) sortedMergesLocked() []mergeRecord {
	list := make([]mergeRecord, 0, len(j.merges))
	for _, rec := range j.merges {
		list = append(list, rec)
	}
	sort.Slice(list, func(a, b int) bool {
		if !list[a].At.Equal(list[b].At) {
			return list[a].At.Before(list[b].At)
		}
		return list[a].ID < list[b].ID
	})
	return list
}

func (j *jobs) saveMergesLocked() {
	dir, err := j.c.Config.RunDataDir()
	if err != nil {
		return
	}
	if err := writeFileAtomic(filepath.Join(dir, mergesFileName), mergesFile{Merges: j.sortedMergesLocked()}); err != nil {
		j.c.logf("merges: cannot save %s: %v", mergesFileName, err)
	}
}

// merge handles a TypeMerge. It never blocks the connection: the work is on
// its own goroutine, and the answer is queued like every other message.
func (j *jobs) merge(envRunID string, m *runnerwire.MergeJob) {
	if m == nil {
		return
	}
	req := *m
	if req.RunID == "" {
		req.RunID = envRunID
	}
	if req.ID == "" || len(req.ID) > maxMergeIDLen || req.RunID == "" {
		j.c.logf("merge request without a usable id or run; ignored")
		return
	}
	j.mu.Lock()
	if j.mergeInFlight[req.ID] {
		j.mu.Unlock()
		j.c.logf("run %s: merge %s sent again while in hand; ignored", req.RunID, req.ID)
		return
	}
	j.mergeInFlight[req.ID] = true
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer j.wg.Done()
		j.runMerge(req)
	}()
}

// runMerge answers one merge request: from memory when it was answered
// before, otherwise by checking the PR and merging it.
func (j *jobs) runMerge(req runnerwire.MergeJob) {
	defer func() {
		j.mu.Lock()
		delete(j.mergeInFlight, req.ID)
		j.mu.Unlock()
	}()
	// One merge at a time: two approvals of the same PR must not race.
	j.mergeMu.Lock()
	defer j.mergeMu.Unlock()

	j.mu.Lock()
	rec, answered := j.merges[req.ID]
	j.mu.Unlock()
	if answered {
		j.c.logf("run %s: merge %s sent again; answering as before (%s)", req.RunID, req.ID, outcomeText(rec.Error))
		j.queueMergeResult(req, rec.Error)
		return
	}
	j.c.logf("run %s: merge %s approved at head %s", req.RunID, req.ID, short(req.HeadSHA))
	reason, merged := j.tryMerge(req)
	if j.ctx.Err() != nil {
		// The runner is stopping; gh may have been killed half way. Not
		// answered, so the hub sends it again and a fresh look decides.
		j.c.logf("run %s: merge %s interrupted; not answered", req.RunID, req.ID)
		return
	}
	j.mu.Lock()
	j.merges[req.ID] = mergeRecord{ID: req.ID, RunID: req.RunID, HeadSHA: req.HeadSHA, Error: reason, At: time.Now().UTC()}
	j.pruneMergesLocked(time.Now())
	j.saveMergesLocked()
	j.mu.Unlock()
	j.queueMergeResult(req, reason)
	if reason == "" {
		j.c.logf("run %s: merge %s: merged", req.RunID, req.ID)
	} else {
		j.c.logf("run %s: merge %s refused: %s", req.RunID, req.ID, firstLine(reason))
	}
	if merged != nil {
		j.afterMerge(*merged, req)
	}
}

func outcomeText(reason string) string {
	if reason == "" {
		return "merged"
	}
	return "refused: " + firstLine(reason)
}

func (j *jobs) queueMergeResult(req runnerwire.MergeJob, reason string) {
	j.mu.Lock()
	j.mergeResults = append(j.mergeResults, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, RunID: req.RunID,
		MergeResult: &runnerwire.MergeOutcome{ID: req.ID, RunID: req.RunID, Error: reason}})
	j.mu.Unlock()
	j.wake()
}

// tryMerge checks the PR and merges it. reason is "" when the PR is merged
// (now, or already at the approved head); merged is then the watched PR to
// refresh and clean up after.
func (j *jobs) tryMerge(req runnerwire.MergeJob) (reason string, merged *watchedPR) {
	cfg := j.c.Config
	if !cfg.RemoteMergeOn() {
		return ErrRemoteMergeOff, nil
	}
	if req.HeadSHA == "" {
		return "the merge request names no head commit", nil
	}
	j.mu.Lock()
	var w *watchedPR
	if current := j.watched[req.RunID]; current != nil {
		copied := *current
		w = &copied
	}
	alreadyMerged := false
	for _, rec := range j.merges {
		if rec.RunID == req.RunID && rec.Error == "" && rec.HeadSHA == req.HeadSHA {
			alreadyMerged = true
		}
	}
	j.mu.Unlock()
	if w == nil {
		if alreadyMerged {
			// Another approval of the same head got there first.
			return "", nil
		}
		return fmt.Sprintf("this runner has no open pull request for run %s", req.RunID), nil
	}
	gh, err := j.c.gh()
	if err != nil {
		return err.Error(), nil
	}
	ctx := j.ctx
	dir := w.Repo
	if _, err := os.Stat(dir); err != nil {
		dir = ""
	}
	out, err := gh.call(ctx, dir, "", "pr", "view", w.URL, "--json", prMergeFields)
	if err != nil {
		return mergeError(ghError("gh pr view", err)), nil
	}
	info, err := parsePRView(w.URL, out)
	if err != nil {
		return err.Error(), nil
	}
	var v ghPRView
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &v)

	switch info.State {
	case runnerwire.PRMerged:
		if info.HeadSHA == req.HeadSHA {
			return "", w
		}
		j.updatePR(*w, info)
		return "the pull request was already merged, at a different head", nil
	case runnerwire.PRClosed:
		j.updatePR(*w, info)
		return "the pull request is closed", nil
	}
	if info.Draft {
		return "the pull request is a draft", nil
	}
	if info.HeadSHA != req.HeadSHA {
		return fmt.Sprintf("PR head moved since approval (approved %s, now %s)", short(req.HeadSHA), short(info.HeadSHA)), nil
	}
	switch info.CI {
	case runnerwire.CIPending:
		return "CI is still running", nil
	case runnerwire.CIFailure:
		return "CI failed", nil
	case runnerwire.CINone:
		return "the pull request has no CI checks; a remote merge needs green CI", nil
	}
	mergeable, state := strings.ToUpper(v.Mergeable), strings.ToUpper(v.MergeStateStatus)
	switch {
	case mergeable == "CONFLICTING" || state == "DIRTY":
		return fmt.Sprintf("the pull request has merge conflicts (mergeable %s, mergeStateStatus %s)",
			orDash(v.Mergeable), orDash(v.MergeStateStatus)), nil
	case state == "BLOCKED":
		return "GitHub blocks the merge (mergeStateStatus BLOCKED: a required review or check is missing)", nil
	}

	method := cfg.MergeMethodOrDefault()
	args := []string{"pr", "merge", w.URL, "--" + method, "--match-head-commit", req.HeadSHA}
	if cfg.DeleteBranchOnMerge {
		args = append(args, "--delete-branch")
	}
	j.c.logf("run %s: merging %s (%s, head %s)", req.RunID, w.URL, method, short(req.HeadSHA))
	// Outside any checkout: gh then touches only GitHub, never a local
	// branch (the run's is still checked out in its worktree).
	mergeDir, err := cfg.RunDataDir()
	if err != nil {
		mergeDir = ""
	}
	if _, err := gh.call(ctx, mergeDir, "", args...); err != nil {
		return mergeError(err), nil
	}
	return "", w
}

// mergeError is gh's complaint as the hub shows it: trimmed, at most 1 KB.
func mergeError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "gh pr merge failed"
	}
	if len(msg) > maxMergeError {
		msg = strings.ToValidUTF8(msg[:maxMergeError], "")
	}
	return msg
}

// afterMerge reads the merged PR back and hands it to the watcher, which
// tells the hub and removes the run's worktree.
func (j *jobs) afterMerge(w watchedPR, req runnerwire.MergeJob) {
	info := w.PR
	gh, err := j.c.gh()
	if err == nil {
		dir := w.Repo
		if _, statErr := os.Stat(dir); statErr != nil {
			dir = ""
		}
		var viewed runnerwire.PRInfo
		viewed, err = gh.view(j.ctx, dir, w.URL)
		if err == nil {
			info = viewed
		}
	}
	if err != nil {
		// gh said merged; the read-back failing does not make it less so.
		j.c.logf("run %s: merged, but reading the PR back failed: %v", req.RunID, err)
		info.State, info.HeadSHA = runnerwire.PRMerged, req.HeadSHA
	}
	if info.State != runnerwire.PRMerged {
		// Queued by GitHub (a merge queue) rather than merged at once: the
		// watcher reports the merge when it happens.
		j.c.logf("run %s: gh accepted the merge; the PR is still %s", req.RunID, info.State)
	}
	j.updatePR(w, info)
}

// pendingMergeResults is how many answers wait for the connection, for tests.
func (j *jobs) pendingMergeResults() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.mergeResults)
}
