// Where a live document goes when nobody is holding it any more. A session is
// memory, so a deploy or a crash in the middle of two people typing takes it
// with them, together with everything the leader had not flushed to the file
// yet. The sidecar is that state written down, next to the revision of the file
// it was measured against.
//
// That revision is the whole point. The file is the document at rest, and while
// this server was down anything could have replaced it — an editor, a checkout,
// a restored backup. A sidecar whose revision no longer matches describes a
// document the user has already thrown away, and handing it to the next client
// would put the old text back on top of the new one.

package realtime

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	docSidecarDir     = "crdt"
	docSidecarSuffix  = ".bin"
	docSidecarMagicV1 = "nodevas-crdt v1"
	docSidecarMagic   = "nodevas-crdt v2"

	// A sidecar carries a session's whole log, so its ceiling is the log's plus
	// the framing around it. Anything bigger was not written by this server.
	maxDocSidecarBytes = maxDocSnapshotTotalBytes + maxDocLogBytes + maxDocUpdateBytes + (64 << 10)
	// A node document is text. Something far past this is not one, and hashing
	// it on every first open would cost more than the recovery is worth.
	maxDocFileBytes = 16 << 20
	// Past this an escaped key stops being a filename every filesystem accepts,
	// so the tail of the name becomes a fingerprint instead.
	maxDocSidecarNameBytes = 96
)

// docSidecar is one document's recoverable state: the stored log exactly as the
// session held it, and the file revision it belongs to.
type docSidecar struct {
	rev      string
	updates  []string
	snapshot bool
}

// docStoreRoot is the directory a room's sidecars live in, and whether the room
// is a project directory at all. A client that never subscribed sits in the
// catch-all room, whose key is not a path and must never reach the disk.
func docStoreRoot(room string) (string, bool) {
	if room == "" || !filepath.IsAbs(room) {
		return "", false
	}
	info, err := os.Stat(room)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return filepath.Join(room, store.DataDir, docSidecarDir), true
}

// escapeDocKey reduces a doc key to one safe filename. The key is wire format
// and carries a page id, which is whatever a client sent: everything outside a
// small alphabet is percent-encoded, the dot included, because a key of ".."
// left alone names the parent directory rather than a file in this one.
func escapeDocKey(key string) string {
	var name strings.Builder
	for index := 0; index < len(key); index++ {
		char := key[index]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9', char == '-', char == '_':
			name.WriteByte(char)
		default:
			fmt.Fprintf(&name, "%%%02X", char)
		}
	}
	if name.Len() <= maxDocSidecarNameBytes {
		return name.String()
	}
	// Truncation alone would let two long keys name one file, and the second
	// document to be opened would be handed the first one's text.
	return name.String()[:maxDocSidecarNameBytes] + "-" + store.Rev([]byte(key))
}

func docSidecarPath(dir, key string) string {
	return filepath.Join(dir, escapeDocKey(key)+docSidecarSuffix)
}

// recoverDoc is what a document brings back when the first client opens it and
// no session is live: the revision its file is at now, and the state a previous
// session left — but only when the two still agree. An empty revision means the
// file could not be read at all, which is the one case where nothing is stored
// either, since a sidecar nothing can be measured against is a sidecar that can
// never be discarded.
func (h *Hub) recoverDoc(room, key string) (updates []string, fileRev string, snapshot bool, reserved int, blocked bool) {
	dir, ok := docStoreRoot(room)
	if !ok {
		return nil, "", false, 0, false
	}
	rev, ok := docFileRev(room, key)
	if !ok {
		return nil, "", false, 0, false
	}
	path := docSidecarPath(dir, key)
	file, info, err := store.OpenProjectFile(room, path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("crdt sidecar %s: %v", path, err)
		}
		return nil, rev, false, 0, false
	}
	defer file.Close()
	if info.Size() > maxDocSidecarBytes {
		return nil, dropDocSidecar(room, path, rev, fmt.Errorf("%d bytes is past the ceiling", info.Size())), false, 0, false
	}
	reader := bufio.NewReader(io.LimitReader(file, int64(maxDocSidecarBytes)+1))
	header, err := readDocSidecarHeader(reader)
	if err != nil {
		return nil, dropDocSidecar(room, path, rev, err), false, 0, false
	}
	storedRev, headerSnapshot, legacy, err := parseDocSidecarHeader(header)
	if err != nil {
		return nil, dropDocSidecar(room, path, rev, err), false, 0, false
	}
	if storedRev != rev {
		return nil, dropDocSidecar(room, path, rev, fmt.Errorf("written against revision %s, the file is at %s", storedRev, rev)), false, 0, false
	}
	large := info.Size() > int64(maxDocLogBytes+maxDocUpdateBytes+(64<<10))
	if large && (!headerSnapshot || legacy) {
		// A v1 file cannot prove which entry is the base without reading its
		// potentially hostile body. Preserve it for manual recovery, but do not
		// allocate past the normal delta ceiling.
		log.Printf("crdt sidecar %s: large recovery requires an explicit v2 snapshot base", path)
		return nil, rev, false, 0, true
	}
	if large {
		h.docEncode <- struct{}{}
		defer func() { <-h.docEncode }()
		reservation := int(info.Size())
		h.mu.Lock()
		if h.snapshotBytes+reservation > maxHubSnapshotBytes {
			h.mu.Unlock()
			return nil, rev, false, 0, true
		}
		h.snapshotBytes += reservation
		h.mu.Unlock()
		reserved = reservation
		if h.recoverDocBeforeDecode != nil {
			h.recoverDocBeforeDecode()
		}
	}
	releaseReservation := func() {
		if reserved == 0 {
			return
		}
		h.mu.Lock()
		h.snapshotBytes -= reserved
		h.mu.Unlock()
		reserved = 0
	}
	stored, err := decodeDocSidecarBody(reader, storedRev, headerSnapshot, legacy)
	if err != nil {
		releaseReservation()
		return nil, dropDocSidecar(room, path, rev, err), false, 0, false
	}
	if reserved != 0 {
		baseBytes := 0
		if stored.snapshot && len(stored.updates) > 0 {
			baseBytes = len(stored.updates[0])
		}
		h.mu.Lock()
		h.snapshotBytes += baseBytes - reserved
		h.mu.Unlock()
		reserved = baseBytes
	}
	return stored.updates, rev, stored.snapshot, reserved, false
}

