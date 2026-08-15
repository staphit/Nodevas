package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/export"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pageFilePath(t *testing.T, root, nodeID, pageID, extension string) string {
	t.Helper()
	return filepath.Join(root, "nodes", nodeID+".pages", pageID+extension)
}

func createPageForTest(t *testing.T, server *Server, nodeID, title, format string) store.NodePageInfo {
	t.Helper()
	body, err := json.Marshal(map[string]string{"title": title, "format": format})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/nodes/"+nodeID+"/pages", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create %s page: status %d, body %s", format, response.Code, response.Body)
	}
	var decoded struct {
		Page store.NodePageInfo `json:"page"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Page
}

func TestCreatePageInEveryFormatWritesItsOwnFile(t *testing.T) {
	server, root := exportServerForTest(t)
	for format, extension := range map[string]string{
		"md":   ".md",
		"txt":  ".txt",
		"html": ".html",
		"docx": ".docx",
	} {
		page := createPageForTest(t, server, "alpha", "頁面 "+format, format)
		if page.Format != format {
			t.Errorf("page format = %q, want %q", page.Format, format)
		}
		path := pageFilePath(t, root, "alpha", page.ID, extension)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s page was not written to %s: %v", format, path, err)
		}
	}
}

func TestDocxPageRoundTripsThroughTheEditor(t *testing.T) {
	server, root := exportServerForTest(t)
	page := createPageForTest(t, server, "alpha", "報告", "docx")

	markdown := "# 報告\n\n第一段 **粗體**。\n\n- 項目一\n- 項目二\n"
	body, err := json.Marshal(map[string]string{"content": markdown, "baseRev": ""})
	if err != nil {
		t.Fatal(err)
	}
	// The starter file already exists, so save with the revision it has now.
	_, current, rev, err := server.store(nil).LoadNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current, "報告") {
		t.Errorf("starter content = %q", current)
	}
	body, err = json.Marshal(map[string]string{"content": markdown, "baseRev": rev})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut,
		"/api/nodes/alpha/pages/"+page.ID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save docx page: status %d, body %s", response.Code, response.Body)
	}

	data, err := os.ReadFile(pageFilePath(t, root, "alpha", page.ID, ".docx"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("saved page is not a Word package: %v", err)
	}
	_, reloaded, _, err := server.store(nil).LoadNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 報告", "**粗體**", "- 項目一", "- 項目二"} {
		if !strings.Contains(reloaded, want) {
			t.Errorf("reopened page lost %q:\n%s", want, reloaded)
		}
	}
}

func TestConvertPageRewritesTheFileAndKeepsHistory(t *testing.T) {
	server, root := exportServerForTest(t)
	page := createPageForTest(t, server, "alpha", "轉檔", "md")
	_, _, rev, err := server.store(nil).LoadNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.store(nil).SaveNodePage("alpha", page.ID,
		"# 轉檔\n\n內文一行\n", rev); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]string{"format": "docx"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch,
		"/api/nodes/alpha/pages/"+page.ID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("convert: status %d, body %s", response.Code, response.Body)
	}
	var decoded struct {
		Pages   []store.NodePageInfo `json:"pages"`
		Content string               `json:"content"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	converted, ok := export.FindPage(decoded.Pages, page.ID)
	if !ok || converted.Format != "docx" {
		t.Fatalf("manifest after conversion = %+v", decoded.Pages)
	}
	if !strings.Contains(decoded.Content, "內文一行") {
		t.Errorf("converted content = %q", decoded.Content)
	}
	if _, err := os.Stat(pageFilePath(t, root, "alpha", page.ID, ".md")); !os.IsNotExist(err) {
		t.Error("the Markdown file should be gone after converting")
	}
	if _, err := os.Stat(pageFilePath(t, root, "alpha", page.ID, ".docx")); err != nil {
		t.Errorf("the Word file is missing: %v", err)
	}
	history := filepath.Join(root, store.DataDir, "history", "nodes",
		"alpha.pages", page.ID+".md")
	entries, err := os.ReadDir(history)
	if err != nil || len(entries) == 0 {
		t.Errorf("the replaced file should leave a snapshot in %s (%v)", history, err)
	}
}

func TestImportFileAsPage(t *testing.T) {
	server, root := exportServerForTest(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "劇本草稿.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("第一行\n第二行\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/nodes/alpha/pages/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import: status %d, body %s", response.Code, response.Body)
	}
	var decoded struct {
		Page    store.NodePageInfo `json:"page"`
		Content string             `json:"content"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Page.Format != "txt" || decoded.Page.Title != "劇本草稿" {
		t.Errorf("imported page = %+v", decoded.Page)
	}
	if decoded.Content != "第一行\n第二行\n" {
		t.Errorf("imported content = %q", decoded.Content)
	}
	if _, err := os.Stat(pageFilePath(t, root, "alpha", decoded.Page.ID, ".txt")); err != nil {
		t.Errorf("imported file is missing: %v", err)
	}
}

func TestImportRejectsUnsupportedFileType(t *testing.T) {
	server, _ := exportServerForTest(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "photo.png")
	_, _ = part.Write([]byte("\x89PNG"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost,
		"/api/nodes/alpha/pages/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

// An omitted format uses the Markdown default.
func TestManifestDefaultsToMarkdown(t *testing.T) {
	server, _ := exportServerForTest(t)
	pages, err := server.store(nil).ListNodePages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Format != "md" {
		t.Fatalf("pages = %+v, want one Markdown page", pages)
	}
	_, content, _, err := server.store(nil).LoadNodePage("alpha", pages[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Sub page text.") {
		t.Errorf("page content = %q", content)
	}
}

func TestDeletedDocxPageRestoresIntact(t *testing.T) {
	server, root := exportServerForTest(t)
	page := createPageForTest(t, server, "alpha", "會議記錄", "docx")
	before, err := os.ReadFile(pageFilePath(t, root, "alpha", page.ID, ".docx"))
	if err != nil {
		t.Fatal(err)
	}
	deleteOutcome, err := server.store(nil).DeleteNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.store(nil).RestoreTrash(deleteOutcome.TrashFile); err != nil {
		t.Fatalf("restore: %v", err)
	}
	after, err := os.ReadFile(pageFilePath(t, root, "alpha", page.ID, ".docx"))
	if err != nil {
		t.Fatalf("restored page is missing: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("restored Word file does not match the deleted one byte for byte")
	}
	pages, err := server.store(nil).ListNodePages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := export.FindPage(pages, page.ID)
	if !ok || restored.Format != "docx" {
		t.Errorf("restored manifest entry = %+v", pages)
	}
}

func TestExportOfHtmlPageBecomesText(t *testing.T) {
	server, _ := exportServerForTest(t)
	page := createPageForTest(t, server, "alpha", "網頁", "html")
	_, _, rev, err := server.store(nil).LoadNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.store(nil).SaveNodePage("alpha", page.ID,
		"<h1>網頁</h1><p>段落 <b>粗</b></p>", rev); err != nil {
		t.Fatal(err)
	}
	response := postExportForTest(t, server, map[string]any{
		"format": "txt",
		"scope":  "page",
		"nodeId": "alpha",
		"pageId": page.ID,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	text := response.Body.String()
	if strings.Contains(text, "<h1>") || strings.Contains(text, "<b>") {
		t.Errorf("HTML tags leaked into the plain text export:\n%s", text)
	}
	if !strings.Contains(text, "段落 粗") {
		t.Errorf("export lost the page text:\n%s", text)
	}
}
