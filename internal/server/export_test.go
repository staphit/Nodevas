package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/export"
	"nodevas/internal/realtime"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

func exportServerForTest(t *testing.T) (*Server, string) {
	t.Helper()
	pm := projectManagerForTest(t)
	root := pm.Store().Root()
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "alpha", Title: "第一章"},
			{ID: "beta", Title: "第二章"},
		},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes", "alpha.pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nodes/alpha.md", "---\nid: alpha\ntitle: 第一章\n---\n\n"+
		"Body **bold** line.\n\n## Scene\n\n- one\n- two\n")
	write("nodes/beta.md", "Second node body.\n")
	write("nodes/alpha.pages/pages.json",
		`{"pages":[{"id":"page-0001","title":"文本-2"}]}`)
	write("nodes/alpha.pages/page-0001.md", "# 文本-2\n\nSub page text.\n\n## Detail\n\nmore\n")
	return serverForTest(t, pm, realtime.NewHub(), nil), root
}

func postExportForTest(t *testing.T, server *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/export", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestExportPageAsText(t *testing.T) {
	server, _ := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format": "txt",
		"scope":  "page",
		"nodeId": "alpha",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	text := response.Body.String()
	if !strings.Contains(text, "第一章") || !strings.Contains(text, "Body bold line.") {
		t.Errorf("plain text export = %q", text)
	}
	if strings.Contains(text, "id: alpha") {
		t.Errorf("frontmatter leaked into the export:\n%s", text)
	}
	if strings.Contains(text, "**") {
		t.Errorf("markdown markers survived:\n%s", text)
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("content disposition = %q", got)
	}
}

func TestExportUsesUnsavedBuffer(t *testing.T) {
	server, _ := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format":  "txt",
		"scope":   "page",
		"nodeId":  "alpha",
		"pageId":  "main",
		"content": "# 第一章\n\nEdited but not saved.\n",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Edited but not saved.") {
		t.Errorf("unsaved buffer was ignored:\n%s", response.Body.String())
	}
}

func TestExportSubpage(t *testing.T) {
	server, _ := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format": "md",
		"scope":  "page",
		"nodeId": "alpha",
		"pageId": "page-0001",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.HasPrefix(body, "# 文本-2") || !strings.Contains(body, "Sub page text.") {
		t.Errorf("subpage export = %q", body)
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, "utf-8") {
		t.Errorf("non-ASCII filename should be encoded, got %q", disposition)
	}
}

func TestExportNodeScopeNestsSubpages(t *testing.T) {
	server, _ := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format": "md",
		"scope":  "node",
		"nodeId": "alpha",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "# 第一章") {
		t.Errorf("node title heading missing:\n%s", body)
	}
	if !strings.Contains(body, "## 文本-2") {
		t.Errorf("subpage should sit one level below the node:\n%s", body)
	}
	if !strings.Contains(body, "### Detail") {
		t.Errorf("subpage headings should shift with it:\n%s", body)
	}
}

func TestExportProjectScopeCoversEveryNode(t *testing.T) {
	server, root := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format": "md",
		"scope":  "project",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		"# " + filepath.Base(root),
		"## 第一章",
		"### 文本-2",
		"## 第二章",
		"Second node body.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("project export is missing %q:\n%s", want, body)
		}
	}
}

func TestExportDocxIsAReadablePackage(t *testing.T) {
	server, _ := exportServerForTest(t)
	response := postExportForTest(t, server, map[string]any{
		"format": "docx",
		"scope":  "node",
		"nodeId": "alpha",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	data := response.Body.Bytes()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("docx is not a zip: %v", err)
	}
	var document string
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		document = string(body)
	}
	if !strings.Contains(document, "第一章") || !strings.Contains(document, "文本-2") {
		t.Errorf("document.xml is missing the exported content")
	}
	if want := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"; response.Header().Get("Content-Type") != want {
		t.Errorf("content type = %q", response.Header().Get("Content-Type"))
	}
}

func TestExportRejectsUnknownFormatAndNode(t *testing.T) {
	server, _ := exportServerForTest(t)
	bad := postExportForTest(t, server, map[string]any{
		"format": "pdf",
		"scope":  "page",
		"nodeId": "alpha",
	})
	if bad.Code != http.StatusBadRequest {
		t.Errorf("unknown format status = %d, want 400", bad.Code)
	}
	missing := postExportForTest(t, server, map[string]any{
		"format": "txt",
		"scope":  "page",
		"nodeId": "ghost",
	})
	if missing.Code != http.StatusBadRequest {
		t.Errorf("missing node status = %d, want 400", missing.Code)
	}
}

func TestShiftHeadingsLeavesFencedCodeAlone(t *testing.T) {
	source := "# Title\n\n```\n# not a heading\n```\n\n## Sub\n"
	got := export.ShiftHeadings(source, 1)
	want := "## Title\n\n```\n# not a heading\n```\n\n### Sub\n"
	if got != want {
		t.Errorf("shiftHeadings =\n%q\nwant\n%q", got, want)
	}
}

func TestExportFileNameSanitises(t *testing.T) {
	cases := map[string]string{
		"第一章":            "第一章",
		"a/b:c*d":        "a_b_c_d",
		"  spaced  out ": "spaced out",
		"":               "document",
		"...":            "document",
	}
	for input, want := range cases {
		if got := export.FileName(input); got != want {
			t.Errorf("exportFileName(%q) = %q, want %q", input, got, want)
		}
	}
}
