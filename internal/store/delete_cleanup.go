package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/locale"
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
		return nil, locale.Errorf("delete cleanup has no records", "刪除清理沒有任何紀錄")
	}
	queued, err := s.loadDeleteCleanupQueueLocked()
	if err != nil {
		return nil, err
	}
	if len(queued)+len(records) > maxDeleteCleanupRecords {
		return nil, locale.Errorf("delete cleanup queue would exceed its maximum of %d records", "刪除清理佇列會超過上限 %d 筆紀錄", maxDeleteCleanupRecords)
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
			return nil, locale.Errorf("encode delete cleanup: %w", "編碼刪除清理紀錄：%w", err)
		}
		data = append(data, '\n')
		if len(data) > maxDeleteCleanupRecordBytes {
			return nil, locale.Errorf("delete cleanup record exceeds %d bytes", "刪除清理紀錄超過 %d 位元組", maxDeleteCleanupRecordBytes)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxDeleteCleanupQueueBytes {
			return nil, locale.Errorf("delete cleanup queue would exceed %d bytes", "刪除清理佇列會超過 %d 位元組", maxDeleteCleanupQueueBytes)
		}
		encoded[index] = data
	}
	dir := s.deleteCleanupDir()
	if err := MkdirAllProjectPath(s.root, dir, 0o700); err != nil {
		return nil, locale.Errorf("create delete cleanup queue: %w", "建立刪除清理佇列：%w", err)
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
			return nil, locale.Errorf("persist delete cleanup: %w", "儲存刪除清理紀錄：%w", err)
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
			return nil, locale.Errorf("node id does not fit in delete cleanup record", "節點代號放不進刪除清理紀錄")
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
		return "", locale.Errorf("create delete cleanup id: %w", "建立刪除清理代號：%w", err)
	}
	return "delete-" + hex.EncodeToString(token[:]) + ".json", nil
}

func validateDeleteCleanupRecord(record deleteCleanupRecord) error {
	switch record.Kind {
	case deleteCleanupKindNodes:
		if len(record.Nodes) == 0 || len(record.Nodes) > MaxGraphNodes || record.NodeID != "" || record.PageID != "" || record.Format != "" {
			return locale.Errorf("invalid node delete cleanup record", "節點刪除清理紀錄無效")
		}
		seen := make(map[string]bool, len(record.Nodes))
		for _, id := range record.Nodes {
			if !engine.ValidNodeID(id) || seen[id] {
				return locale.Errorf("invalid node id in delete cleanup record", "刪除清理紀錄中的節點代號無效")
			}
			seen[id] = true
		}
	case deleteCleanupKindPage:
		if len(record.Nodes) != 0 || !engine.ValidNodeID(record.NodeID) || !engine.ValidNodeID(record.PageID) {
			return locale.Errorf("invalid page delete cleanup record", "頁面刪除清理紀錄無效")
		}
		format, err := NormalizePageFormat(record.Format)
		if err != nil || format != record.Format {
			return locale.Errorf("invalid page format in delete cleanup record", "刪除清理紀錄中的頁面格式無效")
		}
	default:
		return locale.Errorf("invalid delete cleanup kind", "刪除清理種類無效")
	}
	return nil
}

func decodeDeleteCleanupRecord(data []byte) (deleteCleanupRecord, error) {
	var record deleteCleanupRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, locale.Errorf("decode delete cleanup: %w", "解讀刪除清理紀錄：%w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return record, locale.Errorf("decode delete cleanup: trailing value", "解讀刪除清理紀錄：結尾有多餘的值")
		}
		return record, locale.Errorf("decode delete cleanup: %w", "解讀刪除清理紀錄：%w", err)
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
		return nil, locale.Errorf("read delete cleanup queue: %w", "讀取刪除清理佇列：%w", err)
	}
	if len(entries) > maxDeleteCleanupRecords {
		return nil, locale.Errorf("delete cleanup queue has %d records, maximum %d", "刪除清理佇列有 %d 筆紀錄，上限 %d", len(entries), maxDeleteCleanupRecords)
	}
	queued := make([]queuedDeleteCleanup, 0, len(entries))
	totalBytes := int64(0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeType != 0 || !strings.HasPrefix(name, "delete-") || !strings.HasSuffix(name, ".json") {
			return nil, locale.Errorf("delete cleanup queue contains invalid entry %q", "刪除清理佇列含有無效項目「%s」", name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, locale.Errorf("stat delete cleanup %q: %w", "查看刪除清理「%s」：%w", name, err)
		}
		if info.Size() < 1 || info.Size() > maxDeleteCleanupRecordBytes {
			return nil, locale.Errorf("delete cleanup %q has invalid size %d", "刪除清理「%s」的大小 %d 無效", name, info.Size())
		}
		totalBytes += info.Size()
		if totalBytes > maxDeleteCleanupQueueBytes {
			return nil, locale.Errorf("delete cleanup queue exceeds %d bytes", "刪除清理佇列超過 %d 位元組", maxDeleteCleanupQueueBytes)
		}
		path := filepath.Join(dir, name)
		data, err := ReadProjectFileLimit(s.root, path, maxDeleteCleanupRecordBytes)
		if err != nil {
			return nil, locale.Errorf("read delete cleanup %q: %w", "讀取刪除清理「%s」：%w", name, err)
		}
		record, err := decodeDeleteCleanupRecord(data)
		if err != nil {
			return nil, locale.Errorf("invalid delete cleanup %q: %w", "刪除清理「%s」無效：%w", name, err)
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
		return locale.Errorf("remove completed delete cleanup marker: %w", "移除已完成的刪除清理標記：%w", err)
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
				errs = append(errs, locale.Errorf("remove node source: %w", "移除節點原始檔：%w", err))
			}
			if err := s.DeleteDraft(id); err != nil {
				errs = append(errs, locale.Errorf("remove node draft: %w", "移除節點草稿：%w", err))
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
			errs = append(errs, locale.Errorf("remove page source: %w", "移除頁面原始檔：%w", err))
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
			return false, locale.Errorf("node pages manifest: %w", "節點頁面清單：%w", err)
		}
		if len(manifest.Pages) > maxNodePages {
			return false, locale.Errorf("node %q has too many pages", "節點「%s」的頁面太多", record.NodeID)
		}
		seen := make(map[string]bool, len(manifest.Pages))
		for _, page := range manifest.Pages {
			if !engine.ValidNodeID(page.ID) || seen[page.ID] {
				return false, locale.Errorf("node %q has invalid page id", "節點「%s」的頁面代號無效", record.NodeID)
			}
			if title := strings.TrimSpace(page.Title); title == "" || len(title) > 256 {
				return false, locale.Errorf("node %q page %q has invalid title", "節點「%s」的頁面「%s」標題無效", record.NodeID, page.ID)
			}
			if _, err := NormalizePageFormat(page.Format); err != nil {
				return false, locale.Errorf("node %q page %q has invalid format", "節點「%s」的頁面「%s」格式無效", record.NodeID, page.ID)
			}
			seen[page.ID] = true
			if page.ID == record.PageID {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, locale.Errorf("invalid delete cleanup kind", "刪除清理種類無效")
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
