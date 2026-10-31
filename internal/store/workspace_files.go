package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"nodevas/internal/locale"
)

// Workspace files for the Explorer's Files tab: the project root's files,
// read-only, with the store's own containment (no traversal, no symlink
// escape) and the secrets deny list hidden.

// Workspace walk limits.
const (
	MaxWorkspaceDepth     = 6
	MaxWorkspaceEntries   = 5000
	MaxWorkspaceFileBytes = 512 << 10
	maxWorkspacePathBytes = 1024
	workspaceSniffBytes   = 8 << 10
)

// Workspace file errors.
var (
	ErrBadPath    = locale.Errorf("bad path", "路徑無效")
	ErrHiddenFile = locale.Errorf("this file is hidden because it may hold secrets", "這個檔案可能含有機密，因此隱藏")
)

// WorkspaceEntry is one file or directory under the project root.
type WorkspaceEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	NodeID string `json:"nodeId,omitempty"`
	Title  string `json:"title,omitempty"`
}

// WorkspaceListing is the project's files.
type WorkspaceListing struct {
	Entries   []WorkspaceEntry `json:"entries"`
	Hidden    int              `json:"hidden"`
	Truncated bool             `json:"truncated"`
}

// WorkspaceFile is one file's content.
type WorkspaceFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated"`
	NodeID    string `json:"nodeId,omitempty"`
	URL       string `json:"url,omitempty"`
}

// imageExts are the files the Explorer previews as images.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// secret deny list (the runner keeps the same one for repos).
var (
	secretDirNames  = map[string]bool{".ssh": true, ".aws": true, ".gnupg": true}
	secretFileNames = map[string]bool{".npmrc": true, ".pypirc": true, ".netrc": true, ".git-credentials": true,
		"credentials.json": true, "runner.json": true}
	secretFileExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".keystore": true, ".kdbx": true}
	envExampleName = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}
)

// SecretName reports whether a file or directory named name may hold
// secrets and so is never shown.
func SecretName(name string) bool {
	n := strings.ToLower(name)
	switch {
	case secretDirNames[n], secretFileNames[n]:
		return true
	case n == ".env" || (strings.HasPrefix(n, ".env.") && !envExampleName[n]):
		return true
	case strings.HasPrefix(n, "id_rsa"), strings.HasPrefix(n, "id_ed25519"), strings.HasPrefix(n, "id_ecdsa"):
		return true
	case strings.HasPrefix(n, "secrets."):
		return true
	}
	return secretFileExts[path.Ext(n)]
}

func secretRel(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg != "" && SecretName(seg) {
			return true
		}
	}
	return false
}

// nodeTitles maps node ids to titles, for naming node files.
func (s *Store) nodeTitles() map[string]string {
	out := map[string]string{}
	g, _, err := s.LoadGraph()
	if err != nil || g == nil {
		return out
	}
	for _, n := range g.Nodes {
		out[n.ID] = n.Title
	}
	return out
}

// nodeOfFile is the node a workspace path's Markdown file is, if any.
func nodeOfFile(rel string, titles map[string]string) (string, string) {
	if !strings.HasPrefix(rel, "nodes/") || !strings.HasSuffix(rel, ".md") {
		return "", ""
	}
	id := strings.TrimSuffix(path.Base(rel), ".md")
	title, ok := titles[id]
	if !ok {
		return "", ""
	}
	return id, title
}

