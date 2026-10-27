package embeddedpg

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nodevas/internal/lockfile"
)

// StampFile is written into an installed binaries directory: the version,
// target and archive hash it was extracted from.
const StampFile = "nodevas-pg.json"

// Environment knobs.
const (
	HomeEnv     = "NODEVAS_PG_HOME"
	BinariesEnv = "NODEVAS_PG_BINARIES"
	OfflineEnv  = "NODEVAS_PG_OFFLINE"
	MirrorEnv   = "NODEVAS_PG_MIRROR"
)

// maxExtractedBytes bounds what one archive may unpack to: the real ones are
// about 125 MB, so anything near this is a decompression bomb.
const maxExtractedBytes = 1 << 30

// InstallOptions says where and how to install a target's binaries.
type InstallOptions struct {
	// Home is the embedded root (NODEVAS_PG_HOME).
	Home string
	// Target is the archive to install.
	Target Target
	// Mirror replaces the URLs' base; the hash is still enforced.
	Mirror string
	// Offline forbids downloading.
	Offline bool
	// Archive installs from a local file instead of downloading.
	Archive string
	// Client fetches the archive; nil is http.DefaultClient.
	Client *http.Client
	// Progress receives the human progress line; nil is silent.
	Progress io.Writer
	// Dest installs into this directory instead of DistDir(Home, Target):
	// the release workflows' desktop bundle.
	Dest string
}

// Stamp is StampFile's content.
type Stamp struct {
	Version       string `json:"version"`
	Target        string `json:"target"`
	ArchiveSHA256 string `json:"archiveSha256"`
}

// DefaultHome is NODEVAS_PG_HOME or the per-OS default: %LOCALAPPDATA%\nodevas\pg
// on Windows, ~/Library/Application Support/nodevas/pg on macOS,
// $XDG_CONFIG_HOME/nodevas/pg elsewhere.
//
// Windows deliberately uses the local, not the roaming, application data
// folder: a roaming profile or folder redirection would copy a live database
// directory to a server share.
func DefaultHome() (string, error) {
	if home := strings.TrimSpace(os.Getenv(HomeEnv)); home != "" {
		return filepath.Abs(home)
	}
	var base string
	var err error
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
		if base == "" {
			base, err = os.UserCacheDir()
		}
	} else {
		base, err = os.UserConfigDir()
	}
	if err != nil {
		return "", fmt.Errorf("embeddedpg: no user directory for the database (set %s): %w", HomeEnv, err)
	}
	if base == "" {
		return "", fmt.Errorf("embeddedpg: no user directory for the database (set %s)", HomeEnv)
	}
	return filepath.Join(base, "nodevas", "pg"), nil
}

// DistDir is where a target's binaries are installed under home.
func DistDir(home string, target Target) string {
	return filepath.Join(home, "dist", PinnedVersion+"-"+target.Name)
}

