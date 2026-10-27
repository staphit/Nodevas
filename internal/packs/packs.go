// Package packs keeps the workspace's view packs: user-installed skins for
// Station's war room and boss map (docs/view-packs.md).
//
// A pack is data, not code, and it comes from someone else, so everything
// here treats it as untrusted: a zip is checked against every rule before a
// byte of it reaches the disk, a pack on disk is checked again whenever it is
// listed, and a file is served only when it is an image a sprite names.
//
// Each pack is one folder, <workspace>/.vised/packs/<id>/, holding its
// pack.json as written, the images its sprites name at their own paths, and
// install.json with who installed it and when. Copying a pack's folder there
// by hand works too; it simply has no install.json.
package packs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodevas/internal/store"
)

const (
	manifestName = "pack.json"
	installName  = "install.json"
)

// ErrNotFound is an unknown pack, or a file that is not one of its sprites.
var ErrNotFound = errors.New("no such view pack")

// Pack is one installed pack as the web client reads it.
type Pack struct {
	ID      string            `json:"id"`
	Name    map[string]string `json:"name"`
	Layout  string            `json:"layout"`
	Palette map[string]string `json:"palette"`
	// Sprites maps each sprite key to the URL its image is served at.
	Sprites     map[string]string `json:"sprites"`
	InstalledAt int64             `json:"installedAt"`
	InstalledBy string            `json:"installedBy"`
}

// installRecord is install.json.
type installRecord struct {
	InstalledAt int64  `json:"installedAt"`
	InstalledBy string `json:"installedBy"`
}

// Dir is where a workspace keeps its packs.
func Dir(workspace string) string {
	return filepath.Join(workspace, store.DataDir, "packs")
}

// Store reads and writes the packs folder.
type Store struct {
	// dir is a function because the server can switch workspaces.
	dir func() string
	// mu serialises installs and removals: each swaps folders in place, and
	// two at once could leave either half-done.
	mu sync.Mutex
}

// New returns a Store over the folder dir returns.
func New(dir func() string) *Store {
	return &Store{dir: dir}
}

// FileURL is the URL a pack's file is served at. The ?v= changes on every
// install, so a browser may cache the file for as long as the URL lasts.
func FileURL(id, path string, installedAt int64) string {
	return "/api/packs/" + id + "/files/" + path + "?v=" + strconv.FormatInt(installedAt, 10)
}

// Install checks a zip and, if it passes, installs it in place of any pack
// with the same id. A zip that breaks a rule fails with *InvalidError and
// changes nothing.
func (s *Store) Install(zipData []byte, by string) (*Pack, error) {
	p, err := parseZip(zipData)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.dir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create packs folder: %w", err)
	}
	id := p.manifest.ID
	final := filepath.Join(root, id)

	record := installRecord{InstalledAt: time.Now().Unix(), InstalledBy: by}
	// Two installs in one second would share a ?v= and a browser would keep
	// the first one's images.
	if previous, err := readInstall(final); err == nil && previous.InstalledAt >= record.InstalledAt {
		record.InstalledAt = previous.InstalledAt + 1
	}

	staging, err := os.MkdirTemp(root, ".tmp-"+id+"-")
	if err != nil {
		return nil, fmt.Errorf("create staging folder: %w", err)
	}
	if err := writePack(staging, p, record); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	// A folder cannot be renamed over a full one, so the old pack steps aside
	// first. Between the two renames the pack is briefly missing, never mixed.
	var old string
	if _, err := os.Lstat(final); err == nil {
		old = filepath.Join(root, ".old-"+id+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("move old pack aside: %w", err)
		}
	}
	if err := os.Rename(staging, final); err != nil {
		if old != "" {
			_ = os.Rename(old, final)
		}
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("install pack: %w", err)
	}
	if old != "" {
		if err := os.RemoveAll(old); err != nil {
			slog.Warn("view pack: could not remove the replaced pack", "path", old, "error", err)
		}
	}
	pack := p.manifest.view(record)
	return &pack, nil
}

func writePack(dir string, p *parsed, record installRecord) error {
	if err := os.WriteFile(filepath.Join(dir, manifestName), p.raw, 0o644); err != nil {
		return fmt.Errorf("write pack.json: %w", err)
	}
	for path, content := range p.images {
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create folder for %s: %w", path, err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, installName), data, 0o644); err != nil {
		return fmt.Errorf("write install.json: %w", err)
	}
	return nil
}

// List returns every installed pack that still passes the rules, by id. A
// folder that does not is logged and left out rather than failing the list:
// one bad pack must not take everyone's view switch with it.
func (s *Store) List() ([]Pack, error) {
	root := s.dir()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []Pack{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read packs folder: %w", err)
	}
	packs := []Pack{}
	for _, entry := range entries {
		// Dot folders are installs and removals in progress.
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		m, record, err := load(root, entry.Name())
		if err != nil {
			slog.Warn("view pack skipped", "pack", entry.Name(), "error", err)
			continue
		}
		packs = append(packs, m.view(record))
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].ID < packs[j].ID })
	return packs, nil
}