// dropDocSidecar removes a sidecar that can never be used again and says why.
// Nothing here is fatal: the document is still on disk, and the client that
// could not be given the session back seeds from the file like the first opener
// it now is.
func dropDocSidecar(room, path, rev string, reason error) string {
	log.Printf("crdt sidecar %s discarded: %v", path, reason)
	if err := store.RemoveProjectPath(room, path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("crdt sidecar %s: %v", path, err)
	}
	return rev
}

// persistDoc writes down what a session would need to come back, or clears the
// file when there is nothing left to recover: a session that ends with an empty
// log has nothing the file does not already have, and an older sidecar left
// behind would speak for the document at the next open.
//
// Called with the hub lock released. It is a disk write, and every broadcast in
// the process queues behind that mutex.
func (h *Hub) persistDoc(room, key string, card docSidecar) error {
	dir, ok := docStoreRoot(room)
	if !ok {
		// The catch-all room is intentionally memory-only: an unsubscribed
		// client may collaborate there, but it has no project root that may be
		// touched on disk. There is therefore no persistence operation to fail.
		return nil
	}
	path := docSidecarPath(dir, key)
	if card.rev == "" || len(card.updates) == 0 {
		if err := store.RemoveProjectPath(room, path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove document sidecar: %w", err)
		}
		return nil
	}
	estimated := 0
	for _, update := range card.updates {
		estimated += len(update)
	}
	if estimated > maxDocLogBytes {
		h.docEncode <- struct{}{}
		defer func() { <-h.docEncode }()
		if h.persistDocBeforeEncode != nil {
			h.persistDocBeforeEncode()
		}
	}
	data := encodeDocSidecar(card)
	if len(data) > maxDocSidecarBytes {
		return fmt.Errorf("document sidecar is %d bytes, above its %d byte limit", len(data), maxDocSidecarBytes)
	}
	// The sidecar is document content, not ordinary project metadata. Preserve
	// the private directory mode the original writer used even though the
	// shared atomic writer defaults ordinary parent directories to 0755.
	if err := store.MkdirAllProjectPath(room, dir, 0o700); err != nil {
		return fmt.Errorf("create document sidecar directory: %w", err)
	}
	if h.persistDocBeforeWrite != nil {
		h.persistDocBeforeWrite()
	}
	if err := store.WriteProjectFileAtomicMode(room, path, data, 0o600); err != nil {
		return fmt.Errorf("write document sidecar: %w", err)
	}
	return nil
}

func encodeDocSidecar(card docSidecar) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s %s %t\n", docSidecarMagic, card.rev, card.snapshot)
	for _, update := range card.updates {
		// Length-prefixed rather than one update per line: the payload is
		// opaque to this server, and a delimiter it happened to contain would
		// come back as two updates that are each half of one.
		fmt.Fprintf(&out, "%d\n", len(update))
		out.WriteString(update)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func decodeDocSidecar(data []byte) (docSidecar, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	header, err := readDocSidecarHeader(reader)
	if err != nil {
		return docSidecar{}, err
	}
	rev, snapshot, legacy, err := parseDocSidecarHeader(header)
	if err != nil {
		return docSidecar{}, err
	}
	return decodeDocSidecarBody(reader, rev, snapshot, legacy)
}

func readDocSidecarHeader(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return "", errors.New("no bounded header")
	}
	return strings.TrimSuffix(string(line), "\n"), nil
}

