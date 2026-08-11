package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"nodevas/internal/project"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Scheduled backup [P3].
//
// A background loop pushes the whole workspace to the configured Remote on an
// interval. It reuses the same bundle build and Push the manual button uses, so
// it stays off the write Path. Each push is a fresh timestamped file, so the
// Remote (Drive especially) keeps every version rather than overwriting one.
//
// Because every run adds a file and nothing used to remove one, the history was
// unbounded: at a one-hour interval that is 8760 full copies of the workspace a
// year, which makes both the bill and the restore list unusable. Retention
// (retainCount / pruneBundles below) bounds it.

const (
	// backupCheckInterval is how often the loop wakes to see if a push is due.
	// It is much finer than any real backup interval so the schedule does not
	// drift by a whole interval when the interval is short.
	backupCheckInterval = 15 * time.Minute

	defaultBackupIntervalHours = 24
	minBackupIntervalHours     = 1

	// defaultRetainBundles is how many of this app's own bundles survive a
	// prune when the config says nothing. Fourteen at the eight-hour interval
	// the schedule is moving to is a little under five days of coverage.
	//
	// Retention is deliberately a count and not a duration, even though the
	// schedule next to it is expressed in hours. A window ("delete anything
	// older than a day") has a failure mode a count does not: if the scheduler
	// was down longer than the window — machine off over a weekend, expired
	// Drive token failing every push — then on the next successful run every
	// surviving bundle is older than the window and a literal reading of the
	// rule deletes all of them, leaving one backup taken seconds ago at exactly
	// the moment the history was most likely to be wanted. Fourteen bundles are
	// fourteen bundles whether they were taken over five days or five months,
	// so the policy needs no separate outage guard to get right.
	defaultRetainBundles = 14

	// minRetainBundles is the floor an explicit setting is clamped up to. One
	// is a number somebody will type — it reads like "keep one day" — and it
	// means the previous backup is destroyed the instant a new one lands, so a
	// corrupt bundle or a mistake that was already in the newest snapshot
	// leaves nothing to fall back to. Three keeps two alternatives and is still
	// far below any useful setting, so it never quietly becomes the policy.
	minRetainBundles = 3

	// retainDisabled is what retainCount returns when the operator has opted
	// out of pruning; pruneBundles reads it as "delete nothing, ever".
	retainDisabled = 0

	// backupTimeout bounds one scheduled push so a hung network cannot wedge
	// the loop forever.
	backupTimeout = 5 * time.Minute
)

// remoteState is runtime state kept next to the config: it is written by the
// server, not the user, so it lives in its own file.
type remoteState struct {
	LastBackupAt       time.Time `json:"lastBackupAt"`
	LastPushedBundleID string    `json:"lastPushedBundleId,omitempty"`
	LastPushedHash     string    `json:"lastPushedHash,omitempty"`
	LastPushedBackend  string    `json:"lastPushedBackend,omitempty"`
	LastPushedModified time.Time `json:"lastPushedModified,omitempty"`
	// KnownBundleIDs is every bundle this instance has uploaded, newest last,
	// bounded by rememberBundle. "Remote newer" is supposed to mean somebody
	// ELSE pushed — another machine, another operator — and the only way to
	// mean that is to remember what we pushed ourselves. Without this list a
	// manual per-project backup became foreign evidence the moment it landed,
	// which flipped the banner to 雲端版本較新 and, worse, made FlushBackup
	// refuse: pressing "back up now" once silently stopped every scheduled
	// backup after it.
	KnownBundleIDs []string `json:"knownBundleIds,omitempty"`
}

// maxKnownBundles bounds the remembered-upload list. It only has to outlive
// retention: once an old bundle is pruned it can never be "latest" again, so
// remembering it buys nothing. Retention's floor is far below this.
const maxKnownBundles = 64

// rememberBundle appends an uploaded bundle's ID, deduplicating and keeping
// the newest maxKnownBundles.
func rememberBundle(ids []string, id string) []string {
	if id == "" {
		return ids
	}
	kept := ids[:0:0]
	for _, existing := range ids {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, id)
	if len(kept) > maxKnownBundles {
		kept = kept[len(kept)-maxKnownBundles:]
	}
	return kept
}

// SyncState describes the relationship between the local workspace and the
// newest remote snapshot. It is intentionally snapshot-level: a remote is not
// a live filesystem and cannot safely merge individual files by itself.
type SyncState string

