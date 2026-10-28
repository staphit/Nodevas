// Version history kept in the project's own git repository, <project>/.vised/git
// (docs/history-and-backup.md). The file backend in history.go is the
// fallback when git is not installed.

package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/gitrepo"
	"nodevas/internal/identity"
	"nodevas/internal/safego"
)

// historyImportMarker says the .vised/history copies are in the repository.
// The copies themselves stay where they are, untouched.
const historyImportMarker = "history-imported"

// importTrailer names, in each imported commit, the copy it came from: the
// copy's path under .vised/history. The newest one is where an interrupted
// import resumes.
const importTrailer = "Nodevas-Import: "

// importChunk is how many copies one fast-import run takes, which bounds the
// memory an import holds at once.
const importChunk = 200

// errProjectGone is a project folder that was deleted or moved away while a
// commit was waiting. There is nothing left to record.
var errProjectGone = errors.New("project folder is gone")

// gitHistory batches saves into commits: at most one per window per project,
// plus one at once for a restore, a delete or a conversion.
//
// Two locks, so a save never waits on git. mu guards the pending batch and
// is only ever held for map and slice work, including under s.mu. gitMu
// serializes this project's git commands; it is never taken under s.mu, and
// while holding it the only store lock taken is s.mu through LoadGraph, so
// the two cannot deadlock.
type gitHistory struct {
	s      *Store
	window time.Duration

	mu       sync.Mutex
	pending  *gitPending
	timer    *time.Timer
	closed   bool
	inflight sync.WaitGroup

	gitMu sync.Mutex
	repo  *gitrepo.Repo
	// opened is repo once it is ready, for readers: they only read the
	// repository, so they never wait on gitMu behind a commit.
	opened atomic.Pointer[gitrepo.Repo]
}

// gitPending is what the next commit records: who saved, and any earlier
// versions that have to go in before the work tree's current state.
type gitPending struct {
	authors []gitrepo.Person
	// carried is who saved before a restore or conversion split the batch.
	// Their versions went into the split's commit; they stay co-authors of
	// the batch's final commit, which may hold more of their work.
	carried []gitrepo.Person
	// kinds names a change the file statuses would not, by project-relative
	// path: "restore", "convert page".
	kinds map[string]string
	// segments are versions no longer on disk, oldest first.
	segments []gitSegment
}

// gitSegment is one earlier version of some files, committed before the
// batch's final state.
type gitSegment struct {
	// authors is who saved this content.
	authors []gitrepo.Person
	// outside is content this store did not write: another program, a sync
	// tool, an editor on disk.
	outside bool
	changes []gitrepo.Change
	at      time.Time
}

func newGitHistory(s *Store, window time.Duration) *gitHistory {
	g := &gitHistory{s: s, window: window}
	// Opening the repository and importing old copies can take a while, so
	// it starts now rather than when someone first opens the history panel.
	g.inflight.Add(1)
	go func() {
		defer g.inflight.Done()
		defer safego.Recover("history open")
		if _, err := g.ready(context.Background()); err != nil && !errors.Is(err, errProjectGone) {
			log.Printf("history for %s: %v", s.root, err)
		}
	}()
	return g
}

