package project

import (
	"errors"
	"fmt"
	"io/fs"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Project creation, import, move, and removal.

const MaxImportPathFiles = 20000

// The two ways an import source overlaps the workspace. Both are refusals, but
// they mean different things to whoever picked the folder, so they stay apart.
var (
	ErrSourceContainsWorkspace = errors.New("來源資料夾包含目前工作區，不能匯入自身")
	ErrSourceInsideWorkspace   = errors.New("該資料夾已在工作區內")
	ErrPathTaken               = errors.New("路徑已存在")
	ErrInvalidName             = errors.New("名稱無效")
)

// CheckImportSource refuses a source directory that overlaps the workspace:
// one that contains it (the copy would recurse into itself) or one that
// already lives inside it (there is nothing to bring in — refresh instead).
func CheckImportSource(src, workspace string) error {
	source, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if pathContains(source, root) {
		return ErrSourceContainsWorkspace
	}
	if pathContains(root, source) {
		return ErrSourceInsideWorkspace
	}
	return nil
}

// pathContains reports whether child is outer itself or anything beneath it.
// Windows reaches one directory through several spellings — case, and the
// drive letter — so the comparison folds case there, like WorkspacePathEqual.
// filepath.Rel is deliberately not used: it fails on two different volumes
// *and* on a case-mismatched one, and the caller cannot tell those apart.
func pathContains(outer, child string) bool {
	outer = filepath.Clean(outer)
	child = filepath.Clean(child)
	if runtime.GOOS == "windows" {
		outer = strings.ToLower(outer)
		child = strings.ToLower(child)
	}
	if outer == child {
		return true
	}
	return strings.HasPrefix(child, outer+string(filepath.Separator))
}

// CreateFolder makes a plain grouping directory in the workspace — a folder
// that holds projects but is not one itself, so no Store is involved.
func (pm *ProjectManager) CreateFolder(name string) error {
	if name == "" || name == "." || !ValidProjectPath(name) {
		return fmt.Errorf("%w：%q", ErrInvalidName, name)
	}
	dir, err := pm.Resolve(name)
	if err != nil {
		return err
	}
	if _, err := store.StatProjectPath(pm.workspace, dir); err == nil {
		return fmt.Errorf("%w：%q", ErrPathTaken, name)
	} else if !os.IsNotExist(err) {
		return err
	}
	return store.MkdirAllProjectPath(pm.workspace, dir, 0o755)
}

// NewProjectStore returns the Store for a workspace path that is about to
// become a project. StoreFor refuses a directory without graph.yaml, which is
// exactly the state a fresh import starts from; the Store still has to come
// from the same cache, or its write lock guards nothing.
func (pm *ProjectManager) NewProjectStore(name string) (*store.Store, error) {
	dir, err := pm.Resolve(name)
	if err != nil {
		return nil, err
	}
	if _, err := store.StatProjectPath(pm.workspace, filepath.Join(dir, "graph.yaml")); err == nil {
		return nil, fmt.Errorf("%w：%q 已經是專案", ErrPathTaken, name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// ImportDocuments is rooted at the new project's directory. Establish that
	// root through the workspace handle before constructing the Store; an empty
	// import has no document write that could create it incidentally.
	if err := store.MkdirAllProjectPath(pm.workspace, dir, 0o755); err != nil {
		return nil, err
	}
	return pm.cachedStore(dir), nil
}

// CollectMarkdownImport reads every *.md under src, in walk order, as the
// documents of a project-to-be. It only reads: the store decides what the
// files on the other side look like.
func CollectMarkdownImport(src string) ([]store.ImportDocument, error) {
	var docs []store.ImportDocument
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := entry.Name()
		if entry.IsDir() {
			if base == ".git" || base == "node_modules" || base == store.DataDir {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 ||
			!strings.EqualFold(filepath.Ext(base), ".md") {
			return nil
		}
		if len(docs) >= MaxImportPathFiles {
			return fmt.Errorf("Markdown 檔超過 %d 個", MaxImportPathFiles)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		docs = append(docs, store.ImportDocument{
			Title: strings.TrimSuffix(base, filepath.Ext(base)),
			Body:  string(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// CopyDirTree copies src into dst (created by the call), skipping VCS/cache
// directories and symlinks. Returns the number of files copied.
func CopyDirTree(src, dst string, copied *int) error {
	return copyDirTree(src, filepath.Dir(dst), src, dst, copied)
}

func copyDirTree(srcRoot, dstRoot, src, dst string, copied *int) error {
	entries, err := store.ReadProjectDir(srcRoot, src)
	if err != nil {
		return err
	}
	if err := store.MkdirAllProjectPath(dstRoot, dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == "node_modules" || name == store.DataDir {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source contains symbolic link %q", relativeDisplayPath(srcRoot, filepath.Join(src, name)))
		}
		from := filepath.Join(src, name)
		to := filepath.Join(dst, name)
		if entry.IsDir() {
			if err := copyDirTree(srcRoot, dstRoot, from, to, copied); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source contains non-regular file %q", relativeDisplayPath(srcRoot, from))
		}
		*copied++
		if *copied > MaxImportPathFiles {
			return fmt.Errorf("來源檔案超過 %d 個，請縮小範圍", MaxImportPathFiles)
		}
		data, err := store.ReadProjectFile(srcRoot, from)
		if err != nil {
			return err
		}
		if err := store.WriteProjectFileAtomicMode(dstRoot, to, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func relativeDisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func (pm *ProjectManager) RemoveProject(name string, deleteFiles bool) (string, error) {
	if name == "." {
		return "", fmt.Errorf("the workspace root project cannot be removed")
	}
	if !ValidProjectPath(name) {
		return "", fmt.Errorf("invalid project name %q", name)
	}

	pm.importMu.Lock()
	defer pm.importMu.Unlock()
	pm.catalogMu.Lock()
	defer pm.catalogMu.Unlock()

	projects, err := pm.List()
	if err != nil {
		return "", err
	}
	var target *ProjectInfo
	active := ""
	activeRoot := pm.Store().Root()
	for index := range projects {
		project := &projects[index]
		if project.Name == name {
			target = project
		}
		if filepath.Clean(project.Path) == filepath.Clean(activeRoot) {
			active = project.Name
		}
	}
	if target == nil {
		return "", fmt.Errorf("project %q is not imported", name)
	}

	removes := func(projectName string) bool {
		return projectName == name || strings.HasPrefix(projectName, name+"/")
	}
	if removes(active) {
		replacement := ""
		for _, project := range projects {
			if !removes(project.Name) && !project.IsFolder {
				replacement = project.Name
				break
			}
		}
		if replacement == "" {
			return "", fmt.Errorf("create or import another project before removing the last project")
		}
		if err := pm.Activate(replacement, false, ""); err != nil {
			return "", fmt.Errorf("switch project before removal: %w", err)
		}
		active = replacement
	}

	detached, err := pm.loadDetachedProjects()
	if err != nil {
		return "", err
	}
	if deleteFiles {
		targetDir, err := pm.Resolve(name)
		if err != nil {
			return "", err
		}
		workspace := filepath.Clean(pm.workspace)
		targetDir = filepath.Clean(targetDir)
		rel, err := filepath.Rel(workspace, targetDir)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing to delete project outside the workspace")
		}
		info, err := store.StatProjectPath(pm.workspace, targetDir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("project target is not a regular directory")
		}
		pm.EvictStores(targetDir)
		if err := store.RemoveAllProjectPath(pm.workspace, targetDir); err != nil {
			return "", fmt.Errorf("delete project files: %w", err)
		}
		for detachedName := range detached {
			if removes(detachedName) {
				delete(detached, detachedName)
			}
		}
	} else {
		detached[name] = true
	}
	if err := pm.saveDetachedProjects(detached); err != nil {
		return "", fmt.Errorf("save detached project list: %w", err)
	}
	return active, nil
}
