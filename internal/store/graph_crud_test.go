package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
)

type gatedAttachmentReader struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
	reader  *bytes.Reader
}

func (r *gatedAttachmentReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.reader.Read(p)
}

func TestDuplicateNodeCopiesAttachmentsAndRetargetsLocalLinks(t *testing.T) {
	_, st := folderProject(t)
	sourceDocument := []byte("# alpha\n\n![local](/api/nodes/alpha/files/photo.png)\n![other](/api/nodes/beta/files/photo.png)\n")
	if err := os.WriteFile(st.NodePath("alpha"), sourceDocument, 0o644); err != nil {
		t.Fatal(err)
	}
	page, _, _, err := st.CreateNodePage(identity.Local, "alpha", "Notes", "md")
	if err != nil {
		t.Fatal(err)
	}
	sourcePage := []byte("[local](/api/nodes/alpha/files/data.bin) [other](/api/nodes/beta/files/data.bin)\n")
	if err := os.WriteFile(st.NodePagePath("alpha", page.ID, page.Format), sourcePage, 0o644); err != nil {
		t.Fatal(err)
	}

	wantFiles := map[string][]byte{
		"photo.png": {0x89, 'P', 'N', 'G', 0x00, 0xff},
		"data.bin":  {0x00, 0x01, 0x02, 0xfe, 0xff},
	}
	if err := os.MkdirAll(st.NodeFilesDir("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range wantFiles {
		if err := os.WriteFile(filepath.Join(st.NodeFilesDir("alpha"), name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	duplicateID, err := st.DuplicateNode("alpha")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	for name, want := range wantFiles {
		got, err := os.ReadFile(filepath.Join(st.NodeFilesDir(duplicateID), name))
		if err != nil {
			t.Fatalf("read duplicate attachment %q: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("duplicate attachment %q = %v, want %v", name, got, want)
		}
	}

	document, err := os.ReadFile(st.NodePath(duplicateID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), "/api/nodes/"+duplicateID+"/files/photo.png") {
		t.Errorf("duplicate document did not retarget its attachment: %s", document)
	}
	if !strings.Contains(string(document), "/api/nodes/beta/files/photo.png") {
		t.Errorf("duplicate document rewrote another node's attachment: %s", document)
	}
	pageData, err := os.ReadFile(st.NodePagePath(duplicateID, page.ID, page.Format))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pageData), "/api/nodes/"+duplicateID+"/files/data.bin") {
		t.Errorf("duplicate page did not retarget its attachment: %s", pageData)
	}
	if !strings.Contains(string(pageData), "/api/nodes/beta/files/data.bin") {
		t.Errorf("duplicate page rewrote another node's attachment: %s", pageData)
	}
	unchangedDocument, err := os.ReadFile(st.NodePath("alpha"))
	if err != nil || !bytes.Equal(unchangedDocument, sourceDocument) {
		t.Errorf("source document changed while duplicating: %q, %v", unchangedDocument, err)
	}
	unchangedPage, err := os.ReadFile(st.NodePagePath("alpha", page.ID, page.Format))
	if err != nil || !bytes.Equal(unchangedPage, sourcePage) {
		t.Errorf("source page changed while duplicating: %q, %v", unchangedPage, err)
	}

	if _, err := st.DeleteNode(identity.Local, "alpha"); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	for name, want := range wantFiles {
		got, err := os.ReadFile(filepath.Join(st.NodeFilesDir(duplicateID), name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("duplicate attachment %q did not survive source deletion: %v, %v", name, got, err)
		}
	}
}

func TestDuplicateNodeRejectsLinkedAttachmentsWithoutCommitting(t *testing.T) {
	_, st := folderProject(t)
	if err := os.MkdirAll(st.NodeFilesDir("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(st.NodeFilesDir("alpha"), "linked.bin")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := st.DuplicateNode("alpha"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("duplicate with linked attachment = %v, want symbolic-link error", err)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("failed duplicate committed %d graph nodes, want 2", len(graph.Nodes))
	}
}

func TestDuplicateNodeRejectsLinkedAttachmentDestination(t *testing.T) {
	_, st := folderProject(t)
	if err := os.MkdirAll(st.NodeFilesDir("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.NodeFilesDir("alpha"), "safe.bin"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	st.mu.Lock()
	graph, _, err := st.loadGraphLocked()
	if err != nil {
		st.mu.Unlock()
		t.Fatal(err)
	}
	duplicateID, err := st.nextNodeIDLocked(graph)
	st.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, st.NodeFilesDir(duplicateID)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := st.DuplicateNode("alpha"); err == nil || !strings.Contains(err.Error(), "destination is not a regular directory") {
		t.Fatalf("duplicate into linked destination = %v, want destination error", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("duplicate wrote outside its node directory: %v", entries)
	}
	graph, _, err = st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID(duplicateID) != nil {
		t.Fatalf("failed duplicate %q was committed", duplicateID)
	}
}

func TestDuplicateNodeCanReuseAnEmptyAttachmentDestination(t *testing.T) {
	_, st := folderProject(t)
	if err := os.MkdirAll(st.NodeFilesDir("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.NodeFilesDir("alpha"), "safe.bin"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	st.mu.Lock()
	graph, _, err := st.loadGraphLocked()
	if err != nil {
		st.mu.Unlock()
		t.Fatal(err)
	}
	duplicateID, err := st.nextNodeIDLocked(graph)
	st.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.NodeFilesDir(duplicateID), 0o755); err != nil {
		t.Fatal(err)
	}

	gotID, err := st.DuplicateNode("alpha")
	if err != nil {
		t.Fatalf("duplicate with empty destination: %v", err)
	}
	if gotID != duplicateID {
		t.Fatalf("duplicate id = %q, want %q", gotID, duplicateID)
	}
	data, err := os.ReadFile(filepath.Join(st.NodeFilesDir(duplicateID), "safe.bin"))
	if err != nil || string(data) != "safe" {
		t.Fatalf("duplicate attachment = %q, %v", data, err)
	}
}

func TestDuplicateNodeSerializesWithAttachmentUpload(t *testing.T) {
	_, st := folderProject(t)
	want := []byte("the complete upload, never a partial snapshot")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseUpload := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseUpload)

	uploadDone := make(chan struct {
		name string
		err  error
	}, 1)
	go func() {
		name, err := st.SaveAttachment("alpha", "concurrent.bin", &gatedAttachmentReader{
			started: started,
			release: release,
			reader:  bytes.NewReader(want),
		})
		uploadDone <- struct {
			name string
			err  error
		}{name: name, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not enter its reader while holding mediaMu")
	}

	duplicateDone := make(chan struct {
		id  string
		err error
	}, 1)
	go func() {
		id, err := st.DuplicateNode("alpha")
		duplicateDone <- struct {
			id  string
			err error
		}{id: id, err: err}
	}()

	// DuplicateNode must first own s.mu and then wait for the upload's
	// mediaMu. Observing s.mu held makes this lock-order assertion
	// deterministic without adding a production test hook.
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case result := <-duplicateDone:
			t.Fatalf("duplicate returned during an incomplete upload: id=%q err=%v", result.id, result.err)
		default:
		}
		if !st.mu.TryLock() {
			break
		}
		st.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("duplicate did not acquire s.mu before waiting for mediaMu")
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case result := <-duplicateDone:
		t.Fatalf("duplicate returned while upload remained blocked: id=%q err=%v", result.id, result.err)
	default:
	}
	releaseUpload()

	var uploadResult struct {
		name string
		err  error
	}
	select {
	case uploadResult = <-uploadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not finish")
	}
	if uploadResult.err != nil {
		t.Fatalf("upload: %v", uploadResult.err)
	}

	var duplicateResult struct {
		id  string
		err error
	}
	select {
	case duplicateResult = <-duplicateDone:
	case <-time.After(2 * time.Second):
		t.Fatal("duplicate did not finish after upload released mediaMu")
	}
	if duplicateResult.err != nil {
		t.Fatalf("duplicate: %v", duplicateResult.err)
	}
	got, err := os.ReadFile(filepath.Join(st.NodeFilesDir(duplicateResult.id), uploadResult.name))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("concurrent attachment snapshot = %q, %v; want %q", got, err, want)
	}
}

func TestReadDuplicateAttachmentsEnforcesBudgets(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string][]byte
		limits  duplicateAttachmentLimits
		wantErr string
	}{
		{
			name:  "file count",
			files: map[string][]byte{"a": {}, "b": {}},
			limits: duplicateAttachmentLimits{
				maxFiles: 1, maxFileBytes: 10, maxTotalBytes: 10,
			},
			wantErr: "file-count limit",
		},
		{
			name:  "stale temporary file count",
			files: map[string][]byte{TmpPrefix + "a": {}, TmpPrefix + "b": {}},
			limits: duplicateAttachmentLimits{
				maxFiles: 1, maxFileBytes: 10, maxTotalBytes: 10,
			},
			wantErr: "file-count limit",
		},
		{
			name:  "single file bytes",
			files: map[string][]byte{"a": []byte("abc")},
			limits: duplicateAttachmentLimits{
				maxFiles: 10, maxFileBytes: 2, maxTotalBytes: 10,
			},
			wantErr: "single-file limit",
		},
		{
			name:  "aggregate bytes",
			files: map[string][]byte{"a": []byte("ab"), "b": []byte("cd")},
			limits: duplicateAttachmentLimits{
				maxFiles: 10, maxFileBytes: 2, maxTotalBytes: 3,
			},
			wantErr: "aggregate limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readDuplicateAttachmentsWithLimits(dir, tt.limits); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("snapshot error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadDuplicateAttachmentRejectsSizeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "changing.bin")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := root.Lstat("changing.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readDuplicateAttachment(root, "changing.bin", before, 100, 100); err == nil || !strings.Contains(err.Error(), "changed while duplicating") {
		t.Fatalf("size-change error = %v, want changed-while-duplicating error", err)
	}
}

func TestDuplicateNodeRejectsOversizeAttachmentWithoutCommitting(t *testing.T) {
	_, st := folderProject(t)
	if err := os.MkdirAll(st.NodeFilesDir("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.NodeFilesDir("alpha"), "too-large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxDuplicateAttachmentFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := st.DuplicateNode("alpha"); err == nil || !strings.Contains(err.Error(), "single-file limit") {
		t.Fatalf("oversize duplicate error = %v, want single-file limit", err)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("oversize duplicate committed %d graph nodes, want 2", len(graph.Nodes))
	}
}
