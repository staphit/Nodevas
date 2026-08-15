// A node's pages and file attachments: the manifest, the files behind it, and the format each page is stored in.

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nodevas/internal/document"
	"nodevas/internal/document/docx"
	dochtml "nodevas/internal/document/html"
	"nodevas/internal/engine"
)

type NodePageInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Format is the file the page is stored as: md (default), txt, html or
	// docx. Manifests written before formats existed have none, and are read
	// as Markdown.
	Format string `json:"format,omitempty"`
}

type nodePagesManifest struct {
	Pages []NodePageInfo `json:"pages"`
}

// LoadNodePagesManifest reads and validates a node's pages.json.
//
// Contract: callers that go on to write hold s.mu, so the manifest they act
// on is the one still on disk. Reading it without the lock is only safe for a
// Store nobody else can reach yet (import validation).
func (s *Store) LoadNodePagesManifest(nodeID string) (nodePagesManifest, error) {
	var manifest nodePagesManifest
	data, err := s.ReadFile(s.NodePagesManifestPath(nodeID))
	if os.IsNotExist(err) {
		return manifest, nil
	}
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("node pages manifest: %w", err)
	}
	if len(manifest.Pages) > maxNodePages {
		return manifest, fmt.Errorf("node %q has too many pages", nodeID)
	}
	seen := map[string]bool{}
	for index, page := range manifest.Pages {
		if !engine.ValidNodeID(page.ID) || seen[page.ID] {
			return manifest, fmt.Errorf("node %q has invalid or duplicate page id %q", nodeID, page.ID)
		}
		if title := strings.TrimSpace(page.Title); title == "" || len(title) > 256 {
			return manifest, fmt.Errorf("node %q page %q has invalid title", nodeID, page.ID)
		}
		format, err := NormalizePageFormat(page.Format)
		if err != nil {
			return manifest, fmt.Errorf("node %q page %q: %w", nodeID, page.ID, err)
		}
		manifest.Pages[index].Format = format
		pagePath := s.NodePagePath(nodeID, page.ID, format)
		info, statErr := s.statPath(pagePath)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return manifest, fmt.Errorf("node %q page %q: %w", nodeID, page.ID, statErr)
		}
		if statErr == nil && !info.Mode().IsRegular() {
			return manifest, fmt.Errorf("node %q page %q is not a regular file", nodeID, page.ID)
		}
		seen[page.ID] = true
	}
	return manifest, nil
}

