package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

func TestCommittedNodeDeleteReturnsSuccessAndRestartCleansSourceAndDraft(t *testing.T) {
	root, st := folderProject(t)
	source := st.NodePath("alpha")
	if err := st.SaveDraft("alpha", "unsaved"); err != nil {
		t.Fatal(err)
	}
	draft := st.draftPath("alpha")
	st.deleteCleanupFault = func(deleteCleanupRecord) error { return errors.New("injected cleanup failure") }

	outcome, err := st.DeleteNode(identity.Local, "alpha")
	if err != nil {
		t.Fatalf("committed delete returned an error: %v", err)
	}
	if !outcome.CleanupPending || outcome.TrashFile == "" {
		t.Fatalf("delete outcome = %+v", outcome)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("alpha") != nil {
		t.Fatal("authoritative graph delete did not commit")
	}
	for _, path := range []string{source, draft} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fault injection did not preserve %s for retry: %v", path, err)
		}
	}
	if entries := cleanupQueueEntries(t, st); len(entries) == 0 {
		t.Fatal("committed delete left no durable cleanup marker")
	}

	restarted := NewStore(root)
	for _, path := range []string{source, draft} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("restart did not clean %s: %v", path, err)
		}
	}
	if entries := cleanupQueueEntries(t, restarted); len(entries) != 0 {
		t.Fatalf("restart left completed cleanup markers: %v", entries)
	}
}

func TestCommittedPageDeleteReturnsSuccessAndRestartCleansSource(t *testing.T) {
	root, st := folderProject(t)
	page, _, _, err := st.CreateNodePage(identity.Local, "alpha", "Notes", PageFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	source := st.NodePagePath("alpha", page.ID, page.Format)
	st.deleteCleanupFault = func(deleteCleanupRecord) error { return errors.New("injected cleanup failure") }

	outcome, err := st.DeleteNodePage(identity.Local, "alpha", page.ID)
	if err != nil {
		t.Fatalf("committed page delete returned an error: %v", err)
	}
	if !outcome.CleanupPending || outcome.TrashFile == "" {
		t.Fatalf("page delete outcome = %+v", outcome)
	}
	pages, err := st.ListNodePages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 0 {
		t.Fatalf("authoritative manifest delete did not commit: %+v", pages)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("fault injection did not preserve page source for retry: %v", err)
	}

	restarted := NewStore(root)
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("restart did not clean page source: %v", err)
	}
	if entries := cleanupQueueEntries(t, restarted); len(entries) != 0 {
		t.Fatalf("restart left completed page cleanup markers: %v", entries)
	}
	if _, err := restarted.RestoreTrash(outcome.TrashFile); err != nil {
		t.Fatalf("cleanup retry damaged restorable trash: %v", err)
	}
}

func TestPageCleanupSurvivesParentNodeDeletionAndNestedFolder(t *testing.T) {
	root, st := folderProject(t)
	if _, err := st.CreateNodeFolder("chapter"); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveNodesToFolder([]string{"alpha"}, "chapter"); err != nil {
		t.Fatal(err)
	}
	page, _, _, err := st.CreateNodePage(identity.Local, "alpha", "Notes", PageFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	source := st.NodePagePath("alpha", page.ID, page.Format)
	st.deleteCleanupFault = func(record deleteCleanupRecord) error {
		if record.Kind == deleteCleanupKindPage {
			return errors.New("injected page cleanup failure")
		}
		return nil
	}
	if outcome, err := st.DeleteNodePage(identity.Local, "alpha", page.ID); err != nil || !outcome.CleanupPending {
		t.Fatalf("page delete = %+v, %v", outcome, err)
	}
	st.deleteCleanupFault = nil
	if _, err := st.DeleteNode(identity.Local, "alpha"); err != nil {
		t.Fatalf("delete parent node: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("pending nested page source disappeared before retry: %v", err)
	}

	restarted := NewStore(root)
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("restart lost the nested page cleanup after parent deletion: %v", err)
	}
	if entries := cleanupQueueEntries(t, restarted); len(entries) != 0 {
		t.Fatalf("nested page cleanup marker survived: %v", entries)
	}
}

func TestRestartNeverExecutesAnUncommittedDeleteMarker(t *testing.T) {
	root, st := folderProject(t)
	source := st.NodePath("alpha")
	st.mu.Lock()
	queued, err := st.queueDeleteCleanupsLocked([]deleteCleanupRecord{{
		Kind: deleteCleanupKindNodes, Nodes: []string{"alpha"},
	}})
	st.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("queued records = %d", len(queued))
	}

	restarted := NewStore(root)
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("restart executed a marker whose graph commit never happened: %v", err)
	}
	if entries := cleanupQueueEntries(t, restarted); len(entries) != 0 {
		t.Fatalf("restart retained abandoned precommit marker: %v", entries)
	}
}

