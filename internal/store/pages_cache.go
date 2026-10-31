// The page-list cache.
//
// Listing a node's pages means reading pages.json and checking that every
// page it names is a regular file, which takes a directory listing: a few
// milliseconds for a few hundred pages on Windows, on an endpoint the board
// calls for every node it opens. So the store keeps each node's last listing
// and its verdict, and serves them again while they still hold.
//
// The manifest is never trusted from memory: every listing reads pages.json
// and compares its SHA-256, as the graph cache does with graph.yaml, so any
// change to the list itself, the store's or anyone else's, is seen at once.
// The verdict on the files is reused under three conditions. Everything the
// store writes into a pages directory is a regular file, so its own writes
// cannot make the verdict wrong; only a change from outside can. The file
// watcher reports those and drops every verdict (InvalidatePageLists). A
// moved node is a different directory, and so a fresh check. And no verdict
// outlives pageListRecheck, for a store with no watcher or a watcher that
// lost events.

package store

import (
	"crypto/sha256"
	"time"
)

// pageListRecheck is how long a page-file verdict is reused at most.
var pageListRecheck = 5 * time.Second

// maxCachedPageLists bounds the cache; past it the cache starts over.
const maxCachedPageLists = 1024

type cachedPageList struct {
	sum [sha256.Size]byte
	// dir is the pages directory the verdict is about.
	dir string
	// pages is the decoded manifest, shared: callers get a copy.
	pages     []NodePageInfo
	checkedAt time.Time
	// generation is pageListGen when the listing began.
	generation uint64
}

// fresh reports whether the remembered verdict still answers for dir.
func (c *cachedPageList) fresh(dir string, generation uint64, now time.Time) bool {
	return c != nil && c.dir == dir && c.generation == generation &&
		now.Sub(c.checkedAt) < pageListRecheck
}

// InvalidatePageLists drops every remembered page-file verdict. The file
// watcher calls it when something other than the store changes a node's
// files.
func (s *Store) InvalidatePageLists() {
	s.pageListGen.Add(1)
}

// cachedPageList is the remembered listing of nodeID if its manifest bytes
// are still sum, or nil.
func (s *Store) cachedPageList(nodeID string, sum [sha256.Size]byte) *cachedPageList {
	s.pageListMu.Lock()
	defer s.pageListMu.Unlock()
	cached := s.pageLists[nodeID]
	if cached == nil || cached.sum != sum {
		return nil
	}
	return cached
}

func (s *Store) rememberPageList(nodeID string, entry *cachedPageList) {
	s.pageListMu.Lock()
	defer s.pageListMu.Unlock()
	if s.pageLists == nil || len(s.pageLists) >= maxCachedPageLists {
		s.pageLists = map[string]*cachedPageList{}
	}
	s.pageLists[nodeID] = entry
}