// beforeOverwrite keeps the version a write is about to replace when the
// commit at the end of the window would not otherwise have it: content this
// store did not write, anything a restore or conversion replaces, and the
// version an editing session started from. Later writes in the same session
// need nothing: the window coalesces them on purpose.
//
// Caller holds s.mu, exclusively or (a page save) for reading together with
// abs's path lock. History never fails a save, so errors are logged.
func (g *gitHistory) beforeOverwrite(abs string, data []byte, always bool) error {
	if data == nil {
		var err error
		data, err = g.s.ReadFile(abs)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("history for %s: read before overwrite: %v", g.s.root, err)
			}
			return nil
		}
	}
	mark, known := g.s.loadHistoryMark(abs)
	own := known && mark.rev == Rev(data)
	now := time.Now()
	if own && !always && !mark.snapshotAt.IsZero() && now.Sub(mark.snapshotAt) < g.window {
		return nil
	}
	rel, ok := g.rel(abs)
	if !ok {
		return nil
	}
	change := gitrepo.Change{Path: rel, Data: data}
	if own && !always {
		// A session's first write keeps the version it started from, as
		// the file copies did, and commits it now rather than at the end
		// of the window: the history panel has it at once, and a version
		// created and then edited within one window is not lost to the
		// window's single commit.
		mark.snapshotAt = now
		g.s.storeHistoryMark(abs, mark)
		g.mu.Lock()
		pending := g.pendingLocked()
		pending.segments = append(pending.segments, gitSegment{changes: []gitrepo.Change{change}, at: now})
		g.mu.Unlock()
		g.commitStartsSoon()
		return nil
	}
	// Content this store did not write starts a session too.
	mark.snapshotAt = now
	g.s.storeHistoryMark(abs, mark)
	g.mu.Lock()
	defer g.mu.Unlock()
	pending := g.pendingLocked()
	if own {
		// The batch so far is these authors' work: it gets its own commit,
		// so the restore has something to undo to, and the restore's commit
		// is the restorer's.
		pending.segments = append(pending.segments, gitSegment{
			authors: append([]gitrepo.Person(nil), pending.authors...),
			changes: []gitrepo.Change{change},
			at:      now,
		})
		for _, person := range pending.authors {
			if !containsPerson(pending.carried, person) {
				pending.carried = append(pending.carried, person)
			}
		}
		pending.authors = nil
		return nil
	}
	// Outside content arrives a file at a time (each file's first save after
	// a restart looks like this too); one commit holds a run of them.
	if n := len(pending.segments); n > 0 && pending.segments[n-1].outside &&
		!pending.segments[n-1].has(rel) {
		pending.segments[n-1].changes = append(pending.segments[n-1].changes, change)
	} else {
		pending.segments = append(pending.segments, gitSegment{outside: true, changes: []gitrepo.Change{change}, at: now})
	}
	// It is committed now, outside g.mu: it exists nowhere else.
	g.mu.Unlock()
	g.commitStartsSoon()
	g.mu.Lock()
	return nil
}

func (seg gitSegment) has(rel string) bool {
	for _, change := range seg.changes {
		if change.Path == rel {
			return true
		}
	}
	return false
}

// wrote adds actor to the next commit's authors. Caller holds s.mu.
func (g *gitHistory) wrote(actor identity.Actor, kind string, paths []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending := g.pendingLocked()
	if person, ok := commitAuthor(actor); ok && !containsPerson(pending.authors, person) {
		pending.authors = append(pending.authors, person)
	}
	if kind == "" {
		return
	}
	for _, abs := range paths {
		if rel, ok := g.rel(abs); ok {
			pending.kinds[rel] = kind
		}
	}
}

// pendingLocked is the open batch, opening one (and its window) if needed.
// Caller holds g.mu.
func (g *gitHistory) pendingLocked() *gitPending {
	if g.pending == nil {
		g.pending = &gitPending{kinds: map[string]string{}}
	}
	if g.timer == nil && !g.closed {
		g.timer = time.AfterFunc(g.window, g.commitSoonSafely)
	}
	return g.pending
}

// commitSoonSafely is commitSoon as the window's timer calls it: a timer
// callback is a bare goroutine, and a panic there would end the server.
func (g *gitHistory) commitSoonSafely() {
	defer safego.Recover("history commit window")
	g.commitSoon()
}

func (g *gitHistory) commitSoon() {
	g.mu.Lock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if g.pending == nil || g.closed {
		g.mu.Unlock()
		return
	}
	// Added under mu, and close sets closed under mu before it waits: no Add
	// can race the Wait.
	g.inflight.Add(1)
	g.mu.Unlock()
	go func() {
		defer g.inflight.Done()
		defer safego.Recover("history commit")
		if err := g.commit(context.Background(), false); err != nil {
			log.Printf("history for %s: %v", g.s.root, err)
		}
	}()
}

// commitStartsSoon commits the batch's earlier versions now, in the
// background, and leaves the rest of the batch (who saved, the final state)
// to the window.
func (g *gitHistory) commitStartsSoon() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.inflight.Add(1)
	g.mu.Unlock()
	go func() {
		defer g.inflight.Done()
		defer safego.Recover("history commit segments")
		if err := g.commitSegments(context.Background()); err != nil {
			log.Printf("history for %s: %v", g.s.root, err)
		}
	}()
}