func parseDocSidecarHeader(header string) (rev string, snapshot, legacy bool, err error) {
	if rest, ok := strings.CutPrefix(header, docSidecarMagic+" "); ok {
		var encoded string
		rev, encoded, ok = strings.Cut(rest, " ")
		if !ok || (encoded != "true" && encoded != "false") {
			return "", false, false, errors.New("invalid v2 sidecar header")
		}
		snapshot = encoded == "true"
	} else if rest, ok := strings.CutPrefix(header, docSidecarMagicV1+" "); ok {
		rev = rest
		legacy = true
	} else {
		return "", false, false, errors.New("not a sidecar this server wrote")
	}
	if !validDocRev(rev) {
		return "", false, false, fmt.Errorf("unusable revision %q", rev)
	}
	return rev, snapshot, legacy, nil
}

func decodeDocSidecarBody(reader *bufio.Reader, rev string, snapshot, legacy bool) (docSidecar, error) {
	card := docSidecar{rev: rev, snapshot: snapshot}
	total := 0
	tailBytes := 0
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			if card.snapshot && len(card.updates) == 0 {
				return docSidecar{}, errors.New("snapshot sidecar has no base")
			}
			return card, nil
		}
		if err != nil {
			return docSidecar{}, errors.New("truncated after the last update")
		}
		length := strings.TrimSuffix(string(line), "\n")
		size, err := strconv.Atoi(length)
		entry := len(card.updates)
		if err != nil || size <= 0 || size > maxDocSnapshotTotalBytes {
			return docSidecar{}, fmt.Errorf("unusable update length %q", length)
		}
		isBase := snapshot && entry == 0
		if !isBase && size > maxDocUpdateBytes {
			if legacy && entry == 0 {
				card.snapshot = true
				snapshot = true
				isBase = true
			} else {
				return docSidecar{}, errors.New("stored delta is above the frame ceiling")
			}
		}
		if !isBase {
			tailBytes += size
		}
		tailEntries := entry + 1
		if snapshot {
			tailEntries--
		}
		if tailEntries > maxDocDeltaEntries {
			return docSidecar{}, errors.New("stored log has too many delta entries")
		}
		if tailBytes > maxDocLogBytes+maxDocUpdateBytes {
			return docSidecar{}, errors.New("stored delta log is past the ceiling")
		}
		total += size
		// The live session retains one bounded update beyond its ordinary
		// ceiling, then freezes until a snapshot arrives. Recovery admits the
		// same state so a restart cannot silently discard that accepted update.
		if total > maxDocSnapshotTotalBytes+maxDocLogBytes+maxDocUpdateBytes {
			return docSidecar{}, errors.New("stored log is past the ceiling")
		}
		var payload strings.Builder
		payload.Grow(size)
		if _, err := io.CopyN(&payload, reader, int64(size)); err != nil {
			return docSidecar{}, errors.New("truncated update")
		}
		if end, err := reader.ReadByte(); err != nil || end != '\n' {
			return docSidecar{}, errors.New("unterminated update")
		}
		card.updates = append(card.updates, payload.String())
	}
}

// docFileRev fingerprints the file this document is stored in, with store.Rev
// so the value means what it means everywhere else — it is compared against the
// revision a leader reports over the wire, which the store computed the same
// way. A file that is not there yet counts as empty rather than as an error, so
// a node opened before it was ever saved is still recoverable.
func docFileRev(room, key string) (string, bool) {
	path, ok := docFilePath(room, key)
	if !ok {
		return store.Rev(nil), true
	}
	data, err := store.ReadProjectFileLimit(room, path, int64(maxDocFileBytes))
	if errors.Is(err, fs.ErrNotExist) {
		return store.Rev(nil), true
	}
	if err != nil {
		return "", false
	}
	return store.Rev(data), true
}

// existingDocFileRev is the stricter form used to authorize doc-flushed. The
// recovery path above intentionally calls a missing document empty; a flush
// must instead prove that a regular project file with exactly this content is
// already on disk before it may discard recovery state.
func existingDocFileRev(room, key string) (string, bool) {
	path, ok := docFilePath(room, key)
	if !ok {
		return "", false
	}
	data, err := store.ReadProjectFileLimit(room, path, int64(maxDocFileBytes))
	if err != nil {
		return "", false
	}
	return store.Rev(data), true
}

// docFilePath is the file behind a doc key: a node's Markdown, or one of its
// subpages, whose extension the page's format decides and which is therefore
// found by scanning the directory rather than by pasting the page id onto it.
// Scanning is also what makes the page id harmless here — it is compared
// against names that already exist, never used as a path component.
//
// False means there is no file to fingerprint, which covers a node that has
// never been saved and a key whose node id points outside the project. Both are
// empty documents as far as this is concerned; the sidecar's own path is safe
// on its own terms.
func docFilePath(room, key string) (string, bool) {
	node, page := splitDocKey(key)
	if page == "" {
		return contained(room, store.NodeDocPath(room, node))
	}
	dir, ok := contained(room, store.NodePagesPath(room, node))
	if !ok {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "pages.json" {
			continue
		}
		if strings.TrimSuffix(name, filepath.Ext(name)) == page {
			return filepath.Join(dir, name), true
		}
	}
	return "", false
}

// contained refuses a path that left the project directory. A node id arrives
// from the wire, and a path built from one is safe only once it has been
// checked rather than assumed.
func contained(room, path string) (string, bool) {
	root := filepath.Clean(room)
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}
