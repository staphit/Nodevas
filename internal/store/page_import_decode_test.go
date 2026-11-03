package store

import (
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/document"
	"nodevas/internal/identity"
)

// An upload that does not decode as the format its name claims is refused
// before anything is written: no page, no file.
func TestAnImportThatDoesNotDecodeLeavesNothing(t *testing.T) {
	st := benchPagesStore(t, 1, 0)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	page, _, _, err := st.ImportNodePage(actor, "n00000", "", "notes.docx", []byte("not a zip archive"))
	if err == nil {
		t.Fatal("a .docx that is not one was imported")
	}
	if page.ID != "" {
		t.Errorf("the failed import still made page %q", page.ID)
	}
	pages, err := st.ListNodePages("n00000")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 0 {
		t.Errorf("pages after a failed import = %+v", pages)
	}
	entries, _ := os.ReadDir(filepath.Join(st.Root(), "nodes", "n00000.pages"))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".docx" {
			t.Errorf("the failed import left %s on disk", entry.Name())
		}
	}
}

// Saving a Word page renders the whole document; that runs outside the
// store's lock, so a graph write never waits behind it (and every reader
// behind that write).
func TestADocxPageIsRenderedOutsideTheStoreLock(t *testing.T) {
	st := benchPagesStore(t, 1, 0)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	page, _, rev, err := st.CreateNodePage(actor, "n00000", "Report", PageFormatDOCX)
	if err != nil {
		t.Fatal(err)
	}
	real := renderDOCX
	t.Cleanup(func() { renderDOCX = real })
	underLock := false
	renderDOCX = func(doc *document.Doc, options document.Options) ([]byte, error) {
		if st.mu.TryLock() {
			st.mu.Unlock()
		} else {
			underLock = true
		}
		return real(doc, options)
	}
	if _, err := st.SaveNodePage(actor, "n00000", page.ID, "# Report\n\nbody\n", rev); err != nil {
		t.Fatal(err)
	}
	if underLock {
		t.Error("the page was rendered while the store's lock was held")
	}
}