// commitSegments commits the earlier versions waiting in the batch, oldest
// first, and nothing else.
func (g *gitHistory) commitSegments(ctx context.Context) error {
	g.gitMu.Lock()
	defer g.gitMu.Unlock()
	if !g.hasSegments() {
		return nil
	}
	// Opened before the segments are taken: a repository opening now commits
	// them ahead of the work tree (importCopies), which is newer than they are.
	repo, err := g.readyLocked(ctx)
	if errors.Is(err, errProjectGone) {
		g.takeSegments()
		return nil
	}
	if err != nil {
		return err
	}
	segments := g.takeSegments()
	if len(segments) == 0 {
		return nil
	}
	return g.commitSegmentsLocked(ctx, repo, g.titlesWithCommitted(ctx, repo), segments)
}

// hasSegments reports whether earlier versions wait in the batch.
func (g *gitHistory) hasSegments() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pending != nil && len(g.pending.segments) > 0
}

// takeSegments removes the earlier versions waiting in the batch and returns
// them, oldest first.
func (g *gitHistory) takeSegments() []gitSegment {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil {
		return nil
	}
	segments := g.pending.segments
	g.pending.segments = nil
	return segments
}

// commitSegmentsLocked commits earlier versions in order. Caller holds gitMu.
func (g *gitHistory) commitSegmentsLocked(ctx context.Context, repo *gitrepo.Repo, titles map[string]string, segments []gitSegment) error {
	for _, seg := range segments {
		author, coAuthors := splitAuthors(seg.authors)
		outside := ""
		if seg.outside {
			outside = "outside edit"
		}
		message := commitMessage(titles, nil, outside, coAuthors)
		if _, err := repo.CommitChanges(ctx, seg.changes, message, author, seg.at); err != nil {
			return fmt.Errorf("record earlier versions: %w", err)
		}
	}
	return nil
}

func (g *gitHistory) flush(ctx context.Context) error {
	return g.commit(ctx, true)
}

func (g *gitHistory) close(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.mu.Unlock()
	err := g.commit(ctx, true)
	done := make(chan struct{})
	go func() {
		g.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		err = errors.Join(err, ctx.Err())
	}
	return err
}

// commit records the pending batch. force commits the work tree even with
// nothing pending, which picks up edits made outside the store (shutdown,
// before a backup).
func (g *gitHistory) commit(ctx context.Context, force bool) error {
	g.gitMu.Lock()
	defer g.gitMu.Unlock()
	g.mu.Lock()
	idle := g.pending == nil
	g.mu.Unlock()
	if idle && !force {
		return nil
	}
	// Opened before the batch is taken: a repository opening now commits the
	// batch's earlier versions ahead of the work tree (importCopies).
	repo, err := g.readyLocked(ctx)
	pending := g.takePending()
	if errors.Is(err, errProjectGone) {
		return nil
	}
	if err != nil {
		// The repository is not usable yet; the batch waits for the next try
		// rather than losing who saved.
		g.requeue(pending)
		return err
	}
	if pending == nil {
		pending = &gitPending{}
	}
	titles := g.titlesWithCommitted(ctx, repo)
	if err := g.commitSegmentsLocked(ctx, repo, titles, pending.segments); err != nil {
		return err
	}
	paths, err := g.s.historyTrackedFiles()
	if err != nil {
		return fmt.Errorf("list tracked files: %w", err)
	}
	authors := append([]gitrepo.Person(nil), pending.authors...)
	for _, person := range pending.carried {
		if !containsPerson(authors, person) {
			authors = append(authors, person)
		}
	}
	author, coAuthors := splitAuthors(authors)
	if _, err := repo.CommitTracked(ctx, paths, commitMessage(titles, pending.kinds, "", coAuthors), author, time.Now()); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	// A commit every few minutes leaves loose objects behind; --auto packs
	// them once there are enough, and does nothing until then.
	if _, err := repo.Run(ctx, nil, "gc", "--auto", "--quiet"); err != nil {
		log.Printf("history for %s: gc: %v", g.s.root, err)
	}
	return nil
}

// takePending removes the open batch, and its window, and returns it.
func (g *gitHistory) takePending() *gitPending {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending := g.pending
	g.pending = nil
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	return pending
}

