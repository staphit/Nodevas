// Commits built from something other than "stage the work tree": a fixed set
// of tracked files, content held in memory, and a stream of backdated
// imports. Version history needs all three (internal/store/history_git.go).

package gitrepo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/locale"
)

// Status is one path a commit changes: 'A' added, 'M' modified, 'D' deleted.
type Status struct {
	Code byte
	Path string
}

// Message writes a commit message from what the commit changes. Messages are
// built from paths, never from content, so the caller gets only the paths.
type Message func(changes []Status) string

// Change is one file of a CommitChanges: path's content as given.
type Change struct {
	Path string
	Data []byte
}

// CommitTracked commits the work tree's current state of exactly paths: each
// one as it is on disk, and every previously committed path not in the list
// removed. paths are relative to the work tree and must exist; a path that
// vanished since the caller listed it is dropped like any other deletion.
// It returns "" with no error when the tree is unchanged.
//
// The caller decides what is history and what is not (attachments, say), so
// nothing here walks the work tree or reads the exclude list.
func (r *Repo) CommitTracked(ctx context.Context, paths []string, message Message, author Person, at time.Time) (string, error) {
	want := make(map[string]bool, len(paths))
	for _, path := range paths {
		want[filepath.ToSlash(path)] = true
	}
	out, err := r.run(ctx, nil, nil, "ls-files", "-z")
	if err != nil {
		return "", err
	}
	var stale []string
	for _, path := range splitNUL(out) {
		if !want[path] {
			stale = append(stale, path)
		}
	}
	if len(stale) > 0 {
		if _, err := r.run(ctx, joinNUL(stale), nil, "update-index", "-z", "--force-remove", "--stdin"); err != nil {
			return "", err
		}
	}
	if len(want) > 0 {
		list := make([]string, 0, len(want))
		for path := range want {
			list = append(list, path)
		}
		sort.Strings(list)
		// --remove: a file deleted between the caller's listing and now leaves
		// the index rather than failing the commit.
		if _, err := r.run(ctx, joinNUL(list), nil, "update-index", "-z", "--add", "--remove", "--stdin"); err != nil {
			return "", err
		}
	}
	return r.commitIndex(ctx, nil, message, author, at)
}