const (
	SyncDisabled    SyncState = "disabled"
	SyncSynced      SyncState = "synced"
	SyncLocalNewer  SyncState = "local-newer"
	SyncRemoteNewer SyncState = "remote-newer"
	SyncConflict    SyncState = "conflict"
	SyncRemoteMiss  SyncState = "remote-missing"
)

var (
	ErrNoRemote     = errors.New("no remote is configured")
	ErrSyncConflict = errors.New("remote snapshot changed; explicit conflict resolution required")
)

// SyncStatus is safe to expose to the UI: it contains hashes and opaque bundle
// IDs, never OAuth credentials.
type SyncStatus struct {
	State              SyncState  `json:"state"`
	LocalHash          string     `json:"localHash,omitempty"`
	LastPushedHash     string     `json:"lastPushedHash,omitempty"`
	LastPushedBundleID string     `json:"lastPushedBundleId,omitempty"`
	RemoteLatest       *RemoteRef `json:"remoteLatest,omitempty"`
	RemoteHash         string     `json:"remoteHash,omitempty"`
	LastBackupAt       time.Time  `json:"lastBackupAt"`
}

func (m *RemoteManager) StatePath() string {
	if m.pm == nil {
		return ""
	}
	return strings.TrimSuffix(m.ConfigPath(), ".json") + ".state"
}

func (m *RemoteManager) loadState() remoteState {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	Path := m.StatePath()
	if Path == "" {
		return remoteState{}
	}
	data, err := os.ReadFile(Path)
	if err != nil {
		return remoteState{}
	}
	var state remoteState
	if json.Unmarshal(data, &state) != nil {
		return remoteState{}
	}
	return state
}

func (m *RemoteManager) saveState(state remoteState) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	Path := m.StatePath()
	if Path == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path), 0o700); err != nil {
		return err
	}
	return store.WriteMetaFile(Path, data)
}

func (m *RemoteManager) LastBackupAt() time.Time {
	return m.loadState().LastBackupAt
}

func (m *RemoteManager) SetLastBackupAt(at time.Time) error {
	state := m.loadState()
	state.LastBackupAt = at
	return m.saveState(state)
}

func backupInterval(cfg Config) time.Duration {
	hours := cfg.IntervalHours
	if hours < minBackupIntervalHours {
		hours = defaultBackupIntervalHours
	}
	return time.Duration(hours) * time.Hour
}

// retainCount resolves the configured retention into the number of this app's
// own bundles to keep.
//
// It clamps rather than rejects, and it clamps here rather than only in the
// HTTP handler, because this is the number the deletion loop actually uses. The
// config is a JSON file on disk that a person can edit and that survives
// restarts; a validation error returned to a
// browser protects none of those paths. Every caller — scheduler, panel, tests
// — asks this one function, so they cannot disagree about what the setting
// means.
func retainCount(cfg Config) int {
	switch {
	case cfg.RetainBundles < 0:
		// An explicit opt-out. Someone who wants every version forever must be
		// able to say so; the storage bill is then their decision, not a bug.
		return retainDisabled
	case cfg.RetainBundles == 0:
		return defaultRetainBundles
	case cfg.RetainBundles < minRetainBundles:
		return minRetainBundles
	default:
		return cfg.RetainBundles
	}
}

// RetainCount exposes the effective retention so the API can show the panel the
// same number the pruner will use, rather than the raw one from the file.
func RetainCount(cfg Config) int { return retainCount(cfg) }

// pruneOutcome is what one retention pass did. It exists so the decision is
// testable without reading log output, and so "we deliberately deleted nothing"
// carries its reason instead of being indistinguishable from "there was nothing
// to delete".
type pruneOutcome struct {
	Deleted []string
	Failed  []string
	// Skipped is non-empty when the pass refused to consider deleting anything.
	Skipped string
}

