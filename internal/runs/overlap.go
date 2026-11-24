package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"nodevas/internal/agents"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// maxOverlapNoteBytes caps the overlap note appended to a global follow-up's
// prompt (§C.3.3).
const maxOverlapNoteBytes = 1024

// SetCoordinationMail registers the callback that delivers Layer 2 overlap
// coordination notices to a node run's own MCP session (§C.3.3). Call it before
// serving.
func (d *Dispatcher) SetCoordinationMail(fn func(ctx context.Context, project, runID, body string) error) {
	if d == nil {
		return
	}
	d.coord.coordinationMail = fn
}

func (d *Dispatcher) sendCoordinationMail(ctx context.Context, project, runID, body string) error {
	if d == nil || d.coord.coordinationMail == nil {
		return agents.ErrNoSession
	}
	return d.coord.coordinationMail(ctx, project, runID, body)
}

// Touched records newly touched repo-relative paths reported by a runner for
// its own running run, computes overlaps, announces thresholds, and mails
// eligible node runs (§C.3.2, §C.3.3). Invalid paths are dropped rather than
// failing the message.
func (d *Dispatcher) Touched(ctx context.Context, runnerID, runID string, t runnerwire.Touched) error {
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return err
	}
	if r.RunnerID != runnerID {
		return ErrNotFound
	}
	if r.Status != runnerwire.RunRunning {
		return ErrTransition
	}
	entries := cleanTouchedInput(t)
	newPaths, err := d.store.UpsertTouchedFiles(ctx, r.ID, entries)
	if err != nil {
		return err
	}
	d.findOverlaps(ctx, r, newPaths)
	return nil
}

// changesTouched adds a run's committed files to Layer 2 touched-file overlap
// tracking in case the runner's watcher missed any (§C.3.2). Called from
// Dispatcher.Changes.
func (d *Dispatcher) changesTouched(ctx context.Context, r Record, files []runnerwire.FileChange) {
	if len(files) == 0 {
		return
	}
	byPath := make(map[string]bool, len(files))
	order := make([]string, 0, len(files))
	add := func(p string, addOnly bool) {
		if !validTouchedPath(p) {
			return
		}
		if prev, exists := byPath[p]; exists {
			byPath[p] = prev && addOnly
			return
		}
		byPath[p] = addOnly
		order = append(order, p)
	}
	for _, f := range files {
		if f.Status == runnerwire.FileRenamed && f.OldPath != "" {
			add(f.OldPath, false)
		}
		ao := !f.Binary && f.Deletions == 0 && f.Status != runnerwire.FileDeleted
		add(f.Path, ao)
	}
	entries := make([]touchedEntry, 0, len(order))
	for _, p := range order {
		entries = append(entries, touchedEntry{path: p, addOnly: byPath[p]})
	}
	newPaths, err := d.store.UpsertTouchedFiles(ctx, r.ID, entries)
	if err != nil {
		slog.Warn("runs: changes touched upsert", slog.String("run", r.ID), slog.Any("error", err))
		return
	}
	d.findOverlaps(ctx, r, newPaths)
}

func cleanTouchedInput(t runnerwire.Touched) []touchedEntry {
	files := t.Files
	if len(files) > runnerwire.MaxTouchedPerMessage {
		files = files[:runnerwire.MaxTouchedPerMessage]
	}
	addOnlySet := make(map[string]bool, len(t.AddOnly))
	for _, p := range t.AddOnly {
		addOnlySet[p] = true
	}
	byPath := make(map[string]bool, len(files))
	order := make([]string, 0, len(files))
	for _, p := range files {
		if !validTouchedPath(p) {
			continue
		}
		ao := addOnlySet[p]
		if prev, exists := byPath[p]; exists {
			byPath[p] = prev && ao
			continue
		}
		byPath[p] = ao
		order = append(order, p)
	}
	out := make([]touchedEntry, 0, len(order))
	for _, p := range order {
		out = append(out, touchedEntry{path: p, addOnly: byPath[p]})
	}
	return out
}

