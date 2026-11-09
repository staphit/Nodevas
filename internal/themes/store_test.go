package themes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/identity"
)

var (
	alice = identity.Actor{ID: "alice", Name: "alice", Role: identity.RoleMember}
	bob   = identity.Actor{ID: "bob", Name: "bob", Role: identity.RoleMember}
	root  = identity.Actor{ID: "root", Name: "root", Role: identity.RoleAdmin}
)

func TestImportStoresThePack(t *testing.T) {
	s, database := newTestStore(t)
	pack := mustImport(t, s, "alice", "magic")
	if pack.ID != "magic" || pack.OwnerID != "alice" || pack.Version != "1.0.0" || pack.Author != "someone" ||
		pack.Name.En != "magic" || pack.Name.ZhTW != "魔法世界" {
		t.Fatalf("pack = %+v", pack)
	}
	if pack.CreatedAt == "" || pack.UpdatedAt == "" {
		t.Fatalf("no timestamps: %+v", pack)
	}
	var onDisk int64
	err := filepath.Walk(filepath.Join(s.dir, "magic"), func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			onDisk += info.Size()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if pack.SizeBytes != onDisk {
		t.Fatalf("sizeBytes = %d, %d on disk", pack.SizeBytes, onDisk)
	}
	if got := string(readPackFile(t, s, "magic", "rooms/lounge.webp")); got != string(webpImage) {
		t.Fatalf("room image = %q", got)
	}
	expectNoTrace(t, s, database, "magic")
}

// What is stored is what was validated: a key spelled in another case or
// sent twice does not reach the browser in a form the server did not check.
func TestTheStoredManifestIsTheValidatedOne(t *testing.T) {
	s, _ := newTestStore(t)
	doc := manifestDoc()
	doc["SCHEMA"] = doc["schema"]
	delete(doc, "schema")
	delete(doc["characters"].(map[string]any)["claude"].(map[string]any), "match")
	if err := importErr(t, s, doc, goodFiles()); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(readPackFile(t, s, "magic", ManifestFile), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["schema"] != float64(1) || stored["SCHEMA"] != nil {
		t.Fatalf("stored = %v", stored)
	}
	match := stored["characters"].(map[string]any)["claude"].(map[string]any)["match"].(map[string]any)
	if agents, ok := match["agents"].([]any); !ok || len(agents) != 0 {
		t.Fatalf("match = %v", match)
	}
}

func TestListIsSortedAndNamesTheAccount(t *testing.T) {
	s, database := newTestStore(t)
	if _, err := database.Exec(`INSERT INTO accounts (id, name, role, created_at) VALUES ('acc-1', 'Alice Chen', 'member', ?)`, db.Now()); err != nil {
		t.Fatal(err)
	}
	mustImport(t, s, "acc-1", "zebra")
	mustImport(t, s, "local", "apple")
	packs, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 || packs[0].ID != "apple" || packs[1].ID != "zebra" {
		t.Fatalf("packs = %+v", packs)
	}
	if packs[1].Owner != "Alice Chen" || packs[1].OwnerID != "acc-1" {
		t.Fatalf("an account's pack = %+v", packs[1])
	}
	if packs[0].Owner != "local" {
		t.Fatalf("no account's pack = %+v", packs[0])
	}
}

func TestSomeoneElsesIDIsAConflict(t *testing.T) {
	s, database := newTestStore(t)
	mustImport(t, s, "alice", "magic")
	doc := manifestDoc()
	doc["version"] = "2.0.0"
	_, err := doImport(context.Background(), s, "bob", packZip(t, doc, goodFiles()))
	expectRefused(t, err, http.StatusConflict)
	// An administrator owns nothing by being one.
	_, err = doImport(context.Background(), s, "root", packZip(t, doc, goodFiles()))
	expectRefused(t, err, http.StatusConflict)
	pack, err := s.Get(context.Background(), "magic")
	if err != nil || pack.Version != "1.0.0" || pack.OwnerID != "alice" {
		t.Fatalf("pack = %+v, %v", pack, err)
	}
	expectNoTrace(t, s, database, "magic")
}

func TestReplacingYourOwnPack(t *testing.T) {
	s, database := newTestStore(t)
	first := mustImport(t, s, "alice", "magic")
	doc := manifestDoc()
	doc["version"] = "2.0.0"
	doc["rooms"] = map[string]any{"lounge": "rooms/new.png"}
	second, err := doImport(context.Background(), s, "alice", packZip(t, doc, map[string][]byte{
		"rooms/new.png":           pngImage,
		"figures/claude/read.png": pngImage,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != "2.0.0" || second.CreatedAt != first.CreatedAt {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	if _, _, err := s.Open(context.Background(), "magic", "rooms/lounge.webp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old image: %v", err)
	}
	readPackFile(t, s, "magic", "rooms/new.png")
	expectNoTrace(t, s, database, "magic")
}

// expectOldPackWorks checks the original pack from mustImport is intact.
func expectOldPackWorks(t *testing.T, s *Store, database *db.DB) {
	t.Helper()
	pack, err := s.Get(context.Background(), "magic")
	if err != nil || pack.Version != "1.0.0" {
		t.Fatalf("pack = %+v, %v", pack, err)
	}
	if got := string(readPackFile(t, s, "magic", "rooms/lounge.webp")); got != string(webpImage) {
		t.Fatalf("room image = %q", got)
	}
	readPackFile(t, s, "magic", ManifestFile)
	expectNoTrace(t, s, database, "magic")
}

// replacement is a good second version of mustImport's pack.
func replacement(t *testing.T) []byte {
	t.Helper()
	doc := manifestDoc()
	doc["version"] = "2.0.0"
	return packZip(t, doc, goodFiles())
}

func TestAFailedReplacementLeavesTheOldPackWorking(t *testing.T) {
	ctx := context.Background()
	t.Run("a hostile zip", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		doc, files := manifestDoc(), goodFiles()
		files["figures/claude/read.png"] = []byte("<svg/>")
		_, err := doImport(ctx, s, "alice", packZip(t, doc, files))
		expectRefused(t, err, http.StatusBadRequest)
		expectOldPackWorks(t, s, database)
	})
	t.Run("the old pack cannot be moved", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		s.rename = func(*os.Root, string, string) error { return errors.New("sharing violation") }
		_, err := doImport(ctx, s, "alice", replacement(t))
		expectRefused(t, err, http.StatusConflict)
		s.rename = renameInRoot
		expectOldPackWorks(t, s, database)
	})
	t.Run("the new pack cannot be moved in", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		s.rename = func(root *os.Root, from, to string) error {
			if to == "magic" && isTemp(from) {
				return errors.New("no space")
			}
			return renameInRoot(root, from, to)
		}
		_, err := doImport(ctx, s, "alice", replacement(t))
		expectRefused(t, err, http.StatusConflict)
		expectOldPackWorks(t, s, database)
	})
	t.Run("the row cannot be written", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		failCommits(t, database, "INSERT OR UPDATE")
		if _, err := doImport(ctx, s, "alice", replacement(t)); err == nil {
			t.Fatal("accepted")
		}
		expectOldPackWorks(t, s, database)
	})
	t.Run("the client leaves while it extracts", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		gone, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := doImport(gone, s, "alice", replacement(t)); err == nil {
			t.Fatal("accepted")
		}
		expectOldPackWorks(t, s, database)
	})
}

