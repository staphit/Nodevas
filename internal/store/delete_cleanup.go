package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/engine"
)

const (
	deleteCleanupKindNodes      = "nodes"
	deleteCleanupKindPage       = "page"
	maxDeleteCleanupRecords     = 256
	maxDeleteCleanupRecordBytes = 8 << 10
	maxDeleteCleanupQueueBytes  = 2 << 20
	maxCleanupManifestBytes     = 256 << 10
)

// DeleteCleanupUnavailableError is returned only before an authoritative
// delete commit. Its text is safe for an HTTP response; the underlying queue
// error is logged server-side and is deliberately not wrapped into this value.
type DeleteCleanupUnavailableError struct{}

func (*DeleteCleanupUnavailableError) Error() string {
	return "delete cleanup is temporarily unavailable"
}

// DeleteNodesOutcome distinguishes a failure before the graph commit from a
// best-effort cleanup that remains after it. CleanupPending is deliberately a
// boolean: callers and clients must never receive a filesystem path or error.
type DeleteNodesOutcome struct {
	TrashFiles     []string
	CleanupPending bool
}

type DeleteNodeOutcome struct {
	TrashFile      string
	CleanupPending bool
}

type DeleteNodePageOutcome struct {
	TrashFile      string
	CleanupPending bool
}

// deleteCleanupRecord is the durable outbox for post-commit filesystem work.
// It stores only validated identifiers; paths are derived inside the project
// root when the record is executed.
type deleteCleanupRecord struct {
	Kind   string   `json:"kind"`
	Nodes  []string `json:"nodes,omitempty"`
	NodeID string   `json:"nodeId,omitempty"`
	PageID string   `json:"pageId,omitempty"`
	Format string   `json:"format,omitempty"`
}

type queuedDeleteCleanup struct {
	path   string
	record deleteCleanupRecord
	size   int64
}

func (s *Store) deleteCleanupDir() string {
	return filepath.Join(s.root, DataDir, "cleanup")
}

func deleteCleanupUnavailable(err error) error {
	log.Printf("delete cleanup queue unavailable: %v", err)
	return &DeleteCleanupUnavailableError{}
}

// queueDeleteCleanupsLocked persists cleanup intents before the authoritative
// graph/manifest commit. A large batch is split across small bounded records;
// all records are preflighted and either all land or every newly written one
// is removed. Callers hold s.mu.
func (s *Store) queueDeleteCleanupsLocked(records []deleteCleanupRecord) ([]queuedDeleteCleanup, error) {
	if len(records) == 0 {
		return nil, errors.New("delete cleanup has no records")
	}
	queued, err := s.loadDeleteCleanupQueueLocked()
	if err != nil {
		return nil, err
	}
	if len(queued)+len(records) > maxDeleteCleanupRecords {
		return nil, fmt.Errorf("delete cleanup queue would exceed its maximum of %d records", maxDeleteCleanupRecords)
	}
	totalBytes := int64(0)
	for _, item := range queued {
		totalBytes += item.size
	}
	encoded := make([][]byte, len(records))
	for index, record := range records {
		if err := validateDeleteCleanupRecord(record); err != nil {
			return nil, err
		}
		data, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("encode delete cleanup: %w", err)
		}
		data = append(data, '\n')
		if len(data) > maxDeleteCleanupRecordBytes {
			return nil, fmt.Errorf("delete cleanup record exceeds %d bytes", maxDeleteCleanupRecordBytes)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxDeleteCleanupQueueBytes {
			return nil, fmt.Errorf("delete cleanup queue would exceed %d bytes", maxDeleteCleanupQueueBytes)
		}
		encoded[index] = data
	}
	dir := s.deleteCleanupDir()
	if err := MkdirAllProjectPath(s.root, dir, 0o700); err != nil {
		return nil, fmt.Errorf("create delete cleanup queue: %w", err)
	}
	written := make([]queuedDeleteCleanup, 0, len(records))
	rollback := func() {
		for _, item := range written {
			_ = s.removePath(item.path)
		}
	}
	for index, record := range records {
		name, err := randomDeleteCleanupName()
		if err != nil {
			rollback()
			return nil, err
		}
		path := filepath.Join(dir, name)
		if err := s.WriteAtomic(path, encoded[index]); err != nil {
			rollback()
			return nil, fmt.Errorf("persist delete cleanup: %w", err)
		}
		written = append(written, queuedDeleteCleanup{path: path, record: record, size: int64(len(encoded[index]))})
	}
	return written, nil
}

