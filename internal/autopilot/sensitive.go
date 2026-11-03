package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// contentHash is a task node's content as its runs see it: the title and
// the body the prompt is built from.
func contentHash(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

// sensitivePath reports whether a changed file is one an unattended merge
// must not change. The list is shared with the staged pipeline's diff guard
// (runs.SensitivePath).
func sensitivePath(p string) bool { return runs.SensitivePath(p) }

// maxHoldPaths bounds the paths a hold's reason names.
const maxHoldPaths = 8

// mergeHold is why a try's PR must not merge without a person, or "": it
// changes a sensitive path, its changes are unknown or incomplete, or its
// head is not a commit the try made. It fails closed: when the changes
// cannot be read, it holds.
func (o *Orchestrator) mergeHold(ctx context.Context, obs observation) string {
	if obs.prRun == nil {
		return "the pull request's run is unknown"
	}
	var touched []string
	commits := map[string]bool{}
	reported := false
	for _, r := range obs.runs {
		if r.Kind != runnerwire.JobImplement && r.Kind != runnerwire.JobRevise {
			continue // review runs change nothing
		}
		ch, err := o.opts.Runs.Changes(ctx, r.ID)
		if err != nil {
			return "its changed files cannot be read: " + err.Error()
		}
		if ch == nil {
			continue
		}
		reported = true
		if len(ch.Files) >= runnerwire.MaxChangedFiles {
			return "it changes too many files to check"
		}
		if ch.Commit != "" {
			commits[ch.Commit] = true
		}
		for _, f := range ch.Files {
			for _, p := range []string{f.Path, f.OldPath} {
				if p != "" && sensitivePath(p) && !slices.Contains(touched, p) {
					touched = append(touched, p)
				}
			}
		}
	}
	if !reported {
		return "its runner reported no changed files"
	}
	if head := obs.prRun.PRHeadSHA; head != "" && !commits[head] {
		return "its head " + shortSHA(head) + " is not a commit the autopilot made"
	}
	if len(touched) == 0 {
		return ""
	}
	slices.Sort(touched)
	list := touched
	if len(list) > maxHoldPaths {
		list = list[:maxHoldPaths]
	}
	reason := "it changes " + strings.Join(list, ", ")
	if extra := len(touched) - len(list); extra > 0 {
		reason += " and " + strconv.Itoa(extra) + " more"
	}
	return reason
}
