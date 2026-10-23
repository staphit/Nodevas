package agentsetup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// backupSuffix names the one copy of a config file taken before nodevas
// first changes it.
const backupSuffix = ".bak-nodevas"

// readFile returns a file's bytes and whether it exists.
func readFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// writeFile replaces path with content if it differs, and reports whether
// it did (or, dry, would). The original is backed up once — not when it was
// already wired by nodevas, since that copy is not the person's original —
// and the new text is written beside it and renamed over, so an agent never
// reads a half-written config. The file keeps its permissions: a config
// holding tokens may be 0600.
func (s *setup) writeFile(path string, content []byte) (bool, error) {
	old, existed, err := readFile(path)
	if err != nil {
		return false, err
	}
	if existed && bytes.Equal(old, content) {
		return false, nil
	}
	if s.opts.DryRun {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	backup := path + backupSuffix
	if existed && !mentionsExe(string(old), s.env.Exe) {
		if _, err := os.Stat(backup); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(backup, old, 0o600); err != nil {
				return false, err
			}
			s.backups = append(s.backups, backup)
		}
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".tmp-nodevas"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return false, err
	}
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// writeJSON writes doc to path, except that a removal from a file that never
// existed does not create an empty one.
func (s *setup) writeJSON(path string, doc *object, existed bool) (bool, error) {
	if !existed && hollow(doc) {
		return false, nil
	}
	return s.writeFile(path, encodeDoc(doc))
}

// readJSON loads a settings file as an ordered object.
func readJSON(path string) (*object, bool, error) {
	raw, existed, err := readFile(path)
	if err != nil {
		return nil, false, err
	}
	doc, err := parseDoc(raw)
	if err != nil {
		return nil, existed, &fs.PathError{Op: "parse", Path: path, Err: err}
	}
	return doc, existed, nil
}

// tilde shortens a path under home to `~/…` for the report.
func tilde(home, path string) string {
	if home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}