// pruneBundles deletes the oldest bundles beyond keep. It is destructive and
// unattended, so it is written to refuse rather than to guess:
//
//   - it is only ever called after a push that succeeded and whose state was
//     persisted (see pushThenPrune) — pruning on a run whose upload failed would
//     leave fewer backups than the run started with;
//   - it re-lists the remote and requires the bundle just pushed to be in the
//     listing, so the fresh backup is verified to exist before an old one goes;
//   - a List error, or a listing whose entries it cannot order, deletes nothing
//     and says why: a partial listing is indistinguishable from "there are only
//     two backups here", and acting on one would delete the rest;
//   - only refs carrying this app's own marker are candidates, and anything
//     else in the folder is neither deleted nor counted;
//   - the newest bundle and the one just pushed are never deleted whatever the
//     arithmetic says, because an off-by-one here is unrecoverable.
//
// It returns rather than errors: the backup already succeeded, and losing that
// because the cleanup broke is the wrong trade.
func pruneBundles(ctx context.Context, store RemoteStore, keep int, pushed RemoteRef) pruneOutcome {
	if store == nil {
		return pruneOutcome{Skipped: "no remote is configured"}
	}
	if keep <= retainDisabled {
		return pruneOutcome{Skipped: "retention is disabled"}
	}
	if pushed.ID == "" {
		return pruneOutcome{Skipped: "the pushed bundle has no id, so a fresh backup cannot be verified"}
	}
	if err := ctx.Err(); err != nil {
		return pruneOutcome{Skipped: fmt.Sprintf("context ended before pruning: %v", err)}
	}

	refs, err := store.List(ctx)
	if err != nil {
		return pruneOutcome{Skipped: fmt.Sprintf("listing the remote failed (%v)", err)}
	}

	ours := make([]RemoteRef, 0, len(refs))
	for _, ref := range refs {
		// A bundle that is not ours survives and does not count towards keep.
		// The folder backend may hold anything a person copied in; a Drive
		// folder may hold anything at all.
		if !ref.AppOwned {
			continue
		}
		// Retention is an ordering over our own bundles, so an entry we cannot
		// place in that order makes the whole listing unusable. Refusing is the
		// only safe answer: treating an unreadable timestamp as "very old" is
		// how the oldest surviving backup gets deleted first.
		if ref.ID == "" || ref.Modified.IsZero() {
			return pruneOutcome{
				Skipped: fmt.Sprintf("the remote listing has an entry with no id or timestamp (%q)", ref.Name),
			}
		}
		ours = append(ours, ref)
	}

	verified := false
	for _, ref := range ours {
		if ref.ID == pushed.ID {
			verified = true
			break
		}
	}
	if !verified {
		return pruneOutcome{
			Skipped: fmt.Sprintf("the bundle just pushed (%s) is not in the remote listing", pushed.ID),
		}
	}

	// Sort here rather than trusting the backend's ordering: what gets deleted
	// depends on it. The id tiebreak only makes equal timestamps deterministic.
	sort.SliceStable(ours, func(i, j int) bool {
		if !ours[i].Modified.Equal(ours[j].Modified) {
			return ours[i].Modified.After(ours[j].Modified)
		}
		return ours[i].ID > ours[j].ID
	})
	if len(ours) <= keep {
		return pruneOutcome{}
	}
	// Belt and braces. keep is already at least minRetainBundles, so neither of
	// these can be in the deletable tail today; they are named explicitly so a
	// future change to the floor, or a clock skew that sorts the new bundle
	// down the list, cannot make the newest backup disappear.
	protected := map[string]struct{}{pushed.ID: {}, ours[0].ID: {}}

	outcome := pruneOutcome{}
	for _, ref := range ours[keep:] {
		if _, safe := protected[ref.ID]; safe {
			continue
		}
		if err := ctx.Err(); err != nil {
			outcome.Skipped = fmt.Sprintf("context ended part way through pruning: %v", err)
			break
		}
		if err := store.Delete(ctx, ref.ID); err != nil {
			// One failed delete is a slightly larger history, not a failed
			// backup. Record it and keep going: the next bundle may well be
			// deletable, and the next run will retry this one.
			outcome.Failed = append(outcome.Failed, fmt.Sprintf("%s: %v", ref.Name, err))
			continue
		}
		outcome.Deleted = append(outcome.Deleted, ref.Name)
	}
	return outcome
}

// pushThenPrune is the only path that reaches pruneBundles. The ordering is the
// point: nothing is deleted unless push returned a ref and no error, so a run
// that could not upload can only ever leave the history as long as it found it.
func pushThenPrune(
	ctx context.Context, store RemoteStore, keep int, push func(context.Context) (RemoteRef, error),
) (RemoteRef, error) {
	ref, err := push(ctx)
	if err != nil {
		return RemoteRef{}, err
	}
	logPrune(store, pruneBundles(ctx, store, keep, ref))
	return ref, nil
}