func marshalNodePagesManifest(manifest nodePagesManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (s *Store) ListNodePages(nodeID string) ([]NodePageInfo, error) {
	if !engine.ValidNodeID(nodeID) {
		return nil, fmt.Errorf("invalid node id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if g.NodeByID(nodeID) == nil {
		return nil, fmt.Errorf("node %q not found in graph", nodeID)
	}
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return nil, err
	}
	pages := make([]NodePageInfo, len(manifest.Pages))
	copy(pages, manifest.Pages)
	return pages, nil
}

// CreateNodePage adds a page stored in the requested format; an empty format
// means Markdown.
func (s *Store) CreateNodePage(nodeID, title, format string) (NodePageInfo, string, string, error) {
	var zero NodePageInfo
	if !engine.ValidNodeID(nodeID) {
		return zero, "", "", fmt.Errorf("invalid node id")
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 256 {
		return zero, "", "", fmt.Errorf("page title must contain 1 to 256 characters")
	}
	format, err := NormalizePageFormat(format)
	if err != nil {
		return zero, "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return zero, "", "", err
	}
	if g.NodeByID(nodeID) == nil {
		return zero, "", "", fmt.Errorf("node %q not found in graph", nodeID)
	}
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return zero, "", "", err
	}
	if len(manifest.Pages) >= maxNodePages {
		return zero, "", "", fmt.Errorf("node pages reached the maximum of %d", maxNodePages)
	}
	used := make(map[string]bool, len(manifest.Pages))
	for _, page := range manifest.Pages {
		used[page.ID] = true
	}
	pageID := ""
	for index := 1; index <= maxNodePages+1; index++ {
		candidate := fmt.Sprintf("page-%04d", index)
		if !used[candidate] {
			pageID = candidate
			break
		}
	}
	if pageID == "" {
		return zero, "", "", fmt.Errorf("unable to assign page id")
	}
	page := NodePageInfo{ID: pageID, Title: title, Format: format}
	manifest.Pages = append(manifest.Pages, page)
	manifestData, err := marshalNodePagesManifest(manifest)
	if err != nil {
		return zero, "", "", err
	}
	content := starterPageContent(title, format)
	contentData, err := s.encodePageContent(nodeID, format, content)
	if err != nil {
		return zero, "", "", err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{
		{path: s.NodePagesManifestPath(nodeID), data: manifestData},
		{path: s.NodePagePath(nodeID, pageID, format), data: contentData},
	}); err != nil {
		return zero, "", "", err
	}
	editable, err := s.decodePageContent(nodeID, format, contentData)
	if err != nil {
		return zero, "", "", err
	}
	return page, editable, Rev(contentData), nil
}

// ImportNodePage turns an uploaded file into a subpage, keeping the original
// bytes so nothing is lost before the first edit.
func (s *Store) ImportNodePage(nodeID, title, filename string, data []byte) (NodePageInfo, string, string, error) {
	var zero NodePageInfo
	format, ok := PageFormatFromFilename(filename)
	if !ok {
		return zero, "", "", fmt.Errorf("unsupported file type %q", filepath.Ext(filename))
	}
	if strings.TrimSpace(title) == "" {
		base := filepath.Base(filename)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	page, _, _, err := s.CreateNodePage(nodeID, title, format)
	if err != nil {
		return zero, "", "", err
	}
	// The starter file is immediately replaced by the upload: the page is
	// whatever the user handed over, byte for byte.
	path := s.NodePagePath(nodeID, page.ID, format)
	s.mu.Lock()
	err = s.applyUpdatesLocked([]fileUpdate{{path: path, data: data}})
	s.mu.Unlock()
	if err != nil {
		return zero, "", "", err
	}
	content, err := s.decodePageContent(nodeID, format, data)
	if err != nil {
		// The file is stored; only its conversion failed.
		return page, "", Rev(data), err
	}
	return page, content, Rev(data), nil
}

// ConvertNodePage rewrites a page in another format: the new file replaces
// the old one, which is kept as a history snapshot.
func (s *Store) ConvertNodePage(nodeID, pageID, target string) ([]NodePageInfo, string, string, error) {
	if !engine.ValidNodeID(nodeID) || !engine.ValidNodeID(pageID) {
		return nil, "", "", fmt.Errorf("invalid node or page id")
	}
	target, err := NormalizePageFormat(target)
	if err != nil {
		return nil, "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return nil, "", "", err
	}
	index := -1
	for candidate, page := range manifest.Pages {
		if page.ID == pageID {
			index = candidate
			break
		}
	}
	if index < 0 {
		return nil, "", "", fmt.Errorf("page %q not found for node %q", pageID, nodeID)
	}
	page := manifest.Pages[index]
	oldPath := s.NodePagePath(nodeID, pageID, page.Format)
	data, err := s.ReadFile(oldPath)
	if err != nil {
		return nil, "", "", err
	}
	if page.Format == target {
		content, decodeErr := s.decodePageContent(nodeID, target, data)
		if decodeErr != nil {
			return nil, "", "", decodeErr
		}
		return append([]NodePageInfo(nil), manifest.Pages...), content, Rev(data), nil
	}
	converted, err := s.convertPageContent(nodeID, page.Title, page.Format, target, data)
	if err != nil {
		return nil, "", "", err
	}
	manifest.Pages[index].Format = target
	manifestData, err := marshalNodePagesManifest(manifest)
	if err != nil {
		return nil, "", "", err
	}
	// The old file leaves a snapshot behind: a conversion is lossy, and the
	// previous version has to stay recoverable.
	if err := s.snapshotHistoryAlways(oldPath); err != nil {
		return nil, "", "", err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{
		{path: s.NodePagesManifestPath(nodeID), data: manifestData},
		{path: s.NodePagePath(nodeID, pageID, target), data: converted},
	}); err != nil {
		return nil, "", "", err
	}
	s.markSelfWrite(oldPath)
	if err := s.removePath(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", "", fmt.Errorf("page converted but the old file remains: %w", err)
	}
	content, err := s.decodePageContent(nodeID, target, converted)
	if err != nil {
		return nil, "", "", err
	}
	return append([]NodePageInfo(nil), manifest.Pages...), content, Rev(converted), nil
}

// LoadNodePage returns the page's metadata, the text to edit and the
// revision of the file on disk.
func (s *Store) LoadNodePage(nodeID, pageID string) (NodePageInfo, string, string, error) {
	var zero NodePageInfo
	if !engine.ValidNodeID(nodeID) || !engine.ValidNodeID(pageID) {
		return zero, "", "", fmt.Errorf("invalid node or page id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return zero, "", "", err
	}
	if g.NodeByID(nodeID) == nil {
		return zero, "", "", fmt.Errorf("node %q not found in graph", nodeID)
	}
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return zero, "", "", err
	}
	page, found := findManifestPage(manifest, pageID)
	if !found {
		return zero, "", "", fmt.Errorf("page %q not found for node %q", pageID, nodeID)
	}
	data, err := s.ReadFile(s.NodePagePath(nodeID, pageID, page.Format))
	if err != nil {
		return zero, "", "", err
	}
	// The revision always fingerprints the file on disk, so conflict
	// detection works the same for a converted format as for plain text.
	content, err := s.decodePageContent(nodeID, page.Format, data)
	if err != nil {
		return zero, "", "", err
	}
	return page, content, Rev(data), nil
}

func findManifestPage(manifest nodePagesManifest, pageID string) (NodePageInfo, bool) {
	for _, page := range manifest.Pages {
		if page.ID == pageID {
			return page, true
		}
	}
	return NodePageInfo{}, false
}

func (s *Store) SaveNodePage(nodeID, pageID, content, baseRev string) (string, error) {
	if !engine.ValidNodeID(nodeID) || !engine.ValidNodeID(pageID) {
		return "", fmt.Errorf("invalid node or page id")
	}
	if len(content) > maxPageBytes {
		return "", fmt.Errorf("page content is too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	if g.NodeByID(nodeID) == nil {
		return "", fmt.Errorf("node %q not found in graph", nodeID)
	}
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return "", err
	}
	page, found := findManifestPage(manifest, pageID)
	if !found {
		return "", fmt.Errorf("page %q not found for node %q", pageID, nodeID)
	}
	data, err := s.encodePageContent(nodeID, page.Format, content)
	if err != nil {
		return "", err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{{
		path:          s.NodePagePath(nodeID, pageID, page.Format),
		data:          data,
		checkRevision: true,
		expectedRev:   baseRev,
	}}); err != nil {
		return "", err
	}
	return Rev(data), nil
}

// UpdateNodePage changes page metadata without changing its stable ID or content.
func (s *Store) UpdateNodePage(nodeID, pageID, title string, targetIndex *int) ([]NodePageInfo, error) {
	if !engine.ValidNodeID(nodeID) || !engine.ValidNodeID(pageID) {
		return nil, fmt.Errorf("invalid node or page id")
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 256 {
		return nil, fmt.Errorf("page title must contain 1 to 256 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if g.NodeByID(nodeID) == nil {
		return nil, fmt.Errorf("node %q not found in graph", nodeID)
	}
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return nil, err
	}
	currentIndex := -1
	for index := range manifest.Pages {
		if manifest.Pages[index].ID == pageID {
			currentIndex = index
			manifest.Pages[index].Title = title
			break
		}
	}
	if currentIndex < 0 {
		return nil, fmt.Errorf("page %q not found for node %q", pageID, nodeID)
	}
	if targetIndex != nil && len(manifest.Pages) > 1 {
		index := *targetIndex
		if index < 0 {
			index = 0
		}
		if index >= len(manifest.Pages) {
			index = len(manifest.Pages) - 1
		}
		if index != currentIndex {
			page := manifest.Pages[currentIndex]
			manifest.Pages = append(manifest.Pages[:currentIndex], manifest.Pages[currentIndex+1:]...)
			manifest.Pages = append(manifest.Pages, NodePageInfo{})
			copy(manifest.Pages[index+1:], manifest.Pages[index:])
			manifest.Pages[index] = page
		}
	}
	data, err := marshalNodePagesManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{{
		path: s.NodePagesManifestPath(nodeID),
		data: data,
	}}); err != nil {
		return nil, err
	}
	pages := make([]NodePageInfo, len(manifest.Pages))
	copy(pages, manifest.Pages)
	return pages, nil
}

// DeleteNodePage removes a page from its node and keeps a restorable trash record.
func (s *Store) DeleteNodePage(nodeID, pageID string) (DeleteNodePageOutcome, error) {
	var zero DeleteNodePageOutcome
	if !engine.ValidNodeID(nodeID) || !engine.ValidNodeID(pageID) {
		return zero, fmt.Errorf("invalid node or page id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, err := s.LoadNodePagesManifest(nodeID)
	if err != nil {
		return zero, err
	}
	index := -1
	var page NodePageInfo
	for candidate, item := range manifest.Pages {
		if item.ID == pageID {
			index = candidate
			page = item
			break
		}
	}
	if index < 0 {
		return zero, fmt.Errorf("page %q not found for node %q", pageID, nodeID)
	}
	content, err := s.ReadFile(s.NodePagePath(nodeID, pageID, page.Format))
	if err != nil {
		return zero, err
	}
	manifest.Pages = append(manifest.Pages[:index], manifest.Pages[index+1:]...)
	manifestData, err := marshalNodePagesManifest(manifest)
	if err != nil {
		return zero, err
	}
	deletedAt := time.Now().UTC()
	record, err := json.MarshalIndent(trashedNodePage{
		NodeID: nodeID,
		PageID: pageID,
		Title:  page.Title,
		Format: page.Format,
		Data:   content,
		At:     deletedAt.Format(time.RFC3339Nano),
	}, "", "  ")
	if err != nil {
		return zero, err
	}
	trashDir := filepath.Join(s.root, DataDir, "trash", "pages")
	if err := MkdirAllProjectPath(s.root, trashDir, 0o755); err != nil {
		return zero, err
	}
	trashName := fmt.Sprintf("page-%d.json", deletedAt.UnixNano())
	trashPath := filepath.Join(trashDir, trashName)
	if err := s.WriteAtomic(trashPath, append(record, '\n')); err != nil {
		return zero, err
	}
	queued, err := s.queueDeleteCleanupsLocked([]deleteCleanupRecord{{
		Kind:   deleteCleanupKindPage,
		NodeID: nodeID,
		PageID: pageID,
		Format: page.Format,
	}})
	if err != nil {
		_ = s.removePath(trashPath)
		return zero, deleteCleanupUnavailable(err)
	}
	if err := s.applyUpdatesLocked([]fileUpdate{{
		path: s.NodePagesManifestPath(nodeID),
		data: manifestData,
	}}); err != nil {
		_ = s.removePath(trashPath)
		for _, item := range queued {
			if markerErr := s.removePath(item.path); markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove uncommitted delete cleanup marker: %w", markerErr))
			}
		}
		return zero, err
	}
	outcome := DeleteNodePageOutcome{TrashFile: filepath.ToSlash(filepath.Join("pages", trashName))}
	if err := s.finishDeleteCleanupLocked(queued[0]); err != nil {
		outcome.CleanupPending = true
		log.Printf("page delete committed; cleanup queued for retry: %v", err)
	}
	return outcome, nil
}

// A subpage is a real file in the project folder, and its format decides what
// that file is. The editor always edits text: Markdown, plain text and HTML
// are edited as they are stored, while a .docx is converted to Markdown on
// the way in and rebuilt on the way out.

const (
	PageFormatMarkdown = "md"
	PageFormatText     = "txt"
	PageFormatHTML     = "html"
	PageFormatDOCX     = "docx"
)

var pageFormatExtension = map[string]string{
	PageFormatMarkdown: ".md",
	PageFormatText:     ".txt",
	PageFormatHTML:     ".html",
	PageFormatDOCX:     ".docx",
}

// NormalizePageFormat validates a requested format; empty means Markdown, so
// projects written before formats existed keep working untouched.
func NormalizePageFormat(format string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(format))
	if trimmed == "" {
		return PageFormatMarkdown, nil
	}
	if _, ok := pageFormatExtension[trimmed]; !ok {
		return "", fmt.Errorf("unsupported page format %q", format)
	}
	return trimmed, nil
}

// PageFormatFromFilename maps an uploaded file to a page format.
func PageFormatFromFilename(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mdown":
		return PageFormatMarkdown, true
	case ".txt", ".text", ".log":
		return PageFormatText, true
	case ".html", ".htm", ".xhtml":
		return PageFormatHTML, true
	case ".docx":
		return PageFormatDOCX, true
	}
	return "", false
}

// PageFormatIsText reports whether the stored bytes are the editable text.
func PageFormatIsText(format string) bool {
	return format != PageFormatDOCX
}

// starterPageContent is the file a freshly created page starts from.
func starterPageContent(title, format string) string {
	switch format {
	case PageFormatText:
		return title + "\n\n"
	case PageFormatHTML:
		escaped := html.EscapeString(title)
		return "<!doctype html>\n<html lang=\"zh-Hant\">\n<head>\n" +
			"<meta charset=\"utf-8\">\n<title>" + escaped + "</title>\n" +
			"</head>\n<body>\n<h1>" + escaped + "</h1>\n\n</body>\n</html>\n"
	default:
		return "# " + title + "\n\n"
	}
}

// pageMediaSink assigns images found inside a .docx their final attachment
// URL. readDOCXPage stages the bytes and writes them only after parsing.
func (s *Store) pageMediaSink(nodeID string) docx.MediaSink {
	return func(name string, data []byte) (string, error) {
		sum := sha256.Sum256(data)
		clean := SanitizeAttachmentName(name)
		extension := filepath.Ext(clean)
		unique := strings.TrimSuffix(clean, extension) + "-" +
			hex.EncodeToString(sum[:])[:12] + extension
		return AttachmentURL(nodeID, unique), nil
	}
}

type pendingPageMedia struct {
	name string
	data []byte
}

// readDOCXPage stages embedded media in memory and only commits it after the
// complete document has parsed. A malformed document therefore cannot leave
// attachments behind, and a failed commit removes every file it created.
func (s *Store) readDOCXPage(nodeID string, data []byte) (*document.Doc, error) {
	pending := map[string]pendingPageMedia{}
	sink := s.pageMediaSink(nodeID)
	doc, err := docx.ReadDOCX(data, func(name string, data []byte) (string, error) {
		mediaURL, err := sink(name, data)
		if err != nil {
			return "", err
		}
		filename, err := url.PathUnescape(path.Base(mediaURL))
		if err != nil {
			return "", fmt.Errorf("decode attachment URL: %w", err)
		}
		pending[filename] = pendingPageMedia{name: filename, data: data}
		return mediaURL, nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.commitPageMedia(nodeID, pending); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Store) commitPageMedia(nodeID string, pending map[string]pendingPageMedia) error {
	if len(pending) == 0 {
		return nil
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	dir := s.NodeFilesDir(nodeID)
	if err := MkdirAllProjectPath(s.root, dir, 0o755); err != nil {
		return err
	}
	created := make([]string, 0, len(pending))
	rollback := func() {
		for _, target := range created {
			s.markSelfWrite(target)
			_ = s.removePath(target)
		}
	}
	for _, media := range pending {
		target := filepath.Join(dir, media.name)
		if _, err := s.statPath(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			rollback()
			return err
		}
		if err := s.WriteAtomic(target, media.data); err != nil {
			rollback()
			return err
		}
		created = append(created, target)
	}
	return nil
}

// readPageBlocks parses stored page bytes into the shared block model.
func (s *Store) readPageBlocks(nodeID, format string, data []byte) (*document.Doc, error) {
	switch format {
	case PageFormatDOCX:
		return s.readDOCXPage(nodeID, data)
	case PageFormatHTML:
		return dochtml.ReadHTML(string(data))
	default:
		return document.Parse(string(data)), nil
	}
}

// decodePageContent turns stored bytes into the text the editor edits.
func (s *Store) decodePageContent(nodeID, format string, data []byte) (string, error) {
	if PageFormatIsText(format) {
		return string(data), nil
	}
	doc, err := s.readDOCXPage(nodeID, data)
	if err != nil {
		return "", err
	}
	return document.RenderMarkdown(doc), nil
}

// encodePageContent turns editor text back into the bytes stored on disk.
func (s *Store) encodePageContent(nodeID, format, content string) ([]byte, error) {
	if PageFormatIsText(format) {
		return []byte(content), nil
	}
	return docx.RenderDOCX(document.Parse(content), document.Options{
		Assets: s.DocumentAssets(),
	})
}

// convertPageContent rewrites a page's bytes for a different format, going
// through the block model so every pair of formats is covered by one path.
func (s *Store) convertPageContent(nodeID, title, from, to string, data []byte) ([]byte, error) {
	if from == to {
		return data, nil
	}
	// Plain text is already valid Markdown; re-parsing it would only add
	// escapes the author never wrote.
	if from == PageFormatText && to == PageFormatMarkdown {
		return data, nil
	}
	doc, err := s.readPageBlocks(nodeID, from, data)
	if err != nil {
		return nil, err
	}
	switch to {
	case PageFormatMarkdown:
		return []byte(document.RenderMarkdown(doc)), nil
	case PageFormatText:
		return []byte(document.RenderText(doc)), nil
	case PageFormatHTML:
		return []byte(dochtml.RenderHTML(doc, document.Options{Title: title})), nil
	case PageFormatDOCX:
		return docx.RenderDOCX(doc, document.Options{
			Title:  title,
			Assets: s.DocumentAssets(),
		})
	}
	return nil, fmt.Errorf("unsupported page format %q", to)
}

// Node attachments live next to the node document in nodes/<id>.files/.
// They are referenced from Markdown as /api/nodes/<id>/files/<name>.

// maxPageBytes caps a single page's content. It matches the request body
// limit the HTTP layer enforces, restated here so the store refuses an
// oversized page whatever called it.
const maxPageBytes = 16 << 20

const MaxAttachmentBytes = 64 << 20

func (s *Store) NodeFilesDir(id string) string {
	return NodeAttachmentsPath(s.root, id)
}

var attachmentBadChars = regexp.MustCompile(`[^\p{L}\p{N} ._()\-\[\]]+`)

const (
	maxAttachmentNameRunes      = 120
	maxAttachmentExtensionRunes = 16
)

// SanitizeAttachmentName reduces an uploaded filename to a safe basename.
func SanitizeAttachmentName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = attachmentBadChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, " .")
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	extRunes := []rune(ext)
	if len(extRunes) > maxAttachmentExtensionRunes {
		extRunes = extRunes[:maxAttachmentExtensionRunes]
	}
	stemRunes := []rune(stem)
	stemBudget := maxAttachmentNameRunes - len(extRunes)
	if stemBudget < 1 {
		stemBudget = 1
	}
	if len(stemRunes) > stemBudget {
		stemRunes = stemRunes[:stemBudget]
	}
	if len(stemRunes) == 0 {
		stemRunes = []rune("attachment")
	}
	return string(stemRunes) + string(extRunes)
}

// UniqueAttachmentPath appends " (n)" before the extension until unused.
func UniqueAttachmentPath(dir, name string) (string, string) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for n := 1; ; n++ {
		path := filepath.Join(dir, candidate)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, candidate
		}
		candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
	}
}

