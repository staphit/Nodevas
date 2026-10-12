package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

func requireSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func requireUnsafeProjectPath(t *testing.T, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrUnsafeProjectPath) {
		t.Fatalf("error = %v, want ErrUnsafeProjectPath", err)
	}
}

func TestProjectPathHelpersRejectLeafAndParentSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe", "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}

	leaf := filepath.Join(root, "leaf.txt")
	requireSymlink(t, secret, leaf)
	_, err := ReadProjectFile(root, leaf)
	requireUnsafeProjectPath(t, err)
	if err := WriteProjectFileAtomic(root, leaf, []byte("overwritten")); err == nil {
		t.Fatal("atomic write followed a leaf symlink")
	} else {
		requireUnsafeProjectPath(t, err)
	}
	if err := RemoveProjectPath(root, leaf); err == nil {
		t.Fatal("remove accepted a leaf symlink")
	} else {
		requireUnsafeProjectPath(t, err)
	}

	parent := filepath.Join(root, "linked")
	requireSymlink(t, outside, parent)
	_, err = ReadProjectFile(root, filepath.Join(parent, "secret.txt"))
	requireUnsafeProjectPath(t, err)
	if err := WriteProjectFileAtomic(root, filepath.Join(parent, "created.txt"), []byte("escape")); err == nil {
		t.Fatal("atomic write followed a parent symlink")
	} else {
		requireUnsafeProjectPath(t, err)
	}
	if err := MkdirAllProjectPath(root, filepath.Join(parent, "nested"), 0o755); err == nil {
		t.Fatal("mkdir followed a parent symlink")
	} else {
		requireUnsafeProjectPath(t, err)
	}
	if _, err := MkdirTempProjectPath(root, parent, "temp-", 0o700); err == nil {
		t.Fatal("temporary mkdir followed a parent symlink")
	} else {
		requireUnsafeProjectPath(t, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
	got, err := os.ReadFile(secret)
	if err != nil || string(got) != "outside-secret" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestReadProjectFileLimitRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 65), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProjectFileLimit(root, path, 64); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want size limit", err)
	}
}

func TestDurableProjectMutationsSyncTheirParentDirectories(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for _, dir := range []string{left, right} {
		if err := MkdirAllProjectPath(root, dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(left, "sidecar.bin")
	target := filepath.Join(right, "sidecar.bin")
	if err := WriteProjectFileAtomicMode(root, source, []byte("durable"), 0o600); err != nil {
		t.Fatalf("atomic write and parent sync: %v", err)
	}
	if err := RenameProjectPath(root, source, root, target); err != nil {
		t.Fatalf("rename and parent sync: %v", err)
	}
	if err := RemoveProjectPath(root, target); err != nil {
		t.Fatalf("remove and parent sync: %v", err)
	}
	tree := filepath.Join(root, "tree", "nested")
	if err := MkdirAllProjectPath(root, tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllProjectPath(root, filepath.Join(root, "tree")); err != nil {
		t.Fatalf("remove tree and parent sync: %v", err)
	}
}

func TestNodeAndAttachmentOperationsFailClosedOnSymlinks(t *testing.T) {
	_, st := folderProject(t)
	outside := t.TempDir()
	outsideNode := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideNode, []byte("do not expose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(st.NodePath("alpha")); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, outsideNode, st.NodePath("alpha"))

	if _, _, err := st.LoadNodeContent("alpha"); err == nil {
		t.Fatal("LoadNodeContent followed a node symlink")
	}
	if _, err := st.ExportNodes([]string{"alpha"}, false); err == nil {
		t.Fatal("ExportNodes followed a node symlink")
	}
	if _, err := st.DeleteNode(identity.Local, "alpha"); err == nil {
		t.Fatal("DeleteNode accepted a node symlink")
	}

	if err := os.Remove(st.NodePath("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.NodePath("alpha"), []byte("# alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	page, pageContent, pageRev, err := st.CreateNodePage(identity.Local, "alpha", "Page", PageFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	pagePath := st.NodePagePath("alpha", page.ID, page.Format)
	if err := os.Remove(pagePath); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, outsideNode, pagePath)
	if _, _, _, err := st.LoadNodePage("alpha", page.ID); err == nil {
		t.Fatal("LoadNodePage followed a page symlink")
	}
	if _, err := st.SaveNodePage(identity.Local, "alpha", page.ID, "escape", pageRev); err == nil {
		t.Fatal("SaveNodePage accepted a page symlink")
	}
	if err := os.Remove(pagePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte(pageContent), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFiles := filepath.Join(outside, "files")
	if err := os.MkdirAll(outsideFiles, 0o755); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, outsideFiles, st.NodeFilesDir("alpha"))
	if _, err := st.SaveAttachment("alpha", "new.txt", strings.NewReader("escape")); err == nil {
		t.Fatal("SaveAttachment followed an attachment-directory symlink")
	}
	if _, err := st.ExportNodes([]string{"alpha"}, false); err == nil {
		t.Fatal("ExportNodes followed an attachment-directory symlink")
	}
	entries, err := os.ReadDir(outsideFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside attachment directory changed: %v", entries)
	}
}