// CommitChanges commits files with the given content on top of HEAD, without
// touching the work tree or its index. It is for versions that are no longer
// on disk: the content an outside edit replaced, or an imported copy.
// It returns "" with no error when the content is what HEAD already has.
func (r *Repo) CommitChanges(ctx context.Context, changes []Change, message Message, author Person, at time.Time) (string, error) {
	if len(changes) == 0 {
		return "", nil
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	index := filepath.Join(r.gitDir, "nodevas-index-"+hex.EncodeToString(token))
	defer os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index}
	if r.hasHead(ctx) {
		if _, err := r.run(ctx, nil, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	} else if _, err := r.run(ctx, nil, env, "read-tree", "--empty"); err != nil {
		return "", err
	}
	var info bytes.Buffer
	for _, change := range changes {
		path, err := cleanPath(change.Path)
		if err != nil {
			return "", err
		}
		// No --path: the bytes are stored exactly, with no filter applied.
		blob, err := r.run(ctx, change.Data, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s\x00", strings.TrimSpace(string(blob)), path)
	}
	if _, err := r.run(ctx, info.Bytes(), env, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	commit, err := r.commitIndex(ctx, env, message, author, at)
	if err != nil || commit == "" {
		return commit, err
	}
	// The work tree's own index follows HEAD to these files, or `git status`
	// reports a file this commit added as deleted (and the same file on disk
	// as untracked) until the next CommitTracked. The entries carry no stat
	// data, so status compares their content with the work tree. Only status
	// reads the index between commits; a failure here costs it a wrong line
	// until then, not the commit.
	_, _ = r.run(ctx, info.Bytes(), nil, "update-index", "-z", "--index-info")
	return commit, nil
}

// CommitSnapshot is CommitTracked for a state the caller read earlier: files
// with the content given, each path in keep as HEAD has it (left out if HEAD
// has none), and every other path removed. The work tree may have moved on
// since, and what it holds now is not taken. The work tree's index follows
// the commit, as after CommitChanges. It returns "" with no error when the
// tree is unchanged.
func (r *Repo) CommitSnapshot(ctx context.Context, files []Change, keep []string, message Message, author Person, at time.Time) (string, error) {
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	index := filepath.Join(r.gitDir, "nodevas-index-"+hex.EncodeToString(token))
	defer os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := r.run(ctx, nil, env, "read-tree", "--empty"); err != nil {
		return "", err
	}
	held := make(map[string]bool, len(keep))
	var info bytes.Buffer
	if len(keep) > 0 {
		list := make([]string, 0, len(keep))
		for _, path := range keep {
			path, err := cleanPath(path)
			if err != nil {
				return "", err
			}
			if !held[path] {
				held[path] = true
				list = append(list, path)
			}
		}
		if r.hasHead(ctx) {
			// ls-tree's lines are a form --index-info reads as they are.
			entries, err := r.run(ctx, nil, []string{"GIT_LITERAL_PATHSPECS=1"},
				append([]string{"ls-tree", "-z", "--full-tree", "HEAD", "--"}, list...)...)
			if err != nil {
				return "", err
			}
			info.Write(entries)
		}
	}
	var taken []Change
	for _, file := range files {
		path, err := cleanPath(file.Path)
		if err != nil {
			return "", err
		}
		if !held[path] {
			taken = append(taken, Change{Path: path, Data: file.Data})
		}
	}
	blobs, err := r.writeBlobs(ctx, taken)
	if err != nil {
		return "", err
	}
	for i, file := range taken {
		fmt.Fprintf(&info, "100644 %s\t%s\x00", blobs[i], file.Path)
	}
	if info.Len() > 0 {
		if _, err := r.run(ctx, info.Bytes(), env, "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	commit, err := r.commitIndex(ctx, env, message, author, at)
	if err != nil || commit == "" {
		return commit, err
	}
	// As after CommitChanges, best effort: the entries carry no stat data,
	// so status compares content until the next CommitTracked.
	_, _ = r.run(ctx, nil, nil, "read-tree", "HEAD")
	return commit, nil
}

// writeBlobs stores each file's content as a blob, in one git process
// however many there are, and returns their IDs in order. The bytes are
// stored exactly, with no filter applied.
func (r *Repo) writeBlobs(ctx context.Context, files []Change) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	marks := filepath.Join(r.gitDir, "nodevas-marks-"+hex.EncodeToString(token))
	defer os.Remove(marks)
	var stream bytes.Buffer
	for i, file := range files {
		fmt.Fprintf(&stream, "blob\nmark :%d\ndata %d\n", i+1, len(file.Data))
		stream.Write(file.Data)
		stream.WriteString("\n")
	}
	stream.WriteString("done\n")
	if _, err := r.run(ctx, stream.Bytes(), nil, "fast-import", "--quiet", "--done", "--export-marks="+filepath.ToSlash(marks)); err != nil {
		return nil, err
	}
	out, err := os.ReadFile(marks)
	if err != nil {
		return nil, err
	}
	blobs := make([]string, len(files))
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		mark, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		number, err := strconv.Atoi(strings.TrimPrefix(mark, ":"))
		if !ok || err != nil || number < 1 || number > len(files) || !ValidSHA(sha) {
			return nil, locale.Errorf("unexpected blob mark %q", "無法辨識的 blob 標記「%s」", line)
		}
		blobs[number-1] = sha
	}
	for i, blob := range blobs {
		if blob == "" {
			return nil, locale.Errorf("no blob written for %s", "未寫入 %s 的 blob", files[i].Path)
		}
	}
	return blobs, nil
}

// commitIndex commits the index env points at (the repository's own when env
// is nil) as a child of HEAD. Plumbing rather than `git commit`, so the same
// path serves an index that is not the work tree's.
func (r *Repo) commitIndex(ctx context.Context, env []string, message Message, author Person, at time.Time) (string, error) {
	head := ""
	if r.hasHead(ctx) {
		out, err := r.run(ctx, nil, nil, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return "", err
		}
		head = strings.TrimSpace(string(out))
	}
	var changes []Status
	if head == "" {
		out, err := r.run(ctx, nil, env, "ls-files", "-z")
		if err != nil {
			return "", err
		}
		for _, path := range splitNUL(out) {
			changes = append(changes, Status{Code: 'A', Path: path})
		}
	} else {
		out, err := r.run(ctx, nil, env, "diff-index", "--cached", "--no-renames", "--name-status", "-z", head)
		if err != nil {
			return "", err
		}
		fields := splitNUL(out)
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == "" {
				continue
			}
			changes = append(changes, Status{Code: fields[i][0], Path: fields[i+1]})
		}
	}
	if len(changes) == 0 {
		return "", nil
	}
	treeOut, err := r.run(ctx, nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(treeOut))
	args := []string{"commit-tree", tree}
	if head != "" {
		args = append(args, "-p", head)
	}
	args = append(args, "-F", "-")
	commitOut, err := r.run(ctx, []byte(message(changes)), commitEnv(author, at), args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(commitOut))
	// The old value makes this a compare-and-swap: a ref that moved under us
	// fails the update instead of dropping someone else's commit.
	if _, err := r.run(ctx, nil, nil, "update-ref", "-m", "nodevas", "HEAD", commit, head); err != nil {
		return "", err
	}
	return commit, nil
}

func commitEnv(author Person, at time.Time) []string {
	if author.Name == "" {
		author = Committer
	}
	if author.Email == "" {
		author.Email = Committer.Email
	}
	date := at.UTC().Format(time.RFC3339)
	return []string{
		"GIT_AUTHOR_NAME=" + author.Name,
		"GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + Committer.Name,
		"GIT_COMMITTER_EMAIL=" + Committer.Email,
		"GIT_COMMITTER_DATE=" + date,
	}
}

// Import is one backdated commit of an Import run: Path set to Data.
type Import struct {
	Path    string
	Data    []byte
	Message string
	Author  Person
	At      time.Time
}

// Import appends commits to HEAD's branch, in order, in one git process.
// The branch moves once, at the end, and only if every commit was written:
// a run that fails or is killed midway leaves the branch where it was.
func (r *Repo) Import(ctx context.Context, commits []Import) error {
	if len(commits) == 0 {
		return nil
	}
	refOut, err := r.run(ctx, nil, nil, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(string(refOut))
	var stream bytes.Buffer
	for i, commit := range commits {
		path, err := cleanPath(commit.Path)
		if err != nil {
			return err
		}
		author := commit.Author
		if author.Name == "" {
			author = Committer
		}
		if author.Email == "" {
			author.Email = Committer.Email
		}
		when := strconv.FormatInt(commit.At.Unix(), 10) + " +0000"
		fmt.Fprintf(&stream, "commit %s\n", ref)
		fmt.Fprintf(&stream, "author %s %s\n", identLine(author), when)
		fmt.Fprintf(&stream, "committer %s %s\n", identLine(Committer), when)
		fmt.Fprintf(&stream, "data %d\n%s\n", len(commit.Message), commit.Message)
		if i == 0 && r.hasHead(ctx) {
			stream.WriteString("from " + ref + "^0\n")
		}
		fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n", path, len(commit.Data))
		stream.Write(commit.Data)
		stream.WriteString("\n\n")
	}
	// --done: a stream cut short (the hub killed mid-write) is an error, not
	// a shorter import.
	stream.WriteString("done\n")
	_, err = r.run(ctx, stream.Bytes(), nil, "fast-import", "--quiet", "--done")
	return err
}

// identLine is "Name <email>" with the characters git's ident format cannot
// hold removed.
func identLine(p Person) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '<' || r == '>' || r == '\n' || r == '\r' || r == 0 {
				return -1
			}
			return r
		}, s)
	}
	return strings.TrimSpace(clean(p.Name)) + " <" + strings.TrimSpace(clean(p.Email)) + ">"
}

