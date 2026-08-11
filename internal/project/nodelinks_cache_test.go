package project

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// writeDocument writes one node document and dates it in the future, so a
// filesystem with a coarse clock still reports the change the cache watches
// for.
func writeDocument(t *testing.T, root, id, body string) {
	t.Helper()
	path := filepath.Join(root, "nodes", id+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func writeGraph(t *testing.T, root, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scannedLinks(t *testing.T, info ProjectInfo) []string {
	t.Helper()
	links := ScanNodeLinks([]ProjectInfo{info})
	out := make([]string, 0, len(links))
	for _, link := range links {
		out = append(out, link.FromNode+"->"+link.ToProject+"/"+link.ToNode)
	}
	sort.Strings(out)
	return out
}

func TestScanNodeLinksCacheFollowsChanges(t *testing.T) {
	const baseGraph = "version: 1\nnodes:\n  - id: n1\n    title: One\n  - id: n2\n    title: Two\n"

	cases := []struct {
		name   string
		change func(t *testing.T, root string)
		want   []string
	}{
		{
			// Nothing was touched, so the previous scan still answers. The
			// rewrite below keeps the document's size and timestamp, which is
			// exactly what a cache hit is allowed to miss; seeing the old link
			// is what proves the scan was not repeated.
			name: "untouched project is served from the cache",
			change: func(t *testing.T, root string) {
				path := filepath.Join(root, "nodes", "n1.md")
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("see [[n9]]"), 0o644); err != nil {
					t.Fatal(err)
				}
				stamp := before.ModTime()
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"n1->P/n2"},
		},
		{
			name: "edited document is picked up",
			change: func(t *testing.T, root string) {
				writeDocument(t, root, "n1", "see [[n2]] and [[Other/x1]]")
			},
			want: []string{"n1->Other/x1", "n1->P/n2"},
		},
		{
			name: "link removed from a document",
			change: func(t *testing.T, root string) {
				writeDocument(t, root, "n1", "nothing here")
			},
			want: []string{},
		},
		{
			name: "document deleted",
			change: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "nodes", "n1.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{},
		},
		{
			name: "node added to the graph",
			change: func(t *testing.T, root string) {
				writeGraph(t, root, baseGraph+"  - id: n3\n    title: Three\n")
				writeDocument(t, root, "n3", "see [[n1]]")
			},
			want: []string{"n1->P/n2", "n3->P/n1"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeGraph(t, root, baseGraph)
			writeDocument(t, root, "n1", "see [[n2]]")
			writeDocument(t, root, "n2", "no links")
			info := ProjectInfo{Name: "P", Path: root}

			if got := scannedLinks(t, info); len(got) != 1 || got[0] != "n1->P/n2" {
				t.Fatalf("first scan = %v, want [n1->P/n2]", got)
			}
			testCase.change(t, root)

			got := scannedLinks(t, info)
			if len(got) != len(testCase.want) {
				t.Fatalf("second scan = %v, want %v", got, testCase.want)
			}
			for i, want := range testCase.want {
				if got[i] != want {
					t.Errorf("second scan[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestGraphNodesCacheFollowsGraphChanges covers the header cache the node
// index reads through, which is invalidated by graph.yaml alone.
func TestGraphNodesCacheFollowsGraphChanges(t *testing.T) {
	root := t.TempDir()
	writeGraph(t, root, "version: 1\nnodes:\n  - id: a\n    title: Alpha\n")
	info := ProjectInfo{Name: "P", Path: root}

	if got := NodeTitles(info); len(got) != 1 || got[0] != [2]string{"a", "Alpha"} {
		t.Fatalf("NodeTitles = %v, want [[a Alpha]]", got)
	}
	writeGraph(t, root, "version: 1\nnodes:\n  - id: a\n    title: Alpha\n  - id: b\n    title: Beta\n")

	got := NodeTitles(info)
	if len(got) != 2 || got[1] != [2]string{"b", "Beta"} {
		t.Fatalf("NodeTitles after edit = %v, want two nodes ending in [b Beta]", got)
	}
	if ids := NodeIDsByProject([]ProjectInfo{info})["P"]; !ids["b"] {
		t.Errorf("NodeIDsByProject = %v, want b", ids)
	}
}