func AttachmentURL(nodeID, name string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/files/" + url.PathEscape(name)
}

// Extensions that execute or script when rendered inline are always sent as
// downloads; everything else renders inline (images, PDF, text).
var ForceDownloadExt = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".svg": true,
	".js": true, ".mjs": true, ".css": true, ".xml": true,
}

// SafeInlineExt is the allowlist of attachment extensions that may be served
// inline, mapped to the exact Content-Type to declare. A denylist cannot be
// trusted here: an attachment with no extension (or an unknown one) whose body
// begins with "<!DOCTYPE html" is sniffed as text/html by net/http and then
// runs as same-origin script. Anything not in this table is served as
// application/octet-stream with a download disposition, so content sniffing
// never decides the type. Keys must be lowercase, including the leading dot.
var SafeInlineExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/vnd.microsoft.icon",
	".avif": "image/avif",
	".pdf":  "application/pdf",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".mp4":  "video/mp4",
	".webm": "video/webm",
}

// InlineContentType reports the exact type an attachment may be served inline
// with. The second result is false when the file must be downloaded instead.
func InlineContentType(name string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if ForceDownloadExt[ext] {
		return "", false
	}
	mimeType, ok := SafeInlineExt[ext]
	return mimeType, ok
}

var attachmentPathPattern = regexp.MustCompile(`^/api/nodes/([^/]+)/files/([^/]+)$`)

