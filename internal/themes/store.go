// Package themes keeps theme packs: a JSON manifest and images in a zip, taken
// from any signed-in person at runtime and served to whoever picks the pack.
//
// A pack is data. Nothing in one runs, and the zip it arrives in is treated as
// hostile: readArchive and extract refuse the whole of it for any one fault.
package themes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/store"
)

const (
	// ChangedEvent is the realtime event each change to the packs sends.
	ChangedEvent = "themes-changed"
	// DirName is the packs' folder under the workspace's data directory.
	DirName = "themes"

	defaultSetting = "theme.default"

	// Beside the packs in their folder, and no pack id can start with a dot.
	// A set-aside directory names its pack: .old.<id>.<random>.
	tempPrefix  = ".tmp."
	asidePrefix = ".old."
)

// Limits are the caps on what a pack and the server may hold. They are a value
// on the Store so that a test need not write half a gigabyte to pass one.
type Limits struct {
	MaxZipBytes      int64
	MaxFileBytes     int64
	MaxManifestBytes int64
	MaxTotalBytes    int64
	MaxFiles         int
	MaxServerBytes   int64
	MaxPacksPerOwner int
}

// DefaultLimits are the limits a server runs with.
func DefaultLimits() Limits {
	return Limits{
		MaxZipBytes:      200 << 20,
		MaxFileBytes:     16 << 20,
		MaxManifestBytes: 4 << 20,
		MaxTotalBytes:    500 << 20,
		MaxFiles:         2000,
		MaxServerBytes:   2 << 30,
		MaxPacksPerOwner: 5,
	}
}

// Error is a failure that knows its HTTP status.
type Error struct {
	status int
	err    error
}

// Status is the HTTP status this failure maps to.
func (e *Error) Status() int { return e.status }

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return e.err }

// ErrNotFound is a pack or a file that is not there.
var ErrNotFound error = &Error{status: http.StatusNotFound, err: locale.Errorf("theme pack not found", "找不到主題套件")}

func tooLarge(limit int64) error {
	return &Error{status: http.StatusRequestEntityTooLarge,
		err: locale.Errorf("the zip is over %d bytes", "zip 超過 %d 位元組", limit)}
}

// TooLarge is the refusal of an upload over the limit of limits.
func (l Limits) TooLarge() error { return tooLarge(l.MaxZipBytes) }

func conflict(english, chinese string, args ...any) error {
	return &Error{status: http.StatusConflict, err: locale.Errorf(english, chinese, args...)}
}

func forbidden(english, chinese string, args ...any) error {
	return &Error{status: http.StatusForbidden, err: locale.Errorf(english, chinese, args...)}
}