func TestUnsafeOrCorruptCleanupQueueFailsDeleteBeforeCommit(t *testing.T) {
	tests := map[string]func(t *testing.T, st *Store){
		"malformed": func(t *testing.T, st *Store) {
			writeCleanupFixture(t, st, "delete-malformed.json", []byte("{"))
		},
		"unknown field": func(t *testing.T, st *Store) {
			writeCleanupFixture(t, st, "delete-unknown.json", []byte(`{"kind":"nodes","nodes":["ghost"],"path":"../../outside"}`))
		},
		"symbolic link": func(t *testing.T, st *Store) {
			dir := st.deleteCleanupDir()
			if err := MkdirAllProjectPath(st.root, dir, 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "marker.json")
			if err := os.WriteFile(outside, []byte(`{"kind":"nodes","nodes":["ghost"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			requireSymlink(t, outside, filepath.Join(dir, "delete-linked.json"))
		},
	}
	for name, arrange := range tests {
		t.Run(name, func(t *testing.T) {
			_, st := folderProject(t)
			source := st.NodePath("alpha")
			arrange(t, st)

			_, err := st.DeleteNode(identity.Local, "alpha")
			var unavailable *DeleteCleanupUnavailableError
			if !errors.As(err, &unavailable) || err.Error() != "delete cleanup is temporarily unavailable" {
				t.Fatalf("delete error = %v, want sanitized typed queue error", err)
			}
			graph, _, loadErr := st.LoadGraph()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if graph.NodeByID("alpha") == nil {
				t.Fatal("unsafe cleanup queue allowed graph commit")
			}
			if _, statErr := os.Stat(source); statErr != nil {
				t.Fatalf("unsafe cleanup queue touched source: %v", statErr)
			}
		})
	}
}

func TestCleanupQueueBoundsFailClosedAndLargeBatchRecordsStaySmall(t *testing.T) {
	_, st := folderProject(t)
	for index := range maxDeleteCleanupRecords {
		record := deleteCleanupRecord{Kind: deleteCleanupKindNodes, Nodes: []string{fmt.Sprintf("ghost-%03d", index)}}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		writeCleanupFixture(t, st, fmt.Sprintf("delete-%032x.json", index), append(data, '\n'))
	}
	if _, err := st.DeleteNode(identity.Local, "alpha"); err == nil {
		t.Fatal("saturated cleanup queue allowed a new delete")
	}
	graph, _, err := st.LoadGraph()
	if err != nil || graph.NodeByID("alpha") == nil {
		t.Fatalf("saturated queue changed graph: graph=%+v err=%v", graph, err)
	}

	ids := make([]string, MaxGraphNodes)
	for index := range ids {
		ids[index] = fmt.Sprintf("n%05d-%s", index, strings.Repeat("x", 110))
	}
	records, err := nodeDeleteCleanupRecords(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) > maxDeleteCleanupRecords {
		t.Fatalf("maximum batch needs %d cleanup records, limit %d", len(records), maxDeleteCleanupRecords)
	}
	total := 0
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if len(data)+1 > maxDeleteCleanupRecordBytes {
			t.Fatalf("cleanup record = %d bytes, limit %d", len(data)+1, maxDeleteCleanupRecordBytes)
		}
		total += len(data) + 1
	}
	if total > maxDeleteCleanupQueueBytes {
		t.Fatalf("maximum batch cleanup = %d bytes, queue limit %d", total, maxDeleteCleanupQueueBytes)
	}
}

func cleanupQueueEntries(t *testing.T, st *Store) []os.DirEntry {
	t.Helper()
	entries, err := st.ReadDir(st.deleteCleanupDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func writeCleanupFixture(t *testing.T, st *Store, name string, data []byte) {
	t.Helper()
	if err := MkdirAllProjectPath(st.root, st.deleteCleanupDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteAtomic(filepath.Join(st.deleteCleanupDir(), name), data); err != nil {
		t.Fatal(err)
	}
}