// maxExportImageBytes caps an image embedded into an exported document;
// anything larger stays a link.
const maxExportImageBytes = 16 << 20

// DocumentAssets lets the renderers embed node attachments instead of linking
// to a server the exported file may never reach.
func (s *Store) DocumentAssets() document.AssetResolver {
	return func(raw string) (document.Asset, bool) {
		path := raw
		if cut := strings.IndexAny(path, "?#"); cut >= 0 {
			path = path[:cut]
		}
		match := attachmentPathPattern.FindStringSubmatch(path)
		if match == nil {
			return document.Asset{}, false
		}
		nodeID, err := url.PathUnescape(match[1])
		if err != nil || !engine.ValidNodeID(nodeID) {
			return document.Asset{}, false
		}
		name, err := url.PathUnescape(match[2])
		if err != nil || name != filepath.Base(name) || strings.Contains(name, "..") {
			return document.Asset{}, false
		}
		file := filepath.Join(s.NodeFilesDir(nodeID), name)
		info, statErr := s.statPath(file)
		if statErr != nil || info.IsDir() || info.Size() > maxExportImageBytes {
			return document.Asset{}, false
		}
		data, readErr := s.ReadFile(file)
		if readErr != nil {
			return document.Asset{}, false
		}
		config, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 {
			return document.Asset{}, false
		}
		return document.Asset{
			Data:   data,
			MIME:   "image/" + format,
			Name:   name,
			Width:  config.Width,
			Height: config.Height,
		}, true
	}
}
