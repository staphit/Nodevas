// Node links: `[[project/node-id|label]]` inside a node's markdown.
//
// The graph's edges describe dependencies; these links describe references —
// "see also", a table of contents, a place a rule is explained. They are
// therefore text, live in the documents, and cross project boundaries inside
// one workspace.

package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

// nodeLinkPattern matches one `[[target]]` or `[[target|label]]`.
var nodeLinkPattern = regexp.MustCompile(`\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]`)

// NodeLink is one link found in a document.
type NodeLink struct {
	// Where the link was written.
	FromProject string `json:"fromProject"`
	FromNode    string `json:"fromNode"`
	FromTitle   string `json:"fromTitle,omitempty"`
	// Where it points, always resolved to an absolute project/node pair.
	ToProject string `json:"toProject"`
	ToNode    string `json:"toNode"`
	Label     string `json:"label,omitempty"`
}

// SplitLinkTarget splits "Story/node-0012" into project and node. The split is
// at the last slash, because project names are nested paths. An empty project
// means the document's own project.
func SplitLinkTarget(target string) (project, nodeID string) {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		return strings.TrimSpace(trimmed[:slash]), strings.TrimSpace(trimmed[slash+1:])
	}
	return "", trimmed
}

// ParseNodeLinks returns every link in one document, resolved against the
// project the document belongs to.
func ParseNodeLinks(source, ownProject string) []NodeLink {
	matches := nodeLinkPattern.FindAllStringSubmatch(source, -1)
	links := make([]NodeLink, 0, len(matches))
	for _, match := range matches {
		toProject, toNode := SplitLinkTarget(match[1])
		if toNode == "" {
			continue
		}
		if toProject == "" {
			toProject = ownProject
		}
		links = append(links, NodeLink{
			FromProject: ownProject,
			ToProject:   toProject,
			ToNode:      toNode,
			Label:       strings.TrimSpace(match[2]),
		})
	}
	return links
}

// maxLinkScanBytes bounds one document read during a workspace-wide scan; a
// link lives near the top of a document, not in a megabyte of appendix.
const maxLinkScanBytes = 1 << 20

// ScanNodeLinks walks the given projects and returns every link their
// documents contain. Unreadable projects and documents are skipped: a
// backlink list is a convenience, and half of it beats an error.
func ScanNodeLinks(projects []ProjectInfo) []NodeLink {
	links := make([]NodeLink, 0, 64)
	for _, info := range projects {
		if info.IsFolder {
			continue
		}
		links = append(links, projectLinks(info)...)
	}
	return links
}

// scanProjectLinks reads one project's documents and returns their links.
func scanProjectLinks(info ProjectInfo) []NodeLink {
	var links []NodeLink
	for _, node := range graphNodes(info.Path) {
		data, err := store.ReadProjectFileLimit(info.Path, store.NodeDocPath(info.Path, node.ID), maxLinkScanBytes)
		if err != nil {
			continue
		}
		for _, link := range ParseNodeLinks(string(data), info.Name) {
			link.FromNode = node.ID
			link.FromTitle = node.Title
			links = append(links, link)
		}
	}
	return links
}

// NodeTitles returns one project's node ids with their titles, in graph
// order. Order matters: a picker that shuffles between openings is unusable.
func NodeTitles(info ProjectInfo) [][2]string {
	nodes := graphNodes(info.Path)
	out := make([][2]string, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, [2]string{node.ID, node.Title})
	}
	return out
}

// NodeIDsByProject maps each project to the set of node ids it holds, for
// telling a live link from one that points at something deleted.
func NodeIDsByProject(projects []ProjectInfo) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(projects))
	for _, info := range projects {
		if info.IsFolder {
			continue
		}
		nodes := graphNodes(info.Path)
		ids := make(map[string]bool, len(nodes))
		for _, node := range nodes {
			ids[node.ID] = true
		}
		out[info.Name] = ids
	}
	return out
}

// ---------- scan caches ----------
//
// The node panel asks for links every time a node is opened, and answering
// meant opening every document in the workspace again. Both the parsed links
// and the parsed graph headers are therefore kept per project directory and
// reused until the files they came from change. The validity signal is a stat
// of exactly the files that would have been read, which is a small fraction of
// reading them, so no filesystem watcher is involved.
//
// A rewrite that keeps both the size and the modification timestamp of a file
// is invisible to this. That is a fair trade for a backlink list, and outside
// tests it takes a filesystem whose timestamps are coarser than the interval
// between two saves of the same length.

