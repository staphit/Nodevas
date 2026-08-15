// Reading and writing project archives: the .veproj bundle format and importing one back into the workspace.

package project

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

const (
	ProjectArchiveFormat       = "nodevas-project"
	ProjectArchiveVersion      = 2
	MaxProjectArchiveBytes     = 64 << 20
	maxProjectArchiveFiles     = 10_000
	maxProjectArchiveFileBytes = 16 << 20
	maxProjectExtractedBytes   = 128 << 20
)

type ProjectArchiveManifest struct {
	Format       string `json:"format"`
	Version      int    `json:"version"`
	GraphVersion int    `json:"graphVersion,omitempty"`
	Name         string `json:"name,omitempty"`
}

func (pm *ProjectManager) ActiveProject() (ProjectInfo, error) {
	root := pm.Store().Root()
	projects, err := pm.List()
	if err != nil {
		return ProjectInfo{}, err
	}
	for _, project := range projects {
		if project.Path == root {
			return project, nil
		}
	}
	return ProjectInfo{}, fmt.Errorf("active project is not in the workspace")
}

func addArchiveFile(writer *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	}
	header.SetMode(0o644)
	header.Modified = time.Now()
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

// archiveWriter caps how much a single archive may hold, so that one huge
// project cannot blow up memory or disk on the importing side.
type archiveWriter struct {
	writer *zip.Writer
	files  int
	bytes  int64
}

func (a *archiveWriter) add(name string, data []byte) error {
	if a.files >= maxProjectArchiveFiles {
		return fmt.Errorf("project has too many files to export")
	}
	if int64(len(data)) > maxProjectArchiveFileBytes {
		return fmt.Errorf("project file %q is too large", name)
	}
	if a.bytes+int64(len(data)) > maxProjectExtractedBytes {
		return fmt.Errorf("project exceeds the archive size limit")
	}
	a.files++
	a.bytes += int64(len(data))
	return addArchiveFile(a.writer, name, data)
}