func nodeDeleteCleanupRecords(ids []string) ([]deleteCleanupRecord, error) {
	var records []deleteCleanupRecord
	current := deleteCleanupRecord{Kind: deleteCleanupKindNodes}
	for _, id := range ids {
		candidate := current
		candidate.Nodes = append(append([]string(nil), current.Nodes...), id)
		data, err := json.Marshal(candidate)
		if err != nil {
			return nil, err
		}
		if len(data)+1 <= maxDeleteCleanupRecordBytes {
			current = candidate
			continue
		}
		if len(current.Nodes) == 0 {
			return nil, fmt.Errorf("node id does not fit in delete cleanup record")
		}
		records = append(records, current)
		current = deleteCleanupRecord{Kind: deleteCleanupKindNodes, Nodes: []string{id}}
	}
	if len(current.Nodes) > 0 {
		records = append(records, current)
	}
	return records, nil
}

func randomDeleteCleanupName() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("create delete cleanup id: %w", err)
	}
	return "delete-" + hex.EncodeToString(token[:]) + ".json", nil
}

func validateDeleteCleanupRecord(record deleteCleanupRecord) error {
	switch record.Kind {
	case deleteCleanupKindNodes:
		if len(record.Nodes) == 0 || len(record.Nodes) > MaxGraphNodes || record.NodeID != "" || record.PageID != "" || record.Format != "" {
			return errors.New("invalid node delete cleanup record")
		}
		seen := make(map[string]bool, len(record.Nodes))
		for _, id := range record.Nodes {
			if !engine.ValidNodeID(id) || seen[id] {
				return errors.New("invalid node id in delete cleanup record")
			}
			seen[id] = true
		}
	case deleteCleanupKindPage:
		if len(record.Nodes) != 0 || !engine.ValidNodeID(record.NodeID) || !engine.ValidNodeID(record.PageID) {
			return errors.New("invalid page delete cleanup record")
		}
		format, err := NormalizePageFormat(record.Format)
		if err != nil || format != record.Format {
			return errors.New("invalid page format in delete cleanup record")
		}
	default:
		return errors.New("invalid delete cleanup kind")
	}
	return nil
}

func decodeDeleteCleanupRecord(data []byte) (deleteCleanupRecord, error) {
	var record deleteCleanupRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, fmt.Errorf("decode delete cleanup: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return record, errors.New("decode delete cleanup: trailing value")
		}
		return record, fmt.Errorf("decode delete cleanup: %w", err)
	}
	if err := validateDeleteCleanupRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func (s *Store) loadDeleteCleanupQueueLocked() ([]queuedDeleteCleanup, error) {
	dir := s.deleteCleanupDir()
	entries, err := s.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read delete cleanup queue: %w", err)
	}
	if len(entries) > maxDeleteCleanupRecords {
		return nil, fmt.Errorf("delete cleanup queue has %d records, maximum %d", len(entries), maxDeleteCleanupRecords)
	}
	queued := make([]queuedDeleteCleanup, 0, len(entries))
	totalBytes := int64(0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeType != 0 || !strings.HasPrefix(name, "delete-") || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("delete cleanup queue contains invalid entry %q", name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat delete cleanup %q: %w", name, err)
		}
		if info.Size() < 1 || info.Size() > maxDeleteCleanupRecordBytes {
			return nil, fmt.Errorf("delete cleanup %q has invalid size %d", name, info.Size())
		}
		totalBytes += info.Size()
		if totalBytes > maxDeleteCleanupQueueBytes {
			return nil, fmt.Errorf("delete cleanup queue exceeds %d bytes", maxDeleteCleanupQueueBytes)
		}
		path := filepath.Join(dir, name)
		data, err := ReadProjectFileLimit(s.root, path, maxDeleteCleanupRecordBytes)
		if err != nil {
			return nil, fmt.Errorf("read delete cleanup %q: %w", name, err)
		}
		record, err := decodeDeleteCleanupRecord(data)
		if err != nil {
			return nil, fmt.Errorf("invalid delete cleanup %q: %w", name, err)
		}
		queued = append(queued, queuedDeleteCleanup{path: path, record: record, size: info.Size()})
	}
	return queued, nil
}

// finishDeleteCleanupLocked executes a record whose authoritative commit is
// already known to have succeeded. Any error leaves the marker for restart.
func (s *Store) finishDeleteCleanupLocked(queued queuedDeleteCleanup) error {
	if s.deleteCleanupFault != nil {
		if err := s.deleteCleanupFault(queued.record); err != nil {
			return err
		}
	}
	if err := s.removeDeleteArtifactsLocked(queued.record); err != nil {
		return err
	}
	if err := s.removePath(queued.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove completed delete cleanup marker: %w", err)
	}
	return nil
}

