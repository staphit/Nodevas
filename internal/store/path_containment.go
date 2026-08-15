package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnsafeProjectPath marks a project path that is outside its trusted root,
// crosses a symbolic link, or changes identity while it is being opened.
var ErrUnsafeProjectPath = errors.New("unsafe project path")

func projectRelativePath(root, target string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: project root: %v", ErrUnsafeProjectPath, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("%w: target: %v", ErrUnsafeProjectPath, err)
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q is outside project root", ErrUnsafeProjectPath, target)
	}
	return rel, nil
}

// validateProjectPath rejects every existing symlink component. The os.Root
// used by the caller is the containment boundary even if a hostile local
// process races this validation; this check adds fail-closed semantics for
// links that stay within the root too.
func validateProjectPath(root *os.Root, rel string, allowMissing bool) error {
	if rel == "." {
		return nil
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	current := ""
	for index, part := range parts {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symbolic link", ErrUnsafeProjectPath, current)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("%w: %q is not a directory", ErrUnsafeProjectPath, current)
		}
	}
	return nil
}

func openProjectRoot(root string) (*os.Root, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	info, err := opened.Stat(".")
	if err != nil || !info.IsDir() {
		opened.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: project root is not a directory", ErrUnsafeProjectPath)
	}
	return opened, nil
}

// syncProjectDirectory makes a completed directory-entry mutation durable.
// Windows does not expose a portable directory fsync through os.File; file
// data is still synced before rename there, and the directory sync remains a
// best-effort platform limitation rather than making every write fail.
func syncProjectDirectory(root *os.Root, rel string) error {
	if err := validateProjectPath(root, rel, false); err != nil {
		return err
	}
	dir, err := root.Open(rel)
	if err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrUnsafeProjectPath, rel)
	}
	if err := dir.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}

// ValidateProjectPath checks lexical containment and rejects every existing
// symlink component. allowMissing is for creation paths: all existing
// ancestors are still checked before the first absent component.
func ValidateProjectPath(rootPath, path string, allowMissing bool) error {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return validateProjectPath(root, rel, allowMissing)
}

func openProjectFile(rootPath, path string) (*os.Root, *os.File, fs.FileInfo, string, error) {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return nil, nil, nil, "", err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	fail := func(err error) (*os.Root, *os.File, fs.FileInfo, string, error) {
		root.Close()
		return nil, nil, nil, "", err
	}
	if err := validateProjectPath(root, rel, false); err != nil {
		return fail(err)
	}
	file, err := root.Open(rel)
	if err != nil {
		return fail(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fail(err)
	}
	pathInfo, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		file.Close()
		if err != nil {
			return fail(err)
		}
		return fail(fmt.Errorf("%w: %q is not a stable regular file", ErrUnsafeProjectPath, rel))
	}
	return root, file, info, rel, nil
}

// OpenProjectFile opens an existing regular file beneath root without ever
// following a symlink outside it. The caller owns the returned file.
func OpenProjectFile(root, path string) (*os.File, fs.FileInfo, error) {
	openedRoot, file, info, _, err := openProjectFile(root, path)
	if err != nil {
		return nil, nil, err
	}
	// The file descriptor remains safe after its root handle closes: it names
	// the already-opened file, not a path that can be redirected later.
	if err := openedRoot.Close(); err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

// ReadProjectFile reads one existing regular file beneath root, capped at the
// largest managed project file (an attachment). Missing is a real
// os.ErrNotExist error; it is never treated as an empty file.
func ReadProjectFile(rootPath, path string) ([]byte, error) {
	return ReadProjectFileLimit(rootPath, path, MaxAttachmentBytes)
}

// ReadProjectFileLimit is ReadProjectFile with a caller-specific byte ceiling.
// maxBytes is checked from the open descriptor before allocation and enforced
// again by a bounded read, so a changing file cannot turn the check into an
// allocation denial of service.
func ReadProjectFileLimit(rootPath, path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("invalid project file limit %d", maxBytes)
	}
	root, file, before, rel, err := openProjectFile(rootPath, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	defer file.Close()
	if before.Size() < 0 || before.Size() > maxBytes {
		return nil, fmt.Errorf("project file %q exceeds %d bytes", rel, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("project file %q exceeds %d bytes", rel, maxBytes)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathAfter, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if pathAfter.Mode()&os.ModeSymlink != 0 || !pathAfter.Mode().IsRegular() ||
		!os.SameFile(before, after) || !os.SameFile(before, pathAfter) ||
		before.Size() != after.Size() || int64(len(data)) != before.Size() {
		return nil, fmt.Errorf("%w: %q changed while reading", ErrUnsafeProjectPath, rel)
	}
	return data, nil
}

func (s *Store) ReadFile(path string) ([]byte, error) {
	return ReadProjectFile(s.root, path)
}

func readProjectDir(rootPath, path string) ([]os.DirEntry, error) {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return nil, err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, false); err != nil {
		return nil, err
	}
	dir, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %q is not a directory", ErrUnsafeProjectPath, rel)
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %q is a symbolic link", ErrUnsafeProjectPath, filepath.Join(rel, entry.Name()))
		}
	}
	pathAfter, err := root.Lstat(rel)
	if err != nil || pathAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathAfter) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: directory %q changed while reading", ErrUnsafeProjectPath, rel)
	}
	return entries, nil
}

