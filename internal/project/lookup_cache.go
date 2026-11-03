// The project-lookup cache: what StoreFor remembers about a project name so
// that a request naming it need not stat the disk.
//
// StoreFor runs on every request that names a project (?project=). Finding one
// means resolving the name, statting the directory and then graph.yaml, and on
// Windows a stat of a file written a moment ago waits for whatever scanner is
// holding it: milliseconds per request, under exactly the load that writes.
// So a project StoreFor found is remembered, and the next lookup of the same
// name in the same workspace answers from memory.
//
// Only "it is there" is remembered, never "it is not": a project that is
// created must be found at once. What can make a remembered project go away:
//
//   - The manager renames, moves or deletes it: those all go through
//     EvictStores, which drops the lookup with the stores.
//   - Something outside deletes graph.yaml: the watcher (on the active project
//     and every watched one) reports graph-changed, which drops it.
//   - Neither of those saw it (an unwatched project changed from outside):
//     no lookup is trusted for longer than projectLookupTTL.
//
// The store's own graph writes replace graph.yaml atomically, so they prove
// the file is there; they refresh the lookup instead of dropping it, which is
// what keeps a request that follows a write off the disk.

package project

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// projectLookupTTL is how long StoreFor trusts a project it found without
// looking again. A var only so tests can pin it.
var projectLookupTTL = 2 * time.Second

// maxProjectLookups bounds the cache; past it the cache starts over.
const maxProjectLookups = 1024

type projectLookup struct {
	workspace string
	dir       string
	at        time.Time
}

type projectLookups struct {
	mu      sync.Mutex
	entries map[string]projectLookup
}

// get is the directory remembered for name in workspace, if still trusted.
func (c *projectLookups) get(workspace, name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[name]
	if !ok || entry.workspace != workspace || time.Since(entry.at) >= projectLookupTTL {
		return "", false
	}
	return entry.dir, true
}

func (c *projectLookups) remember(workspace, name, dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxProjectLookups {
		c.entries = make(map[string]projectLookup)
	}
	c.entries[name] = projectLookup{workspace: workspace, dir: dir, at: time.Now()}
}

// confirm restarts the clock on every lookup of dir: something just proved
// its graph.yaml is there.
func (c *projectLookups) confirm(dir string) {
	key := storeCacheKey(dir)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, entry := range c.entries {
		if storeCacheKey(entry.dir) == key {
			entry.at = now
			c.entries[name] = entry
		}
	}
}

// forget drops every lookup of dir and of any project under it.
func (c *projectLookups) forget(dir string) {
	prefix := storeCacheKey(dir)
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, entry := range c.entries {
		key := storeCacheKey(entry.dir)
		if key == prefix || strings.HasPrefix(key, prefix+string(filepath.Separator)) {
			delete(c.entries, name)
		}
	}
}

// noteStoreChange is the lookup half of the store write hook: a write of the
// project's graph.yaml proves it is there.
func (pm *ProjectManager) noteStoreChange(dir, path string) {
	if filepath.Clean(path) == filepath.Join(filepath.Clean(dir), "graph.yaml") {
		pm.lookups.confirm(dir)
	}
}
