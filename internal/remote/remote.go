package remote

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A Remote is off-box storage for project bundles (.veproj archives). It is an
// import source and a backup target, never part of the write Path: the
// server's local disk stays the single source of truth. See plan.md P3.
//
// The interface is deliberately small — Push / Pull / List over opaque bundle
// blobs — so a new backend is one implementation, not a change spread across
// handlers. Every method takes a context so a slow network never wedges a
// request goroutine.

// RemoteRef identifies one bundle a Remote holds.
type RemoteRef struct {
	// ID is the backend-specific handle used to Pull the bundle back. For the
	// folder backend it is the file name; for Drive it is the file id.
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Hash is the SHA-256 of the bundle bytes when the backend can provide it.
	// It is optional for remote files created before sync manifests.
	Hash string `json:"hash,omitempty"`
	// AppOwned reports that this object carries the marker Nodevas stamps on
	// every bundle it uploads. Retention deletes nothing without it: a bundle
	// someone else dropped in the backup folder, or one without a marker, is not
	// ours to remove. Each backend sets it
	// from the object's own metadata rather than from the query that found it,
	// so widening a listing query can never widen what may be deleted.
	AppOwned bool `json:"appOwned,omitempty"`
}

// RemoteStore is a place bundles can be pushed to and pulled from.
type RemoteStore interface {
	// Kind is the stable backend identifier ("folder", "drive").
	Kind() string
	// Push uploads a bundle under a display name and returns its ref. size is
	// the byte length when known (>=0) so backends that need it (Drive) can set
	// Content-Length; pass -1 when unknown.
	Push(ctx context.Context, name string, data io.Reader, size int64) (RemoteRef, error)
	// Pull opens a bundle by id for reading. The caller closes it.
	Pull(ctx context.Context, id string) (io.ReadCloser, error)
	// List returns every bundle the Remote holds, newest first.
	List(ctx context.Context) ([]RemoteRef, error)
	// Delete removes one bundle by id. It is only ever called by retention
	// (see backup.go) and only for refs whose AppOwned is true. Deleting an id
	// that is already gone is success, so a retry after a half-failed prune
	// does not report an error for work that is already done.
	Delete(ctx context.Context, id string) error
}

const RemoteBundleExt = ".veproj"

// folderBundleMarker is how the folder backend recognises its own uploads. The
// Drive backend has appProperties for this; a directory has nothing but names,
// so Push writes the marker into the file name and List reads it back. Bundles
// written before this marker existed simply never match, which means retention
// leaves them alone forever — the safe direction to be wrong in.
const folderBundleMarker = "nvb"