// cleanPath refuses what a fast-import line or an index entry cannot carry
// as-is, and anything that is not a plain relative path.
func cleanPath(path string) (string, error) {
	path = filepath.ToSlash(path)
	if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\"") ||
		strings.ContainsAny(path, "\n\r\x00\\") {
		return "", locale.Errorf("unsupported path %q", "不支援的路徑「%s」", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", locale.Errorf("unsupported path %q", "不支援的路徑「%s」", path)
		}
	}
	return path, nil
}

// Version is one commit in a file's history, following renames.
type Version struct {
	Commit
	// Path is the file's name in that commit: a node moved into a folder
	// was somewhere else before.
	Path string
	Size int64
}

const recordSep = "\x1e"

// History is path's versions, newest first, at most limit (all when limit
// <= 0), following the file across renames. A commit that deleted the file
// has no content and is left out.
func (r *Repo) History(ctx context.Context, path string, limit int) ([]Version, error) {
	if !r.hasHead(ctx) {
		return []Version{}, nil
	}
	args := []string{"log", "--follow", "--name-only",
		"--format=" + recordSep + "%H" + logSep + "%aI" + logSep + "%an" + logSep + "%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}
	args = append(args, "--", filepath.ToSlash(path))
	out, err := r.run(ctx, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	versions := []Version{}
	for _, record := range strings.Split(string(out), recordSep) {
		header, names, _ := strings.Cut(record, "\n")
		parts := strings.SplitN(header, logSep, 4)
		if len(parts) != 4 {
			continue
		}
		name := ""
		for _, line := range strings.Split(names, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				name = line
			}
		}
		if name == "" {
			name = filepath.ToSlash(path)
		}
		at, _ := time.Parse(time.RFC3339, parts[1])
		versions = append(versions, Version{
			Commit: Commit{SHA: parts[0], At: at, Author: parts[2], Subject: parts[3]},
			Path:   name,
		})
	}
	if len(versions) == 0 {
		return versions, nil
	}
	// One process for every size: "<commit>:<path>" names the blob.
	var query bytes.Buffer
	for _, version := range versions {
		query.WriteString(version.SHA + ":" + version.Path + "\n")
	}
	sizes, err := r.run(ctx, query.Bytes(), nil, "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(sizes), "\n"), "\n")
	present := versions[:0]
	for i, version := range versions {
		if i >= len(lines) {
			break
		}
		size, err := strconv.ParseInt(strings.TrimSpace(lines[i]), 10, 64)
		if err != nil {
			continue // "<name> missing": the commit removed the file
		}
		version.Size = size
		present = append(present, version)
	}
	return present, nil
}

// LastMessageContaining is the full message of the newest commit whose
// message contains text, or "" when none does.
func (r *Repo) LastMessageContaining(ctx context.Context, text string) (string, error) {
	if !r.hasHead(ctx) {
		return "", nil
	}
	out, err := r.run(ctx, nil, nil, "log", "-n1", "--fixed-strings", "--grep="+text, "--format=%B")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func splitNUL(out []byte) []string {
	var fields []string
	for _, field := range strings.Split(string(out), "\x00") {
		if field != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

func joinNUL(values []string) []byte {
	var buf bytes.Buffer
	for _, value := range values {
		buf.WriteString(value)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}
