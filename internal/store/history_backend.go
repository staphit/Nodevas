// Where version history is kept: copies under .vised/history (history.go) or
// the project's own git repository (history_git.go).

package store

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/gitrepo"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// historyBackend is one way of keeping version history. The store calls the
// write-side hooks with s.mu held, so they must be quick and must never wait
// on git; the read side and flush run without it.
type historyBackend interface {
	// beforeOverwrite runs just before path is replaced, whose content is
	// current (nil: read it from disk; a caller that has just read the file
	// under s.mu passes what it read, sparing a read). always is for the
	// writes that destroy a version on purpose (a restore, a lossy format
	// conversion): that version must be recoverable however recent it is.
	beforeOverwrite(path string, current []byte, always bool) error
	// wrote runs after paths were written for actor. kind names the change
	// when the paths alone would not say it ("restore"); "" means derive it.
	wrote(actor identity.Actor, kind string, paths []string)
	// list is rel's versions, newest first.
	list(rel string) ([]HistoryVersion, error)
	// read is rel's content at version.
	read(rel, version string) ([]byte, error)
	// restoreTarget is the file a restore of rel writes to.
	restoreTarget(rel string) (string, error)
	// commitSoon asks for what is pending to be recorded now rather than at
	// the end of its window. It returns at once.
	commitSoon()
	// flush records what is pending and waits for it.
	flush(ctx context.Context) error
	// close flushes and stops any timer; later writes are still recorded on
	// the next flush.
	close(ctx context.Context) error
}

// HistoryMode chooses a history backend.
type HistoryMode string

const (
	// HistoryAuto is git when git is installed, files otherwise.
	HistoryAuto HistoryMode = "auto"
	// HistoryFiles keeps copies under .vised/history.
	HistoryFiles HistoryMode = "files"
	// HistoryGit uses the project's repository under .vised/git.
	HistoryGit HistoryMode = "git"
)

// ParseHistoryMode reads a setting such as NODEVAS_HISTORY; empty is auto.
func ParseHistoryMode(value string) (HistoryMode, error) {
	switch mode := HistoryMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return HistoryAuto, nil
	case HistoryAuto, HistoryFiles, HistoryGit:
		return mode, nil
	default:
		return "", locale.Errorf("history mode %q: want auto, files or git", "歷史模式「%s」無效：應為 auto、files 或 git", value)
	}
}

var (
	defaultHistoryMu   sync.Mutex
	defaultHistoryMode = HistoryFiles

	// gitAvailable is a seam: tests stand in a machine without git.
	gitAvailable = gitrepo.Available
)

// SetDefaultHistoryMode sets the backend stores use unless told otherwise.
//
// The package default is files, and the server switches it to auto at
// startup. Plenty of code opens a Store briefly (tests, the catalog, an
// archive check); none of it should start a repository and a commit timer in
// whatever directory it touched.
func SetDefaultHistoryMode(mode HistoryMode) {
	defaultHistoryMu.Lock()
	defer defaultHistoryMu.Unlock()
	defaultHistoryMode = mode
}

func currentDefaultHistoryMode() HistoryMode {
	defaultHistoryMu.Lock()
	defer defaultHistoryMu.Unlock()
	return defaultHistoryMode
}

// UseHistory picks this store's backend. It takes effect only before the
// store first touches history; afterwards the backend in use stays, since
// switching midway would split one file's versions across two places.
func (s *Store) UseHistory(mode HistoryMode) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.historyImpl == nil {
		s.historyMode = mode
	}
}

// history is this store's backend, chosen on first use. Lazily, so a Store
// that never writes a tracked file never creates a repository.
func (s *Store) history() historyBackend {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.historyImpl != nil {
		return s.historyImpl
	}
	mode := s.historyMode
	if mode == "" {
		mode = currentDefaultHistoryMode()
	}
	switch {
	case mode == HistoryFiles:
		s.historyImpl = fileHistory{s: s}
	case gitAvailable():
		s.historyImpl = newGitHistory(s, historyCoalesceWindow)
	default:
		if mode == HistoryGit {
			log.Printf("history for %s: %v (%s); keeping file copies", s.root, gitrepo.ErrUnavailable, gitrepo.UnavailableReason())
		}
		s.historyImpl = fileHistory{s: s}
	}
	return s.historyImpl
}

// noteTreeChanged is for the writes that move files around rather than
// through applyUpdatesLocked (folders): nobody's save, but the next history
// record has to include them. Caller holds s.mu.
func (s *Store) noteTreeChanged() {
	s.history().wrote(identity.Actor{}, "", nil)
}

// HistoryBackend names the backend in use: "files" or "git".
func (s *Store) HistoryBackend() HistoryMode {
	if _, ok := s.history().(*gitHistory); ok {
		return HistoryGit
	}
	return HistoryFiles
}

// FlushHistory records every pending change now and waits for it. The
// server calls it before a final backup, so the backup holds the history.
func (s *Store) FlushHistory(ctx context.Context) error {
	s.historyMu.Lock()
	backend := s.historyImpl
	s.historyMu.Unlock()
	if backend == nil {
		return nil
	}
	return backend.flush(ctx)
}

// Close records pending history and stops its timer. The store stays usable.
func (s *Store) Close(ctx context.Context) error {
	s.historyMu.Lock()
	backend := s.historyImpl
	s.historyMu.Unlock()
	if backend == nil {
		return nil
	}
	return backend.close(ctx)
}

// fileHistory is the original backend: a copy of each file before it is
// overwritten, under .vised/history. Its code is in history.go.
type fileHistory struct{ s *Store }

func (f fileHistory) beforeOverwrite(path string, current []byte, always bool) error {
	window := historyCoalesceWindow
	if always {
		window = 0
	}
	return f.s.snapshotContentWithin(path, current, window)
}

func (fileHistory) wrote(identity.Actor, string, []string) {}
func (f fileHistory) list(rel string) ([]HistoryVersion, error) {
	return f.s.listHistoryFiles(rel)
}
func (f fileHistory) read(rel, version string) ([]byte, error) {
	return f.s.readHistoryFile(rel, version)
}
func (f fileHistory) restoreTarget(rel string) (string, error) {
	if _, err := f.s.historyDir(rel); err != nil {
		return "", err
	}
	return filepath.Join(f.s.root, filepath.FromSlash(rel)), nil
}
func (fileHistory) commitSoon()                 {}
func (fileHistory) flush(context.Context) error { return nil }
func (fileHistory) close(context.Context) error { return nil }