func TestANewPackWhoseRowCannotBeWrittenLeavesNothing(t *testing.T) {
	s, database := newTestStore(t)
	failCommits(t, database, "INSERT OR UPDATE")
	doc := manifestDoc()
	if _, err := doImport(context.Background(), s, "alice", packZip(t, doc, goodFiles())); err == nil {
		t.Fatal("accepted")
	}
	expectNoTrace(t, s, database)
}

// failCommits makes every commit of the named change to theme_packs fail, as
// a database going away at that moment does.
func failCommits(t *testing.T, database *db.DB, events string) {
	t.Helper()
	statements := []string{
		`CREATE FUNCTION theme_packs_refuse() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'refused by the test'; END; $$ LANGUAGE plpgsql`,
		`CREATE CONSTRAINT TRIGGER theme_packs_refuse AFTER ` + events + ` ON theme_packs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION theme_packs_refuse()`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func allowCommits(t *testing.T, database *db.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TRIGGER theme_packs_refuse ON theme_packs`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP FUNCTION theme_packs_refuse()`); err != nil {
		t.Fatal(err)
	}
}

func TestQuotas(t *testing.T) {
	ctx := context.Background()
	t.Run("packs per person", func(t *testing.T) {
		s, _ := newTestStore(t)
		for _, id := range []string{"a1", "a2", "a3", "a4", "a5"} {
			mustImport(t, s, "alice", id)
		}
		doc := manifestDoc()
		doc["name"] = map[string]any{"en": "a6"}
		_, err := doImport(ctx, s, "alice", packZip(t, doc, goodFiles()))
		expectRefused(t, err, http.StatusForbidden)
		if _, err := os.Stat(filepath.Join(s.dir, "a6")); !os.IsNotExist(err) {
			t.Fatalf("the sixth pack is on disk: %v", err)
		}
		// Replacing is not a sixth, and somebody else has their own five.
		doc["name"] = map[string]any{"en": "a5"}
		if _, err := doImport(ctx, s, "alice", packZip(t, doc, goodFiles())); err != nil {
			t.Fatalf("replacing at the quota: %v", err)
		}
		mustImport(t, s, "bob", "b1")
	})
	t.Run("bytes on the server", func(t *testing.T) {
		s, database := newTestStore(t)
		first := mustImport(t, s, "alice", "magic")
		limits := DefaultLimits()
		limits.MaxServerBytes = first.SizeBytes*2 - 1
		s.SetLimits(limits)
		doc := manifestDoc()
		// As long a name as the first's, so the two packs weigh the same.
		doc["name"] = map[string]any{"en": "magix", "zh-TW": "魔法世界"}
		_, err := doImport(ctx, s, "bob", packZip(t, doc, goodFiles()))
		expectRefused(t, err, http.StatusForbidden)
		expectNoTrace(t, s, database, "magic")

		// A replacement counts its new size in place of the old one's.
		if _, err := doImport(ctx, s, "alice", packZip(t, manifestDoc(), goodFiles())); err != nil {
			t.Fatalf("replacing near the quota: %v", err)
		}
		limits.MaxServerBytes = first.SizeBytes
		s.SetLimits(limits)
		grown := manifestDoc()
		grown["author"] = "somebody a little longer"
		_, err = doImport(ctx, s, "alice", packZip(t, grown, goodFiles()))
		expectRefused(t, err, http.StatusForbidden)
		expectNoTrace(t, s, database, "magic")
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s, database := newTestStore(t)
	mustImport(t, s, "alice", "magic")
	expectRefused(t, s.Delete(ctx, bob, "magic"), http.StatusForbidden)
	expectNoTrace(t, s, database, "magic")
	if err := s.Delete(ctx, alice, "magic"); err != nil {
		t.Fatal(err)
	}
	expectNoTrace(t, s, database)
	if err := s.Delete(ctx, alice, "magic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("again: %v", err)
	}
	if err := s.Delete(ctx, alice, "../outside"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a bad id: %v", err)
	}

	mustImport(t, s, "alice", "magic")
	if err := s.Delete(ctx, root, "magic"); err != nil {
		t.Fatalf("an administrator: %v", err)
	}
	expectNoTrace(t, s, database)
}

func TestDeletingTheDefaultClearsIt(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	mustImport(t, s, "alice", "magic")
	mustImport(t, s, "alice", "other")
	if err := s.SetDefault(ctx, "magic"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, alice, "other"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Default(ctx); got != "magic" {
		t.Fatalf("default after deleting another = %q", got)
	}
	if err := s.Delete(ctx, alice, "magic"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Default(ctx); got != "" || err != nil {
		t.Fatalf("default = %q, %v", got, err)
	}
}

func TestSetDefault(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	if got, err := s.Default(ctx); got != "" || err != nil {
		t.Fatalf("unset = %q, %v", got, err)
	}
	if err := s.SetDefault(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	mustImport(t, s, "alice", "magic")
	if err := s.SetDefault(ctx, "magic"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Default(ctx); got != "magic" {
		t.Fatalf("default = %q", got)
	}
	if err := s.SetDefault(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Default(ctx); got != "" {
		t.Fatalf("cleared = %q", got)
	}
}

// Files that will not go are said to, the row stays, and asking again works.
func TestDeleteNeverLeavesARowWithoutADirectoryQuietly(t *testing.T) {
	ctx := context.Background()
	t.Run("the directory cannot be moved", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		if err := s.SetDefault(ctx, "magic"); err != nil {
			t.Fatal(err)
		}
		s.rename = func(*os.Root, string, string) error { return errors.New("sharing violation") }
		expectRefused(t, s.Delete(ctx, alice, "magic"), http.StatusConflict)
		s.rename = renameInRoot
		expectOldPackWorks(t, s, database)
		if got, _ := s.Default(ctx); got != "magic" {
			t.Fatalf("default = %q", got)
		}
		if err := s.Delete(ctx, alice, "magic"); err != nil {
			t.Fatal(err)
		}
		expectNoTrace(t, s, database)
	})
	t.Run("the files cannot be removed", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		s.removeAll = func(*os.Root, string) error { return errors.New("permission denied") }
		expectRefused(t, s.Delete(ctx, alice, "magic"), http.StatusConflict)
		s.removeAll = removeAllInRoot
		expectOldPackWorks(t, s, database)
		if err := s.Delete(ctx, alice, "magic"); err != nil {
			t.Fatal(err)
		}
		expectNoTrace(t, s, database)
	})
	t.Run("the row cannot be removed", func(t *testing.T) {
		s, database := newTestStore(t)
		mustImport(t, s, "alice", "magic")
		failCommits(t, database, "DELETE")
		expectRefused(t, s.Delete(ctx, alice, "magic"), http.StatusConflict)
		allowCommits(t, database)
		// The files went first, so the row is what asking again finishes.
		if err := s.Delete(ctx, alice, "magic"); err != nil {
			t.Fatal(err)
		}
		expectNoTrace(t, s, database)
	})
}

func TestRecover(t *testing.T) {
	s, database := newTestStore(t)
	mustImport(t, s, "alice", "magic")
	mustImport(t, s, "alice", "other")
	// A crash with "gone" set aside and nothing in its place, one after
	// "other" was replaced, and one during an extraction.
	if err := os.Rename(filepath.Join(s.dir, "magic"), filepath.Join(s.dir, ".old.gone.0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	for _, leftover := range []string{".tmp.0123456789abcdef", ".old.other.0123456789abcdef"} {
		if err := os.MkdirAll(filepath.Join(s.dir, leftover, "rooms"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if got, want := dirNames(t, s.dir), []string{"gone", "other"}; !equalStrings(got, want) {
		t.Fatalf("themes directory = %v, want %v", got, want)
	}

	empty := NewStore(database, filepath.Join(t.TempDir(), "nothing-here"))
	if err := empty.Recover(); err != nil {
		t.Fatalf("no directory yet: %v", err)
	}
}

func TestOpenStaysInsideThePack(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	mustImport(t, s, "alice", "magic")
	mustImport(t, s, "alice", "other")
	secret := filepath.Join(s.dir, "secret.png")
	if err := os.WriteFile(secret, pngImage, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	for _, rel := range []string{
		"../secret.png", "../other/rooms/lounge.webp", "rooms/../../secret.png",
		"/etc/passwd.png", "C:/secret.png", "rooms\\lounge.webp", "..\\secret.png",
		"", ".", "rooms", "missing.png", "rooms/missing.webp",
	} {
		if file, _, err := s.Open(ctx, "magic", rel); !errors.Is(err, ErrNotFound) {
			if file != nil {
				file.Close()
			}
			t.Errorf("%q: %v", rel, err)
		}
	}
	for _, id := range []string{"..", "../magic", "MAGIC", ".old", "nothing"} {
		if file, _, err := s.Open(ctx, id, ManifestFile); !errors.Is(err, ErrNotFound) {
			if file != nil {
				file.Close()
			}
			t.Errorf("id %q: %v", id, err)
		}
	}

	// A directory with no row is not a pack, however it came to be there.
	orphan := filepath.Join(s.dir, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "a.png"), pngImage, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, "orphan", "a.png"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an orphan: %v", err)
	}

	// A link planted in a pack is refused, wherever it points.
	link := filepath.Join(s.dir, "magic", "linked.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if file, _, err := s.Open(ctx, "magic", "linked.png"); !errors.Is(err, ErrNotFound) {
		if file != nil {
			file.Close()
		}
		t.Errorf("a symlink: %v", err)
	}
}

func TestContentTypeComesFromTheExtension(t *testing.T) {
	for name, want := range map[string]string{
		"a.webp": "image/webp", "a/b.png": "image/png", "a.jpg": "image/jpeg", "a.jpeg": "image/jpeg",
	} {
		if got, ok := ContentType(name); !ok || got != want {
			t.Errorf("%s = %q, %v", name, got, ok)
		}
	}
	for _, name := range []string{"a.svg", "a.html", "a.PNG", "a", "theme.json", "a.png.exe"} {
		if got, ok := ContentType(name); ok {
			t.Errorf("%s = %q", name, got)
		}
	}
}

func TestForViewer(t *testing.T) {
	pack := Pack{OwnerID: "alice"}
	agent := alice
	agent.Agent = identity.AgentWorker
	for _, tc := range []struct {
		name            string
		actor           identity.Actor
		mine, canDelete bool
	}{
		{"owner", alice, true, true},
		{"somebody else", bob, false, false},
		{"administrator", root, false, true},
		{"the owner's agent", agent, true, false},
	} {
		got := pack.For(tc.actor)
		if got.Mine != tc.mine || got.CanDelete != tc.canDelete {
			t.Errorf("%s: mine %v, canDelete %v", tc.name, got.Mine, got.CanDelete)
		}
	}
}