// WorkspaceFiles walks the project root: at most MaxWorkspaceDepth deep and
// MaxWorkspaceEntries entries, dot directories and secrets left out, and
// symlinks never followed.
func (s *Store) WorkspaceFiles() (WorkspaceListing, error) {
	out := WorkspaceListing{Entries: []WorkspaceEntry{}}
	titles := s.nodeTitles()
	root := s.root
	err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if abs == root {
			return err
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() && strings.HasPrefix(name, ".") {
			if SecretName(name) {
				out.Hidden++
			}
			return fs.SkipDir
		}
		if SecretName(name) {
			out.Hidden++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// A link is never followed: shown neither as its target nor at all.
			return nil
		}
		if len(out.Entries) >= MaxWorkspaceEntries {
			out.Truncated = true
			return fs.SkipAll
		}
		entry := WorkspaceEntry{Path: rel, Type: "file"}
		if d.IsDir() {
			entry.Type = "dir"
		} else if info, err := d.Info(); err == nil {
			entry.Size = info.Size()
			entry.NodeID, entry.Title = nodeOfFile(rel, titles)
		}
		out.Entries = append(out.Entries, entry)
		if d.IsDir() && strings.Count(rel, "/")+1 >= MaxWorkspaceDepth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return out, err
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, nil
}

// cleanWorkspacePath checks a request path: relative, inside, plain.
func cleanWorkspacePath(rel string) (string, error) {
	if rel == "" || len(rel) > maxWorkspacePathBytes || !utf8.ValidString(rel) ||
		strings.ContainsAny(rel, "\x00\\:") || strings.HasPrefix(rel, "/") {
		return "", ErrBadPath
	}
	var segs []string
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", ErrBadPath
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return "", ErrBadPath
	}
	return strings.Join(segs, "/"), nil
}

// workspaceVisible refuses a path inside a dot directory (bad path) or one
// that may hold secrets (hidden). A dot file at any level is fine.
func workspaceVisible(rel string) error {
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		if strings.HasPrefix(seg, ".") && !SecretName(seg) {
			return locale.Errorf("%w: inside a hidden directory", "%w：位於隱藏資料夾內", ErrBadPath)
		}
	}
	if secretRel(rel) {
		return ErrHiddenFile
	}
	return nil
}

// ReadWorkspaceFile reads one file under the project root.
func (s *Store) ReadWorkspaceFile(rel string) (WorkspaceFile, error) {
	clean, err := cleanWorkspacePath(rel)
	if err != nil {
		return WorkspaceFile{}, err
	}
	if err := workspaceVisible(clean); err != nil {
		return WorkspaceFile{}, err
	}
	// The name asked for may not be the file's own (a Windows short name, a
	// different case, a link inside the root): its real path is checked too.
	if realRoot, err := filepath.EvalSymlinks(s.root); err == nil {
		if real, err := filepath.EvalSymlinks(filepath.Join(s.root, filepath.FromSlash(clean))); err == nil {
			if rel, err := filepath.Rel(realRoot, real); err == nil {
				if err := workspaceVisible(filepath.ToSlash(rel)); err != nil {
					return WorkspaceFile{}, err
				}
			}
		}
	}
	file, info, err := OpenProjectFile(s.root, filepath.Join(s.root, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorkspaceFile{}, os.ErrNotExist
		}
		return WorkspaceFile{}, fmt.Errorf("%w: %v", ErrBadPath, err)
	}
	defer file.Close()
	out := WorkspaceFile{Path: clean, Size: info.Size()}
	out.NodeID, _ = nodeOfFile(clean, s.nodeTitles())
	parts := strings.Split(clean, "/")
	if imageExts[strings.ToLower(path.Ext(clean))] {
		out.Kind = "image"
		if len(parts) == 4 && parts[0] == "nodes" && parts[2] == "files" {
			out.URL = "/api/nodes/" + parts[1] + "/files/" + parts[3]
		}
		return out, nil
	}
	buf, err := io.ReadAll(io.LimitReader(file, MaxWorkspaceFileBytes+1))
	if err != nil {
		return WorkspaceFile{}, err
	}
	if len(buf) > MaxWorkspaceFileBytes {
		out.Truncated = true
		buf = buf[:MaxWorkspaceFileBytes]
	}
	if strings.IndexByte(string(buf[:min(len(buf), workspaceSniffBytes)]), 0) >= 0 {
		out.Kind = "binary"
		return out, nil
	}
	if out.Truncated {
		for cut := 0; cut < utf8.UTFMax && !utf8.Valid(buf) && len(buf) > 0; cut++ {
			buf = buf[:len(buf)-1]
		}
	}
	if !utf8.Valid(buf) {
		out.Kind = "binary"
		return out, nil
	}
	out.Kind, out.Content = "text", string(buf)
	return out, nil
}
