package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// transferTestProject makes an empty project directory with a graph in it.
func transferTestProject(t *testing.T, workspace, name string, nodes ...*engine.Node) *Store {
	t.Helper()
	dir := filepath.Join(workspace, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := NewStore(dir)
	if _, err := st.SaveGraph(identity.Local, &engine.Graph{Version: 1, Nodes: nodes}, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

// The document under test: one link to a node that travels with it, one to a
// node left behind, one already naming another project, and one that only
// looks like a link because it spans a newline.
const transferLinkDocument = "See [[node-2|Two]] and [[node-3|Three]].\n" +
	"Elsewhere [[Other/node-9|Nine]].\n" +
	"Not a link: [[node-3\n|Broken]]\n"

func TestImportNodesRewritesNodeLinks(t *testing.T) {
	workspace := t.TempDir()
	// A workspace is recognised by its lock file, so the source project's name
	// is the nested path rather than just its directory name.
	if err := os.MkdirAll(filepath.Join(workspace, DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, DataDir, workspaceLockFileName), []byte("{}"), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	source := transferTestProject(t, workspace, "team/Story",
		&engine.Node{ID: "node-1", Title: "One"},
		&engine.Node{ID: "node-2", Title: "Two"},
		&engine.Node{ID: "node-3", Title: "Three"},
	)
	_, rev, err := source.LoadNodeContent("node-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := source.SaveNodeContent(identity.Local, "node-1", transferLinkDocument, rev); err != nil {
		t.Fatal(err)
	}

	for _, forMove := range []bool{true, false} {
		// A copy leaves the source project just as a move does, so both have
		// to rewrite the links the same way.
		name := "copy"
		if forMove {
			name = "cut"
		}
		t.Run(name, func(t *testing.T) {
			// A sibling inside the same folder: the nearest shared ancestor is
			// not the workspace, so the name has to come from the lock file.
			target := transferTestProject(t, workspace, "team/Target-"+name)
			payload, err := source.ExportNodes([]string{"node-1", "node-2"}, forMove)
			if err != nil {
				t.Fatal(err)
			}
			result, err := target.ImportNodes(payload)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target.NodePath(result.IDs["node-1"]))
			if err != nil {
				t.Fatal(err)
			}
			document := string(data)
			for _, want := range []string{
				"[[" + result.IDs["node-2"] + "|Two]]", // travelled along, stays bare
				"[[team/Story/node-3|Three]]",          // stayed behind, now qualified
				"[[Other/node-9|Nine]]",                // already qualified, untouched
				"[[node-3\n|Broken]]",                  // not a link, must not change
			} {
				if !strings.Contains(document, want) {
					t.Fatalf("document is missing %q:\n%s", want, document)
				}
			}
			if strings.Contains(document, "[[node-2|Two]]") {
				t.Fatalf("link to a moved node kept the source id:\n%s", document)
			}
		})
	}
}
