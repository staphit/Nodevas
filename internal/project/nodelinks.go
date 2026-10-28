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
	"nodevas/internal/safego"
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

// readLinkDocument reads one document for a link scan. A var so tests can
// watch the reads.
var readLinkDocument = func(root, path string) ([]byte, error) {
	return store.ReadProjectFileLimit(root, path, maxLinkScanBytes)
}

// WarmNodeLinks scans every project's links in the background, so the first
// node drawer opened after a start is answered from the cache rather than from
// a read of every document in the workspace (seconds, on a large one with a
// cold disk). A drawer opened during the warm-up waits for the project being
// scanned instead of reading it a second time. Close stops it at the next
// project and waits for it.
func (pm *ProjectManager) WarmNodeLinks() {
	if pm.closing.Load() {
		return
	}
	pm.linksWarm.Add(1)
	go func() {
		defer pm.linksWarm.Done()
		defer safego.Recover("warm node links")
		projects, err := pm.List()
		if err != nil {
			return
		}
		for _, info := range projects {
			if pm.closing.Load() {
				return
			}
			if !info.IsFolder {
				projectLinks(info)
			}
		}
	}()
}

// linkScanWorkers is how many documents of one project a scan reads at once.
// The reads wait on the disk, not the CPU: overlapping them is what turns a
// cold scan of thousands of documents from seconds into a fraction of that.
const linkScanWorkers = 8

// documentLinks is what one document said, with the stamp of the version it
// was read at. The links carry no FromNode or FromTitle: those come from the
// graph, which changes without the document changing.
type documentLinks struct {
	stamp string
	links []NodeLink
}

// scanProjectLinks returns one project's links in graph order, and what each
// document said. documents holds the stamp of every document on disk, keyed
// by path; a node whose document is not in it has none. A document whose
// stamp matches previous is not read again. With documents nil (a project
// too large to stamp) every document is read.
func scanProjectLinks(info ProjectInfo, documents map[string]string, previous map[string]documentLinks) ([]NodeLink, map[string]documentLinks) {
	nodes := graphNodes(info.Path)
	paths := make([]string, len(nodes))
	found := make([]documentLinks, len(nodes))
	var unread []int
	var located map[string]string
	if documents != nil {
		located = store.NodeDocPaths(info.Path)
	}
	for i, node := range nodes {
		if documents == nil {
			paths[i] = store.NodeDocPath(info.Path, node.ID)
			unread = append(unread, i)
			continue
		}
		paths[i] = located[node.ID]
		stamp, onDisk := documents[paths[i]]
		if paths[i] == "" || !onDisk {
			continue
		}
		if known, ok := previous[paths[i]]; ok && known.stamp == stamp {
			found[i] = known
			continue
		}
		found[i].stamp = stamp
		unread = append(unread, i)
	}

	next := make(chan int)
	var wg sync.WaitGroup
	for range min(linkScanWorkers, len(unread)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safego.Recover("scan node links")
			for i := range next {
				data, err := readLinkDocument(info.Path, paths[i])
				if err != nil {
					// Not remembered: the next scan tries it again.
					found[i] = documentLinks{}
					continue
				}
				found[i].links = ParseNodeLinks(string(data), info.Name)
			}
		}()
	}
	for _, i := range unread {
		next <- i
	}
	close(next)
	wg.Wait()

	var links []NodeLink
	read := make(map[string]documentLinks, len(nodes))
	for i, node := range nodes {
		if found[i].stamp != "" {
			read[paths[i]] = found[i]
		}
		for _, link := range found[i].links {
			link.FromNode = node.ID
			link.FromTitle = node.Title
			links = append(links, link)
		}
	}
	return links, read
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

	// linkCacheMu guards linkCache and linkScans.
	linkCacheMu sync.Mutex
	linkCache   = map[string]*projectLinkCache{}
	// linkScans are the scans running now, per project directory: a second
	// caller with the same stamp and name waits for the first one's result
	// instead of reading the project again.
	linkScans = map[string]*linkScan{}
)

// projectLinkCache is one project's links as last scanned: the project's
// stamp and name then (the name is written into every link), the links, and
// what each document said, so the next scan reads only what changed.
type projectLinkCache struct {
	stamp     string
	name      string
	links     []NodeLink
	documents map[string]documentLinks
}

type linkScan struct {
	stamp string
	name  string
	done  chan struct{}
	links []NodeLink
}

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
// files changed, and reading only the documents that did when some have. The
// returned slice is shared with the cache and must not be modified.
func projectLinks(info ProjectInfo) []NodeLink {
	stamp, documents, cacheable := projectDocuments(info.Path)
	if !cacheable {
		links, _ := scanProjectLinks(info, nil, nil)
		return links
	}
	linkCacheMu.Lock()
	cached := linkCache[info.Path]
	if cached != nil && cached.stamp == stamp && cached.name == info.Name {
		linkCacheMu.Unlock()
		return cached.links
	}
	if running, ok := linkScans[info.Path]; ok && running.stamp == stamp && running.name == info.Name {
		linkCacheMu.Unlock()
		<-running.done
		return running.links
	}
	scan := &linkScan{stamp: stamp, name: info.Name, done: make(chan struct{})}
	linkScans[info.Path] = scan
	linkCacheMu.Unlock()

	defer func() {
		linkCacheMu.Lock()
		if linkScans[info.Path] == scan {
			delete(linkScans, info.Path)
		}
		linkCacheMu.Unlock()
		close(scan.done)
	}()
	var previous map[string]documentLinks
	if cached != nil && cached.name == info.Name {
		previous = cached.documents
	}
	links, read := scanProjectLinks(info, documents, previous)
	scan.links = links
	linkCacheMu.Lock()
	if len(linkCache) >= maxCachedProjects {
		linkCache = map[string]*projectLinkCache{}
	}
	linkCache[info.Path] = &projectLinkCache{stamp: stamp, name: info.Name, links: links, documents: read}
	linkCacheMu.Unlock()
	return links
}

// projectDocuments fingerprints the files a scan of this project would read:
// graph.yaml, which decides which documents are visited, and every markdown
// file under nodes/, whose stamps it also returns by path. The last return
// is false for a project too large to fingerprint honestly.
func projectDocuments(root string) (string, map[string]string, bool) {
	sum := sha256.New()
	fmt.Fprintf(sum, "graph:%s\n", fileStamp(root, filepath.Join(root, "graph.yaml")))
	if err := store.ValidateProjectTree(root, filepath.Join(root, "nodes")); err != nil {
		return "", nil, false
	}
	documents := map[string]string{}
	tooMany := false
	_ = filepath.WalkDir(
		filepath.Join(root, "nodes"),
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}
			if len(documents) >= maxStampedDocuments {
				tooMany = true
				return fs.SkipAll
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return nil
			}
			stamp := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
			documents[path] = stamp
			fmt.Fprintf(sum, "%s:%s\n", path, stamp)
			return nil
		},
	)
	if tooMany {
		return "", nil, false
	}
	return hex.EncodeToString(sum.Sum(nil)), documents, true
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