// BuildProjectArchive writes a single project as a .veproj.
//
// shared is the workspace's custom lifecycle vocabulary, normally
// ExportScope's SharedStatuses. The definitions this project uses are folded
// into the archived graph.yaml, because that file is stripped of them on disk
// and the archive is all the destination workspace gets. Nil is valid and
// means "no shared vocabulary" — correct for a caller that has none, and
// correct for a test that does not care.
func BuildProjectArchive(
	destination io.Writer, project ProjectInfo, shared []engine.StatusDefinition,
) error {
	writer := zip.NewWriter(destination)
	archive := &archiveWriter{writer: writer}
	failed := true
	defer func() {
		if failed {
			_ = writer.Close()
		}
	}()

	manifest, err := json.MarshalIndent(ProjectArchiveManifest{
		Format:       ProjectArchiveFormat,
		Version:      ProjectArchiveVersion,
		GraphVersion: 1,
		Name:         project.Name,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := archive.add("manifest.json", append(manifest, '\n')); err != nil {
		return err
	}
	if err := writeProjectFiles(archive, "", project, shared); err != nil {
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

// usedSharedStatuses picks the workspace-shared lifecycle states this project
// refers to but does not define itself.
//
// Two places refer to one: planned milestones in graph.yaml, and the actual
// transitions recorded under run/. Those recordings are matched as text rather
// than parsed — every candidate id comes from the shared list, so a substring
// hit cannot invent one, and every entry shape past and future is covered
// without this having to know any of them.
func usedSharedStatuses(
	projectPath string,
	graph *engine.Graph,
	shared []engine.StatusDefinition,
) []engine.StatusDefinition {
	if len(shared) == 0 {
		return nil
	}
	defined := map[string]bool{}
	if graph.UI != nil {
		for _, definition := range graph.UI.CustomStatuses {
			defined[definition.ID] = true
		}
	}
	used := map[string]bool{}
	if graph.UI != nil {
		for _, milestones := range graph.UI.Plans {
			for _, milestone := range milestones {
				used[string(milestone.Status)] = true
			}
		}
	}
	// Read once, not per definition. Both files are small next to the node
	// documents the archive already copies.
	var recorded []byte
	for _, name := range []string{"journal.jsonl", "state.json"} {
		data, err := store.ReadProjectFile(projectPath, filepath.Join(projectPath, "run", name))
		if err == nil {
			recorded = append(recorded, data...)
		}
	}
	var out []engine.StatusDefinition
	for _, definition := range shared {
		if defined[definition.ID] {
			continue
		}
		if used[definition.ID] ||
			(len(recorded) > 0 && bytes.Contains(recorded, []byte(`"`+definition.ID+`"`))) {
			out = append(out, definition)
		}
	}
	return out
}

// writeProjectFiles copies one project's graph, nodes, sub-pages, attachments
// and run journal into the archive under prefix ("" for a single-project
// archive, the project's relative path inside a bundle).
func writeProjectFiles(
	archive *archiveWriter,
	prefix string,
	project ProjectInfo,
	shared []engine.StatusDefinition,
) error {
	add := func(name string, data []byte) error {
		return archive.add(path.Join(prefix, name), data)
	}

	graphData, err := store.ReadProjectFile(project.Path, filepath.Join(project.Path, "graph.yaml"))
	if err != nil {
		return fmt.Errorf("read graph.yaml: %w", err)
	}
	graph, err := engine.ParseGraph(graphData)
	if err != nil {
		return fmt.Errorf("parse graph.yaml: %w", err)
	}
	// Custom lifecycle states shared across the workspace are stripped from
	// graph.yaml on write — they live once in <workspace>/.vised/workflow.json
	// — so the file on disk names states it does not define. That is right for
	// a project sitting in its workspace and wrong for one leaving it: the
	// archive is all the destination gets, and a node whose status has no
	// definition there imports with its state unresolvable.
	//
	// So the ones this project actually uses travel with it, as its own
	// definitions. Only the ones it uses: copying the whole workspace palette
	// would turn every import into 200 project-local overrides that then never
	// track the shared list again.
	if used := usedSharedStatuses(project.Path, graph, shared); len(used) > 0 {
		merged := *graph
		ui := engine.UIState{}
		if graph.UI != nil {
			ui = *graph.UI
		}
		ui.CustomStatuses = append(append([]engine.StatusDefinition(nil), ui.CustomStatuses...), used...)
		merged.UI = &ui
		graphData, err = engine.MarshalGraph(&merged)
		if err != nil {
			return fmt.Errorf("write graph.yaml: %w", err)
		}
		graph = &merged
	}
	if err := add("graph.yaml", graphData); err != nil {
		return err
	}

	nodesDir := filepath.Join(project.Path, "nodes")
	if err := store.ValidateProjectTree(project.Path, nodesDir); err != nil {
		return fmt.Errorf("validate project nodes: %w", err)
	}
	// Node documents can sit in folders, and the archive keeps that layout so
	// an exported project comes back organised the way it left.
	folderOf := store.NodeFolderAssignments(project.Path)
	if err := store.WalkNodeDocuments(project.Path, func(id, folder, absolute string) error {
		data, readErr := store.ReadProjectFile(project.Path, absolute)
		if readErr != nil {
			return fmt.Errorf("read node %q: %w", id, readErr)
		}
		return add(path.Join("nodes", folder, id+".md"), data)
	}); err != nil {
		return err
	}
	for _, node := range graph.Nodes {
		if node == nil || !engine.ValidNodeID(node.ID) {
			continue
		}
		folder := folderOf[node.ID]
		pageDirName := path.Join(folder, node.ID+".pages")
		pageDir := filepath.Join(nodesDir, filepath.FromSlash(pageDirName))
		pageEntries, readDirErr := store.ReadProjectDir(project.Path, pageDir)
		if os.IsNotExist(readDirErr) {
			pageEntries = nil
		} else if readDirErr != nil {
			return fmt.Errorf("read pages for node %q: %w", node.ID, readDirErr)
		}
		for _, pageEntry := range pageEntries {
			if pageEntry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe page entry %q for node %q", pageEntry.Name(), node.ID)
			}
			if pageEntry.IsDir() {
				continue
			}
			name := pageEntry.Name()
			if name != "pages.json" {
				if _, ok := store.PageFormatFromFilename(name); !ok {
					continue
				}
				pageID := strings.TrimSuffix(name, filepath.Ext(name))
				if !engine.ValidNodeID(pageID) {
					continue
				}
			}
			data, readErr := store.ReadProjectFile(project.Path, filepath.Join(pageDir, name))
			if readErr != nil {
				return fmt.Errorf("read page file %q: %w", name, readErr)
			}
			if err := add(
				path.Join("nodes", pageDirName, name),
				data,
			); err != nil {
				return err
			}
		}

		fileDirName := path.Join(folder, node.ID+".files")
		fileDir := filepath.Join(nodesDir, filepath.FromSlash(fileDirName))
		fileEntries, readDirErr := store.ReadProjectDir(project.Path, fileDir)
		if os.IsNotExist(readDirErr) {
			continue
		}
		if readDirErr != nil {
			return fmt.Errorf("read attachments for node %q: %w", node.ID, readDirErr)
		}
		for _, fileEntry := range fileEntries {
			if fileEntry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe attachment entry %q for node %q", fileEntry.Name(), node.ID)
			}
			if fileEntry.IsDir() {
				continue
			}
			name := fileEntry.Name()
			if store.SanitizeAttachmentName(name) != name {
				continue
			}
			data, readErr := store.ReadProjectFile(project.Path, filepath.Join(fileDir, name))
			if readErr != nil {
				return fmt.Errorf("read attachment %q for node %q: %w", name, node.ID, readErr)
			}
			if err := add(path.Join("nodes", fileDirName, name), data); err != nil {
				return err
			}
		}
	}
	journal, err := readProjectJournal(project.Path)
	if err != nil {
		return err
	}
	if journal != nil {
		if err := add("run/journal.jsonl", journal); err != nil {
			return err
		}
	}
	return nil
}

// readProjectJournal returns the project's lifecycle events as one journal
// body. Compaction rotates the live journal to journal.jsonl.1, so the two
// segments are concatenated oldest-first — reading only the live file would
// export a project that has lost everything before its last checkpoint.
//
// The result is deliberately still a single run/journal.jsonl archive entry:
// the .veproj format and every importer stay unchanged, and a concatenation of
// two valid journals in recorded order is itself a valid journal.
func readProjectJournal(root string) ([]byte, error) {
	var journal []byte
	for _, name := range []string{"journal.jsonl.1", "journal.jsonl"} {
		data, err := store.ReadProjectFile(root, filepath.Join(root, "run", name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read run journal: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		if len(journal) > 0 && journal[len(journal)-1] != '\n' {
			journal = append(journal, '\n')
		}
		journal = append(journal, data...)
	}
	return journal, nil
}

func cleanArchiveEntry(name, root string) (string, bool) {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if root != "" {
		prefix := root + "/"
		if !strings.HasPrefix(clean, prefix) {
			return "", false
		}
		clean = strings.TrimPrefix(clean, prefix)
	}
	return clean, clean != "" && clean != "."
}

func archiveRoot(reader *zip.Reader) (string, error) {
	root := ""
	found := false
	for _, entry := range reader.File {
		clean := path.Clean(entry.Name)
		switch {
		case clean == "graph.yaml":
			if found && root != "" {
				return "", fmt.Errorf("archive contains multiple graph.yaml files")
			}
			root, found = "", true
		case strings.HasSuffix(clean, "/graph.yaml") && strings.Count(clean, "/") == 1:
			candidate := strings.TrimSuffix(clean, "/graph.yaml")
			if found && root != candidate {
				return "", fmt.Errorf("archive contains multiple graph.yaml files")
			}
			root, found = candidate, true
		}
	}
	if !found {
		return "", fmt.Errorf("archive does not contain graph.yaml")
	}
	return root, nil
}

func allowedProjectArchivePath(name string) bool {
	if name == "graph.yaml" || name == "manifest.json" || name == "run/journal.jsonl" {
		return true
	}
	if !strings.HasPrefix(name, "nodes/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(name, "nodes/"), "/")
	// Node files may sit inside folders, so peel off any leading folder
	// segments before reading the node part of the path.
	depth := 0
	for len(parts) > 1 && store.ValidFolderName(parts[0]) &&
		!strings.HasSuffix(parts[0], ".pages") && !strings.HasSuffix(parts[0], ".files") {
		if len(parts) == 2 && !strings.HasSuffix(strings.ToLower(parts[1]), ".md") {
			// The tail must stay a document, or a payload directory entry
			// which keeps its own two-part shape below.
			break
		}
		depth++
		if depth > store.MaxFolderDepth {
			return false
		}
		parts = parts[1:]
	}
	if len(parts) == 1 {
		base := parts[0]
		if !strings.HasSuffix(strings.ToLower(base), ".md") {
			return false
		}
		return engine.ValidNodeID(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if len(parts) == 2 && strings.HasSuffix(parts[0], ".pages") {
		nodeID := strings.TrimSuffix(parts[0], ".pages")
		if !engine.ValidNodeID(nodeID) {
			return false
		}
		if parts[1] == "pages.json" {
			return true
		}
		if _, ok := store.PageFormatFromFilename(parts[1]); !ok {
			return false
		}
		return engine.ValidNodeID(strings.TrimSuffix(parts[1], filepath.Ext(parts[1])))
	}
	if len(parts) == 2 && strings.HasSuffix(parts[0], ".files") {
		nodeID := strings.TrimSuffix(parts[0], ".files")
		fileName := parts[1]
		return engine.ValidNodeID(nodeID) && fileName != "" &&
			store.SanitizeAttachmentName(fileName) == fileName
	}
	return false
}

func ExtractProjectArchive(reader *zip.Reader, destination string) (ProjectArchiveManifest, error) {
	root, err := archiveRoot(reader)
	if err != nil {
		return ProjectArchiveManifest{}, err
	}
	if len(reader.File) > maxProjectArchiveFiles {
		return ProjectArchiveManifest{}, fmt.Errorf("archive has too many files")
	}
	seen := make(map[string]struct{}, len(reader.File))
	var total int64
	var manifest ProjectArchiveManifest
	for _, entry := range reader.File {
		name, inRoot := cleanArchiveEntry(entry.Name, root)
		if !inRoot {
			continue
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return ProjectArchiveManifest{}, fmt.Errorf("archive contains a symbolic link")
		}
		if !allowedProjectArchivePath(name) {
			return ProjectArchiveManifest{}, fmt.Errorf("unsupported archive path %q", name)
		}
		if _, exists := seen[name]; exists {
			return ProjectArchiveManifest{}, fmt.Errorf("duplicate archive path %q", name)
		}
		seen[name] = struct{}{}
		if entry.UncompressedSize64 > maxProjectArchiveFileBytes {
			return ProjectArchiveManifest{}, fmt.Errorf("archive file %q is too large", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > maxProjectExtractedBytes {
			return ProjectArchiveManifest{}, fmt.Errorf("archive expands beyond the size limit")
		}

		source, err := entry.Open()
		if err != nil {
			return ProjectArchiveManifest{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(source, maxProjectArchiveFileBytes+1))
		closeErr := source.Close()
		if readErr != nil {
			return ProjectArchiveManifest{}, readErr
		}
		if closeErr != nil {
			return ProjectArchiveManifest{}, closeErr
		}
		if len(data) > maxProjectArchiveFileBytes {
			return ProjectArchiveManifest{}, fmt.Errorf("archive file %q is too large", name)
		}
		if name == "manifest.json" {
			if len(data) > 64<<10 {
				return ProjectArchiveManifest{}, fmt.Errorf("manifest is too large")
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return ProjectArchiveManifest{}, fmt.Errorf("invalid manifest: %w", err)
			}
			continue
		}

		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ProjectArchiveManifest{}, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return ProjectArchiveManifest{}, err
		}
	}
	if manifest.Format != "" &&
		(manifest.Format != ProjectArchiveFormat ||
			manifest.Version != ProjectArchiveVersion) {
		return ProjectArchiveManifest{}, fmt.Errorf(
			"unsupported project archive %q version %d",
			manifest.Format,
			manifest.Version,
		)
	}
	return manifest, nil
}

func ValidateImportedProject(root string) error {
	if err := store.ValidateProjectTree(root, root); err != nil {
		return fmt.Errorf("validate imported project tree: %w", err)
	}
	graphData, err := store.ReadProjectFile(root, filepath.Join(root, "graph.yaml"))
	if err != nil {
		return fmt.Errorf("read imported graph: %w", err)
	}
	graph, err := engine.ParseGraph(graphData)
	if err != nil {
		return fmt.Errorf("parse imported graph: %w", err)
	}
	if err := store.ValidateGraphForStorage(graph); err != nil {
		return err
	}
	for _, issue := range engine.Validate(graph) {
		if issue.Severity == "error" {
			return fmt.Errorf("invalid imported graph: %s", issue.Msg)
		}
	}
	st := store.NewStore(root)
	// Payload directories travel with their node, so they can be nested any
	// number of folders deep.
	for nodeID := range store.ListNodePayloadDirs(root, ".pages") {
		if graph.NodeByID(nodeID) == nil {
			return fmt.Errorf("pages reference unknown node %q", nodeID)
		}
		if _, err := store.StatProjectPath(root, st.NodePagesManifestPath(nodeID)); err != nil {
			return fmt.Errorf("read pages manifest for node %q: %w", nodeID, err)
		}
		pageManifest, err := st.LoadNodePagesManifest(nodeID)
		if err != nil {
			return fmt.Errorf("validate pages for node %q: %w", nodeID, err)
		}
		expectedPages := make(map[string]bool, len(pageManifest.Pages))
		for _, page := range pageManifest.Pages {
			expectedPages[page.ID] = true
			if _, err := store.StatProjectPath(root, st.NodePagePath(nodeID, page.ID, page.Format)); err != nil {
				return fmt.Errorf(
					"read page %q for node %q: %w",
					page.ID,
					nodeID,
					err,
				)
			}
		}
		pageFiles, err := store.ReadProjectDir(root, st.NodePagesDir(nodeID))
		if err != nil {
			return err
		}
		for _, pageFile := range pageFiles {
			if pageFile.IsDir() || pageFile.Name() == "pages.json" {
				continue
			}
			pageID := strings.TrimSuffix(pageFile.Name(), filepath.Ext(pageFile.Name()))
			if !expectedPages[pageID] {
				return fmt.Errorf(
					"page file %q is missing from node %q manifest",
					pageFile.Name(),
					nodeID,
				)
			}
		}
	}
	for nodeID, dir := range store.ListNodePayloadDirs(root, ".files") {
		if graph.NodeByID(nodeID) == nil {
			return fmt.Errorf("attachments reference unknown node %q", nodeID)
		}
		files, readErr := store.ReadProjectDir(root, dir)
		if readErr != nil {
			return fmt.Errorf("read attachments for node %q: %w", nodeID, readErr)
		}
		for _, file := range files {
			if file.IsDir() || file.Type()&os.ModeSymlink != 0 || store.SanitizeAttachmentName(file.Name()) != file.Name() {
				return fmt.Errorf("invalid attachment %q for node %q", file.Name(), nodeID)
			}
		}
	}

	journalPath := filepath.Join(root, "run", "journal.jsonl")
	if journal, err := store.ReadProjectFile(root, journalPath); err == nil {
		if _, err := engine.StateFromJournalChecked(journal); err != nil {
			return fmt.Errorf("invalid imported journal: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func fallbackImportName(filename string) string {
	base := filepath.Base(filename)
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".veproj"):
		base = base[:len(base)-len(".veproj")]
	case strings.HasSuffix(lower, ".zip"):
		base = base[:len(base)-len(".zip")]
	}
	base = strings.TrimSpace(base)
	if base == "." || len(base) > 100 || !ProjectNamePattern.MatchString(base) {
		return "imported-project"
	}
	return base
}

func (pm *ProjectManager) availableProjectName(base string) (string, string, error) {
	for index := 1; index <= 10_000; index++ {
		name := base
		if index > 1 {
			name = fmt.Sprintf("%s-%d", base, index)
		}
		if !ValidProjectPath(name) {
			return "", "", fmt.Errorf("invalid imported project name %q", name)
		}
		destination, err := pm.Resolve(name)
		if err != nil {
			return "", "", err
		}
		if _, err := store.StatProjectPath(pm.workspace, destination); os.IsNotExist(err) {
			return name, destination, nil
		} else if err != nil {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("could not allocate a unique project name")
}

// InstallError carries the HTTP status the caller should report for a failed
// install, so the shared installArchive core keeps the 400/413/500 distinctions
// PostProjectImport made inline before it was extracted.
type InstallError struct {
	status int
	err    error
}

// Status is the HTTP status this failure maps to.
func (e *InstallError) Status() int { return e.status }

func (e *InstallError) Error() string { return e.err.Error() }
func (e *InstallError) Unwrap() error { return e.err }

// ImportMode says where the contents of a bundle should land.
type ImportMode string

const (
	// ImportModeFolder wraps everything the archive holds in one new project
	// directory named after the manifest.
	ImportModeFolder ImportMode = "folder"
	// ImportModeRoot installs a bundle's top-level members beside the projects
	// the workspace already has, instead of under a wrapper. Restoring a
	// whole-workspace backup into a workspace is the case that needs it: the
	// wrapper it would otherwise get is named after the exporting machine's
	// directory, holds the active project, and so cannot be deleted afterwards.
	ImportModeRoot ImportMode = "root"
)

// ParseImportMode reads the mode a client asked for. Anything unrecognised —
// including the empty string an older client sends — means the wrapping
// import: that is what every caller got before the choice existed, and it is
// the reversible one. Spreading an archive across a workspace by accident takes
// a folder-by-folder cleanup to undo.
func ParseImportMode(value string) ImportMode {
	if strings.TrimSpace(value) == string(ImportModeRoot) {
		return ImportModeRoot
	}
	return ImportModeFolder
}

// InstallArchive installs an archive the way it has always been installed, as
// one new project directory. It stays as it was so callers that have no reason
// to offer the choice — the remote pull endpoints — keep reading as they did.
func (pm *ProjectManager) InstallArchive(
	data []byte, requestedName, fallbackFilename string,
) (string, string, error) {
	return pm.InstallArchiveMode(data, requestedName, fallbackFilename, ImportModeFolder, "")
}

// resolveImportParent checks the project or folder an import was asked to land
// under and answers with the path prefix the new project's name needs. An empty
// parent means the workspace root, which is where every import went before the
// choice existed, so it is answered without touching the disk.
//
// The parent goes through ValidProjectPath and Resolve rather than any joining
// of our own: this is the one place an uploaded archive gets to influence where
// it is written, so the workspace confinement has to be the same one the rest
// of the manager uses. A parent that names nothing on disk is refused instead
// of created, so a typo cannot scatter a stray folder through the workspace.
func (pm *ProjectManager) resolveImportParent(parent string) (string, error) {
	parent = strings.Trim(strings.TrimSpace(parent), "/")
	if parent == "" || parent == "." {
		return "", nil
	}
	if !ValidProjectPath(parent) {
		return "", &InstallError{
			http.StatusBadRequest, fmt.Errorf("invalid destination folder %q", parent),
		}
	}
	dir, err := pm.Resolve(parent)
	if err != nil {
		return "", &InstallError{http.StatusBadRequest, err}
	}
	if info, err := store.StatProjectPath(pm.workspace, dir); err != nil || !info.IsDir() {
		return "", &InstallError{
			http.StatusBadRequest, fmt.Errorf("destination folder %q does not exist", parent),
		}
	}
	return parent, nil
}

// InstallArchiveMode unpacks a .veproj or ZIP archive into the workspace and
// activates a project from it, returning the activated name and its directory.
// It never overwrites an existing project. Both the multipart upload endpoint
// and the remote-import endpoint go through here so the security-sensitive
// extraction lives in exactly one place. requestedName may be empty to fall
// back to the manifest name (then fallbackFilename); it names the wrapper, so
// ImportModeRoot, which builds no wrapper, has nothing to do with it.
//
// parent names an existing project or folder to install under, and empty — what
// every caller sent before it existed — means the workspace root. It is refused
// alongside ImportModeRoot rather than ignored: root mode spreads a bundle's
// members across the workspace and builds no wrapper, so there is nothing for a
// parent to apply to, and quietly dropping it would scatter projects at the top
// level after the caller asked for them to be tucked away. That is the outcome
// that takes a folder-by-folder cleanup to undo, so the combination is an error
// the caller can see and correct.
func (pm *ProjectManager) InstallArchiveMode(
	data []byte, requestedName, fallbackFilename string, mode ImportMode, parent string,
) (string, string, error) {
	parentPath, err := pm.resolveImportParent(parent)
	if err != nil {
		return "", "", err
	}
	if parentPath != "" && mode == ImportModeRoot {
		return "", "", &InstallError{
			http.StatusBadRequest,
			fmt.Errorf("a workspace import cannot be placed under %q", parentPath),
		}
	}
	if len(data) > MaxProjectArchiveBytes {
		return "", "", &InstallError{
			http.StatusRequestEntityTooLarge,
			fmt.Errorf("project archive is too large"),
		}
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", &InstallError{
			http.StatusBadRequest, fmt.Errorf("invalid ZIP archive: %w", err),
		}
	}

	pm.importMu.Lock()
	defer pm.importMu.Unlock()

	importRoot := filepath.Join(pm.workspace, store.DataDir, "imports")
	if err := store.MkdirAllProjectPath(pm.workspace, importRoot, 0o755); err != nil {
		return "", "", &InstallError{http.StatusInternalServerError, err}
	}
	temp, err := store.MkdirTempProjectPath(pm.workspace, importRoot, "project-", 0o700)
	if err != nil {
		return "", "", &InstallError{http.StatusInternalServerError, err}
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = store.RemoveAllProjectPath(pm.workspace, temp)
		}
	}()

	bundleRoot, bundle, isBundle, err := DetectProjectBundle(reader)
	if err != nil {
		return "", "", &InstallError{http.StatusBadRequest, err}
	}
	manifestName := ""
	activateRel := "."
	if isBundle {
		if err := ExtractProjectBundle(reader, bundleRoot, bundle, temp); err != nil {
			return "", "", &InstallError{http.StatusBadRequest, err}
		}
		for _, project := range bundle.Projects {
			dir := filepath.Join(temp, filepath.FromSlash(bundleMemberPrefix(project)))
			if err := ValidateImportedProject(dir); err != nil {
				return "", "", &InstallError{
					http.StatusBadRequest, fmt.Errorf("project %q: %w", project, err),
				}
			}
		}
		manifestName = bundle.Name
		activateRel = bundleActivationTarget(bundle)
		if mode == ImportModeRoot && !bundleWrapsItsRoot(bundle) {
			return pm.installBundleAtRoot(temp, bundle)
		}
	} else {
		manifest, err := ExtractProjectArchive(reader, temp)
		if err != nil {
			return "", "", &InstallError{http.StatusBadRequest, err}
		}
		if err := ValidateImportedProject(temp); err != nil {
			return "", "", &InstallError{http.StatusBadRequest, err}
		}
		manifestName = manifest.Name
	}

	requestedName = strings.TrimSpace(requestedName)
	baseName := requestedName
	if baseName == "" {
		baseName = strings.TrimSpace(manifestName)
	}
	if baseName == "" {
		baseName = fallbackImportName(fallbackFilename)
	}
	if baseName == "." || len(baseName) > 100 || !ProjectNamePattern.MatchString(baseName) {
		if requestedName != "" {
			return "", "", &InstallError{
				http.StatusBadRequest, fmt.Errorf("invalid project name %q", baseName),
			}
		}
		baseName = fallbackImportName(fallbackFilename)
	}
	// ProjectNamePattern above judged the leaf name only, which is the whole of
	// what it knows how to judge — it has no opinion on "/". The parent has
	// already been resolved inside the workspace, and availableProjectName runs
	// the joined path back through ValidProjectPath and Resolve before it hands
	// out a directory, so a nested import is confined exactly as a top-level one
	// is and still gets the -2 suffix when a sibling already holds the name.
	if parentPath != "" {
		baseName = parentPath + "/" + baseName
	}
	name, destination, err := pm.availableProjectName(baseName)
	if err != nil {
		return "", "", &InstallError{http.StatusBadRequest, err}
	}
	if err := store.RenameProjectPath(pm.workspace, temp, pm.workspace, destination); err != nil {
		return "", "", &InstallError{
			http.StatusInternalServerError, fmt.Errorf("install imported project: %w", err),
		}
	}
	keepTemp = true
	activateName := name
	if activateRel != "." && activateRel != "" {
		activateName = name + "/" + activateRel
	}
	if err := pm.Activate(activateName, false, ""); err != nil {
		_ = store.RemoveAllProjectPath(pm.workspace, destination)
		return "", "", &InstallError{
			http.StatusInternalServerError, fmt.Errorf("activate imported project: %w", err),
		}
	}
	pm.hub.Broadcast("project-changed", activateName)
	return activateName, destination, nil
}

// installBundleAtRoot moves the bundle's top-level directories into the
// workspace itself rather than into a wrapper. temp holds the already
// extracted and validated tree, so this is only the placement step.
//
// Every directory gets a name availableProjectName says is free, and each move
// happens before the next name is allocated: asking for all the names up front
// would hand the same free name to two members whose own names differ only by
// the -2 suffix a rename adds. The destination therefore never exists when it
// is moved onto, which is what keeps an import from replacing a project or
// merging into one. A move that fails partway puts the members already placed
// back where they came from, so the workspace is never left holding half a
// bundle.
func (pm *ProjectManager) installBundleAtRoot(
	temp string, bundle ProjectBundleManifest,
) (string, string, error) {
	entries, err := store.ReadProjectDir(temp, temp)
	if err != nil {
		return "", "", &InstallError{http.StatusInternalServerError, err}
	}
	placed := map[string]string{}
	undo := map[string]string{}
	abandon := func(status int, err error) (string, string, error) {
		for destination, origin := range undo {
			_ = store.RenameProjectPath(pm.workspace, destination, pm.workspace, origin)
		}
		return "", "", &InstallError{status, err}
	}
	for _, entry := range entries {
		// Only directories: members and grouping folders are all directories,
		// and the manifest is never written out here.
		if !entry.IsDir() {
			continue
		}
		name, destination, err := pm.availableProjectName(entry.Name())
		if err != nil {
			return abandon(http.StatusBadRequest, err)
		}
		origin := filepath.Join(temp, entry.Name())
		if err := store.RenameProjectPath(pm.workspace, origin, pm.workspace, destination); err != nil {
			return abandon(
				http.StatusInternalServerError,
				fmt.Errorf("install imported project %q: %w", entry.Name(), err),
			)
		}
		placed[entry.Name()] = name
		undo[destination] = origin
	}

	// The bundle names its activation target as a path inside itself, and only
	// that path's first segment is a workspace-level name — the one a collision
	// may have renamed.
	head, tail, nested := strings.Cut(bundleActivationTarget(bundle), "/")
	activateName, ok := placed[head]
	if !ok {
		return abandon(
			http.StatusBadRequest,
			fmt.Errorf("project bundle does not contain %q", head),
		)
	}
	if nested {
		activateName += "/" + tail
	}
	destination, err := pm.Resolve(activateName)
	if err != nil {
		return abandon(http.StatusBadRequest, err)
	}
	if err := pm.Activate(activateName, false, ""); err != nil {
		return abandon(
			http.StatusInternalServerError,
			fmt.Errorf("activate imported project: %w", err),
		)
	}
	pm.hub.Broadcast("project-changed", activateName)
	return activateName, destination, nil
}

// bundleWrapsItsRoot reports whether the bundle's own root directory is a
// project rather than a grouping folder. Such a bundle has a graph.yaml at its
// top level and sub-projects hanging off it, so there is nothing to spread
// across the workspace: installing it at the root can only mean the one new
// project directory a folder import already produces.
func bundleWrapsItsRoot(manifest ProjectBundleManifest) bool {
	for _, project := range manifest.Projects {
		if project == "." {
			return true
		}
	}
	return false
}

const (
	ProjectBundleKind    = "bundle"
	ProjectBundleVersion = 3
	maxProjectBundleSize = 64 << 10
)

// ProjectBundleManifest describes a multi-project archive: a directory mirror
// of one workspace subtree. Every entry in Projects is a directory inside the
// archive holding a normal single-project layout ("." for the bundle root
// itself); Folders keeps grouping directories that hold no graph.yaml.
type ProjectBundleManifest struct {
	Format   string   `json:"format"`
	Version  int      `json:"version"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Projects []string `json:"projects"`
	Folders  []string `json:"folders,omitempty"`
}

type bundleMember struct {
	rel  string
	info ProjectInfo
}

// ExportScope is the resolved subtree an export request covers.
type ExportScopeResult struct {
	root    ProjectInfo
	members []bundleMember
	folders []string
	// shared is the workspace's custom lifecycle vocabulary. It is captured
	// here because the archive builders have a project directory and no way
	// back to the workspace, and because an export must not read a moving
	// target: the palette resolved once at scope time is the one every member
	// of a bundle is written against.
	shared []engine.StatusDefinition
}

// Root is the project the export is anchored on.
func (s ExportScopeResult) Root() ProjectInfo { return s.root }

// SharedStatuses is the workspace vocabulary this export was resolved against.
func (s ExportScopeResult) SharedStatuses() []engine.StatusDefinition { return s.shared }

// isBundle reports whether the scope needs the multi-project format: a folder
// has no graph.yaml of its own, and a project with descendants cannot be
// represented by the flat single-project archive.
func (s ExportScopeResult) IsBundle() bool {
	return s.root.IsFolder || len(s.members) > 1
}

// ExportScope resolves which project the export covers, plus every descendant
// project and grouping folder underneath it. An empty name means the active
// project.
func (pm *ProjectManager) ExportScope(name string) (ExportScopeResult, error) {
	projects, err := pm.List()
	if err != nil {
		return ExportScopeResult{}, err
	}
	var root ProjectInfo
	found := false
	if name == "" {
		activeRoot := pm.Store().Root()
		for _, project := range projects {
			if project.Path == activeRoot {
				root, found = project, true
				break
			}
		}
		if !found {
			return ExportScopeResult{}, fmt.Errorf("active project is not in the workspace")
		}
	} else {
		for _, project := range projects {
			if project.Name == name {
				root, found = project, true
				break
			}
		}
		if !found && name == "." {
			// A workspace root without a graph.yaml never shows up in List(),
			// but it is still the tree node users right-click to export.
			root = ProjectInfo{
				Name:     ".",
				Label:    filepath.Base(pm.workspace),
				Path:     pm.workspace,
				IsFolder: true,
			}
			found = true
		}
		if !found {
			return ExportScopeResult{}, fmt.Errorf("project %q is not in the workspace", name)
		}
	}

	scope := ExportScopeResult{
		root:   root,
		shared: store.LoadWorkspaceStatuses(pm.workspaceRoot()),
	}
	if !root.IsFolder {
		scope.members = append(scope.members, bundleMember{rel: ".", info: root})
	}
	prefix := root.Name + "/"
	if root.Name == "." {
		prefix = ""
	}
	for _, project := range projects {
		if project.Name == root.Name || project.Name == "." {
			continue
		}
		rel := project.Name
		if prefix != "" {
			if !strings.HasPrefix(project.Name, prefix) {
				continue
			}
			rel = strings.TrimPrefix(project.Name, prefix)
		}
		if rel == "" || !ValidProjectPath(rel) {
			continue
		}
		if project.IsFolder {
			scope.folders = append(scope.folders, rel)
			continue
		}
		scope.members = append(scope.members, bundleMember{rel: rel, info: project})
	}
	if len(scope.members) == 0 {
		return ExportScopeResult{}, fmt.Errorf("%q contains no projects to export", root.Label)
	}
	return scope, nil
}

func bundleMemberPrefix(rel string) string {
	if rel == "." {
		return ""
	}
	return rel
}

// BuildProjectBundle writes every project in the scope into a single archive,
// mirroring the workspace directory layout so the tree can be restored as-is.
func BuildProjectBundle(destination io.Writer, scope ExportScopeResult) error {
	writer := zip.NewWriter(destination)
	archive := &archiveWriter{writer: writer}
	failed := true
	defer func() {
		if failed {
			_ = writer.Close()
		}
	}()

	name := scope.root.Label
	if name == "" || name == "." {
		name = filepath.Base(scope.root.Path)
	}
	projects := make([]string, 0, len(scope.members))
	for _, member := range scope.members {
		projects = append(projects, member.rel)
	}
	folders := append([]string(nil), scope.folders...)
	sort.Strings(folders)
	manifest, err := json.MarshalIndent(ProjectBundleManifest{
		Format:   ProjectArchiveFormat,
		Version:  ProjectBundleVersion,
		Kind:     ProjectBundleKind,
		Name:     name,
		Projects: projects,
		Folders:  folders,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := archive.add("manifest.json", append(manifest, '\n')); err != nil {
		return err
	}
	for _, member := range scope.members {
		if err := writeProjectFiles(
			archive, bundleMemberPrefix(member.rel), member.info, scope.shared,
		); err != nil {
			return fmt.Errorf("export %q: %w", member.rel, err)
		}
	}

	if err := writer.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}

// DetectProjectBundle looks for a bundle manifest at the archive root (or one
// directory below it, so a re-zipped bundle still imports). It returns the
// prefix that manifest sits under.
func DetectProjectBundle(reader *zip.Reader) (string, ProjectBundleManifest, bool, error) {
	for _, entry := range reader.File {
		clean := path.Clean(entry.Name)
		if clean != "manifest.json" && !(strings.HasSuffix(clean, "/manifest.json") &&
			strings.Count(clean, "/") == 1) {
			continue
		}
		if entry.UncompressedSize64 > maxProjectBundleSize {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return "", ProjectBundleManifest{}, false, err
		}
		data, readErr := io.ReadAll(io.LimitReader(source, maxProjectBundleSize+1))
		closeErr := source.Close()
		if readErr != nil {
			return "", ProjectBundleManifest{}, false, readErr
		}
		if closeErr != nil {
			return "", ProjectBundleManifest{}, false, closeErr
		}
		var manifest ProjectBundleManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		if manifest.Kind != ProjectBundleKind {
			continue
		}
		if manifest.Format != ProjectArchiveFormat {
			return "", ProjectBundleManifest{}, false, fmt.Errorf(
				"unsupported project bundle %q", manifest.Format)
		}
		if manifest.Version < ProjectBundleVersion {
			return "", ProjectBundleManifest{}, false, fmt.Errorf(
				"unsupported project bundle version %d", manifest.Version)
		}
		if manifest.Version > ProjectBundleVersion {
			return "", ProjectBundleManifest{}, false, fmt.Errorf(
				"project bundle version %d is newer than this build supports",
				manifest.Version)
		}
		if err := validateBundleManifest(&manifest); err != nil {
			return "", ProjectBundleManifest{}, false, err
		}
		root := strings.TrimSuffix(clean, "manifest.json")
		return strings.TrimSuffix(root, "/"), manifest, true, nil
	}
	return "", ProjectBundleManifest{}, false, nil
}

func validateBundleManifest(manifest *ProjectBundleManifest) error {
	if len(manifest.Projects) == 0 {
		return fmt.Errorf("project bundle lists no projects")
	}
	if len(manifest.Projects)+len(manifest.Folders) > maxProjectArchiveFiles {
		return fmt.Errorf("project bundle lists too many entries")
	}
	seen := make(map[string]bool, len(manifest.Projects)+len(manifest.Folders))
	for _, group := range [][]string{manifest.Projects, manifest.Folders} {
		for _, entry := range group {
			if entry != "." && !ValidProjectPath(entry) {
				return fmt.Errorf("invalid project bundle path %q", entry)
			}
			if seen[entry] {
				return fmt.Errorf("duplicate project bundle path %q", entry)
			}
			seen[entry] = true
		}
	}
	return nil
}

// bundleEntryPath maps an archive entry to its path inside the owning project.
// Prefixes are matched deepest-first so a sub-project's files never land in
// its parent.
func bundleEntryPath(prefixes []string, name string) (string, bool) {
	for _, prefix := range prefixes {
		if prefix == "" {
			return name, true
		}
		if inner, ok := strings.CutPrefix(name, prefix+"/"); ok {
			return inner, true
		}
	}
	return "", false
}

// ExtractProjectBundle unpacks every project in the bundle below destination,
// keeping the same path checks the single-project importer applies.
func ExtractProjectBundle(
	reader *zip.Reader,
	root string,
	manifest ProjectBundleManifest,
	destination string,
) error {
	if len(reader.File) > maxProjectArchiveFiles {
		return fmt.Errorf("archive has too many files")
	}
	prefixes := make([]string, 0, len(manifest.Projects))
	for _, project := range manifest.Projects {
		prefixes = append(prefixes, bundleMemberPrefix(project))
	}
	// Deepest first: "a/b" must win over "a" and over the bundle root.
	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i]) > len(prefixes[j])
	})

	seen := make(map[string]struct{}, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		name, inRoot := cleanArchiveEntry(entry.Name, root)
		if !inRoot || entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains a symbolic link")
		}
		if name == "manifest.json" {
			continue
		}
		inner, ok := bundleEntryPath(prefixes, name)
		if !ok || !allowedProjectArchivePath(inner) {
			return fmt.Errorf("unsupported archive path %q", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate archive path %q", name)
		}
		seen[name] = struct{}{}
		if entry.UncompressedSize64 > maxProjectArchiveFileBytes {
			return fmt.Errorf("archive file %q is too large", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > maxProjectExtractedBytes {
			return fmt.Errorf("archive expands beyond the size limit")
		}

		source, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(source, maxProjectArchiveFileBytes+1))
		closeErr := source.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxProjectArchiveFileBytes {
			return fmt.Errorf("archive file %q is too large", name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}

	for _, project := range manifest.Projects {
		dir := filepath.Join(destination, filepath.FromSlash(bundleMemberPrefix(project)))
		if _, err := os.Stat(filepath.Join(dir, "graph.yaml")); err != nil {
			return fmt.Errorf("project %q is missing graph.yaml", project)
		}
	}
	for _, folder := range manifest.Folders {
		if err := os.MkdirAll(
			filepath.Join(destination, filepath.FromSlash(folder)), 0o755,
		); err != nil {
			return err
		}
	}
	return nil
}

// bundleActivationTarget picks which project to open after import: the bundle
// root when it is a project itself, otherwise the shallowest sub-project.
func bundleActivationTarget(manifest ProjectBundleManifest) string {
	best := ""
	for _, project := range manifest.Projects {
		if project == "." {
			return "."
		}
		if best == "" || strings.Count(project, "/") < strings.Count(best, "/") ||
			(strings.Count(project, "/") == strings.Count(best, "/") && project < best) {
			best = project
		}
	}
	return best
}
