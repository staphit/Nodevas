package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// Changes and pull requests (W3). The runner reports what a run committed
// once, and its pull request whenever the PR's state or CI moves, for as long
// as it watches the PR. The hub records both; it never touches git itself.

// ChangesGrace is how long after a run ends its changes are still accepted.
// The runner sends them before the finish, so this only covers a message that
// was delayed or resent after a reconnect.
const ChangesGrace = time.Hour

// Limits on what a changes or pr message may carry, besides runnerwire's.
const (
	MaxPathBytes   = 1024
	MaxPRURLBytes  = 512
	maxCommitBytes = 64 // a SHA-256 object name; SHA-1 is 40
)

var fileStatuses = map[string]bool{
	runnerwire.FileAdded: true, runnerwire.FileModified: true,
	runnerwire.FileDeleted: true, runnerwire.FileRenamed: true,
}

var prStates = map[string]bool{
	runnerwire.PROpen: true, runnerwire.PRMerged: true, runnerwire.PRClosed: true,
}

var ciStatuses = map[string]bool{
	runnerwire.CINone: true, runnerwire.CIPending: true,
	runnerwire.CISuccess: true, runnerwire.CIFailure: true,
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// validPath is a repository path as git prints it: text, no NUL (PostgreSQL
// text cannot hold one), within MaxPathBytes.
func validPath(p string, required bool) bool {
	if p == "" {
		return !required
	}
	return len(p) <= MaxPathBytes && utf8.ValidString(p) && !strings.ContainsRune(p, 0)
}

// ValidateChanges checks a changes report against the limits. A report that
// breaks them is refused whole rather than stored in part.
func ValidateChanges(ch runnerwire.RunChanges) error {
	if len(ch.Commit) > maxCommitBytes || !isHex(ch.Commit) {
		return invalidf("commit is not an object name")
	}
	if len(ch.Base) > maxShortField || !utf8.ValidString(ch.Base) || strings.ContainsRune(ch.Base, 0) {
		return invalidf("base is not a branch name")
	}
	if len(ch.Files) > runnerwire.MaxChangedFiles {
		return invalidf("%d files, over %d", len(ch.Files), runnerwire.MaxChangedFiles)
	}
	if len(ch.Patch) > runnerwire.MaxPatchBytes {
		return invalidf("patch over %d bytes", runnerwire.MaxPatchBytes)
	}
	if ch.Additions < 0 || ch.Deletions < 0 {
		return invalidf("negative line counts")
	}
	for i, f := range ch.Files {
		if !validPath(f.Path, true) || !validPath(f.OldPath, false) {
			return invalidf("file %d: path missing, over %d bytes or not text", i, MaxPathBytes)
		}
		if !fileStatuses[f.Status] {
			return invalidf("file %d: status %q", i, f.Status)
		}
		if f.Additions < 0 || f.Deletions < 0 {
			return invalidf("file %d: negative line counts", i)
		}
	}
	return nil
}

// ValidatePR checks a pull request report. URL is "" when no PR could be
// opened (Error says why); otherwise it is an https:// link and State is set.
func ValidatePR(pr runnerwire.PRInfo) error {
	if pr.URL != "" {
		if len(pr.URL) > MaxPRURLBytes || !strings.HasPrefix(pr.URL, "https://") {
			return invalidf("pr url must be https:// and at most %d bytes", MaxPRURLBytes)
		}
		if strings.IndexFunc(pr.URL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return invalidf("pr url has spaces or control characters")
		}
		u, err := url.Parse(pr.URL)
		if err != nil || u.Host == "" || u.User != nil {
			return invalidf("pr url is not a link")
		}
		if !prStates[pr.State] {
			return invalidf("pr state %q", pr.State)
		}
	} else if pr.State != "" && !prStates[pr.State] {
		return invalidf("pr state %q", pr.State)
	}
	if pr.Number < 0 || pr.Number > 1<<31-1 {
		return invalidf("pr number out of range")
	}
	if !ciStatuses[pr.CI] {
		return invalidf("ci status %q", pr.CI)
	}
	if len(pr.HeadSHA) > maxCommitBytes || !isHex(pr.HeadSHA) {
		return invalidf("pr head is not an object name")
	}
	return nil
}

// acceptsChanges reports whether a run in this state may still record its
// changes: while running, or within ChangesGrace of ending after it ran.
func (s *Store) acceptsChanges(r Record) bool {
	switch r.Status {
	case runnerwire.RunRunning:
		return true
	case runnerwire.RunSucceeded, runnerwire.RunFailed, runnerwire.RunCancelled:
		if r.StartedAt == "" {
			return false // ended before it ever ran
		}
		finished, err := time.Parse(time.RFC3339Nano, r.FinishedAt)
		return err == nil && s.clock().Sub(finished) <= ChangesGrace
	}
	return false
}

// SetChanges records what a run committed, as its runner reports it, and
// copies the counts onto the run. It reports whether anything was stored: a
// run keeps the first report it gets, and a resend is ignored.
func (s *Store) SetChanges(ctx context.Context, runID, runnerID string, ch runnerwire.RunChanges) (Record, bool, error) {
	if err := ValidateChanges(ch); err != nil {
		return Record{}, false, err
	}
	current, err := s.Get(ctx, runID)
	if err != nil {
		return Record{}, false, err
	}
	if current.RunnerID != runnerID {
		return Record{}, false, ErrNotFound
	}
	if !s.acceptsChanges(current) {
		return current, false, fmt.Errorf("%w: changes for a %s run", ErrTransition, current.Status)
	}
	files := ch.Files
	if files == nil {
		files = []runnerwire.FileChange{}
	}
	rawFiles, err := json.Marshal(files)
	if err != nil {
		return Record{}, false, err
	}
	patch := strings.ReplaceAll(Truncate(ch.Patch, runnerwire.MaxPatchBytes), "\x00", "")
	truncated := 0
	if ch.PatchTruncated {
		truncated = 1
	}
	stored := false
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		stored = false
		result, err := tx.ExecContext(ctx, `
INSERT INTO run_changes (run_id, commit_sha, base, files, additions, deletions, patch, patch_truncated, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (run_id) DO NOTHING`,
			runID, ch.Commit, ch.Base, string(rawFiles), int64(ch.Additions), int64(ch.Deletions),
			patch, int64(truncated), s.stamp())
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			return err
		}
		stored = true
		_, err = tx.ExecContext(ctx,
			`UPDATE runs SET commit_sha = ?, files_changed = ?, additions = ?, deletions = ? WHERE id = ?`,
			ch.Commit, int64(len(files)), int64(ch.Additions), int64(ch.Deletions), runID)
		return err
	})
	if err != nil {
		return Record{}, false, err
	}
	if !stored {
		return current, false, nil
	}
	latest, err := s.Get(ctx, runID)
	return latest, true, err
}