// Pack is what is known of a stored pack.
type Pack struct {
	ID      string `json:"id"`
	Name    Name   `json:"name"`
	Version string `json:"version"`
	Author  string `json:"author"`
	// Owner is who imported it as people read it: the account's name, or the
	// stored id once that account is gone. OwnerID is what decides.
	Owner     string `json:"owner"`
	OwnerID   string `json:"-"`
	SizeBytes int64  `json:"sizeBytes"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Mine      bool   `json:"mine"`
	CanDelete bool   `json:"canDelete"`
}

// For is the pack as actor sees it.
func (p Pack) For(actor identity.Actor) Pack {
	p.Mine = p.OwnerID == actor.ID
	p.CanDelete = !actor.IsAgent() && (p.Mine || actor.IsAdmin())
	return p
}

// Store keeps packs: their files in dir, what is known of them in the
// database.
type Store struct {
	db     *db.DB
	dir    string
	limits Limits
	// mu makes the check of quotas and owners, the swap of directories and the
	// write of the row one step. One process holds a workspace, so this is
	// all the serialising there is to do; extraction runs outside it.
	mu sync.Mutex
	// Vars only so tests can stand in for a disk that refuses.
	rename    func(root *os.Root, from, to string) error
	removeAll func(root *os.Root, name string) error
}

func renameInRoot(root *os.Root, from, to string) error {
	return store.RenameWithRetry(func() error { return root.Rename(from, to) })
}

func removeAllInRoot(root *os.Root, name string) error { return root.RemoveAll(name) }

func isTemp(name string) bool { return strings.HasPrefix(name, tempPrefix) }

// NewStore is a Store on database with its packs in dir.
func NewStore(database *db.DB, dir string) *Store {
	return &Store{db: database, dir: dir, limits: DefaultLimits(), rename: renameInRoot, removeAll: removeAllInRoot}
}

// Limits are the store's limits.
func (s *Store) Limits() Limits { return s.limits }

// SetLimits replaces the limits, for tests.
func (s *Store) SetLimits(limits Limits) { s.limits = limits }

func (s *Store) openRoot() (*os.Root, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenRoot(s.dir)
}

func sideName(prefix string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random[:]), nil
}

func exists(root *os.Root, name string) (bool, error) {
	_, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Recover clears what a crash left, as the server starts: an extraction is
// thrown away, and a pack set aside goes back unless its replacement made it.
func (s *Store) Recover() error {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var failures []error
	for _, entry := range entries {
		name := entry.Name()
		if isTemp(name) {
			failures = append(failures, s.removeAll(root, name))
			continue
		}
		rest, ok := strings.CutPrefix(name, asidePrefix)
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, ".")
		present, err := exists(root, id)
		if err != nil {
			failures = append(failures, err)
		} else if present || !ValidID(id) {
			failures = append(failures, s.removeAll(root, name))
		} else {
			failures = append(failures, s.rename(root, name, id))
		}
	}
	return errors.Join(failures...)
}

// Import validates a zip and stores its pack for owner, replacing the pack
// with that id if it is theirs. Any failure leaves things as they were.
func (s *Store) Import(ctx context.Context, owner string, archive io.ReaderAt, size int64) (Pack, error) {
	limits := s.limits
	if size > limits.MaxZipBytes {
		return Pack{}, tooLarge(limits.MaxZipBytes)
	}
	parsed, err := readArchive(archive, size, limits)
	if err != nil {
		return Pack{}, err
	}
	id := parsed.manifest.ID
	// Asked now so that a doomed upload costs no disk, and again below, as the
	// answer may change while this one extracts.
	if err := s.admit(ctx, id, owner, 0, limits); err != nil {
		return Pack{}, err
	}
	root, err := s.openRoot()
	if err != nil {
		return Pack{}, err
	}
	defer root.Close()
	temp, err := sideName(tempPrefix)
	if err != nil {
		return Pack{}, err
	}
	if err := root.Mkdir(temp, 0o755); err != nil {
		return Pack{}, err
	}
	// After a swap there is nothing here to remove; this is every other path.
	defer func() {
		if err := s.removeAll(root, temp); err != nil {
			log.Printf("theme pack: remove %s: %v", temp, err)
		}
	}()
	total, err := extract(ctx, root, temp, parsed, limits)
	if err != nil {
		return Pack{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// All the work is done: a client leaving now must not stop it half-way.
	ctx = context.WithoutCancel(ctx)
	if err := s.admit(ctx, id, owner, total, limits); err != nil {
		return Pack{}, err
	}
	if err := s.install(ctx, root, temp, parsed.manifest, owner, total); err != nil {
		return Pack{}, err
	}
	return s.Get(ctx, id)
}

// admit asks whether owner may hold id at size bytes: it must not be someone
// else's, and the quotas count the new size in place of a replaced pack's.
func (s *Store) admit(ctx context.Context, id, owner string, size int64, limits Limits) error {
	var current string
	err := s.db.QueryRowContext(ctx, `SELECT owner FROM theme_packs WHERE id = ?`, id).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && current != owner {
		return conflict("the id %q belongs to a pack someone else imported", "代號「%s」屬於別人匯入的套件", id)
	}
	var count int
	var others int64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM theme_packs WHERE owner = ? AND id <> ?`, owner, id).Scan(&count)
	if err != nil {
		return err
	}
	if count >= limits.MaxPacksPerOwner {
		return forbidden("you can import at most %d theme packs; delete one first", "你最多可匯入 %d 個主題套件，請先刪除一個", limits.MaxPacksPerOwner)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0)::BIGINT FROM theme_packs WHERE id <> ?`, id).Scan(&others)
	if err != nil {
		return err
	}
	if others+size > limits.MaxServerBytes {
		return forbidden("theme packs on this server may take at most %d bytes", "這個伺服器的主題套件最多佔用 %d 位元組", limits.MaxServerBytes)
	}
	return nil
}

// install swaps temp in as the pack's directory and writes its row, or puts
// back what was there. Directories cannot be exchanged in one step, so a
// reader may for a moment find no pack, but never half of one.
func (s *Store) install(ctx context.Context, root *os.Root, temp string, manifest *Manifest, owner string, size int64) error {
	id := manifest.ID
	name, err := json.Marshal(manifest.Name)
	if err != nil {
		return err
	}
	aside, err := s.setAside(root, id)
	if err != nil {
		return conflict("the pack could not be replaced, as its files are in use; it is unchanged, so try again",
			"無法取代套件，因為它的檔案正在使用中；套件沒有變更，請再試一次")
	}
	if err := s.rename(root, temp, id); err != nil {
		s.putBack(root, aside, id)
		log.Printf("theme pack %s: move into place: %v", id, err)
		return conflict("the pack could not be moved into place; anything it replaced is unchanged, so try again",
			"無法將套件移到定位；被取代的內容沒有變更，請再試一次")
	}
	now := db.Now()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO theme_packs (id, owner, version, name, author, size_bytes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET version = excluded.version, name = excluded.name, author = excluded.author,
    size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`,
		id, owner, manifest.Version, string(name), manifest.Author, size, now, now)
	if err != nil {
		// Back to temp, which Import removes on its way out.
		if undoErr := s.rename(root, id, temp); undoErr != nil {
			log.Printf("theme pack %s: take the new files back out: %v", id, undoErr)
		}
		s.putBack(root, aside, id)
		return err
	}
	if aside != "" {
		// The pack is in place and right; what is left is trash Recover clears.
		if err := s.removeAll(root, aside); err != nil {
			log.Printf("theme pack %s: remove replaced files %s: %v", id, aside, err)
		}
	}
	return nil
}