// logPrune reports retention at a level an operator watching the server log
// will actually see. Deleting a backup is not a debug-level event.
func logPrune(store RemoteStore, outcome pruneOutcome) {
	kind := "remote"
	if store != nil {
		kind = store.Kind()
	}
	if outcome.Skipped != "" {
		log.Printf("backup retention on %s: deleted nothing: %s", kind, outcome.Skipped)
	}
	if len(outcome.Deleted) > 0 {
		log.Printf("backup retention on %s: deleted %d old bundle(s): %s",
			kind, len(outcome.Deleted), strings.Join(outcome.Deleted, ", "))
	}
	for _, failure := range outcome.Failed {
		log.Printf("backup retention on %s: could not delete %s (the backup itself succeeded)", kind, failure)
	}
}

// DueForBackup reports whether a scheduled push should run now. It is pure so
// the schedule decision is testable without waiting on a clock.
func DueForBackup(cfg Config, last, now time.Time) bool {
	if !cfg.AutoBackup || cfg.Kind == "" {
		return false
	}
	return now.Sub(last) >= backupInterval(cfg)
}

// RunBackupLoop drives the scheduler until stop is closed. It checks once at
// startup (so a workspace with no backup yet gets one without waiting a full
// interval) and then on every tick.
func (m *RemoteManager) RunBackupLoop(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	select {
	case <-ctx.Done():
		return
	default:
	}
	m.maybeBackup(ctx)
	ticker := time.NewTicker(backupCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.maybeBackup(ctx)
		}
	}
}

// MaybeBackup pushes the workspace if one is due. It is a no-op when auto
// backup is off or no Remote is configured, so it is cheap to call often.
func (m *RemoteManager) MaybeBackup() {
	m.maybeBackup(context.Background())
}

func (m *RemoteManager) maybeBackup(parent context.Context) {
	if !DueForBackup(m.Load(), m.LastBackupAt(), time.Now()) {
		return
	}
	ctx, cancel := context.WithTimeout(parent, backupTimeout)
	defer cancel()
	if err := m.acquireBackup(ctx); err != nil {
		log.Printf("scheduled backup: %v", err)
		return
	}
	defer m.releaseBackup()
	// Re-check under the lock: config or state may have changed while waiting.
	cfg := m.Load()
	if !DueForBackup(cfg, m.LastBackupAt(), time.Now()) {
		return
	}
	prepared, err := m.prepareSync(ctx)
	if err != nil {
		log.Printf("scheduled backup: sync check: %v", err)
		return
	}
	defer prepared.Close()
	status := prepared.status
	if status.State == SyncSynced {
		return
	}
	if status.State == SyncRemoteNewer || status.State == SyncConflict {
		log.Printf("scheduled backup paused: %s", status.State)
		return
	}
	ref, err := m.pushPrepared(ctx, prepared)
	if err != nil {
		log.Printf("scheduled backup failed: %v", err)
		return
	}
	log.Printf("scheduled backup pushed %s to %s", ref.Name, prepared.remote.Kind())
}