// requeue puts a batch that could not be committed back in front of
// whatever arrived since.
func (g *gitHistory) requeue(batch *gitPending) {
	if batch == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil {
		for _, person := range g.pending.authors {
			if !containsPerson(batch.authors, person) {
				batch.authors = append(batch.authors, person)
			}
		}
		for _, person := range g.pending.carried {
			if !containsPerson(batch.carried, person) {
				batch.carried = append(batch.carried, person)
			}
		}
		for rel, kind := range g.pending.kinds {
			batch.kinds[rel] = kind
		}
		batch.segments = append(batch.segments, g.pending.segments...)
	}
	g.pending = batch
	if g.timer == nil && !g.closed {
		g.timer = time.AfterFunc(g.window, g.commitSoonSafely)
	}
}

// ready is the repository with the old copies imported.
//
// Once it is open, ready does not take gitMu: its callers only read the
// repository (log, show, a status that takes no lock), which git allows while
// a commit runs, and a history panel must not wait out every commit a stream
// of saves keeps starting.
func (g *gitHistory) ready(ctx context.Context) (*gitrepo.Repo, error) {
	if repo := g.opened.Load(); repo != nil {
		if _, err := g.s.statPath(g.s.GraphPath()); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, errProjectGone
			}
			return nil, err
		}
		return repo, nil
	}
	g.gitMu.Lock()
	defer g.gitMu.Unlock()
	return g.readyLocked(ctx)
}

// readyLocked opens the repository and imports .vised/history on first use.
// Caller holds gitMu.
func (g *gitHistory) readyLocked(ctx context.Context) (*gitrepo.Repo, error) {
	// Checked every time: a project deleted while a commit was waiting must
	// not get a fresh .vised/git planted where it was.
	if _, err := g.s.statPath(g.s.GraphPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errProjectGone
		}
		return nil, err
	}
	if g.repo != nil {
		return g.repo, nil
	}
	repo, err := gitrepo.Open(ctx, g.s.root)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	if err := g.importCopies(ctx, repo); err != nil {
		return nil, fmt.Errorf("import .vised/history: %w", err)
	}
	g.repo = repo
	g.opened.Store(repo)
	return repo, nil
}

// historyCopy is one file under .vised/history.
type historyCopy struct {
	// rel is the file it is a version of; name is the copy's file name,
	// which starts with the time it was taken.
	rel  string
	name string
}

func (c historyCopy) stamp() string  { return c.name[:len("20060102-150405.000000000")] }
func (c historyCopy) source() string { return c.rel + "/" + c.name }

func (c historyCopy) after(other historyCopy) bool {
	if c.stamp() != other.stamp() {
		return c.stamp() > other.stamp()
	}
	return c.rel > other.rel
}