// validTouchedPath checks that p is a clean repo-relative slash path within
// MaxPathBytes (1024), with no NUL/control bytes, no absolute prefix and no "..".
func validTouchedPath(p string) bool {
	if !validPath(p, true) {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func isOwnPair(a, b Record) bool {
	return (a.CreatedBy != "" && a.CreatedBy == b.CreatedBy) ||
		(a.OwnerID != "" && a.OwnerID == b.OwnerID)
}

func (d *Dispatcher) excludedOverlapPair(ctx context.Context, r, b Record) bool {
	if r.ID == b.ID {
		return true
	}
	if r.PipelineID != "" && b.PipelineID == r.PipelineID {
		return true
	}
	if r.ContestID != "" && b.ContestID == r.ContestID {
		return true
	}
	if r.Global && b.Global && workRootOf(r) == workRootOf(b) {
		return true
	}
	if r.RetryOf != "" || b.RetryOf != "" {
		if d.store.fallbackRoot(ctx, r) == d.store.fallbackRoot(ctx, b) {
			return true
		}
	}
	return false
}

// findOverlaps compares r against in-scope runs sharing r.RepoKey, upserts
// run_overlaps rows, fires realtime announcements at thresholds, and delivers
// (or retries) coordination mail for own node pairs (§C.3.2, §C.3.3).
func (d *Dispatcher) findOverlaps(ctx context.Context, r Record, _ []string) {
	if r.RepoKey == "" {
		return
	}
	candidates, err := d.store.overlapCandidates(ctx, r, d.clockNow())
	if err != nil {
		slog.Warn("runs: overlap candidates", slog.String("run", r.ID), slog.Any("error", err))
		return
	}

	seenPairs := make(map[[2]string]bool, len(candidates))
	for _, b := range candidates {
		if d.excludedOverlapPair(ctx, r, b) {
			continue
		}
		own := isOwnPair(r, b)
		if !own {
			// Cross-member pairs require both runs to be active node runs in the same project.
			if r.Global || b.Global || r.Project == "" || r.Project != b.Project ||
				runnerwire.RunFinal(r.Status) || runnerwire.RunFinal(b.Status) {
				continue
			}
		}

		runA, runB := r, b
		if runA.ID > runB.ID {
			runA, runB = runB, runA
		}
		pairKey := [2]string{runA.ID, runB.ID}
		seenPairs[pairKey] = true

		shared, err := d.store.sharedTouchedPaths(ctx, runA.ID, runB.ID)
		if err != nil {
			slog.Warn("runs: shared touched paths", slog.String("runA", runA.ID), slog.String("runB", runB.ID), slog.Any("error", err))
			continue
		}
		if len(shared) == 0 {
			continue
		}

		strongCount := 0
		top := make([]string, 0, min(len(shared), runnerwire.MaxOverlapPaths))
		for _, sp := range shared {
			if sp.strong {
				strongCount++
			}
			if len(top) < runnerwire.MaxOverlapPaths {
				top = append(top, sp.path)
			}
		}

		prev, existed, err := d.store.getOverlap(ctx, runA.ID, runB.ID)
		if err != nil {
			slog.Warn("runs: get overlap", slog.String("runA", runA.ID), slog.String("runB", runB.ID), slog.Any("error", err))
			continue
		}
		prevStrong := 0
		if existed {
			prevStrong = prev.StrongCount
		}

		ov, err := d.store.upsertOverlap(ctx, runA.ID, runB.ID, top, len(shared), strongCount, !own)
		if err != nil {
			slog.Warn("runs: upsert overlap", slog.String("runA", runA.ID), slog.String("runB", runB.ID), slog.Any("error", err))
			continue
		}

		if shouldAnnounceOverlap(prevStrong, strongCount, !own) {
			d.announceOverlapPair(runA, runB)
		}
		if own && strongCount >= 1 {
			d.deliverOverlapMail(ctx, ov, runA, runB)
		}
	}

	// Retry any existing own overlap pairs for r whose mail is still owed (§C.3.2, §C.3.3).
	existing, err := d.store.overlapsForRun(ctx, r.ID)
	if err != nil {
		return
	}
	for _, ov := range existing {
		if ov.CrossMember || ov.StrongCount < 1 {
			continue
		}
		pairKey := [2]string{ov.RunA, ov.RunB}
		if seenPairs[pairKey] {
			continue
		}
		isA := ov.RunA == r.ID
		if (isA && ov.MailedA) || (!isA && ov.MailedB) {
			continue
		}
		otherID := ov.RunB
		if !isA {
			otherID = ov.RunA
		}
		other, err := d.store.Get(ctx, otherID)
		if err != nil {
			continue
		}
		runA, runB := r, other
		if !isA {
			runA, runB = other, r
		}
		d.deliverOverlapMail(ctx, ov, runA, runB)
	}
}

func shouldAnnounceOverlap(prevStrong, newStrong int, crossMember bool) bool {
	if newStrong <= prevStrong || newStrong < 1 {
		return false
	}
	if crossMember {
		// Cross-member pairs announce once on first strong overlap and never move (§C.3.2).
		return prevStrong == 0
	}
	for _, threshold := range []int{1, 5, 20} {
		if prevStrong < threshold && newStrong >= threshold {
			return true
		}
	}
	return false
}

func (d *Dispatcher) announceOverlapPair(a, b Record) {
	d.announceChanged(a)
	// Avoid emitting two identical runs-changed frames when both runs are on the same node.
	if !a.Global && !b.Global && a.Project == b.Project && a.NodeID == b.NodeID {
		return
	}
	d.announceChanged(b)
}

func (d *Dispatcher) deliverOverlapMail(ctx context.Context, ov overlapRow, runA, runB Record) {
	strongPaths := ov.Paths
	if ov.StrongCount < len(strongPaths) {
		strongPaths = strongPaths[:ov.StrongCount]
	}
	if len(strongPaths) == 0 {
		return
	}
	tryOne := func(recipient, peer Record, isA bool, alreadyMailed bool) {
		if alreadyMailed || recipient.Global || recipient.Status != runnerwire.RunRunning {
			return
		}
		body := formatCoordinationMail(peer, strongPaths, ov.StrongCount)
		err := d.sendCoordinationMail(ctx, recipient.Project, recipient.ID, body)
		switch {
		case err == nil, errors.Is(err, agents.ErrCoordinationCap):
			if markErr := d.store.markOverlapMailed(ctx, ov.RunA, ov.RunB, isA); markErr != nil {
				slog.Warn("runs: mark overlap mailed", slog.String("run", recipient.ID), slog.Any("error", markErr))
			}
		case errors.Is(err, agents.ErrNoSession), errors.Is(err, agents.ErrRateLimited):
			// Keep mailed_x = false so the next touched report retries (§C.3.3).
		default:
			slog.Warn("runs: coordination mail", slog.String("run", recipient.ID), slog.Any("error", err))
		}
	}
	tryOne(runA, runB, true, ov.MailedA)
	tryOne(runB, runA, false, ov.MailedB)
}

func formatCoordinationMail(peer Record, strongPaths []string, strongCount int) string {
	const shown = 3
	n := min(len(strongPaths), shown)
	parts := make([]string, n)
	for i := range n {
		parts[i] = "`" + strongPaths[i] + "`"
	}
	pathList := strings.Join(parts, ", ")
	if more := strongCount - n; more > 0 {
		pathList += fmt.Sprintf(" (+%d more)", more)
	}
	title := runDisplayTitle(peer)
	title = agents.EscapeTrustMarkers(title)
	if peer.Global || peer.NodeID == "" {
		return fmt.Sprintf(
			`Coordination: global run %q is also changing: %s. Keep your edits to these files minimal.`,
			title, pathList,
		)
	}
	return fmt.Sprintf(
		"Coordination: run for node `%s` %q is also changing: %s. If your changes may clash, send_message node:%s to agree who changes what, or keep your edits to these files minimal.",
		peer.NodeID, title, pathList, peer.NodeID,
	)
}

func runDisplayTitle(r Record) string {
	if r.Title != "" {
		return r.Title
	}
	if r.NodeTitle != "" {
		return r.NodeTitle
	}
	return r.NodeID
}

// Overlaps returns the Layer 2 overlap list for run r as seen by actor (§C.6, §C.10).
// Cross-member pairs (and any pair whose other side actor cannot see) are
// always hidden — even for an admin — returning only {hidden: true, since}.
func (d *Dispatcher) Overlaps(ctx context.Context, actor identity.Actor, r Record) ([]runnerwire.Overlap, error) {
	rows, err := d.store.overlapsForRun(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := make([]runnerwire.Overlap, 0, len(rows))
	for _, ov := range rows {
		otherID := ov.RunB
		if ov.RunB == r.ID {
			otherID = ov.RunA
		}
		if ov.CrossMember {
			// Weak-only cross-member pairs reveal nothing; strong cross-member
			// pairs reveal only {hidden: true, since} even for an admin (§C.3.2, §C.6).
			if ov.StrongCount < 1 {
				continue
			}
			out = append(out, runnerwire.Overlap{
				Hidden: true,
				Since:  ov.FirstAt,
			})
			continue
		}
		other, err := d.store.Get(ctx, otherID)
		if err != nil {
			continue
		}
		if !CanSee(actor, other) {
			if ov.StrongCount < 1 {
				continue
			}
			out = append(out, runnerwire.Overlap{
				Hidden: true,
				Since:  ov.FirstAt,
			})
			continue
		}
		paths := ov.Paths
		if len(paths) > runnerwire.MaxOverlapPaths {
			paths = paths[:runnerwire.MaxOverlapPaths]
		}
		out = append(out, runnerwire.Overlap{
			RunID:       other.ID,
			Title:       runDisplayTitle(other),
			NodeID:      other.NodeID,
			Agent:       other.Agent,
			Status:      other.Status,
			Mine:        other.CreatedBy == actor.ID || other.OwnerID == actor.ID,
			Paths:       paths,
			PathCount:   ov.PathCount,
			StrongCount: ov.StrongCount,
			Since:       ov.FirstAt,
		})
	}
	return out, nil
}

// overlapNote returns a short prompt note ("Note from Nodevas: …", <= 1 KiB)
// describing strong overlaps from own pairs for a global follow-up (§C.3.3).
func (d *Dispatcher) overlapNote(ctx context.Context, prior Record) string {
	rows, err := d.store.overlapsForRun(ctx, prior.ID)
	if err != nil || len(rows) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var strong []string
	for _, ov := range rows {
		if ov.CrossMember || ov.StrongCount < 1 {
			continue
		}
		limit := min(ov.StrongCount, len(ov.Paths))
		for _, p := range ov.Paths[:limit] {
			if !seen[p] {
				seen[p] = true
				strong = append(strong, p)
			}
		}
	}
	if len(strong) == 0 {
		return ""
	}
	const prefix = "Note from Nodevas: another task of yours is also changing: "
	const suffix = ". Keep your edits to these files minimal unless needed."
	var b strings.Builder
	b.WriteString(prefix)
	for i, p := range strong {
		sep := ""
		if i > 0 {
			sep = ", "
		}
		rest := len(strong) - i
		moreNote := fmt.Sprintf(" (+%d more)", rest)
		if b.Len()+len(sep)+len(p)+len(moreNote)+len(suffix) > maxOverlapNoteBytes {
			if i > 0 {
				b.WriteString(moreNote)
			}
			break
		}
		b.WriteString(sep)
		b.WriteString(p)
	}
	b.WriteString(suffix)
	return Truncate(b.String(), maxOverlapNoteBytes)
}
