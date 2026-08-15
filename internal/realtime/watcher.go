package realtime

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"nodevas/internal/engine"
)

// Watch monitors the project directory and broadcasts change events for
// files modified by external tools (own writes are suppressed).
func Watch(st *store.Store, hub *Hub) (func() error, error) {
	return WatchWithCallback(st, hub, nil)
}

// WatchWithCallback monitors a project and invokes onEvent for emitted changes.
func WatchWithCallback(st *store.Store, hub *Hub, onEvent func(string, string)) (func() error, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	root := st.Root()
	nodesDir := filepath.Join(root, "nodes")
	watchDirs := []string{root, nodesDir, filepath.Join(root, "run")}
	// nodes/ nests: user folders, and each node's .pages directory inside
	// whichever folder holds it.
	_ = filepath.WalkDir(nodesDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || current == nodesDir {
			return nil
		}
		if !isWatchableNodeDir(root, current) {
			return fs.SkipDir
		}
		watchDirs = append(watchDirs, current)
		return nil
	})
	for _, dir := range watchDirs {
		if err := w.Add(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			_ = w.Close()
			return nil, fmt.Errorf("watch %s: %w", dir, err)
		}
	}

	// Debounce: editors fire several events per save.
	pending := map[string]time.Time{}

	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				if ev.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() &&
						isWatchableNodeDir(root, ev.Name) {
						if err := w.Add(ev.Name); err != nil {
							log.Printf("watch add %s: %v", ev.Name, err)
						}
					}
				}
				// A folder appearing or disappearing moves node files with
				// it, so the cached layout can no longer be trusted.
				if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 &&
					strings.HasPrefix(filepath.Clean(ev.Name), nodesDir) {
					store.InvalidateNodeTree(root)
				}
				pending[ev.Name] = time.Now()
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("watch: %v", err)
			case <-ticker.C:
				for path, t := range pending {
					if time.Since(t) < 200*time.Millisecond {
						continue
					}
					delete(pending, path)
					if st.WasSelfWrite(path) {
						continue
					}
					emitEvent(hub, root, path, onEvent)
				}
			}
		}
	}()
	return w.Close, nil
}

// isWatchableNodeDir reports whether a directory inside the project is one the
// watcher should follow: nodes/ and run/ themselves, any user folder under
// nodes/, and any node's .pages directory wherever its folder is. Attachment
// directories are deliberately left out, as they were before folders existed.
func isWatchableNodeDir(root, path string) bool {
	clean := filepath.Clean(path)
	nodesDir := filepath.Join(root, "nodes")
	if clean == nodesDir || clean == filepath.Join(root, "run") {
		return true
	}
	rel, err := filepath.Rel(nodesDir, clean)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	name := filepath.Base(clean)
	if strings.HasSuffix(name, ".pages") {
		return engine.ValidNodeID(strings.TrimSuffix(name, ".pages"))
	}
	return store.ValidFolderName(name) &&
		strings.Count(filepath.ToSlash(rel), "/")+1 <= store.MaxFolderDepth
}

func emit(hub *Hub, root, path string) {
	emitEvent(hub, root, path, nil)
}

func emitEvent(hub *Hub, root, path string, onEvent func(string, string)) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	notify := func(eventType, id string) {
		if onEvent != nil {
			onEvent(eventType, id)
		}
		hub.BroadcastRoom(root, eventType, id)
	}
	if strings.HasPrefix(rel, store.DataDir+"/") || strings.Contains(rel, "/"+store.TmpPrefix) || strings.HasPrefix(filepath.Base(rel), store.TmpPrefix) {
		return // internal bookkeeping — never broadcast
	}
	switch {
	case rel == "graph.yaml":
		notify("graph-changed", "")
	case strings.HasPrefix(rel, "nodes/"):
		// A node lives at nodes/<folder…>/<id>.md, with its subpages beside
		// it in <id>.pages/, so the id is found from the tail of the path
		// rather than from a fixed depth.
		parts := strings.Split(strings.TrimPrefix(rel, "nodes/"), "/")
		last := parts[len(parts)-1]
		switch {
		case strings.HasSuffix(last, ".md"):
			notify("node-changed", strings.TrimSuffix(last, ".md"))
		case len(parts) >= 2 && strings.HasSuffix(parts[len(parts)-2], ".pages"):
			notify("node-changed", strings.TrimSuffix(parts[len(parts)-2], ".pages"))
		}
	case rel == "run/state.json" || rel == "run/journal.jsonl":
		notify("state-changed", "")
	}
}
