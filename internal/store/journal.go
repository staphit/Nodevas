// Journal checkpointing: run/journal.jsonl stays the append-only source of
// truth, but it must not grow forever. Periodically the whole journal is
// replayed into run/checkpoint.json (an atomically written snapshot of the
// reduced state plus the exact journal prefix it covers) and only then is the
// journal rotated away. The order is the entire point — the checkpoint is
// durable before the journal is shortened, so a crash between the two loses
// nothing and merely replays a few events twice.

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nodevas/internal/engine"
)

// journalCompactBytes is the soft threshold: crossing it triggers a checkpoint
// + rotation on the next append. It sits well below maxJournalBytes so the hard
// bound is unreachable in normal operation. A variable only so tests can lower
// it; nothing outside this package writes it.
var journalCompactBytes int64 = 8 << 20

const (
	// maxCheckpointBytes bounds the reduced snapshot read at load time.
	maxCheckpointBytes = 256 << 20
	checkpointVersion  = 1
	// compactRetryDelay keeps a failing checkpoint (full disk, read-only
	// media) from re-replaying the journal on every single append.
	compactRetryDelay = time.Minute
)

// CheckpointPath is the compacted snapshot replay starts from.
func (s *Store) CheckpointPath() string {
	return filepath.Join(s.root, "run", "checkpoint.json")
}

// RotatedJournalPath holds the one previous journal segment kept for forensics
// and as the fallback replay source when the checkpoint is unusable.
func (s *Store) RotatedJournalPath() string {
	return s.JournalPath() + ".1"
}

// journalCheckpoint is run/checkpoint.json. State is the fully reduced run
// state; the Journal* fields pin down exactly which journal bytes produced it.
type journalCheckpoint struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
	// JournalBytes/JournalSHA256 identify the journal prefix this snapshot
	// covers. After rotation the live journal no longer matches, which is how
	// the load path tells "rotation happened" from "crashed before rotation".
	JournalBytes  int64            `json:"journalBytes"`
	JournalSHA256 string           `json:"journalSha256"`
	Events        int              `json:"events"`
	LastEventTime string           `json:"lastEventTime,omitempty"`
	State         *engine.RunState `json:"state"`
}

// readCheckpoint returns nil (no error) when there is no usable checkpoint:
// a missing, unreadable or corrupt snapshot must never be a single point of
// failure, the caller falls back to a full replay.
func (s *Store) readCheckpoint() *journalCheckpoint {
	data, err := ReadProjectFileLimit(s.root, s.CheckpointPath(), maxCheckpointBytes)
	if err != nil {
		return nil
	}
	var cp journalCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil
	}
	if cp.Version != checkpointVersion || cp.State == nil || cp.JournalSHA256 == "" {
		return nil
	}
	if cp.State.Nodes == nil {
		cp.State.Nodes = map[string]*engine.NodeState{}
	}
	if cp.State.History == nil {
		cp.State.History = []engine.HistoryEvent{}
	}
	return &cp
}

// covers reports whether the live journal still begins with the exact bytes
// this checkpoint reduced. True means we crashed (or simply have not rotated
// yet) and only the tail past JournalBytes is new; false means the journal was
// rotated after the checkpoint, so all of it is new.
func (cp *journalCheckpoint) covers(journal []byte) bool {
	if cp.JournalBytes <= 0 || int64(len(journal)) < cp.JournalBytes {
		return false
	}
	sum := sha256.Sum256(journal[:cp.JournalBytes])
	return hex.EncodeToString(sum[:]) == cp.JournalSHA256
}

// replayJournal rebuilds run state from checkpoint + journal, and also hands
// back the raw live journal so compaction does not have to read it twice.
// A nil state with a nil error means the project has no run history at all.
func (s *Store) replayJournal() (*engine.RunState, []byte, error) {
	journal, err := s.readJournalBounded(s.JournalPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		journal = nil
	}

	if cp := s.readCheckpoint(); cp != nil {
		remainder := journal
		if cp.covers(journal) {
			remainder = journal[cp.JournalBytes:]
		}
		rs, replayErr := engine.ResumeJournal(cp.State, remainder)
		if replayErr != nil {
			return nil, journal, replayErr
		}
		return rs, journal, nil
	}

	// No usable checkpoint: replay from the rotated segment forward. Each
	// segment is replayed on its own so the torn-trailing-line tolerance
	// applies per file rather than across a splice.
	var rs *engine.RunState
	if rotated, rotErr := s.readJournalBounded(s.RotatedJournalPath()); rotErr == nil {
		rs, err = engine.ResumeJournal(nil, rotated)
		if err != nil {
			return nil, journal, err
		}
	} else if !errors.Is(rotErr, os.ErrNotExist) {
		return nil, journal, rotErr
	}
	if journal == nil && rs == nil {
		return nil, nil, nil
	}
	rs, err = engine.ResumeJournal(rs, journal)
	if err != nil {
		return nil, journal, err
	}
	return rs, journal, nil
}

// compactJournalLocked replays everything, commits a checkpoint, and only then
// rotates the journal away. The caller must hold s.mu.
func (s *Store) compactJournalLocked() error {
	rs, journal, err := s.replayJournal()
	if err != nil {
		return err
	}
	if rs == nil || len(journal) == 0 {
		return nil
	}
	// Bytes that reduce to no events at all are not something we understand
	// well enough to rotate away — leave them where they are.
	if len(rs.History) == 0 {
		return fmt.Errorf("journal holds no replayable events; refusing to rotate")
	}

	sum := sha256.Sum256(journal)
	cp := journalCheckpoint{
		Version:       checkpointVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		JournalBytes:  int64(len(journal)),
		JournalSHA256: hex.EncodeToString(sum[:]),
		Events:        len(rs.History),
		State:         rs,
	}
	if len(rs.History) > 0 {
		cp.LastEventTime = rs.History[len(rs.History)-1].T
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	// WriteAtomic fsyncs the temp file before the rename, so once this
	// returns the snapshot is durable — the precondition for shortening the
	// journal below.
	if err := s.WriteAtomic(s.CheckpointPath(), data); err != nil {
		return err
	}
	return s.rotateJournalLocked()
}

// rotateJournalLocked moves the live journal aside, keeping exactly one
// previous segment. The caller must hold s.mu, and must have committed a
// checkpoint covering this journal first.
func (s *Store) rotateJournalLocked() error {
	rotated := s.RotatedJournalPath()
	if err := s.removePath(rotated); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.markSelfWrite(s.JournalPath())
	s.markSelfWrite(rotated)
	if err := RenameProjectPath(s.root, s.JournalPath(), s.root, rotated); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

// maybeCompactLocked compacts when the journal has crossed the soft threshold.
// Compaction is best effort: a failure leaves the journal intact and simply
// defers the attempt, it must never turn into a rejected status change.
// The caller must hold s.mu.
func (s *Store) maybeCompactLocked(size int64, incoming int) {
	if size+int64(incoming) <= journalCompactBytes || journalCompactBytes <= 0 {
		return
	}
	if !s.compactFailedAt.IsZero() && time.Since(s.compactFailedAt) < compactRetryDelay {
		return
	}
	if err := s.compactJournalLocked(); err != nil {
		s.compactFailedAt = time.Now()
		return
	}
	s.compactFailedAt = time.Time{}
}