// importCopies turns the file backend's copies into backdated commits, oldest
// first, then commits the live tree, then leaves the marker.
//
// An import cut short resumes rather than repeats: each commit names its copy
// in a trailer, each fast-import run lands whole or not at all, and the
// newest trailer is where the next attempt starts.
func (g *gitHistory) importCopies(ctx context.Context, repo *gitrepo.Repo) error {
	marker := filepath.Join(g.s.root, DataDir, historyImportMarker)
	if _, err := g.s.statPath(marker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	copies, err := g.s.historyCopies()
	if err != nil {
		return err
	}
	last, err := lastImportedCopy(ctx, repo)
	if err != nil {
		return err
	}
	if last != nil {
		remaining := copies[:0]
		for _, c := range copies {
			if c.after(*last) {
				remaining = append(remaining, c)
			}
		}
		copies = remaining
	}
	titles := g.titles()
	imported := 0
	for start := 0; start < len(copies); start += importChunk {
		end := min(start+importChunk, len(copies))
		batch := make([]gitrepo.Import, 0, end-start)
		for _, c := range copies[start:end] {
			data, err := g.s.ReadFile(filepath.Join(g.s.root, DataDir, "history", filepath.FromSlash(c.source())))
			if err != nil {
				log.Printf("history for %s: skip unreadable copy %s: %v", g.s.root, c.source(), err)
				continue
			}
			at, err := time.Parse("20060102-150405.000000000", c.stamp())
			if err != nil {
				continue
			}
			label, _ := describePath(titles, c.rel)
			batch = append(batch, gitrepo.Import{
				Path:    c.rel,
				Data:    data,
				Message: label + ": earlier version\n\n" + importTrailer + c.source() + "\n",
				At:      at,
			})
		}
		if err := repo.Import(ctx, batch); err != nil {
			return err
		}
		imported += len(batch)
	}
	// Earlier versions saved while the repository was opening are older than
	// the work tree: committed after it, they would read as a change undoing
	// what the work tree holds.
	if segments := g.takeSegments(); len(segments) > 0 {
		if err := g.commitSegmentsLocked(ctx, repo, titles, segments); err != nil {
			return err
		}
	}
	paths, err := g.s.historyTrackedFiles()
	if err != nil {
		return err
	}
	if _, err := repo.CommitTracked(ctx, paths, commitMessage(titles, nil, "", nil), gitrepo.Person{}, time.Now()); err != nil {
		return err
	}
	note := fmt.Sprintf("%d versions imported from .vised/history at %s\n", imported, time.Now().UTC().Format(time.RFC3339))
	return g.s.WriteAtomic(marker, []byte(note))
}

// lastImportedCopy is the copy named by the newest import trailer, or nil.
func lastImportedCopy(ctx context.Context, repo *gitrepo.Repo) (*historyCopy, error) {
	message, err := repo.LastMessageContaining(ctx, importTrailer)
	if err != nil || message == "" {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(message))
	for scanner.Scan() {
		value, ok := strings.CutPrefix(scanner.Text(), importTrailer)
		if !ok {
			continue
		}
		slash := strings.LastIndex(value, "/")
		if slash < 0 || !historyVersionPattern.MatchString(value[slash+1:]) {
			continue
		}
		return &historyCopy{rel: value[:slash], name: value[slash+1:]}, nil
	}
	return nil, nil
}

// historyCopies lists the file backend's copies, oldest first.
func (s *Store) historyCopies() ([]historyCopy, error) {
	base := filepath.Join(s.root, DataDir, "history")
	var copies []historyCopy
	err := filepath.WalkDir(base, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			if abs == base && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return nil
		}
		if !entry.Type().IsRegular() || !historyVersionPattern.MatchString(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(base, filepath.Dir(abs))
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel != "graph.yaml" && !strings.HasPrefix(rel, "nodes/") {
			return nil
		}
		copies = append(copies, historyCopy{rel: rel, name: entry.Name()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(copies, func(i, j int) bool { return copies[j].after(copies[i]) })
	return copies, nil
}

// historyTrackedFiles is what a commit holds, project-relative: graph.yaml,
// every node document in any folder, and each node's subpages with their
// manifest. Attachments (<id>.files) are left out, as the file backend
// never kept them either, and so is anything a crash left half-written.
func (s *Store) historyTrackedFiles() ([]string, error) {
	var paths []string
	if info, err := s.statPath(s.GraphPath()); err == nil && info.Mode().IsRegular() {
		paths = append(paths, "graph.yaml")
	}
	nodes := filepath.Join(s.root, "nodes")
	err := filepath.WalkDir(nodes, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			if abs == nodes {
				return filepath.SkipAll
			}
			return nil
		}
		name := entry.Name()
		if abs == nodes {
			return nil
		}
		if strings.HasPrefix(name, TmpPrefix) {
			return nil
		}
		if entry.IsDir() {
			if strings.HasSuffix(name, ".files") {
				return filepath.SkipDir
			}
			return nil
		}
		// Symbolic links and other non-files are not history.
		if !entry.Type().IsRegular() {
			return nil
		}
		parent := filepath.Base(filepath.Dir(abs))
		keep := false
		if strings.HasSuffix(parent, ".pages") {
			keep = name == "pages.json" || historyPageExtensions[filepath.Ext(name)]
		} else {
			keep = filepath.Ext(name) == ".md"
		}
		if !keep {
			return nil
		}
		rel, err := filepath.Rel(s.root, abs)
		if err != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths, err
}

// list is rel's commits, newest first. rel is the name the UI knows,
// nodes/<id>.md; the file may live in a folder, and may have moved.
func (g *gitHistory) list(rel string) ([]HistoryVersion, error) {
	actual, err := g.actualRel(rel)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	repo, err := g.ready(ctx)
	if errors.Is(err, errProjectGone) {
		return []HistoryVersion{}, nil
	}
	if err != nil {
		return nil, err
	}
	versions, err := repo.History(ctx, actual, historyPerFile)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryVersion, 0, len(versions))
	for _, version := range versions {
		out = append(out, HistoryVersion{
			Name:   version.SHA,
			At:     version.At.UTC().Format(time.RFC3339),
			Size:   version.Size,
			Author: version.Author,
		})
	}
	return out, nil
}

func (g *gitHistory) read(rel, version string) ([]byte, error) {
	actual, err := g.actualRel(rel)
	if err != nil {
		return nil, err
	}
	if !gitrepo.ValidSHA(version) {
		return nil, fmt.Errorf("invalid version name")
	}
	ctx := context.Background()
	repo, err := g.ready(ctx)
	if err != nil {
		return nil, err
	}
	data, err := repo.Show(ctx, version, actual)
	if err == nil {
		return data, nil
	}
	// The file had another name in that commit: it has moved into a folder
	// since. Its own history says which.
	versions, historyErr := repo.History(ctx, actual, 0)
	if historyErr == nil {
		for _, candidate := range versions {
			if candidate.SHA == version && candidate.Path != actual {
				return repo.Show(ctx, version, candidate.Path)
			}
		}
	}
	return nil, fmt.Errorf("version %.12s of %s: %w", version, rel, err)
}

func (g *gitHistory) restoreTarget(rel string) (string, error) {
	actual, err := g.actualRel(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(g.s.root, filepath.FromSlash(actual)), nil
}

// actualRel validates rel as the file backend does and maps it to where the
// file is now: a node in a folder is at nodes/<folder>/<id>.md.
func (g *gitHistory) actualRel(rel string) (string, error) {
	if _, err := g.s.historyDir(rel); err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	tail, isNode := strings.CutPrefix(rel, "nodes/")
	if !isNode {
		return rel, nil
	}
	var abs string
	if dir, file, isPage := strings.Cut(tail, "/"); isPage {
		abs = filepath.Join(g.s.NodePagesDir(strings.TrimSuffix(dir, ".pages")), file)
	} else {
		abs = g.s.NodePath(strings.TrimSuffix(tail, ".md"))
	}
	actual, ok := g.rel(abs)
	if !ok {
		return "", fmt.Errorf("history not tracked for %q", rel)
	}
	return actual, nil
}

// rel is abs relative to the project, with forward slashes.
func (g *gitHistory) rel(abs string) (string, bool) {
	rel, err := filepath.Rel(g.s.root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// titles maps node IDs to titles for commit messages; a graph that cannot be
// read leaves messages with IDs.
func (g *gitHistory) titles() map[string]string {
	titles := map[string]string{}
	if graph, _, err := g.s.LoadGraph(); err == nil {
		addTitles(titles, graph)
	}
	return titles
}

// titlesWithCommitted is titles plus those of nodes only the last commit
// still has: a deleted node is named by its title, not its ID.
func (g *gitHistory) titlesWithCommitted(ctx context.Context, repo *gitrepo.Repo) map[string]string {
	titles := map[string]string{}
	if head, err := repo.Run(ctx, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		if data, err := repo.Show(ctx, strings.TrimSpace(string(head)), "graph.yaml"); err == nil {
			if graph, err := engine.ParseGraph(data); err == nil {
				addTitles(titles, graph)
			}
		}
	}
	for id, title := range g.titles() {
		titles[id] = title
	}
	return titles
}

func addTitles(titles map[string]string, graph *engine.Graph) {
	for _, node := range graph.Nodes {
		if node != nil && strings.TrimSpace(node.Title) != "" {
			titles[node.ID] = node.Title
		}
	}
}

// commitAuthor is the person a save is attributed to. An actor carries no
// e-mail address, so the address is made up under nodevas.local, which is
// enough for git to tell people apart.
func commitAuthor(actor identity.Actor) (gitrepo.Person, bool) {
	name := strings.TrimSpace(actor.Name)
	if name == "" {
		name = strings.TrimSpace(actor.ID)
	}
	if name == "" {
		return gitrepo.Person{}, false
	}
	local := emailLocalPart(name)
	if local == "" {
		local = emailLocalPart(actor.ID)
	}
	if local == "" {
		local = "user"
	}
	if actor.IsAgent() {
		name += " (" + string(actor.Agent) + ")"
		local += "+" + emailLocalPart(string(actor.Agent))
	}
	return gitrepo.Person{Name: name, Email: local + "@nodevas.local"}, true
}

func emailLocalPart(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

func containsPerson(people []gitrepo.Person, person gitrepo.Person) bool {
	for _, p := range people {
		if p == person {
			return true
		}
	}
	return false
}

// splitAuthors is the first author and the rest, who become co-authors.
func splitAuthors(people []gitrepo.Person) (gitrepo.Person, []gitrepo.Person) {
	if len(people) == 0 {
		return gitrepo.Person{}, nil
	}
	return people[0], people[1:]
}

const (
	maxMessageGroups = 6
	maxSubjectLength = 200
	coAuthorTrailer  = "Co-authored-by: "
)

// commitMessage describes a commit from its paths and the kind of each change,
// never from content: "Auth API: edit page; Billing: create; graph: edit".
// kinds overrides the kind derived from a path's status; fallback, when set,
// is the kind of every path (an outside edit).
func commitMessage(titles map[string]string, kinds map[string]string, fallback string, coAuthors []gitrepo.Person) gitrepo.Message {
	return func(changes []gitrepo.Status) string {
		type group struct {
			label string
			kinds []string
		}
		var order []string
		groups := map[string]*group{}
		for _, change := range changes {
			label, key := describePath(titles, change.Path)
			kind := kinds[change.Path]
			if kind == "" {
				kind = fallback
			}
			if kind == "" {
				kind = changeKind(change)
			}
			current, ok := groups[key]
			if !ok {
				current = &group{label: label}
				groups[key] = current
				order = append(order, key)
			}
			if !containsString(current.kinds, kind) {
				current.kinds = append(current.kinds, kind)
			}
		}
		// Nearly every change touches the graph too; the nodes say more, so
		// they come first.
		sort.SliceStable(order, func(i, j int) bool { return order[i] != "graph" && order[j] == "graph" })
		var parts []string
		for i, key := range order {
			if i == maxMessageGroups {
				parts = append(parts, fmt.Sprintf("and %d more", len(order)-i))
				break
			}
			current := groups[key]
			// A node whose document left one folder and arrived in another
			// was moved, not deleted and created.
			if containsString(current.kinds, "delete") && containsString(current.kinds, "create") {
				var merged []string
				for _, kind := range current.kinds {
					if kind == "delete" {
						continue
					}
					if kind == "create" {
						kind = "move"
					}
					merged = append(merged, kind)
				}
				current.kinds = merged
			}
			parts = append(parts, current.label+": "+strings.Join(current.kinds, ", "))
		}
		subject := strings.Join(parts, "; ")
		if subject == "" {
			subject = "update"
		}
		subject = truncateRunes(subject, maxSubjectLength)
		var b strings.Builder
		b.WriteString(subject)
		b.WriteString("\n")
		if len(coAuthors) > 0 {
			b.WriteString("\n")
			for _, person := range coAuthors {
				fmt.Fprintf(&b, "%s%s <%s>\n", coAuthorTrailer, person.Name, person.Email)
			}
		}
		return b.String()
	}
}

// describePath is the label a path is grouped under in a commit message, and
// the key it is grouped by: a node's document and its pages share one.
func describePath(titles map[string]string, rel string) (label, key string) {
	if rel == "graph.yaml" {
		return "graph", "graph"
	}
	tail, ok := strings.CutPrefix(rel, "nodes/")
	if !ok {
		return rel, rel
	}
	base := path.Base(tail)
	parent := path.Base(path.Dir(tail))
	id := strings.TrimSuffix(base, ".md")
	if pagesOf, isPage := strings.CutSuffix(parent, ".pages"); isPage {
		id = pagesOf
	}
	title := titles[id]
	if title == "" {
		title = id
	}
	return singleLine(title), "node:" + id
}

// changeKind names a change from its status and what kind of file it is.
func changeKind(change gitrepo.Status) string {
	verb := map[byte]string{'A': "create", 'D': "delete"}[change.Code]
	if verb == "" {
		verb = "edit"
	}
	if change.Path == "graph.yaml" {
		return verb
	}
	if strings.HasSuffix(path.Dir(change.Path), ".pages") {
		if path.Base(change.Path) == "pages.json" {
			return "pages"
		}
		if verb == "create" {
			return "add page"
		}
		return verb + " page"
	}
	return verb
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
