package server

import "testing"

func TestNodePageImportPathOnlyMatchesImport(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"/api/nodes/node-1/pages/import":  true,
		"/api/nodes/node-1/pages":         false,
		"/api/nodes/node-1/pages/page-1":  false,
		"/api/nodes//pages/import":        false,
		"/api/nodes/node-1/pages/import/": false,
		"/api/nodes/node-1/files/import":  false,
	}
	for path, want := range tests {
		path, want := path, want
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if got := nodePageImportPath(path); got != want {
				t.Fatalf("nodePageImportPath(%q) = %v, want %v", path, got, want)
			}
		})
	}
}

func TestOrdinaryPageRoutesAreNotExpensive(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/nodes/node-1/pages",
		"/api/nodes/node-1/pages/page-1",
	} {
		if expensivePath(path) {
			t.Fatalf("ordinary page route %q must not use the expensive-work limiter", path)
		}
		if docxHeavyPath(path) {
			t.Fatalf("ordinary page route %q must not use the DOCX semaphore", path)
		}
	}

	if !expensivePath("/api/nodes/node-1/pages/import") ||
		!docxHeavyPath("/api/nodes/node-1/pages/import") {
		t.Fatal("page import must retain expensive-work and DOCX limits")
	}
}