// maxCachedProjects bounds both caches. A workspace larger than this drops
// them wholesale rather than growing the process; refilling costs one scan.
const maxCachedProjects = 512

// maxStampedDocuments bounds the stat walk that decides whether a cached scan
// is still valid. A project with more documents is scanned every time, because
// a fingerprint that ignored the rest would go stale silently.
const maxStampedDocuments = 20000

type graphNode struct {
	ID    string
	Title string
}

type cacheEntry[T any] struct {
	stamp string
	value T
}

var (
	graphCacheMu sync.Mutex
	graphCache   = map[string]cacheEntry[[]graphNode]{}

	linkCacheMu sync.Mutex
	linkCache   = map[string]cacheEntry[[]NodeLink]{}
	// linkCacheNames keeps the project name each cached scan was made under,
	// because the name is written into every link a project produces.
	linkCacheNames = map[string]string{}
)

// graphNodes returns the ids and titles in one project's graph.yaml, parsing
// the file only when it changed. The returned slice is shared with the cache
// and must not be modified.
func graphNodes(root string) []graphNode {
	stamp := fileStamp(root, filepath.Join(root, "graph.yaml"))
	graphCacheMu.Lock()
	entry, ok := graphCache[root]
	graphCacheMu.Unlock()
	if ok && entry.stamp == stamp {
		return entry.value
	}
	nodes := readGraphNodes(root)
	graphCacheMu.Lock()
	if len(graphCache) >= maxCachedProjects {
		graphCache = map[string]cacheEntry[[]graphNode]{}
	}
	graphCache[root] = cacheEntry[[]graphNode]{stamp: stamp, value: nodes}
	graphCacheMu.Unlock()
	return nodes
}

func readGraphNodes(root string) []graphNode {
	data, err := store.ReadProjectFile(root, filepath.Join(root, "graph.yaml"))
	if err != nil {
		return nil
	}
	graph, err := engine.ParseGraph(data)
	if err != nil {
		return nil
	}
	nodes := make([]graphNode, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node != nil {
			nodes = append(nodes, graphNode{ID: node.ID, Title: node.Title})
		}
	}
	return nodes
}

// projectLinks returns one project's links, from the cache while none of its
// files changed. The returned slice is shared with the cache and must not be
// modified.
func projectLinks(info ProjectInfo) []NodeLink {
	stamp, cacheable := projectDocumentsStamp(info.Path)
	if cacheable {
		linkCacheMu.Lock()
		entry, ok := linkCache[info.Path]
		name := linkCacheNames[info.Path]
		linkCacheMu.Unlock()
		if ok && entry.stamp == stamp && name == info.Name {
			return entry.value
		}
	}
	links := scanProjectLinks(info)
	if cacheable {
		linkCacheMu.Lock()
		if len(linkCache) >= maxCachedProjects {
			linkCache = map[string]cacheEntry[[]NodeLink]{}
			linkCacheNames = map[string]string{}
		}
		linkCache[info.Path] = cacheEntry[[]NodeLink]{stamp: stamp, value: links}
		linkCacheNames[info.Path] = info.Name
		linkCacheMu.Unlock()
	}
	return links
}

// projectDocumentsStamp fingerprints the files a scan of this project would
// read: graph.yaml, which decides which documents are visited, and every
// markdown file under nodes/. The second return is false for a project too
// large to fingerprint honestly.
func projectDocumentsStamp(root string) (string, bool) {
	sum := sha256.New()
	fmt.Fprintf(sum, "graph:%s\n", fileStamp(root, filepath.Join(root, "graph.yaml")))
	documents := 0
	if err := store.ValidateProjectTree(root, filepath.Join(root, "nodes")); err != nil {
		return "", false
	}
	_ = filepath.WalkDir(
		filepath.Join(root, "nodes"),
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}
			documents++
			if documents > maxStampedDocuments {
				return fs.SkipAll
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return nil
			}
			fmt.Fprintf(sum, "%s:%d:%d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		},
	)
	if documents > maxStampedDocuments {
		return "", false
	}
	return hex.EncodeToString(sum.Sum(nil)), true
}

// fileStamp describes a file by size and modification time. A missing file has
// the empty stamp, which is stable: it stays a cache hit until the file
// appears.
func fileStamp(root, path string) string {
	info, err := store.StatProjectPath(root, path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}
