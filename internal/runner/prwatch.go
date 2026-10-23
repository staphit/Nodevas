package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"nodevas/internal/runnerwire"
)

// The PR watcher: every PRPollInterval the runner asks gh about each pull
// request its runs opened, and tells the hub when its state, checks, head or
// draft flag change. A merged or closed PR's worktree is removed (its branch
// is GitHub's business) and the PR is no longer watched.
//
// Watched PRs live in <dataDir>/prs.json, so a restarted runner carries on.
// Updates go out only while connected; after every (re)connect the latest of
// each watched PR is sent once, so the hub catches up on what it missed.

// watchedPR is one entry of prs.json.
type watchedPR struct {
	RunID    string            `json:"runId"`
	NodeID   string            `json:"nodeId,omitempty"`
	URL      string            `json:"url"`
	Worktree string            `json:"worktree"`
	Repo     string            `json:"repo"`
	Branch   string            `json:"branch,omitempty"`
	PR       runnerwire.PRInfo `json:"pr"`
}

type prsFile struct {
	PRs []watchedPR `json:"prs"`
}

const prsFileName = "prs.json"

// loadPRs reads prs.json. A runner without one watches nothing yet.
func (j *jobs) loadPRs() {
	dir, err := j.c.Config.RunDataDir()
	if err != nil {
		j.c.logf("pull requests: %v", err)
		return
	}
	path := filepath.Join(dir, prsFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var file prsFile
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		j.c.logf("pull requests: cannot read %s: %v", path, err)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, w := range file.PRs {
		if w.RunID == "" || w.URL == "" || j.watched[w.RunID] != nil {
			continue
		}
		entry := w
		j.watched[w.RunID] = &entry
		j.watchOrder = append(j.watchOrder, w.RunID)
	}
	if n := len(j.watchOrder); n > 0 {
		j.c.logf("watching %d open pull request(s)", n)
	}
}

// savePRsLocked writes prs.json, owner-only, through a rename.
func (j *jobs) savePRsLocked() {
	dir, err := j.c.Config.RunDataDir()
	if err != nil {
		return
	}
	file := prsFile{PRs: []watchedPR{}}
	for _, id := range j.watchOrder {
		file.PRs = append(file.PRs, *j.watched[id])
	}
	if err := writeFileAtomic(filepath.Join(dir, prsFileName), file); err != nil {
		j.c.logf("pull requests: cannot save %s: %v", prsFileName, err)
	}
}

func writeFileAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prs-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// watch starts watching a run's PR. Its first state goes out with the run's
// own messages, not from here.
func (j *jobs) watch(w watchedPR) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.watched[w.RunID] == nil {
		j.watchOrder = append(j.watchOrder, w.RunID)
	}
	j.watched[w.RunID] = &w
	j.savePRsLocked()
}

// resendPRs queues the latest state of every watched PR, once per connection.
func (j *jobs) resendPRs() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, id := range j.watchOrder {
		j.queuePRLocked(id, j.watched[id].PR)
	}
}

// queuePRLocked queues a PR update; a newer one for the same run replaces it.
func (j *jobs) queuePRLocked(runID string, pr runnerwire.PRInfo) {
	if _, queued := j.prPending[runID]; !queued {
		j.prOrder = append(j.prOrder, runID)
	}
	j.prPending[runID] = pr
}

// watchPRs polls until the runner stops.
func (j *jobs) watchPRs(interval func() time.Duration) {
	defer j.wg.Done()
	for {
		j.pollPRs()
		select {
		case <-j.ctx.Done():
			return
		case <-time.After(interval()):
		}
	}
}

// pollPRs asks gh about every watched PR once.
func (j *jobs) pollPRs() {
	j.mu.Lock()
	var list []watchedPR
	for _, id := range j.watchOrder {
		list = append(list, *j.watched[id])
	}
	j.mu.Unlock()
	if len(list) == 0 {
		return
	}
	gh, err := j.c.gh()
	if err != nil {
		j.noteWatchError("", err)
		return
	}
	for _, w := range list {
		if j.ctx.Err() != nil {
			return
		}
		dir := w.Repo
		if _, err := os.Stat(dir); err != nil {
			dir = ""
		}
		info, err := gh.view(j.ctx, dir, w.URL)
		if err != nil {
			j.noteWatchError(w.RunID, err)
			continue
		}
		j.noteWatchError(w.RunID, nil)
		j.updatePR(w, info)
	}
}

// updatePR records a PR's new state and, when something the hub shows has
// changed, queues it. A merged or closed PR ends its watch.
func (j *jobs) updatePR(w watchedPR, info runnerwire.PRInfo) {
	final := info.State == runnerwire.PRMerged || info.State == runnerwire.PRClosed
	if final {
		wt := Worktree{Path: w.Worktree, Branch: w.Branch}
		if err := removeWorktree(j.ctx, w.Repo, wt, false); err != nil {
			j.c.logf("run %s: PR %s; removing its worktree: %v", w.RunID, info.State, err)
		} else {
			j.c.logf("run %s: PR %s; worktree %s removed", w.RunID, info.State, w.Worktree)
		}
	}
	j.mu.Lock()
	current := j.watched[w.RunID]
	if current == nil {
		j.mu.Unlock()
		return
	}
	changed := prChanged(current.PR, info)
	current.PR = info
	if final {
		delete(j.watched, w.RunID)
		for i, id := range j.watchOrder {
			if id == w.RunID {
				j.watchOrder = append(j.watchOrder[:i], j.watchOrder[i+1:]...)
				break
			}
		}
	}
	if changed || final {
		j.queuePRLocked(w.RunID, info)
		j.savePRsLocked()
	}
	j.mu.Unlock()
	if changed || final {
		j.wake()
	}
}

// prChanged: does the hub need to hear about this?
func prChanged(old, now runnerwire.PRInfo) bool {
	return old.State != now.State || old.CI != now.CI || old.HeadSHA != now.HeadSHA || old.Draft != now.Draft ||
		old.Number != now.Number
}

// noteWatchError logs a failing gh once, not every poll.
func (j *jobs) noteWatchError(runID string, err error) {
	text := ""
	if err != nil {
		text = err.Error()
	}
	j.mu.Lock()
	same := j.watchErrs[runID] == text
	if text == "" {
		delete(j.watchErrs, runID)
	} else {
		j.watchErrs[runID] = text
	}
	j.mu.Unlock()
	if same || text == "" {
		return
	}
	if runID == "" {
		j.c.logf("pull requests: cannot check them: %s", text)
	} else {
		j.c.logf("run %s: cannot check its PR: %s", runID, text)
	}
}

// watchedRuns lists the watched run IDs, for tests and status.
func (j *jobs) watchedRuns() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.watchOrder...)
}