// Install makes sure the target's verified binaries are under opts.Home and
// returns their directory. A second call finds them and downloads nothing.
func Install(ctx context.Context, opts InstallOptions) (string, error) {
	target := opts.Target
	dir := opts.Dest
	if dir == "" {
		if opts.Home == "" {
			return "", errors.New("embeddedpg: Install needs a home directory")
		}
		dir = DistDir(opts.Home, target)
	}
	if VerifyBinaries(dir, target) == nil {
		return dir, nil
	}
	if opts.Offline && opts.Archive == "" {
		return "", OfflineError(target, fmt.Errorf("%s is set", OfflineEnv))
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	// Two processes installing at once (serve and a CLI command, two test
	// binaries) take turns; the second finds the first's result.
	lockPath := filepath.Join(parent, ".install.lock")
	if opts.Home != "" && opts.Dest == "" {
		lockPath = filepath.Join(opts.Home, "install.lock")
	}
	lock, err := lockfile.AcquireWait(lockPath, 15*time.Minute)
	if err != nil {
		return "", fmt.Errorf("embeddedpg: install lock: %w", err)
	}
	defer lock.Release()
	if VerifyBinaries(dir, target) == nil {
		return dir, nil
	}

	archive := opts.Archive
	if archive == "" {
		downloads := filepath.Join(parent, "..", "downloads")
		if opts.Dest != "" {
			downloads = filepath.Join(parent, ".downloads")
		}
		archive, err = download(ctx, opts, downloads)
		if err != nil {
			return "", err
		}
		defer func() {
			os.Remove(archive)
			os.Remove(filepath.Dir(archive)) // only when empty
		}()
	} else if err := checkFileHash(archive, target.SHA256); err != nil {
		return "", err
	}

	tmp := filepath.Join(parent, ".tmp-"+randomHex(6))
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if strings.HasSuffix(strings.ToLower(target.Archive), ".zip") {
		err = extractZip(archive, target.Root, tmp)
	} else {
		err = extractTarGz(archive, target.Root, tmp)
	}
	if err != nil {
		return "", err
	}
	stamp, _ := json.MarshalIndent(Stamp{Version: PinnedVersion, Target: target.Name, ArchiveSHA256: target.SHA256}, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, StampFile), append(stamp, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("embeddedpg: replace %s: %w", dir, err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", fmt.Errorf("embeddedpg: install %s: %w", dir, err)
	}
	return dir, nil
}

// VerifyBinaries checks a preinstalled binaries directory (NODEVAS_PG_BINARIES,
// a desktop bundle) against the manifest: its stamp must name PinnedVersion,
// the target and the target's SHA-256, and bin/postgres must exist.
func VerifyBinaries(dir string, target Target) error {
	data, err := os.ReadFile(filepath.Join(dir, StampFile))
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrStampMismatch, dir, err)
	}
	var stamp Stamp
	if err := json.Unmarshal(data, &stamp); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrStampMismatch, dir, err)
	}
	if stamp.Version != PinnedVersion || stamp.Target != target.Name || !strings.EqualFold(stamp.ArchiveSHA256, target.SHA256) {
		return fmt.Errorf("%w: %s holds %s %s (sha256 %s), want %s %s (sha256 %s)", ErrStampMismatch, dir,
			stamp.Version, stamp.Target, stamp.ArchiveSHA256, PinnedVersion, target.Name, target.SHA256)
	}
	binary := filepath.Join(dir, "bin", executable(target, "postgres"))
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("embeddedpg: %s is missing from %s", filepath.Base(binary), dir)
	}
	return nil
}

// offlineError is OfflineError's type: it is ErrOffline and its cause.
type offlineError struct {
	message string
	cause   error
}

func (e *offlineError) Error() string   { return e.message }
func (e *offlineError) Unwrap() []error { return []error{ErrOffline, e.cause} }

// OfflineError explains a download that could not happen and what to do
// instead: retry online, fetch the URL elsewhere and install it with
// `nodevas db install-postgres --archive`, or set NODEVAS_DATABASE_URL.
func OfflineError(target Target, cause error) error {
	url := "(no download URL for this platform)"
	if len(target.URLs) > 0 {
		url = target.URLs[0]
	}
	message := fmt.Sprintf("PostgreSQL %s is needed and could not be downloaded (%v).\n"+
		"Connect and retry, or download %s elsewhere, check sha256 %s, and run\n"+
		"`nodevas db install-postgres --archive <file>`, or set NODEVAS_DATABASE_URL to an existing server.",
		PinnedVersion, cause, url, target.SHA256)
	return &offlineError{message: message, cause: cause}
}

// urls is the target's download list, rebased on a mirror when one is set.
func urls(opts InstallOptions) []string {
	if opts.Mirror == "" {
		return opts.Target.URLs
	}
	return []string{strings.TrimRight(opts.Mirror, "/") + "/" + opts.Target.Archive}
}

