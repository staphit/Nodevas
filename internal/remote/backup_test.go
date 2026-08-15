package remote

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testArchive(t *testing.T, modified time.Time) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body string
	}{
		{name: "z.txt", body: "last"},
		{name: "a.txt", body: "first"},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: modified}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBytesSHA256IgnoresArchiveTimestamps(t *testing.T) {
	first := testArchive(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	second := testArchive(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if rawSHA256(first) == rawSHA256(second) {
		t.Fatal("test archives unexpectedly have identical raw hashes")
	}
	if bytesSHA256(first) != bytesSHA256(second) {
		t.Fatal("canonical archive hashes differ when only timestamps change")
	}
}

func TestClassifySync(t *testing.T) {
	latest := &RemoteRef{ID: "remote-2", Hash: "hash-2"}
	// A per-project bundle this instance uploaded manually: newer than the
	// workspace sync point, carrying a different hash, and entirely ours.
	ownManual := &RemoteRef{ID: "manual-1", Hash: "hash-manual"}
	cases := []struct {
		name                string
		local, last, lastID string
		known               []string
		latest              *RemoteRef
		want                SyncState
	}{
		{"no remote", "local", "", "", nil, nil, SyncRemoteMiss},
		{"untracked remote", "local", "", "", nil, latest, SyncRemoteNewer},
		{"synced by id", "hash-2", "hash-2", "remote-2", nil, latest, SyncSynced},
		{"local newer", "hash-local", "hash-2", "remote-2", nil, latest, SyncLocalNewer},
		{"remote newer", "hash-2", "hash-2", "remote-1", nil, &RemoteRef{ID: "remote-2", Hash: "hash-3"}, SyncRemoteNewer},
		{"both changed", "hash-local", "hash-1", "remote-1", nil, latest, SyncConflict},
		// The regression this list exists for: a manual backup from this very
		// instance must not read as somebody else's newer snapshot — that
		// misreading also made FlushBackup refuse, so one press of "back up
		// now" silently stopped every scheduled backup after it.
		{"own manual bundle on top", "hash-2", "hash-2", "remote-2",
			[]string{"manual-1"}, ownManual, SyncSynced},
		{"own manual bundle, local edited since", "hash-local", "hash-2", "remote-2",
			[]string{"manual-1"}, ownManual, SyncLocalNewer},
		{"foreign bundle despite known list", "hash-2", "hash-2", "remote-2",
			[]string{"manual-1"}, &RemoteRef{ID: "other-machine", Hash: "hash-x"}, SyncRemoteNewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySync(tc.local, tc.last, tc.lastID, tc.known, tc.latest); got != tc.want {
				t.Fatalf("classifySync = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRememberBundleDeduplicatesAndBounds(t *testing.T) {
	var ids []string
	for i := 0; i < maxKnownBundles+10; i++ {
		ids = rememberBundle(ids, fmt.Sprintf("bundle-%d", i))
	}
	if len(ids) != maxKnownBundles {
		t.Fatalf("len = %d, want %d", len(ids), maxKnownBundles)
	}
	// Re-remembering moves an ID to the newest end instead of duplicating it.
	ids = rememberBundle(ids, ids[0])
	if len(ids) != maxKnownBundles {
		t.Fatalf("after re-remember: len = %d, want %d", len(ids), maxKnownBundles)
	}
	if ids[len(ids)-1] != fmt.Sprintf("bundle-%d", 10) {
		t.Fatalf("re-remembered ID is not at the newest end: %v", ids[len(ids)-3:])
	}
	if rememberBundle(ids, "") == nil || len(rememberBundle(ids, "")) != len(ids) {
		t.Fatal("an empty ID must be ignored")
	}
}

func TestSyncStatusUsesBoundedOperationGate(t *testing.T) {
	manager := NewManager(nil)
	if err := manager.acquireBackup(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := manager.SyncStatus(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SyncStatus while gate is occupied returned %v", err)
	}
	manager.releaseBackup()
	if err := manager.acquireBackup(context.Background()); err != nil {
		t.Fatalf("gate remained occupied after release: %v", err)
	}
	manager.releaseBackup()
}

// fakeRemote is an in-memory RemoteStore that records what retention asked it
// to do. Every failure mode the pruner has to survive — a List that errors, a
// Delete that errors — is a field here rather than a separate stub, so a test
// reads as one sentence about the remote's behaviour.
type fakeRemote struct {
	bundles   []RemoteRef
	listErr   error
	deleteErr map[string]error
	pushErr   error

	listed  int
	deleted []string
}

func (f *fakeRemote) Kind() string { return "fake" }

func (f *fakeRemote) Push(_ context.Context, name string, _ io.Reader, _ int64) (RemoteRef, error) {
	if f.pushErr != nil {
		return RemoteRef{}, f.pushErr
	}
	ref := RemoteRef{
		ID:       name,
		Name:     name,
		Modified: time.Now(),
		AppOwned: true,
	}
	f.bundles = append([]RemoteRef{ref}, f.bundles...)
	return ref, nil
}

func (f *fakeRemote) Pull(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeRemote) List(context.Context) ([]RemoteRef, error) {
	f.listed++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]RemoteRef(nil), f.bundles...), nil
}

func (f *fakeRemote) Delete(_ context.Context, id string) error {
	if err := f.deleteErr[id]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, id)
	kept := f.bundles[:0]
	for _, ref := range f.bundles {
		if ref.ID != id {
			kept = append(kept, ref)
		}
	}
	f.bundles = kept
	return nil
}

func (f *fakeRemote) ids() []string {
	ids := make([]string, 0, len(f.bundles))
	for _, ref := range f.bundles {
		ids = append(ids, ref.ID)
	}
	return ids
}

// scheduledBundles builds count app-owned bundles, newest first, one hour
// apart, ending at end. The names carry their age so a failure message says
// which bundle survived.
func scheduledBundles(count int, end time.Time) []RemoteRef {
	refs := make([]RemoteRef, 0, count)
	for i := 0; i < count; i++ {
		refs = append(refs, RemoteRef{
			ID:       fmt.Sprintf("bundle-%02d", i),
			Name:     fmt.Sprintf("bundle-%02d", i),
			Modified: end.Add(-time.Duration(i) * time.Hour),
			AppOwned: true,
		})
	}
	return refs
}

func TestPruningKeepsExactlyTheConfiguredCountAndAlwaysTheNewest(t *testing.T) {
	// The whole point of retention: an unbounded history becomes a bounded one,
	// and the bundle that was just uploaded is the one that must survive it.
	now := time.Now()
	store := &fakeRemote{bundles: scheduledBundles(20, now)}
	pushed := store.bundles[0]

	outcome := pruneBundles(context.Background(), store, 14, pushed)

	if outcome.Skipped != "" {
		t.Fatalf("pruning refused to run: %s", outcome.Skipped)
	}
	if len(store.bundles) != 14 {
		t.Fatalf("kept %d bundles, want 14: %v", len(store.bundles), store.ids())
	}
	if store.bundles[0].ID != pushed.ID {
		t.Fatalf("newest bundle is %q, want the one just pushed (%q)", store.bundles[0].ID, pushed.ID)
	}
	if len(outcome.Deleted) != 6 {
		t.Fatalf("reported %d deletions, want 6: %v", len(outcome.Deleted), outcome.Deleted)
	}
}

func TestPruningKeepsEverythingWhenTheRemoteHoldsFewerThanTheCount(t *testing.T) {
	// The common case on a young workspace. Nothing to do must mean nothing
	// done, not "delete down to some other number".
	store := &fakeRemote{bundles: scheduledBundles(3, time.Now())}
	outcome := pruneBundles(context.Background(), store, 14, store.bundles[0])
	if len(store.deleted) != 0 {
		t.Fatalf("deleted %v from a remote holding fewer bundles than the retention count", store.deleted)
	}
	if outcome.Skipped != "" {
		t.Fatalf("unexpected refusal: %s", outcome.Skipped)
	}
}

func TestAFailedPushPrunesNothing(t *testing.T) {
	// Pruning after a run that could not upload would leave the workspace with
	// fewer backups than the run started with — the exact opposite of the job.
	store := &fakeRemote{bundles: scheduledBundles(20, time.Now())}
	uploadFailed := errors.New("upload failed")

	_, err := pushThenPrune(context.Background(), store, 14,
		func(context.Context) (RemoteRef, error) { return RemoteRef{}, uploadFailed })

	if !errors.Is(err, uploadFailed) {
		t.Fatalf("pushThenPrune returned %v, want the push error", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("a failed push deleted %v", store.deleted)
	}
	if store.listed != 0 {
		t.Fatal("a failed push still listed the remote, so it was considering deletions")
	}
}

func TestAFailedListPrunesNothing(t *testing.T) {
	// A listing that errors is indistinguishable from a listing that is short.
	// Acting on the short one deletes every backup it failed to mention.
	store := &fakeRemote{bundles: scheduledBundles(20, time.Now()), listErr: errors.New("network is down")}
	outcome := pruneBundles(context.Background(), store, 14, RemoteRef{ID: "bundle-00"})
	if len(store.deleted) != 0 {
		t.Fatalf("deleted %v despite an unreadable listing", store.deleted)
	}
	if !strings.Contains(outcome.Skipped, "network is down") {
		t.Fatalf("refusal did not say why: %q", outcome.Skipped)
	}
}

func TestAListingWithAnUndatedBundlePrunesNothing(t *testing.T) {
	// Retention is an ordering, and an entry with no timestamp cannot be placed
	// in it. Treating "unknown" as "oldest" would delete the wrong bundle
	// first, so the whole pass refuses instead.
	now := time.Now()
	store := &fakeRemote{bundles: scheduledBundles(20, now)}
	store.bundles[9].Modified = time.Time{}

	outcome := pruneBundles(context.Background(), store, 14, store.bundles[0])

	if len(store.deleted) != 0 {
		t.Fatalf("deleted %v from a listing it could not order", store.deleted)
	}
	if outcome.Skipped == "" {
		t.Fatal("an uninterpretable listing was pruned without complaint")
	}
}

func TestAPushThatIsNotInTheListingPrunesNothing(t *testing.T) {
	// The fresh backup has to be verified to exist before an old one is
	// destroyed. If the remote does not list what we just uploaded, we cannot
	// prove there is anything to fall back on.
	now := time.Now()
	store := &fakeRemote{bundles: scheduledBundles(20, now)}

	outcome := pruneBundles(context.Background(), store, 14, RemoteRef{ID: "never-landed"})

	if len(store.deleted) != 0 {
		t.Fatalf("deleted %v without confirming the new bundle exists", store.deleted)
	}
	if !strings.Contains(outcome.Skipped, "never-landed") {
		t.Fatalf("refusal did not name the missing bundle: %q", outcome.Skipped)
	}
}

func TestABundleWithoutTheAppsMarkerIsNeverDeleted(t *testing.T) {
	// The backup folder is a directory, and a Drive folder is a Drive folder:
	// both can hold files this app did not write. Those must survive, and they
	// must not consume a retention slot either, or their presence would push
	// out one of our own backups.
	now := time.Now()
	store := &fakeRemote{bundles: scheduledBundles(20, now)}
	stranger := RemoteRef{
		ID:       "someone-elses.veproj",
		Name:     "someone-elses.veproj",
		Modified: now.Add(-100 * time.Hour),
	}
	store.bundles = append(store.bundles, stranger)

	pruneBundles(context.Background(), store, 14, store.bundles[0])

	for _, id := range store.deleted {
		if id == stranger.ID {
			t.Fatal("deleted a bundle this app did not create")
		}
	}
	if len(store.bundles) != 15 {
		t.Fatalf("expected 14 of our bundles plus the stranger, got %v", store.ids())
	}
}

func TestAFailedDeleteLeavesTheBackupReportedAsSuccessful(t *testing.T) {
	// The backup succeeded. Failing the run because the cleanup broke would
	// trade a working backup for a tidy remote, which is the wrong way round —
	// and the next run retries the delete anyway.
	now := time.Now()
	store := &fakeRemote{
		bundles:   scheduledBundles(20, now),
		deleteErr: map[string]error{"bundle-16": errors.New("permission denied")},
	}
	pushed := store.bundles[0]

	ref, err := pushThenPrune(context.Background(), store, 14,
		func(context.Context) (RemoteRef, error) { return pushed, nil })

	if err != nil {
		t.Fatalf("a failed delete failed the backup: %v", err)
	}
	if ref.ID != pushed.ID {
		t.Fatalf("returned ref %q, want %q", ref.ID, pushed.ID)
	}
	if len(store.deleted) != 5 {
		t.Fatalf("stopped pruning at the first failure: deleted %v", store.deleted)
	}
	if len(store.bundles) != 15 {
		t.Fatalf("expected the undeletable bundle to survive alongside 14, got %v", store.ids())
	}
}

func TestRetentionBelowTheFloorIsClampedRatherThanRejected(t *testing.T) {
	// The setting lives in a JSON file a person can edit and may be incomplete,
	// so rejecting it at the API boundary would not protect the
	// deletion loop. Clamping in the one function the loop calls means every
	// caller agrees about what the number means. A negative value is a
	// different thing — an explicit "never delete" — and is not clamped.
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"an absent setting means the default", Config{}, defaultRetainBundles},
		{"one is clamped up to the floor", Config{RetainBundles: 1}, minRetainBundles},
		{"the floor itself is kept", Config{RetainBundles: minRetainBundles}, minRetainBundles},
		{"a larger setting is honoured", Config{RetainBundles: 60}, 60},
		{"a negative setting disables pruning", Config{RetainBundles: -1}, retainDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retainCount(tc.cfg); got != tc.want {
				t.Fatalf("retainCount = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDisabledRetentionDeletesNothing(t *testing.T) {
	// Someone who wants every version forever must be able to say so, and the
	// storage bill is then their decision rather than a bug.
	store := &fakeRemote{bundles: scheduledBundles(200, time.Now())}
	outcome := pruneBundles(context.Background(), store, retainCount(Config{RetainBundles: -1}), store.bundles[0])
	if len(store.deleted) != 0 {
		t.Fatalf("pruning ran with retention disabled: %v", store.deleted)
	}
	if outcome.Skipped == "" {
		t.Fatal("disabled retention did not say it was skipping")
	}
}

func TestFolderBackendOnlyRecognisesAndDeletesItsOwnBundles(t *testing.T) {
	// A directory offers the process no protection of its own, so the folder
	// backend has to recognise its uploads by the marker Push writes into the
	// name. A .veproj a person copied in by hand carries no marker: List must
	// not call it ours, and Delete must refuse it outright.
	dir := t.TempDir()
	store, err := NewFolderRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ours, err := store.Push(ctx, "workspace-20260804-120000", bytes.NewReader([]byte("payload")), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !ours.AppOwned {
		t.Fatalf("Push returned a ref it does not recognise as ours: %q", ours.Name)
	}
	stranger := "handmade" + RemoteBundleExt
	if err := os.WriteFile(filepath.Join(dir, stranger), []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}

	refs, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if (ref.ID == stranger) == ref.AppOwned {
			t.Fatalf("List marked %q as AppOwned=%v", ref.ID, ref.AppOwned)
		}
	}
	if err := store.Delete(ctx, stranger); err == nil {
		t.Fatal("Delete removed a bundle this app did not create")
	}
	if _, err := os.Stat(filepath.Join(dir, stranger)); err != nil {
		t.Fatalf("the refused bundle is gone anyway: %v", err)
	}
	if err := store.Delete(ctx, ours.ID); err != nil {
		t.Fatalf("Delete refused our own bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ours.ID)); !os.IsNotExist(err) {
		t.Fatalf("our bundle survived its own deletion: %v", err)
	}
	// Deleting again is success: a prune that half failed must be retryable
	// without reporting work that is already done as an error.
	if err := store.Delete(ctx, ours.ID); err != nil {
		t.Fatalf("deleting an already-deleted bundle returned %v", err)
	}
}

func TestCanonicalHashObservesCancellation(t *testing.T) {
	archive := testArchive(t, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readerSHA256(ctx, bytes.NewReader(archive), int64(len(archive))); !errors.Is(err, context.Canceled) {
		t.Fatalf("readerSHA256 returned %v", err)
	}
}