// folderBundlePattern matches exactly what folderRemote.Push produces: a
// sanitized base, the marker, and the 12 hex digits of randomSuffix.
var folderBundlePattern = regexp.MustCompile(`-` + folderBundleMarker + `[0-9a-f]{12}\` + RemoteBundleExt + `$`)

// folderBundleIsOurs reports whether a file name in the backup directory was
// written by this app's Push.
func folderBundleIsOurs(name string) bool {
	return folderBundlePattern.MatchString(name)
}

// bundleNamePattern keeps a Push display name to a safe, human-readable slug so
// it can be embedded in a file name without escaping surprises.
var bundleNamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeBundleName turns an arbitrary label into a file-name-safe base with
// no extension, Path separators, or leading dots.
func sanitizeBundleName(name string) string {
	base := bundleNamePattern.ReplaceAllString(strings.TrimSpace(name), "-")
	base = strings.Trim(base, "-.")
	base = strings.TrimSuffix(base, RemoteBundleExt)
	if len(base) > 80 {
		base = strings.Trim(base[:80], "-.")
	}
	if base == "" {
		base = "bundle"
	}
	return base
}

// randomSuffix returns a short random token so two pushes of the same name
// never collide.
func randomSuffix() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func bytesSHA256(data []byte) string {
	hash, err := readerSHA256(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return rawSHA256(data)
	}
	return hash
}

func rawSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readerSHA256 hashes ZIP contents independent of archive timestamps and entry
// order. Entry bodies are streamed one at a time rather than retained in a
// slice, which keeps memory bounded by the ZIP decompressor's buffer.
func readerSHA256(ctx context.Context, source io.ReaderAt, size int64) (string, error) {
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return rawReaderSHA256(ctx, source, size)
	}
	entries := append([]*zip.File(nil), reader.File...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	hash := sha256.New()
	var length [8]byte
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		_, _ = hash.Write([]byte(entry.Name))
		_, _ = hash.Write([]byte{0})
		binary.BigEndian.PutUint64(length[:], entry.UncompressedSize64)
		_, _ = hash.Write(length[:])
		body, openErr := entry.Open()
		if openErr != nil {
			return rawReaderSHA256(ctx, source, size)
		}
		written, copyErr := copyWithContext(ctx, hash, body)
		closeErr := body.Close()
		if copyErr != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return rawReaderSHA256(ctx, source, size)
		}
		if closeErr != nil || written < 0 || uint64(written) != entry.UncompressedSize64 {
			return rawReaderSHA256(ctx, source, size)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func rawReaderSHA256(ctx context.Context, source io.ReaderAt, size int64) (string, error) {
	hash := sha256.New()
	if _, err := copyWithContext(ctx, hash, io.NewSectionReader(source, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return readerSHA256(ctx, file, info.Size())
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(buf)
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return io.CopyBuffer(dst, &contextReader{ctx: ctx, r: src}, make([]byte, 128<<10))
}

type contextReadCloser struct {
	ctx context.Context
	io.ReadCloser
}

func (r *contextReadCloser) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadCloser.Read(buf)
}

// folderRemote writes bundles to a directory on the server machine. It is the
// simplest backend — a local mirror or a synced-folder target — and the one
// the tests exercise without any network. The directory is a host Path, so the
// endpoints only let it be configured while the server is loopback-only.
type folderRemote struct {
	dir string
}

func NewFolderRemote(dir string) (folderRemote, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return folderRemote{}, fmt.Errorf("folder remote needs a directory")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return folderRemote{}, err
	}
	return folderRemote{dir: abs}, nil
}

func (folderRemote) Kind() string { return "folder" }

func (f folderRemote) Push(ctx context.Context, name string, data io.Reader, _ int64) (RemoteRef, error) {
	if err := ctx.Err(); err != nil {
		return RemoteRef{}, err
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return RemoteRef{}, err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return RemoteRef{}, err
	}
	fileName := sanitizeBundleName(name) + "-" + folderBundleMarker + suffix + RemoteBundleExt
	final := filepath.Join(f.dir, fileName)
	tmp := final + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return RemoteRef{}, err
	}
	written, copyErr := copyWithContext(ctx, out, data)
	syncErr := error(nil)
	if copyErr == nil {
		syncErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return RemoteRef{}, copyErr
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return RemoteRef{}, syncErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return RemoteRef{}, closeErr
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmp)
		return RemoteRef{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return RemoteRef{}, err
	}
	ref := RemoteRef{
		ID:       fileName,
		Name:     fileName,
		Size:     written,
		AppOwned: folderBundleIsOurs(fileName),
	}
	if info, statErr := os.Stat(final); statErr == nil {
		ref.Modified = info.ModTime()
	}
	return ref, nil
}

func (f folderRemote) Pull(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := safeBundleID(id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(f.dir, name))
	if err != nil {
		return nil, err
	}
	return &contextReadCloser{ctx: ctx, ReadCloser: file}, nil
}

// Delete removes one bundle from the backup directory. Unlike Drive, a
// directory gives the process no protection at all — it can unlink anything the
// server user can reach — so the guard has to be here: the id must survive
// safeBundleID (no traversal, no absolute path) *and* carry the marker Push
// writes into the name. A .veproj the user copied into the folder by hand has
// no marker and is refused, not deleted.
func (f folderRemote) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := safeBundleID(id)
	if err != nil {
		return err
	}
	if !folderBundleIsOurs(name) {
		return fmt.Errorf("refusing to delete %q: not a bundle this app created", name)
	}
	if err := os.Remove(filepath.Join(f.dir, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

const (
	folderListBatch         = 256
	maxFolderEntriesScanned = 10_000
	maxRemoteListItems      = 1_000
)

func (f folderRemote) List(ctx context.Context) ([]RemoteRef, error) {
	dir, err := os.Open(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()
	refs := make([]RemoteRef, 0, min(maxRemoteListItems, folderListBatch))
	scanned := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := dir.ReadDir(folderListBatch)
		for _, entry := range entries {
			scanned++
			if scanned > maxFolderEntriesScanned {
				return nil, fmt.Errorf("folder remote contains more than %d entries", maxFolderEntriesScanned)
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), RemoteBundleExt) {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			refs = append(refs, RemoteRef{
				ID:       entry.Name(),
				Name:     entry.Name(),
				Size:     info.Size(),
				Modified: info.ModTime(),
				AppOwned: folderBundleIsOurs(entry.Name()),
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Modified.After(refs[j].Modified)
	})
	if len(refs) > maxRemoteListItems {
		refs = refs[:maxRemoteListItems]
	}
	// Sync classification compares only the newest snapshot. Hashing every
	// retained archive would repeatedly decompress the entire backup history.
	if len(refs) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		refs[0].Hash, _ = fileSHA256(ctx, filepath.Join(f.dir, refs[0].Name))
	}
	return refs, nil
}

// safeBundleID rejects any id that could escape the Remote's directory. A
// bundle id from the folder backend is always a bare file name.
func safeBundleID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("bundle id is required")
	}
	if id != filepath.Base(id) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid bundle id %q", id)
	}
	if !strings.HasSuffix(id, RemoteBundleExt) {
		return "", fmt.Errorf("invalid bundle id %q", id)
	}
	return id, nil
}