// download fetches the archive into dir, hashing it as it streams. A
// mismatching archive is deleted and fails the install outright: another URL
// is not tried, because a wrong file is not an outage.
func download(ctx context.Context, opts InstallOptions, dir string) (string, error) {
	target := opts.Target
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	var lastErr error
	for _, url := range urls(opts) {
		if !strings.HasPrefix(url, "https://") {
			lastErr = fmt.Errorf("refusing non-https URL %s", url)
			continue
		}
		path, err := fetch(ctx, client, url, opts, filepath.Join(dir, target.Archive+".part"))
		if err == nil {
			return path, nil
		}
		if errors.Is(err, ErrChecksumMismatch) || errors.Is(err, context.Canceled) {
			return "", err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no download URL")
	}
	return "", OfflineError(Target{URLs: urls(opts), SHA256: target.SHA256}, lastErr)
}

func fetch(ctx context.Context, client *http.Client, url string, opts InstallOptions, partial string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, response.Status)
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	total := response.ContentLength
	if total <= 0 {
		total = opts.Target.Size
	}
	hash := sha256.New()
	progress := &progressWriter{out: opts.Progress, total: total, last: -1}
	progress.report(0)
	_, copyErr := io.Copy(io.MultiWriter(file, hash, progress), response.Body)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		os.Remove(partial)
		return "", err
	}
	progress.finish()
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, opts.Target.SHA256) {
		os.Remove(partial)
		return "", fmt.Errorf("%w: %s has sha256 %s, want %s", ErrChecksumMismatch, url, got, opts.Target.SHA256)
	}
	final := strings.TrimSuffix(partial, ".part")
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return "", err
	}
	return final, nil
}

func checkFileHash(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: %s has sha256 %s, want %s", ErrChecksumMismatch, path, got, want)
	}
	return nil
}

// progressWriter prints "Downloading PostgreSQL … 42%" as whole percents.
type progressWriter struct {
	out     io.Writer
	total   int64
	written int64
	last    int
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.written += int64(len(b))
	if p.total > 0 {
		p.report(int(p.written * 100 / p.total))
	}
	return len(b), nil
}

func (p *progressWriter) report(percent int) {
	if p.out == nil || percent == p.last || percent > 100 {
		return
	}
	p.last = percent
	fmt.Fprintf(p.out, "\rDownloading PostgreSQL %s for first-time setup (%d MB)… %d%%",
		PinnedVersion, (p.total+(1<<20)-1)>>20, percent)
}

func (p *progressWriter) finish() {
	if p.out == nil {
		return
	}
	p.report(100)
	fmt.Fprintln(p.out)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// entryPath validates an archive entry's name and returns it relative to the
// archive's root ("" for the root itself). Absolute names, "..", backslash
// tricks and anything outside root are ErrUnsafeArchive.
func entryPath(name, root string) (string, error) {
	clean := strings.ReplaceAll(name, "\\", "/")
	if clean == "" || strings.HasPrefix(clean, "/") || filepath.VolumeName(clean) != "" || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
		}
	}
	clean = path.Clean(clean)
	if clean == root {
		return "", nil
	}
	if !strings.HasPrefix(clean, root+"/") {
		return "", fmt.Errorf("%w: %q is outside %s/", ErrUnsafeArchive, name, root)
	}
	return strings.TrimPrefix(clean, root+"/"), nil
}

// linkTarget validates a symlink at rel pointing at link: it must stay
// inside the root and be relative.
func linkTarget(rel, link string) (string, error) {
	link = strings.ReplaceAll(link, "\\", "/")
	if link == "" || strings.HasPrefix(link, "/") || filepath.VolumeName(link) != "" || strings.Contains(link, ":") {
		return "", fmt.Errorf("%w: link %s -> %q", ErrUnsafeArchive, rel, link)
	}
	resolved := path.Clean(path.Join(path.Dir(rel), link))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || resolved == "." {
		return "", fmt.Errorf("%w: link %s -> %q leaves the archive", ErrUnsafeArchive, rel, link)
	}
	return resolved, nil
}

var keptBinaries = map[string]bool{
	"postgres": true, "initdb": true, "pg_ctl": true, "pg_dump": true, "pg_restore": true, "pg_isready": true,
}