// setAside moves a pack's directory out of the way and names where it went,
// "" if it has none.
func (s *Store) setAside(root *os.Root, id string) (string, error) {
	present, err := exists(root, id)
	if err != nil || !present {
		return "", err
	}
	aside, err := sideName(asidePrefix + id + ".")
	if err != nil {
		return "", err
	}
	if err := s.rename(root, id, aside); err != nil {
		log.Printf("theme pack %s: set aside: %v", id, err)
		return "", err
	}
	return aside, nil
}

func (s *Store) putBack(root *os.Root, aside, id string) {
	if aside == "" {
		return
	}
	if err := s.rename(root, aside, id); err != nil {
		// Recover puts it back when the server next starts.
		log.Printf("theme pack %s: put back %s: %v", id, aside, err)
	}
}

const packColumns = `p.id, p.owner, COALESCE(a.name, p.owner), p.version, p.name, p.author, p.size_bytes, p.created_at, p.updated_at`

const packTables = `theme_packs p LEFT JOIN accounts a ON a.id = p.owner`

func scanPack(row interface{ Scan(...any) error }) (Pack, error) {
	var pack Pack
	var name string
	err := row.Scan(&pack.ID, &pack.OwnerID, &pack.Owner, &pack.Version, &name, &pack.Author, &pack.SizeBytes, &pack.CreatedAt, &pack.UpdatedAt)
	if err != nil {
		return Pack{}, err
	}
	if err := json.Unmarshal([]byte(name), &pack.Name); err != nil {
		return Pack{}, fmt.Errorf("theme pack %s: name: %w", pack.ID, err)
	}
	return pack, nil
}

// List is every pack, sorted by id.
func (s *Store) List(ctx context.Context) ([]Pack, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+packColumns+` FROM `+packTables+` ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	packs := []Pack{}
	for rows.Next() {
		pack, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		packs = append(packs, pack)
	}
	return packs, rows.Err()
}

// Get is one pack, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Pack, error) {
	if !ValidID(id) {
		return Pack{}, ErrNotFound
	}
	pack, err := scanPack(s.db.QueryRowContext(ctx, `SELECT `+packColumns+` FROM `+packTables+` WHERE p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Pack{}, ErrNotFound
	}
	return pack, err
}

// Default is the site's default pack, "" when there is none.
func (s *Store) Default(ctx context.Context) (string, error) {
	return s.db.Setting(ctx, defaultSetting, "")
}

// SetDefault makes id the site's default pack; "" clears it. An id with no
// pack is ErrNotFound.
func (s *Store) SetDefault(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
	}
	return s.db.SetSetting(ctx, defaultSetting, id)
}

// Delete removes a pack for its owner or an administrator, and clears the
// site default if it was this one.
//
// The files go first, and the row only once they have: a refusal at either
// step is reported and asking again finishes the job, so neither is ever left
// without the other unsaid. The files go outside the transaction, as there is
// one write connection and thousands of files are slow to remove.
func (s *Store) Delete(ctx context.Context, actor identity.Actor, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pack, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !pack.For(actor).CanDelete {
		return forbidden("only whoever imported a theme pack or an administrator can delete it", "只有匯入主題套件的人或管理員才能刪除它")
	}
	ctx = context.WithoutCancel(ctx)
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	filesRemain := func() error {
		return conflict("the pack's files could not be removed, so the pack is still here; try again",
			"無法移除套件的檔案，所以套件仍然存在；請再試一次")
	}
	aside, err := s.setAside(root, id)
	if err != nil {
		return filesRemain()
	}
	if aside != "" {
		if err := s.removeAll(root, aside); err != nil {
			log.Printf("theme pack %s: remove files: %v", id, err)
			s.putBack(root, aside, id)
			return filesRemain()
		}
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM theme_packs WHERE id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ? AND value = ?`, defaultSetting, id)
		return err
	})
	if err != nil {
		log.Printf("theme pack %s: remove row: %v", id, err)
		return conflict("the pack's files are gone but its record could not be removed; try again",
			"套件的檔案已移除，但無法移除它的紀錄；請再試一次")
	}
	return nil
}

// Open opens a pack's image, or its manifest by ManifestFile, without ever
// leaving the pack's directory or following a link. The caller closes it.
// Whatever cannot be opened that way is ErrNotFound; a directory with no row
// is not a pack.
func (s *Store) Open(ctx context.Context, id, name string) (*os.File, fs.FileInfo, error) {
	if name != ManifestFile {
		if _, ok := ContentType(name); !ok || !validPath(name) {
			return nil, nil, ErrNotFound
		}
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(s.dir, id)
	file, info, err := store.OpenProjectFile(dir, filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return file, info, nil
}
