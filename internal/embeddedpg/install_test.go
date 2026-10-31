package embeddedpg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const testRoot = "postgresql-" + PinnedVersion + "-test"

type archiveEntry struct {
	name     string
	body     string
	linkname string // a symlink when set
	dir      bool
}

// tarGz builds an archive the way the upstream ones are laid out: one
// top-level directory holding bin/, lib/, share/, include/.
func tarGz(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		switch {
		case entry.linkname != "":
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, entry.linkname, 0
		case entry.dir:
			header.Typeflag, header.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fullArchive holds what is kept and what the allowlist must drop.
func fullArchive(t *testing.T) []byte {
	r := testRoot + "/"
	return tarGz(t,
		archiveEntry{name: r + "bin/postgres", body: "pg"},
		archiveEntry{name: r + "bin/postgres.exe", body: "pg"},
		archiveEntry{name: r + "bin/initdb", body: "initdb"},
		archiveEntry{name: r + "bin/pg_ctl", body: "pg_ctl"},
		archiveEntry{name: r + "bin/pg_dump", body: "pg_dump"},
		archiveEntry{name: r + "bin/pg_restore", body: "pg_restore"},
		archiveEntry{name: r + "bin/pg_isready", body: "pg_isready"},
		archiveEntry{name: r + "bin/libpq.dll", body: "dll"},
		archiveEntry{name: r + "bin/python313.dll", body: "python"},
		archiveEntry{name: r + "bin/psql", body: "psql"},
		archiveEntry{name: r + "lib/libpq.so.5", body: "lib"},
		archiveEntry{name: r + "lib/libicudata.so.78", body: "icu"},
		archiveEntry{name: r + "lib/libpython3.13.so", body: "python"},
		archiveEntry{name: r + "lib/plpython3.so", body: "python"},
		archiveEntry{name: r + "lib/python3.13/os.py", body: "python"},
		archiveEntry{name: r + "lib/libpgcommon.a", body: "static"},
		archiveEntry{name: r + "lib/postgres.lib", body: "static"},
		archiveEntry{name: r + "share/timezone/UTC", body: "tz"},
		archiveEntry{name: r + "share/tsearch_data/english.stop", body: "stop"},
		archiveEntry{name: r + "share/extension/plpgsql.control", body: "plpgsql"},
		archiveEntry{name: r + "share/extension/plpython3u.control", body: "python"},
		archiveEntry{name: r + "share/licenses/LICENSE", body: "license"},
		archiveEntry{name: r + "include/postgres.h", body: "header"},
		archiveEntry{name: r + "dependency-notices/icu.txt", body: "notice"},
	)
}

func targetFor(archive []byte, urls ...string) Target {
	sum := sha256.Sum256(archive)
	return Target{
		Name:    "test-target",
		Archive: testRoot + ".tar.gz",
		URLs:    urls,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(archive)),
		Root:    testRoot,
	}
}

// archiveServer serves body over TLS and counts the requests.
func archiveServer(t *testing.T, body []byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// tree lists every regular file and symlink under dir, slash-separated.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

func install(t *testing.T, home string, target Target, server *httptest.Server) (string, error) {
	t.Helper()
	return Install(context.Background(), InstallOptions{Home: home, Target: target, Client: server.Client()})
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	archive := fullArchive(t)
	server, _ := archiveServer(t, archive)
	target := targetFor(archive, server.URL+"/"+testRoot+".tar.gz")
	target.SHA256 = strings.Repeat("0", 64)
	home := t.TempDir()

	_, err := install(t, home, target, server)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Install = %v, want ErrChecksumMismatch", err)
	}
	if files := tree(t, filepath.Join(home, "dist")); len(files) != 0 {
		t.Fatalf("a rejected archive left files in dist: %v", files)
	}
	if files := tree(t, filepath.Join(home, "downloads")); len(files) != 0 {
		t.Fatalf("a rejected download was kept: %v", files)
	}
}

func TestInstallRejectsTraversalAndEscapingSymlinks(t *testing.T) {
	r := testRoot + "/"
	for name, archive := range map[string][]byte{
		"dotdot":         tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: r + "../../evil", body: "x"}),
		"absolute":       tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: "/tmp/evil", body: "x"}),
		"escapingLink":   tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: r + "lib/link", linkname: "../../../outside"}),
		"absoluteLink":   tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: r + "lib/abs", linkname: "/etc/passwd"}),
		"windowsDotdot":  tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: r + `lib\..\..\..\evil`, body: "x"}),
		"outsideTheRoot": tarGz(t, archiveEntry{name: r + "bin/postgres", body: "pg"}, archiveEntry{name: "other-root/bin/x", body: "x"}),
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := archiveServer(t, archive)
			home := t.TempDir()
			_, err := install(t, home, targetFor(archive, server.URL+"/a.tar.gz"), server)
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("Install = %v, want ErrUnsafeArchive", err)
			}
			if files := tree(t, filepath.Join(home, "dist")); len(files) != 0 {
				t.Fatalf("unsafe archive left %v", files)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(home), "evil")); err == nil {
				t.Fatal("an entry was written outside the install root")
			}
		})
	}
}