func (s *Store) removeDeleteArtifactsLocked(record deleteCleanupRecord) error {
	var errs []error
	switch record.Kind {
	case deleteCleanupKindNodes:
		for _, id := range record.Nodes {
			source := s.NodePath(id)
			s.markSelfWrite(source)
			if err := s.removePath(source); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove node source: %w", err))
			}
			if err := s.DeleteDraft(id); err != nil {
				errs = append(errs, fmt.Errorf("remove node draft: %w", err))
			}
		}
	case deleteCleanupKindPage:
		dir, err := s.nodePagesDirForCleanup(record.NodeID)
		if err != nil {
			return err
		}
		extension := pageFormatExtension[record.Format]
		path := filepath.Join(dir, record.PageID+extension)
		s.markSelfWrite(path)
		if err := s.removePath(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove page source: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Store) deleteCleanupCommittedLocked(record deleteCleanupRecord) (bool, error) {
	switch record.Kind {
	case deleteCleanupKindNodes:
		graph, _, err := s.loadGraphLocked()
		if err != nil {
			return false, err
		}
		for _, id := range record.Nodes {
			if graph.NodeByID(id) != nil {
				return false, nil
			}
		}
		return true, nil
	case deleteCleanupKindPage:
		graph, _, err := s.loadGraphLocked()
		if err != nil {
			return false, err
		}
		// Deleting the parent node is stronger authoritative evidence than its
		// now-orphaned page manifest. The payload directory may outlive the node
		// document, so cleanup below locates it independently of NodePath.
		if graph.NodeByID(record.NodeID) == nil {
			return true, nil
		}
		dir, err := s.nodePagesDirForCleanup(record.NodeID)
		if err != nil {
			return false, err
		}
		data, err := ReadProjectFileLimit(s.root, filepath.Join(dir, "pages.json"), maxCleanupManifestBytes)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		var manifest nodePagesManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return false, fmt.Errorf("node pages manifest: %w", err)
		}
		if len(manifest.Pages) > maxNodePages {
			return false, fmt.Errorf("node %q has too many pages", record.NodeID)
		}
		seen := make(map[string]bool, len(manifest.Pages))
		for _, page := range manifest.Pages {
			if !engine.ValidNodeID(page.ID) || seen[page.ID] {
				return false, fmt.Errorf("node %q has invalid page id", record.NodeID)
			}
			if title := strings.TrimSpace(page.Title); title == "" || len(title) > 256 {
				return false, fmt.Errorf("node %q page %q has invalid title", record.NodeID, page.ID)
			}
			if _, err := NormalizePageFormat(page.Format); err != nil {
				return false, fmt.Errorf("node %q page %q has invalid format", record.NodeID, page.ID)
			}
			seen[page.ID] = true
			if page.ID == record.PageID {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, errors.New("invalid delete cleanup kind")
	}
}

// nodePagesDirForCleanup locates an orphaned payload directory even after its
// node document has been removed. The disk scan never supplies authority; the
// graph/manifest check above does. The returned path is revalidated beneath
// the project root before any read or removal uses it.
func (s *Store) nodePagesDirForCleanup(nodeID string) (string, error) {
	if dir := ListNodePayloadDirs(s.root, ".pages")[nodeID]; dir != "" {
		if err := ValidateProjectPath(s.root, dir, false); err != nil {
			return "", err
		}
		return dir, nil
	}
	dir := s.NodePagesDir(nodeID)
	if err := ValidateProjectPath(s.root, dir, true); err != nil {
		return "", err
	}
	return dir, nil
}

// retryDeleteCleanups runs at Store construction. It never guesses: a marker
// written before a commit that never happened is discarded only after the
// graph/manifest proves the item remains authoritative; artifacts are removed
// only when that same source proves deletion committed.
func (s *Store) retryDeleteCleanups() {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued, err := s.loadDeleteCleanupQueueLocked()
	if err != nil {
		log.Printf("delete cleanup queue: %v", err)
		return
	}
	for _, item := range queued {
		committed, err := s.deleteCleanupCommittedLocked(item.record)
		if err != nil {
			log.Printf("delete cleanup retry: %v", err)
			continue
		}
		if !committed {
			if err := s.removePath(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("delete cleanup discard: %v", err)
			}
			continue
		}
		if err := s.finishDeleteCleanupLocked(item); err != nil {
			log.Printf("delete cleanup retry: %v", err)
		}
	}
}