// Changes returns what a run committed, or nil when its runner has reported
// nothing (yet).
func (s *Store) Changes(ctx context.Context, runID string) (*runnerwire.RunChanges, error) {
	var out runnerwire.RunChanges
	var files string
	var additions, deletions, truncated int64
	err := s.db.QueryRowContext(ctx, `
SELECT commit_sha, base, files, additions, deletions, patch, patch_truncated
FROM run_changes WHERE run_id = ?`, runID).Scan(
		&out.Commit, &out.Base, &files, &additions, &deletions, &out.Patch, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(files), &out.Files); err != nil {
		return nil, err
	}
	if out.Files == nil {
		out.Files = []runnerwire.FileChange{}
	}
	out.Additions, out.Deletions, out.PatchTruncated = int(additions), int(deletions), truncated != 0
	return &out, nil
}

// SetPR records a run's pull request as its runner last saw it, in whatever
// status the run is: the runner keeps watching the PR long after the run
// ends. It reports whether anything the API shows changed.
func (s *Store) SetPR(ctx context.Context, runID, runnerID string, pr runnerwire.PRInfo) (Record, bool, error) {
	_, latest, changed, err := s.setPR(ctx, runID, runnerID, pr)
	return latest, changed, err
}

// setPR is SetPR that also returns the run as it was before, so the caller
// can tell what moved (CI turning red).
func (s *Store) setPR(ctx context.Context, runID, runnerID string, pr runnerwire.PRInfo) (Record, Record, bool, error) {
	if err := ValidatePR(pr); err != nil {
		return Record{}, Record{}, false, err
	}
	current, err := s.Get(ctx, runID)
	if err != nil {
		return Record{}, Record{}, false, err
	}
	if current.RunnerID != runnerID {
		return Record{}, Record{}, false, ErrNotFound
	}
	prError := strings.ReplaceAll(Truncate(pr.Error, MaxErrorBytes), "\x00", "")
	if current.PRURL == pr.URL && current.PRNumber == pr.Number && current.PRState == pr.State &&
		current.CIStatus == pr.CI && current.PRError == prError && current.PRHeadSHA == pr.HeadSHA {
		return current, current, false, nil
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE runs SET pr_url = ?, pr_number = ?, pr_state = ?, ci_status = ?, pr_error = ?, pr_head_sha = ?
WHERE id = ? AND runner_id = ?`,
		pr.URL, int64(pr.Number), pr.State, pr.CI, prError, pr.HeadSHA, runID, runnerID); err != nil {
		return Record{}, Record{}, false, err
	}
	latest, err := s.Get(ctx, runID)
	return current, latest, true, err
}