func TestInstallIsAtomicAndIdempotent(t *testing.T) {
	archive := fullArchive(t)
	server, hits := archiveServer(t, archive)
	target := targetFor(archive, server.URL+"/"+testRoot+".tar.gz")
	home := t.TempDir()

	dir, err := install(t, home, target, server)
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if dir != DistDir(home, target) {
		t.Fatalf("installed into %q, want %q", dir, DistDir(home, target))
	}
	again, err := install(t, home, target, server)
	if err != nil || again != dir {
		t.Fatalf("second Install = %q, %v", again, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("%d downloads, want 1: a second Install must find the first", hits.Load())
	}
	entries, _ := os.ReadDir(filepath.Join(home, "dist"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp") {
			t.Fatalf("temporary extraction %s left behind", entry.Name())
		}
	}
	var stamp struct {
		Version, Target, ArchiveSHA256 string
	}
	data, err := os.ReadFile(filepath.Join(dir, StampFile))
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := json.Unmarshal(data, &stamp); err != nil || stamp.Version != PinnedVersion ||
		stamp.Target != target.Name || stamp.ArchiveSHA256 != target.SHA256 {
		t.Fatalf("stamp = %s (%v)", data, err)
	}
	if err := VerifyBinaries(dir, target); err != nil {
		t.Fatalf("VerifyBinaries on a fresh install: %v", err)
	}
}

func TestInstallExtractsOnlyAllowlist(t *testing.T) {
	archive := fullArchive(t)
	server, _ := archiveServer(t, archive)
	target := targetFor(archive, server.URL+"/"+testRoot+".tar.gz")
	dir, err := install(t, t.TempDir(), target, server)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	got := tree(t, dir)
	want := []string{
		"bin/initdb", "bin/libpq.dll", "bin/pg_ctl", "bin/pg_dump", "bin/pg_isready", "bin/pg_restore",
		"bin/postgres", "bin/postgres.exe",
		"lib/libicudata.so.78", "lib/libpq.so.5",
		StampFile,
		"share/extension/plpgsql.control", "share/licenses/LICENSE",
		"share/timezone/UTC", "share/tsearch_data/english.stop",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("installed files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOfflineErrorExplainsRemedies(t *testing.T) {
	target := Target{
		Name: "linux-amd64", Archive: "postgresql-17.11.1-x86_64-unknown-linux-gnu.tar.gz",
		URLs:   []string{"https://example.invalid/postgresql-17.11.1-x86_64-unknown-linux-gnu.tar.gz"},
		SHA256: pinnedSHA256["linux-amd64"], Root: "postgresql-17.11.1-x86_64-unknown-linux-gnu",
	}
	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("no error")
		}
		message := err.Error()
		for _, want := range []string{
			"PostgreSQL " + PinnedVersion, target.URLs[0], target.SHA256,
			"nodevas db install-postgres --archive", "NODEVAS_DATABASE_URL",
		} {
			if !strings.Contains(message, want) {
				t.Errorf("message lacks %q:\n%s", want, message)
			}
		}
	}
	t.Run("OfflineError", func(t *testing.T) {
		err := OfflineError(target, errors.New("dial tcp: no route to host"))
		check(t, err)
		if !errors.Is(err, ErrOffline) {
			t.Fatalf("OfflineError does not wrap ErrOffline: %v", err)
		}
	})
	t.Run("OfflineInstall", func(t *testing.T) {
		_, err := Install(context.Background(), InstallOptions{Home: t.TempDir(), Target: target, Offline: true})
		check(t, err)
	})
	t.Run("UnreachableMirror", func(t *testing.T) {
		server := httptest.NewTLSServer(http.NotFoundHandler())
		server.Close()
		unreachable := target
		unreachable.URLs = []string{server.URL + "/" + target.Archive}
		_, err := Install(context.Background(), InstallOptions{Home: t.TempDir(), Target: unreachable, Client: server.Client()})
		if !errors.Is(err, ErrOffline) {
			t.Fatalf("Install against a closed server = %v, want ErrOffline", err)
		}
	})
}

func TestDownloadReportsProgress(t *testing.T) {
	archive := fullArchive(t)
	server, _ := archiveServer(t, archive)
	target := targetFor(archive, server.URL+"/"+testRoot+".tar.gz")
	var progress bytes.Buffer
	_, err := Install(context.Background(), InstallOptions{
		Home: t.TempDir(), Target: target, Client: server.Client(), Progress: &progress,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	out := progress.String()
	if !strings.Contains(out, "Downloading PostgreSQL "+PinnedVersion+" for first-time setup") || !strings.Contains(out, "100%") {
		t.Fatalf("progress = %q", out)
	}
}

func TestLocalBinariesStampMustMatchManifest(t *testing.T) {
	target := Target{Name: "linux-amd64", SHA256: pinnedSHA256["linux-amd64"]}
	write := func(t *testing.T, stamp any) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"postgres", "postgres.exe"} {
			if err := os.WriteFile(filepath.Join(dir, "bin", name), []byte("pg"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if stamp != nil {
			data, _ := json.Marshal(stamp)
			if err := os.WriteFile(filepath.Join(dir, StampFile), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	good := map[string]string{"version": PinnedVersion, "target": target.Name, "archiveSha256": target.SHA256}
	if err := VerifyBinaries(write(t, good), target); err != nil {
		t.Fatalf("matching stamp refused: %v", err)
	}
	for name, stamp := range map[string]any{
		"missing":      nil,
		"otherSHA":     map[string]string{"version": PinnedVersion, "target": target.Name, "archiveSha256": strings.Repeat("a", 64)},
		"otherVersion": map[string]string{"version": "17.11.0", "target": target.Name, "archiveSha256": target.SHA256},
		"otherTarget":  map[string]string{"version": PinnedVersion, "target": "darwin-arm64", "archiveSha256": target.SHA256},
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyBinaries(write(t, stamp), target); !errors.Is(err, ErrStampMismatch) {
				t.Fatalf("VerifyBinaries = %v, want ErrStampMismatch", err)
			}
		})
	}
	noBinary := t.TempDir()
	data, _ := json.Marshal(good)
	_ = os.WriteFile(filepath.Join(noBinary, StampFile), data, 0o644)
	if err := VerifyBinaries(noBinary, target); err == nil {
		t.Fatal("a stamp without bin/postgres was accepted")
	}
}