// keep is the allowlist: the server, the tools Nodevas runs, the libraries
// and data they load, and the license texts. Python (PL/Python and the
// interpreter bundled for it), headers, static and import libraries, the
// client tools and the build-only files are left out.
func keep(rel string) bool {
	parts := strings.Split(rel, "/")
	base := strings.ToLower(parts[len(parts)-1])
	switch parts[0] {
	case "bin":
		if len(parts) != 2 {
			return false
		}
		if keptBinaries[strings.TrimSuffix(base, ".exe")] {
			return true
		}
		return strings.HasSuffix(base, ".dll") && !strings.HasPrefix(base, "python")
	case "lib":
		for _, part := range parts[1:] {
			lower := strings.ToLower(part)
			if strings.HasPrefix(lower, "python") || strings.HasPrefix(lower, "libpython") || strings.HasPrefix(lower, "plpython") {
				return false
			}
		}
		if len(parts) > 1 && (parts[1] == "pgxs" || parts[1] == "pkgconfig") {
			return false
		}
		return !strings.HasSuffix(base, ".a") && !strings.HasSuffix(base, ".lib") && !strings.HasSuffix(base, ".h")
	case "share":
		if len(parts) > 1 && parts[1] == "extension" && strings.Contains(base, "plpython") {
			return false
		}
		if len(parts) > 1 && parts[1] == "doc" {
			return false
		}
		return true
	}
	return false
}

// extractor writes allowlisted entries below dir and refuses unsafe ones.
type extractor struct {
	dir     string
	written int64
	links   map[string]string // rel -> resolved rel target
}

func (x *extractor) file(rel string, mode os.FileMode, body io.Reader) error {
	dest := filepath.Join(x.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 || strings.HasPrefix(rel, "bin/") {
		perm = 0o755
	}
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(file, io.LimitReader(body, maxExtractedBytes-x.written+1))
	x.written += n
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return err
	}
	if x.written > maxExtractedBytes {
		return fmt.Errorf("%w: archive expands past %d bytes", ErrUnsafeArchive, maxExtractedBytes)
	}
	return nil
}

// finishLinks makes the symlinks once their targets exist; where symlinks
// cannot be made (Windows without the privilege) the target is copied.
func (x *extractor) finishLinks() error {
	for rel, resolved := range x.links {
		dest := filepath.Join(x.dir, filepath.FromSlash(rel))
		source := filepath.Join(x.dir, filepath.FromSlash(resolved))
		if _, err := os.Stat(source); err != nil {
			continue // a link to something the allowlist dropped
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		relLink, err := filepath.Rel(filepath.Dir(dest), source)
		if err == nil && os.Symlink(relLink, dest) == nil {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archive, root, dir string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("embeddedpg: %s: %w", archive, err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	x := &extractor{dir: dir, links: map[string]string{}}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("embeddedpg: %s: %w", archive, err)
		}
		rel, err := entryPath(header.Name, root)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeSymlink:
			resolved, err := linkTarget(rel, header.Linkname)
			if err != nil {
				return err
			}
			if rel != "" && keep(rel) {
				x.links[rel] = resolved
			}
		case tar.TypeReg:
			if rel == "" || !keep(rel) {
				continue
			}
			if err := x.file(rel, os.FileMode(header.Mode), reader); err != nil {
				return err
			}
		case tar.TypeLink:
			return fmt.Errorf("%w: hard link %q", ErrUnsafeArchive, header.Name)
		default:
			// Devices, fifos and the like have no place in a binaries archive.
			return fmt.Errorf("%w: %q has type %c", ErrUnsafeArchive, header.Name, header.Typeflag)
		}
	}
	return x.finishLinks()
}

func extractZip(archive, root, dir string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("embeddedpg: %s: %w", archive, err)
	}
	defer reader.Close()
	x := &extractor{dir: dir, links: map[string]string{}}
	for _, entry := range reader.File {
		rel, err := entryPath(entry.Name, root)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		if mode.IsDir() || rel == "" {
			continue
		}
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeArchive, entry.Name)
		}
		if !keep(rel) {
			continue
		}
		body, err := entry.Open()
		if err != nil {
			return err
		}
		err = x.file(rel, mode, body)
		body.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