func (m *RemoteManager) acquireBackup(ctx context.Context) error {
	select {
	case m.backupMu <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *RemoteManager) releaseBackup() {
	<-m.backupMu
}

type scopeBundleFile struct {
	file *os.File
	size int64
	hash string
}

func (b *scopeBundleFile) Close() {
	if b == nil || b.file == nil {
		return
	}
	name := b.file.Name()
	_ = b.file.Close()
	_ = os.Remove(name)
	b.file = nil
}

func (b *scopeBundleFile) Reader() io.Reader {
	return io.NewSectionReader(b.file, 0, b.size)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w *contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(data)
}

// buildScopeBundle writes directly to a temporary file. The archive builder
// and canonical hash therefore never require a second full in-memory copy.
func buildScopeBundle(ctx context.Context, scope project.ExportScopeResult) (*scopeBundleFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "nodevas-remote-*.veproj")
	if err != nil {
		return nil, err
	}
	bundle := &scopeBundleFile{file: file}
	ok := false
	defer func() {
		if !ok {
			bundle.Close()
		}
	}()

	destination := &contextWriter{ctx: ctx, w: file}
	if scope.IsBundle() {
		err = project.BuildProjectBundle(destination, scope)
	} else {
		err = project.BuildProjectArchive(destination, scope.Root(), scope.SharedStatuses())
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	bundle.size = info.Size()
	bundle.hash, err = readerSHA256(ctx, file, bundle.size)
	if err != nil {
		return nil, err
	}
	ok = true
	return bundle, nil
}

func workspaceScope(ctx context.Context, pm *project.ProjectManager) (project.ExportScopeResult, error) {
	if err := ctx.Err(); err != nil {
		return project.ExportScopeResult{}, err
	}
	if pm == nil {
		return project.ExportScopeResult{}, fmt.Errorf("project manager is unavailable")
	}
	scope, err := pm.ExportScope(".")
	if err != nil {
		return project.ExportScopeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return project.ExportScopeResult{}, err
	}
	return scope, nil
}

func bundleName(scope project.ExportScopeResult) string {
	name := scope.Root().Label
	if name == "" || name == "." {
		name = filepath.Base(scope.Root().Path)
	}
	return name + "-" + time.Now().Format("20060102-150405")
}

func pushBundle(
	ctx context.Context, remote RemoteStore, scope project.ExportScopeResult, bundle *scopeBundleFile,
) (RemoteRef, error) {
	ref, err := remote.Push(ctx, bundleName(scope), bundle.Reader(), bundle.size)
	if err != nil {
		return RemoteRef{}, err
	}
	if ref.Hash == "" {
		ref.Hash = bundle.hash
	}
	return ref, nil
}

type preparedSync struct {
	status SyncStatus
	cfg    Config
	remote RemoteStore
	scope  project.ExportScopeResult
	bundle *scopeBundleFile
}

func (p *preparedSync) Close() {
	if p != nil && p.bundle != nil {
		p.bundle.Close()
		p.bundle = nil
	}
}

func (m *RemoteManager) prepareSync(ctx context.Context) (*preparedSync, error) {
	cfg := m.Load()
	state := m.loadState()
	prepared := &preparedSync{
		cfg: cfg,
		status: SyncStatus{
			State:              SyncDisabled,
			LastPushedHash:     state.LastPushedHash,
			LastPushedBundleID: state.LastPushedBundleID,
			LastBackupAt:       state.LastBackupAt,
		},
	}
	if cfg.Kind == "" || m.pm == nil {
		return prepared, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remote, err := m.BuildRemote(cfg)
	if err != nil {
		return nil, err
	}
	scope, err := workspaceScope(ctx, m.pm)
	if err != nil {
		return nil, err
	}
	bundle, err := buildScopeBundle(ctx, scope)
	if err != nil {
		return nil, err
	}
	prepared.remote = remote
	prepared.scope = scope
	prepared.bundle = bundle
	prepared.status.LocalHash = bundle.hash

	refs, err := remote.List(ctx)
	if err != nil {
		prepared.Close()
		return nil, err
	}
	if len(refs) > 0 {
		latest := refs[0]
		prepared.status.RemoteLatest = &latest
		prepared.status.RemoteHash = latest.Hash
	}
	prepared.status.State = classifySync(
		prepared.status.LocalHash,
		state.LastPushedHash,
		state.LastPushedBundleID,
		state.KnownBundleIDs,
		prepared.status.RemoteLatest,
	)
	return prepared, nil
}

// pushPrepared uploads the prepared bundle, records it as the new sync point,
// and only then lets retention run. Recording the state before pruning matters:
// if the process dies between the two, the next run still knows a good backup
// exists, and the prune it skipped simply happens later.
func (m *RemoteManager) pushPrepared(ctx context.Context, prepared *preparedSync) (RemoteRef, error) {
	return pushThenPrune(ctx, prepared.remote, retainCount(prepared.cfg),
		func(ctx context.Context) (RemoteRef, error) {
			ref, err := pushBundle(ctx, prepared.remote, prepared.scope, prepared.bundle)
			if err != nil {
				return RemoteRef{}, err
			}
			now := time.Now()
			if ref.Modified.IsZero() {
				ref.Modified = now
			}
			state := m.loadState()
			state.LastBackupAt = now
			state.LastPushedBundleID = ref.ID
			state.LastPushedHash = prepared.bundle.hash
			state.LastPushedBackend = prepared.cfg.Kind
			state.LastPushedModified = ref.Modified
			state.KnownBundleIDs = rememberBundle(state.KnownBundleIDs, ref.ID)
			if err := m.saveState(state); err != nil {
				return RemoteRef{}, err
			}
			return ref, nil
		})
}

func (m *RemoteManager) syncStatus(ctx context.Context) (SyncStatus, error) {
	prepared, err := m.prepareSync(ctx)
	if err != nil {
		return SyncStatus{}, err
	}
	defer prepared.Close()
	return prepared.status, nil
}

func classifySync(localHash, lastHash, lastID string, known []string, latest *RemoteRef) SyncState {
	if latest == nil {
		return SyncRemoteMiss
	}
	if lastID == "" {
		return SyncRemoteNewer
	}
	localMatchesLast := localHash == lastHash
	// The question is not "is the newest bundle our sync point" but "did the
	// newest bundle come from somewhere else". A manual per-project backup this
	// instance uploaded is newer than the sync point and entirely expected;
	// treating it as foreign turned pressing "back up now" into a standing
	// remote-newer warning that also blocked every scheduled flush after it.
	remoteMatchesLast := latest.ID == lastID ||
		(latest.Hash != "" && latest.Hash == lastHash) ||
		slices.Contains(known, latest.ID)
	switch {
	case localMatchesLast && remoteMatchesLast:
		return SyncSynced
	case localMatchesLast:
		return SyncRemoteNewer
	case remoteMatchesLast:
		return SyncLocalNewer
	default:
		return SyncConflict
	}
}

// SyncStatus compares the current local snapshot with the newest remote one.
func (m *RemoteManager) SyncStatus(ctx context.Context) (SyncStatus, error) {
	if err := m.acquireBackup(ctx); err != nil {
		return SyncStatus{}, err
	}
	defer m.releaseBackup()
	return m.syncStatus(ctx)
}

// FlushBackup explicitly pushes the whole local workspace when that cannot
// silently overwrite a newer remote snapshot. It is used by the UI and close
// flow; it works even when scheduled backup is disabled.
func (m *RemoteManager) FlushBackup(ctx context.Context) (RemoteRef, error) {
	if err := m.acquireBackup(ctx); err != nil {
		return RemoteRef{}, err
	}
	defer m.releaseBackup()
	prepared, err := m.prepareSync(ctx)
	if err != nil {
		return RemoteRef{}, err
	}
	defer prepared.Close()
	status := prepared.status
	switch status.State {
	case SyncDisabled:
		return RemoteRef{}, ErrNoRemote
	case SyncRemoteNewer, SyncConflict:
		return RemoteRef{}, ErrSyncConflict
	case SyncSynced:
		if status.RemoteLatest == nil {
			return RemoteRef{}, nil
		}
		return *status.RemoteLatest, nil
	}
	return m.pushPrepared(ctx, prepared)
}

// FinalBackup is called after HTTP requests stop accepting new work. It only
// flushes when scheduled backup is enabled; an explicit UI flush is available
// when the user chose manual-only backup.
func (m *RemoteManager) FinalBackup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.Load().AutoBackup {
		return nil
	}
	_, err := m.FlushBackup(ctx)
	return err
}

// PushScope builds the export bundle for an already-resolved scope and uploads
// it under a timestamped name (so the Remote keeps versions). Both the manual
// push endpoint and the scheduler go through here; the caller resolves the
// scope so it can map a bad project name to 400 separately from an upload
// failure.
//
// It deliberately does not prune. A manual bundle is marked exactly like a
// scheduled one — nothing in the object distinguishes them, and inventing a
// distinction would only mean keeping bundles the operator cannot see out of
// the count — so manual pushes do count towards retention and are eligible for
// deletion by a later scheduled run. What they do not do is delete anything
// themselves: the person who pressed "back up now" asked for a backup, not for
// a cleanup, and the scheduler is both the thing that produces the volume and
// the right place to bound it.
func (m *RemoteManager) PushScope(
	ctx context.Context, Remote RemoteStore, scope project.ExportScopeResult,
) (RemoteRef, error) {
	if err := m.acquireBackup(ctx); err != nil {
		return RemoteRef{}, err
	}
	defer m.releaseBackup()
	bundle, err := buildScopeBundle(ctx, scope)
	if err != nil {
		return RemoteRef{}, err
	}
	defer bundle.Close()
	ref, err := pushBundle(ctx, Remote, scope, bundle)
	if err != nil {
		return RemoteRef{}, err
	}
	// Not the sync point — that stays the workspace snapshot's — but ours all
	// the same, so the sync check can tell it from a bundle another machine
	// pushed. A save failure does not undo the backup: the upload is the thing
	// the caller asked for, and the cost of a lost memo is one spurious
	// remote-newer warning, not a lost bundle.
	state := m.loadState()
	state.KnownBundleIDs = rememberBundle(state.KnownBundleIDs, ref.ID)
	if err := m.saveState(state); err != nil {
		log.Printf("remote: could not record pushed bundle %s: %v", ref.ID, err)
	}
	return ref, nil
}