// ReadProjectDir lists a contained non-symlink directory. Entries are not
// silently filtered; callers that only accept regular files must reject an
// entry whose type is a symlink or another special file.
func ReadProjectDir(rootPath, path string) ([]os.DirEntry, error) {
	return readProjectDir(rootPath, path)
}

func (s *Store) ReadDir(path string) ([]os.DirEntry, error) {
	return readProjectDir(s.root, path)
}

// ValidateProjectTree rejects every symlink below path without following it.
// WalkDir is backed by os.Root, so even a concurrent directory replacement
// cannot redirect the walk beyond root on Linux or macOS.
func ValidateProjectTree(rootPath, path string) error {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, false); err != nil {
		return err
	}
	return fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symbolic link", ErrUnsafeProjectPath, current)
		}
		return nil
	})
}

// StatProjectPath returns lstat-style information for a contained path after
// rejecting symlinks in every component, including the leaf.
func StatProjectPath(rootPath, path string) (fs.FileInfo, error) {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return nil, err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, false); err != nil {
		return nil, err
	}
	return root.Lstat(rel)
}

func (s *Store) statPath(path string) (fs.FileInfo, error) {
	return StatProjectPath(s.root, path)
}

func createProjectFileExclusive(rootPath, path string, perm os.FileMode) (*os.File, error) {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return nil, err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, true); err != nil {
		return nil, err
	}
	parent := filepath.Dir(rel)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	if err := validateProjectPath(root, parent, false); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	pathInfo, err := root.Lstat(rel)
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %q is not a stable regular file", ErrUnsafeProjectPath, rel)
	}
	return file, nil
}

// openProjectFileAppend opens a contained regular file for append, creating it
// when absent. The returned descriptor remains confined after the root handle
// closes; callers must close it.
func openProjectFileAppend(rootPath, path string, perm os.FileMode) (*os.File, error) {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return nil, err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, true); err != nil {
		return nil, err
	}
	parent := filepath.Dir(rel)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	if err := validateProjectPath(root, parent, false); err != nil {
		return nil, err
	}
	file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_APPEND, perm)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	pathInfo, err := root.Lstat(rel)
	if err != nil || !opened.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %q is not a stable regular file", ErrUnsafeProjectPath, rel)
	}
	return file, nil
}

// MkdirAllProjectPath creates a directory beneath root while refusing any
// existing symlink component. os.Root keeps a concurrent replacement from
// redirecting creation outside the project.
func MkdirAllProjectPath(rootPath, path string, perm os.FileMode) error {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, true); err != nil {
		return err
	}
	if err := root.MkdirAll(rel, perm); err != nil {
		return err
	}
	return validateProjectPath(root, rel, false)
}

// MkdirTempProjectPath creates a private random directory beneath an existing
// contained directory. Unlike os.MkdirTemp with an absolute parent, the final
// creation is relative to an open root and cannot be redirected by a symlink
// swap in the workspace.
func MkdirTempProjectPath(rootPath, dir, prefix string, perm os.FileMode) (string, error) {
	if prefix != filepath.Base(prefix) {
		return "", fmt.Errorf("%w: invalid temporary directory prefix", ErrUnsafeProjectPath)
	}
	rel, err := projectRelativePath(rootPath, dir)
	if err != nil {
		return "", err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, true); err != nil {
		return "", err
	}
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return "", err
	}
	if err := validateProjectPath(root, rel, false); err != nil {
		return "", err
	}
	for attempt := 0; attempt < 100; attempt++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("create temporary directory name: %w", err)
		}
		child := filepath.Join(rel, prefix+hex.EncodeToString(random[:]))
		if err := root.Mkdir(child, perm); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		if err := validateProjectPath(root, child, false); err != nil {
			_ = root.Remove(child)
			return "", err
		}
		return filepath.Join(rootPath, child), nil
	}
	return "", fmt.Errorf("create temporary directory: exhausted random names")
}

// RemoveProjectPath removes one existing non-symlink path beneath root.
func RemoveProjectPath(rootPath, path string) error {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, false); err != nil {
		return err
	}
	if err := root.Remove(rel); err != nil {
		return err
	}
	return syncProjectDirectory(root, filepath.Dir(rel))
}

