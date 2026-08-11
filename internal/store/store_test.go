package store

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"nodevas/internal/engine"
)

func TestSanitizeAttachmentNameCapsLongExtensionAndKeepsUTF8(t *testing.T) {
	for _, input := range []string{
		"a." + strings.Repeat("x", 500),
		strings.Repeat("檔", 200) + ".png",
		"bad-utf8-\xff.png",
	} {
		got := SanitizeAttachmentName(input)
		if !utf8.ValidString(got) {
			t.Fatalf("SanitizeAttachmentName(%q) returned invalid UTF-8", input)
		}
		if count := utf8.RuneCountInString(got); count > maxAttachmentNameRunes {
			t.Fatalf("SanitizeAttachmentName(%q) has %d runes, limit %d", input, count, maxAttachmentNameRunes)
		}
		if count := utf8.RuneCountInString(filepath.Ext(got)); count > maxAttachmentExtensionRunes {
			t.Fatalf("SanitizeAttachmentName(%q) extension has %d runes", input, count)
		}
	}
}

func testDOCXWithImage(t *testing.T, documentXML string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entries := map[string]string{
		"word/document.xml":            documentXML,
		"word/_rels/document.xml.rels": `<Relationships><Relationship Id="rId1" Target="media/image.png"/></Relationships>`,
		"word/media/image.png":         "not-a-real-image",
	}
	for name, contents := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func TestDOCXMediaCommitsOnlyAfterSuccessfulParse(t *testing.T) {
	documentXML := `<w:document xmlns:w="urn:w" xmlns:r="urn:r" xmlns:a="urn:a"><w:body><w:p><w:r><w:drawing><a:blip r:embed="rId1"/></w:drawing></w:r></w:p></w:body></w:document>`
	root := t.TempDir()
	store := NewStore(root)
	if _, err := store.decodePageContent("node-1", PageFormatDOCX, testDOCXWithImage(t, documentXML)); err != nil {
		t.Fatalf("decode valid DOCX: %v", err)
	}
	entries, err := os.ReadDir(store.NodeFilesDir("node-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("committed media files = %d, want 1", len(entries))
	}
}

func TestMalformedDOCXDoesNotLeavePartialMedia(t *testing.T) {
	documentXML := `<w:document xmlns:w="urn:w" xmlns:r="urn:r" xmlns:a="urn:a"><w:body><w:p><w:r><w:drawing><a:blip r:embed="rId1"/></w:drawing></w:r></w:p><w:p>`
	root := t.TempDir()
	store := NewStore(root)
	if _, err := store.decodePageContent("node-1", PageFormatDOCX, testDOCXWithImage(t, documentXML)); err == nil {
		t.Fatal("malformed DOCX should fail")
	}
	filesDir := store.NodeFilesDir("node-1")
	if entries, err := os.ReadDir(filesDir); err == nil && len(entries) != 0 {
		t.Fatalf("malformed DOCX left %d partial media files", len(entries))
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestStoreSetStatusAllowsEditableLifecycle(t *testing.T) {
	root := t.TempDir()
	g := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "first"},
			{ID: "second", Requires: "first"},
		},
	}
	data, err := engine.MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(root)
	if _, err := store.SetStatus("first", engine.StatusDone, "test", ""); err != nil {
		t.Fatalf("direct completion should be recorded: %v", err)
	}
	if _, err := store.SetStatus("second", engine.StatusStarted, "test", ""); err != nil {
		t.Fatalf("dependency must warn in analysis rather than lock editing: %v", err)
	}
	if _, err := store.SetStatus("first", engine.StatusStarted, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStatus("first", engine.StatusInProgress, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStatus("first", engine.StatusDone, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStatus("first", engine.StatusStarted, "test", ""); err != nil {
		t.Fatalf("rewinding completed node should be recorded: %v", err)
	}
	if _, err := store.SetStatus("second", engine.StatusStarted, "test", ""); err != nil {
		t.Fatalf("dependent node should unlock after first completes: %v", err)
	}
}

func TestStoreSetStatusValidatesCustomStatusDefinition(t *testing.T) {
	root := t.TempDir()
	g := &engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "review"}},
		UI: &engine.UIState{CustomStatuses: []engine.StatusDefinition{{
			ID:    "custom-status-review",
			Label: "審核中",
			Color: "#8b7cf6",
			Shape: "diamond",
		}}},
	}
	data, err := engine.MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(root)
	if _, err := store.SetStatus(
		"review",
		engine.Status("custom-status-review"),
		"test",
		"",
	); err != nil {
		t.Fatalf("defined custom status should be accepted: %v", err)
	}
	if _, err := store.SetStatus(
		"review",
		engine.Status("custom-status-missing"),
		"test",
		"",
	); err == nil {
		t.Fatal("undefined custom status should be rejected")
	}
}

// A project whose edges were rewritten by an older client can carry bend
// points for edges that no longer exist. Loading repairs it, so validation
// does not keep reporting a wire nobody can see.
func TestLoadGraphDropsOrphanWireDecorations(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	graph := &engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "a"}, {ID: "b"}},
		Edges:   []*engine.Edge{{From: "a", To: "b"}},
		UI: &engine.UIState{
			EdgeLabels: map[string]engine.EdgeLabelPlacement{
				"a->b":     {Ratio: 0.5},
				"a->ghost": {Ratio: 0.5},
			},
			WireVertices: map[string][]engine.Position{
				"a->b":            {{X: 1, Y: 2}},
				"a->ghost":        {{X: 3, Y: 4}},
				"gate:ghost":      {{X: 5, Y: 6}},
				"logic:gone:in:a": {{X: 7, Y: 8}},
			},
		},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.GraphPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.UI.EdgeLabels) != 1 || loaded.UI.EdgeLabels["a->b"].Ratio != 0.5 {
		t.Errorf("edge labels = %+v, want only a->b", loaded.UI.EdgeLabels)
	}
	if len(loaded.UI.WireVertices) != 1 || len(loaded.UI.WireVertices["a->b"]) != 1 {
		t.Errorf("wire vertices = %+v, want only a->b", loaded.UI.WireVertices)
	}
	if issues := engine.Validate(loaded); len(issues) > 0 {
		for _, issue := range issues {
			if strings.Contains(issue.Msg, "wire vertices") {
				t.Errorf("validation still reports %q", issue.Msg)
			}
		}
	}
}
