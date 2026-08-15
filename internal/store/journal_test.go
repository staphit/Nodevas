package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
)

// newJournalTestStore writes a tiny graph and returns a store for it.
func newJournalTestStore(t *testing.T, nodeIDs ...string) *Store {
	t.Helper()
	root := t.TempDir()
	g := &engine.Graph{Version: 1}
	for _, id := range nodeIDs {
		g.Nodes = append(g.Nodes, &engine.Node{ID: id})
	}
	data, err := engine.MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return NewStore(root)
}

// withCompactThreshold lowers the soft threshold for one test.
func withCompactThreshold(t *testing.T, limit int64) {
	t.Helper()
	previous := journalCompactBytes
	journalCompactBytes = limit
	t.Cleanup(func() { journalCompactBytes = previous })
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// lifecycle records status changes, cycling through the states so the replay
// under test has real transitions rather than one repeated value.
type lifecycle struct{ round int }

func (l *lifecycle) drive(t *testing.T, st *Store, rounds int, nodes ...string) {
	t.Helper()
	statuses := []engine.Status{
		engine.StatusStarted, engine.StatusInProgress,
		engine.StatusDone, engine.StatusReady,
	}
	for i := 0; i < rounds; i++ {
		status := statuses[l.round%len(statuses)]
		l.round++
		for _, node := range nodes {
			if _, err := st.SetStatus(node, status, "tester", "紀錄"); err != nil {
				t.Fatalf("SetStatus(%s, %s): %v", node, status, err)
			}
		}
	}
}

// driveUntilRotation records until the journal has rotated exactly once, then
// a few events more so the live journal carries a tail past the checkpoint.
// Stopping at one rotation keeps journal.jsonl.1 + journal.jsonl a complete
// copy of everything ever appended, which is what fullReplayJSON compares to.
func driveUntilRotation(t *testing.T, st *Store, nodes ...string) {
	t.Helper()
	var l lifecycle
	for i := 0; i < 2000; i++ {
		l.drive(t, st, 1, nodes...)
		if _, err := os.Stat(st.RotatedJournalPath()); err == nil {
			l.drive(t, st, 2, nodes...)
			return
		}
	}
	t.Fatal("journal never rotated")
}

// fullReplayJSON reduces the un-compacted journal (rotated segment followed by
// the live one) the old way: from event zero, no checkpoint involved.
func fullReplayJSON(t *testing.T, st *Store) []byte {
	t.Helper()
	var journal []byte
	if rotated, err := os.ReadFile(st.RotatedJournalPath()); err == nil {
		journal = append(journal, rotated...)
	}
	if current, err := os.ReadFile(st.JournalPath()); err == nil {
		journal = append(journal, current...)
	}
	rs, err := engine.StateFromJournalChecked(journal)
	if err != nil {
		t.Fatalf("full replay: %v", err)
	}
	data, err := engine.MarshalRunState(rs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadedJSON(t *testing.T, st *Store) []byte {
	t.Helper()
	rs, err := st.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	data, err := engine.MarshalRunState(rs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// firstDifference reports the first line where two reduced states diverge,
// which is far easier to read than two thousand-line dumps.
func firstDifference(got, want []byte) string {
	gotLines := strings.Split(string(got), "\n")
	wantLines := strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "<eof>", "<eof>"
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			return fmt.Sprintf("line %d:\n  got:  %s\n  want: %s", i+1, g, w)
		}
	}
	return "identical"
}

func TestJournalCompactsAtThresholdAndMatchesFullReplay(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a", "b")

	driveUntilRotation(t, st, "a", "b")

	if _, err := os.Stat(st.CheckpointPath()); err != nil {
		t.Fatalf("no checkpoint written: %v", err)
	}
	live, err := os.Stat(st.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if live.Size() > 4<<10 {
		t.Fatalf("live journal is %d bytes, threshold is %d", live.Size(), 4<<10)
	}

	// The compacted project and a from-scratch replay of everything that was
	// ever appended must reduce to exactly the same state.
	if got, want := loadedJSON(t, st), fullReplayJSON(t, st); !bytes.Equal(got, want) {
		t.Fatalf("compacted state differs from full replay: %s", firstDifference(got, want))
	}
}

func TestCheckpointCoversMoveEventsAcrossRotation(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a")

	var l lifecycle
	l.drive(t, st, 6, "a")
	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	// Move the first stamp; by the time the journal rotates its target lives
	// inside the checkpoint, so resumed replay has to find it there.
	first := rs.History[0]
	moved := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339)
	if _, err := st.MoveEvent(first.ID, moved, "tester", "改時間"); err != nil {
		t.Fatalf("MoveEvent: %v", err)
	}
	driveUntilRotation(t, st, "a")

	if _, err := os.Stat(st.CheckpointPath()); err != nil {
		t.Fatalf("no checkpoint written: %v", err)
	}
	if got, want := loadedJSON(t, st), fullReplayJSON(t, st); !bytes.Equal(got, want) {
		t.Fatalf("moved stamp survived compaction differently: %s", firstDifference(got, want))
	}
	reloaded, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.History[0].T != moved {
		t.Fatalf("moved stamp T = %q, want %q", reloaded.History[0].T, moved)
	}
}

// A crash between committing the checkpoint and rotating the journal leaves
// both files describing the same events. Replay must not count them twice.
func TestCrashBetweenCheckpointAndRotationDoesNotDoubleCount(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a", "b")
	driveUntilRotation(t, st, "a", "b")

	want := loadedJSON(t, st)
	events := strings.Count(string(want), `"event": "status"`)

	// Put the rotated segment back under its original name in front of the
	// events recorded after it: exactly the on-disk shape of a crash between
	// WriteAtomic(checkpoint) and the rename.
	rotated := mustRead(t, st.RotatedJournalPath())
	live := mustRead(t, st.JournalPath())
	if err := os.WriteFile(st.JournalPath(), append(append([]byte{}, rotated...), live...), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadedJSON(t, st)
	if !bytes.Equal(got, want) {
		t.Fatalf("crash replay differs: %s", firstDifference(got, want))
	}
	if replayed := strings.Count(string(got), `"event": "status"`); replayed != events {
		t.Fatalf("replayed %d status events after the crash, want %d", replayed, events)
	}

	// And the very next append must find its way back to a compacted state.
	if _, err := st.SetStatus("a", engine.StatusDone, "tester", ""); err != nil {
		t.Fatalf("append after crash: %v", err)
	}
}

func TestMissingOrCorruptCheckpointFallsBackToFullReplay(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a", "b")
	driveUntilRotation(t, st, "a", "b")

	want := fullReplayJSON(t, st)
	if got := loadedJSON(t, st); !bytes.Equal(got, want) {
		t.Fatalf("baseline compacted state already differs: %s", firstDifference(got, want))
	}

	for _, corruption := range []struct {
		name  string
		write func()
	}{
		{"truncated json", func() {
			if err := os.WriteFile(st.CheckpointPath(), []byte(`{"version":1,`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong version", func() {
			if err := os.WriteFile(st.CheckpointPath(), []byte(`{"version":99,"state":{}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty", func() {
			if err := os.WriteFile(st.CheckpointPath(), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing", func() {
			if err := os.Remove(st.CheckpointPath()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(corruption.name, func(t *testing.T) {
			corruption.write()
			if got := loadedJSON(t, st); !bytes.Equal(got, want) {
				t.Fatalf("fallback replay differs: %s", firstDifference(got, want))
			}
		})
	}
}

func TestTornTrailingLineToleratedAfterCompaction(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a")
	driveUntilRotation(t, st, "a")

	want := loadedJSON(t, st)
	live := mustRead(t, st.JournalPath())

	torn := append(append([]byte{}, live...), []byte(`{"t":"2026-01-01T00:00:00Z","eve`)...)
	if err := os.WriteFile(st.JournalPath(), torn, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadedJSON(t, st); !bytes.Equal(got, want) {
		t.Fatalf("a torn final line changed replay: %s", firstDifference(got, want))
	}

	// Corruption before the last line is still an error.
	broken := append([]byte("{not json}\n"), live...)
	if err := os.WriteFile(st.JournalPath(), broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadState(); err == nil {
		t.Fatal("corrupt mid-journal line was accepted")
	}
}

// The old build refused every status change once the journal reached 15 MiB.
// With compaction the same project keeps recording.
func TestJournalKeepsAcceptingPastTheOldFifteenMiBWall(t *testing.T) {
	const oldWall = 15 << 20
	st := newJournalTestStore(t, "a")

	// Seed a journal larger than the old hard limit with valid events.
	if err := os.MkdirAll(filepath.Dir(st.JournalPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	var seed bytes.Buffer
	at := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; seed.Len() <= oldWall; i++ {
		line, err := engine.AppendJournalLine(engine.HistoryEvent{
			ID:    fmt.Sprintf("seed-%d", i),
			T:     at.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			Event: "status",
			Node:  "a",
			To:    engine.StatusInProgress,
			By:    "seed",
			Note:  strings.Repeat("紀錄", 40),
		})
		if err != nil {
			t.Fatal(err)
		}
		seed.Write(line)
	}
	if err := os.WriteFile(st.JournalPath(), seed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetStatus("a", engine.StatusDone, "tester", "after the old wall"); err != nil {
		t.Fatalf("status change refused past the old wall: %v", err)
	}
	info, err := os.Stat(st.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > journalCompactBytes {
		t.Fatalf("journal still %d bytes after compaction", info.Size())
	}
	if _, err := os.Stat(st.CheckpointPath()); err != nil {
		t.Fatalf("no checkpoint after compaction: %v", err)
	}
	rs, err := st.LoadState()
	if err != nil {
		t.Fatalf("LoadState after compaction: %v", err)
	}
	if rs.Nodes["a"].Status != engine.StatusDone {
		t.Fatalf("status after compaction = %s", rs.Nodes["a"].Status)
	}
	if _, err := st.SetStatus("a", engine.StatusStarted, "tester", "and again"); err != nil {
		t.Fatalf("second status change refused: %v", err)
	}
}

// Only one previous segment is kept, and state survives repeated rotations.
func TestRotationKeepsExactlyOnePreviousSegment(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a")

	var l lifecycle
	l.drive(t, st, 120, "a")
	first := mustRead(t, st.RotatedJournalPath())
	l.drive(t, st, 120, "a")
	second := mustRead(t, st.RotatedJournalPath())
	if bytes.Equal(first, second) {
		t.Fatal("second rotation did not replace the kept segment")
	}

	entries, err := os.ReadDir(filepath.Dir(st.JournalPath()))
	if err != nil {
		t.Fatal(err)
	}
	rotations := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "journal.jsonl.") {
			rotations++
		}
	}
	if rotations != 1 {
		t.Fatalf("kept %d rotated journals, want 1", rotations)
	}

	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.History) != 240 {
		t.Fatalf("history lost events across rotations: %d, want 240", len(rs.History))
	}
}

func TestCheckpointRecordsItsJournalBoundary(t *testing.T) {
	withCompactThreshold(t, 4<<10)
	st := newJournalTestStore(t, "a")
	driveUntilRotation(t, st, "a")

	var cp journalCheckpoint
	if err := json.Unmarshal(mustRead(t, st.CheckpointPath()), &cp); err != nil {
		t.Fatal(err)
	}
	rotated := mustRead(t, st.RotatedJournalPath())
	if cp.JournalBytes != int64(len(rotated)) {
		t.Fatalf("checkpoint covers %d bytes, rotated segment is %d", cp.JournalBytes, len(rotated))
	}
	if !cp.covers(rotated) {
		t.Fatal("checkpoint does not recognise the segment it was built from")
	}
	if cp.covers(mustRead(t, st.JournalPath())) {
		t.Fatal("checkpoint claims to cover the post-rotation journal")
	}
	if cp.Events == 0 || cp.LastEventTime == "" {
		t.Fatalf("checkpoint boundary metadata missing: %+v", cp)
	}
}