// RemoveAllProjectPath recursively removes a contained tree only after a
// rooted walk proves that it contains no symbolic links.
func RemoveAllProjectPath(rootPath, path string) error {
	if err := ValidateProjectTree(rootPath, path); err != nil {
		return err
	}
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("%w: refusing to remove project root", ErrUnsafeProjectPath)
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.RemoveAll(rel); err != nil {
		return err
	}
	return syncProjectDirectory(root, filepath.Dir(rel))
}

// RenameProjectPath moves one symlink-free project subtree between trusted
// roots. Same-root moves use os.Root.Rename; cross-root moves retain os.Rename
// semantics after both sides have been independently confined.
func RenameProjectPath(sourceRoot, source, targetRoot, target string) error {
	if err := ValidateProjectTree(sourceRoot, source); err != nil {
		return err
	}
	sourceRel, err := projectRelativePath(sourceRoot, source)
	if err != nil {
		return err
	}
	targetRel, err := projectRelativePath(targetRoot, target)
	if err != nil {
		return err
	}
	targetHandle, err := openProjectRoot(targetRoot)
	if err != nil {
		return err
	}
	if err := validateProjectPath(targetHandle, filepath.Dir(targetRel), false); err != nil {
		targetHandle.Close()
		return err
	}
	sourceAbs, sourceErr := filepath.Abs(sourceRoot)
	targetAbs, targetErr := filepath.Abs(targetRoot)
	sameRoot := sourceErr == nil && targetErr == nil && filepath.Clean(sourceAbs) == filepath.Clean(targetAbs)
	caseOnlyRename := sameRoot && sourceRel != targetRel && strings.EqualFold(sourceRel, targetRel)
	if _, err := targetHandle.Lstat(targetRel); err == nil && !caseOnlyRename {
		targetHandle.Close()
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		if !(caseOnlyRename && err == nil) {
			targetHandle.Close()
			return err
		}
	}

	if sameRoot {
		defer targetHandle.Close()
		if err := targetHandle.Rename(sourceRel, targetRel); err != nil {
			return err
		}
		sourceParent := filepath.Dir(sourceRel)
		targetParent := filepath.Dir(targetRel)
		if err := syncProjectDirectory(targetHandle, sourceParent); err != nil {
			return err
		}
		if targetParent != sourceParent {
			return syncProjectDirectory(targetHandle, targetParent)
		}
		return nil
	}
	defer targetHandle.Close()
	if err := os.Rename(source, target); err != nil {
		return err
	}
	sourceHandle, err := openProjectRoot(sourceRoot)
	if err != nil {
		return err
	}
	defer sourceHandle.Close()
	return errors.Join(
		syncProjectDirectory(sourceHandle, filepath.Dir(sourceRel)),
		syncProjectDirectory(targetHandle, filepath.Dir(targetRel)),
	)
}

func (s *Store) removePath(path string) error {
	return RemoveProjectPath(s.root, path)
}

func writeProjectFileAtomic(rootPath, path string, data []byte, perm os.FileMode, markWrite func(string)) error {
	rel, err := projectRelativePath(rootPath, path)
	if err != nil {
		return err
	}
	root, err := openProjectRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := validateProjectPath(root, rel, true); err != nil {
		return err
	}
	parent := filepath.Dir(rel)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if err := validateProjectPath(root, parent, false); err != nil {
		return err
	}
	if err := validateProjectPath(root, rel, true); err != nil {
		return err
	}

	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("create temporary filename: %w", err)
	}
	tmpRel := filepath.Join(parent, TmpPrefix+hex.EncodeToString(random[:])+"-"+filepath.Base(rel))
	file, err := root.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	discard := func(cause error) error {
		file.Close()
		_ = root.Remove(tmpRel)
		return cause
	}
	if _, err := file.Write(data); err != nil {
		return discard(err)
	}
	if err := file.Sync(); err != nil {
		return discard(err)
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(tmpRel)
		return err
	}
	if err := validateProjectPath(root, parent, false); err != nil {
		_ = root.Remove(tmpRel)
		return err
	}
	if err := validateProjectPath(root, rel, true); err != nil {
		_ = root.Remove(tmpRel)
		return err
	}
	if markWrite != nil {
		markWrite(filepath.Join(rootPath, tmpRel))
		markWrite(filepath.Join(rootPath, rel))
	}
	if err := root.Rename(tmpRel, rel); err != nil {
		_ = root.Remove(tmpRel)
		return err
	}
	return syncProjectDirectory(root, parent)
}

// WriteProjectFileAtomic atomically replaces a regular project file without
// following symlink components. Missing parent directories are created safely.
func WriteProjectFileAtomic(rootPath, path string, data []byte) error {
	return WriteProjectFileAtomicMode(rootPath, path, data, 0o644)
}

// WriteProjectFileAtomicMode is WriteProjectFileAtomic with an explicit mode
// for newly created files. Existing files keep atomic-replacement semantics.
func WriteProjectFileAtomicMode(rootPath, path string, data []byte, perm os.FileMode) error {
	return writeProjectFileAtomic(rootPath, path, data, perm, nil)
}