// Remove deletes a pack.
func (s *Store) Remove(id string) error {
	if !idPattern.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.dir()
	final := filepath.Join(root, id)
	info, err := os.Lstat(final)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find pack: %w", err)
	}
	// Renamed first, so the pack disappears at once even if deleting its
	// files is slow or fails part way.
	gone := filepath.Join(root, ".old-"+id+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Rename(final, gone); err != nil {
		return fmt.Errorf("remove pack: %w", err)
	}
	if err := os.RemoveAll(gone); err != nil {
		slog.Warn("view pack: could not delete the removed pack's files", "path", gone, "error", err)
	}
	return nil
}

// Open returns one of a pack's sprite images and its kind. Anything that is
// not a file a sprite of that pack names, and still an image, is ErrNotFound.
func (s *Store) Open(id, path string) ([]byte, Kind, error) {
	if !idPattern.MatchString(id) || checkSpritePath(path) != nil {
		return nil, "", ErrNotFound
	}
	root := s.dir()
	m, _, err := load(root, id)
	if err != nil {
		return nil, "", ErrNotFound
	}
	named := false
	for _, sprite := range m.Sprites {
		if sprite == path {
			named = true
			break
		}
	}
	if !named {
		return nil, "", ErrNotFound
	}
	content, err := readRegular(filepath.Join(root, id, filepath.FromSlash(path)), MaxImage)
	if err != nil {
		return nil, "", ErrNotFound
	}
	kind := DetectKind(content)
	if kind == "" {
		return nil, "", ErrNotFound
	}
	return content, kind, nil
}

// load reads a pack folder and checks it again: pack.json passes every rule,
// names the folder's own id, and every sprite is a file of at most MaxImage.
// Whether each one is still an image is left to Open, which reads it anyway;
// reading every image on every list would cost far more than it finds.
func load(root, id string) (*Manifest, installRecord, error) {
	if !idPattern.MatchString(id) {
		return nil, installRecord{}, fmt.Errorf("folder name %q is not a pack id", id)
	}
	dir := filepath.Join(root, id)
	raw, err := readRegular(filepath.Join(dir, manifestName), maxManifest)
	if err != nil {
		return nil, installRecord{}, fmt.Errorf("read pack.json: %w", err)
	}
	m, err := parseManifest(raw)
	if err != nil {
		return nil, installRecord{}, err
	}
	if m.ID != id {
		return nil, installRecord{}, fmt.Errorf("pack.json says id %q but its folder is %q", m.ID, id)
	}
	for _, key := range sortedKeys(m.Sprites) {
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(m.Sprites[key])))
		if err != nil {
			return nil, installRecord{}, fmt.Errorf("sprite %q: %w", key, err)
		}
		if !info.Mode().IsRegular() || info.Size() > MaxImage {
			return nil, installRecord{}, fmt.Errorf("sprite %q: %s is not an image file of at most %d MB", key, m.Sprites[key], MaxImage>>20)
		}
	}
	record, err := readInstall(dir)
	if err != nil {
		// A pack copied in by hand has no install.json: when pack.json last
		// changed is the best "installed at" there is.
		record = installRecord{}
		if info, statErr := os.Stat(filepath.Join(dir, manifestName)); statErr == nil {
			record.InstalledAt = info.ModTime().Unix()
		}
	}
	return m, record, nil
}

func readInstall(dir string) (installRecord, error) {
	var record installRecord
	raw, err := readRegular(filepath.Join(dir, installName), 4<<10)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, fmt.Errorf("read install.json: %w", err)
	}
	return record, nil
}

// readRegular reads a file that must be a plain file, not a link, of at most
// limit bytes. A link in a hand-copied pack folder could point anywhere on
// the server.
func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a plain file", filepath.Base(path))
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), limit)
	}
	return content, nil
}

// view is the manifest as List and Install answer it, with each sprite
// resolved to its URL.
func (m *Manifest) view(record installRecord) Pack {
	pack := Pack{
		ID:          m.ID,
		Name:        m.Name,
		Layout:      m.Layout,
		Palette:     map[string]string{},
		Sprites:     map[string]string{},
		InstalledAt: record.InstalledAt,
		InstalledBy: record.InstalledBy,
	}
	for key, value := range m.Palette {
		pack.Palette[key] = value
	}
	for key, path := range m.Sprites {
		pack.Sprites[key] = FileURL(m.ID, path, record.InstalledAt)
	}
	return pack
}
