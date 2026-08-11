import { useCallback, useEffect, useMemo, useState } from "react";
import { IconFolder, IconFolderOpen, IconImport, IconPlus } from "../icons";
import { api } from "../api";
import { reportError, useApp, usePreference } from "../store";
import { useNarrowViewport } from "./TopbarOverflow";
import { StatusLegend } from "./explorer/StatusLegend";
import { WorkspaceTree } from "./explorer/WorkspaceTree";
import {
  PROJECT_SORT_LABELS,
  type ProjectSort,
} from "./explorer/sortProjects";
import { useExplorerFilter } from "./explorer/useExplorerFilter";
import { useExplorerSelection } from "./explorer/useExplorerSelection";
import { useProjectExpansion } from "./explorer/useProjectExpansion";
import { useProjectDrag } from "./explorer/useProjectDrag";
import { useProjectTransfer } from "./explorer/useProjectTransfer";
import { ProjectTransferBar } from "./explorer/ProjectTransferBar";
import {
  ProjectCreatePanel,
  useProjectCreate,
} from "./explorer/ProjectCreatePanel";
import {
  ProjectRenamePanel,
  useProjectRename,
} from "./explorer/ProjectRenamePanel";
import { SaveAsPanel, useSaveAs } from "./explorer/SaveAsPanel";
import {
  WorkspacePickerPanel,
  useWorkspacePicker,
} from "./explorer/WorkspacePickerPanel";
import { TreeContextMenu } from "./explorer/TreeContextMenu";
import { useCanEdit } from "./SignIn";
import {
  NodeContextMenu,
  type NodeMenuTarget,
} from "./explorer/NodeContextMenu";
import {
  ProjectContextMenu,
  type ProjectMenuTarget,
} from "./explorer/ProjectContextMenu";
import { pickProject } from "./ProjectPickerDialog";

/**
 * The scrim behind the phone-width explorer overlay. Rendered only when the
 * viewport is narrow and the explorer is open; a tap on it collapses the
 * explorer, which is the gesture every other overlay in the app answers to.
 * The board underneath is unreachable while the overlay covers it, so the
 * backdrop says so visually instead of leaving half a dead board showing.
 */
export function SidebarBackdrop({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const narrow = useNarrowViewport();
  if (!narrow || !open) return null;
  return <div className="sidebar-backdrop" onClick={onClose} aria-hidden="true" />;
}

export function Sidebar() {
  // Read-only sessions browse the tree and search; the rows that create,
  // import, rename or transfer are not offered. The server refuses them all
  // regardless — this only stops the sidebar promising what will be refused.
  const canEdit = useCanEdit();
  const graph = useApp((state) => state.graph);
  const statuses = useApp((state) => state.statuses);
  const issues = useApp((state) => state.issues);
  const trash = useApp((state) => state.trash);
  const workspace = useApp((state) => state.workspace);
  const workspaces = useApp((state) => state.workspaces);
  const projects = useApp((state) => state.projects);
  const activeProject = useApp((state) => state.activeProject);
  const activeTab = useApp((state) => state.activeTab);
  const openTab = useApp((state) => state.openTab);
  const switchProject = useApp((state) => state.switchProject);
  const switchWorkspace = useApp((state) => state.switchWorkspace);
  const restoreTrash = useApp((state) => state.restoreTrash);
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  const narrow = useNarrowViewport();
  // On a phone the explorer is an overlay and opening a node opens the
  // bottom-sheet drawer over it, so the panel left behind is dead weight:
  // collapse it as part of the same gesture. Only after the open succeeds —
  // a failed open leaves the tree up so the row can be tried again. Wide
  // windows keep the panel; there the tree and the document sit side by side.
  const openTabAndCollapse = useCallback(
    async (id: string) => {
      await openTab(id);
      if (narrow) updateUIPreference("explorerCollapsed", true);
    },
    [openTab, narrow, updateUIPreference],
  );
  // Node folders [B-06]: file organisation only, never a graph edit.
  const nodeFolders = useApp((state) => state.nodeFolders);
  const nodeFolderOf = useApp((state) => state.nodeFolderOf);
  const createNodeFolder = useApp((state) => state.createNodeFolder);
  const renameNodeFolder = useApp((state) => state.renameNodeFolder);
  const deleteNodeFolder = useApp((state) => state.deleteNodeFolder);
  const moveNodeFolder = useApp((state) => state.moveNodeFolder);
  const moveNodesToFolder = useApp((state) => state.moveNodesToFolder);
  const [switchingProject, setSwitchingProject] = useState<string | null>(null);
  const [switchingWorkspace, setSwitchingWorkspace] = useState<string | null>(
    null,
  );
  const nodesExpanded = usePreference("explorerNodesExpanded");
  const workspaceExpanded = usePreference("explorerWorkspaceExpanded");
  const [validationExpanded, setValidationExpanded] = useState(false);
  const [projectTransferNotice, setProjectTransferNotice] = useState<string | null>(
    null,
  );
  const [folderOpenBusy, setFolderOpenBusy] = useState<string | null>(null);
  const [nodeContextMenu, setNodeContextMenu] = useState<NodeMenuTarget | null>(
    null,
  );
  const [projectContextMenu, setProjectContextMenu] =
    useState<ProjectMenuTarget | null>(null);
  const [treeContextMenu, setTreeContextMenu] = useState<{
    x: number;
    y: number;
  } | null>(null);
  const customStatuses = graph?.ui?.customStatuses ?? [];

  const projectByName = useMemo(
    () => new Map(projects.map((project) => [project.name, project])),
    [projects],
  );

  const { expandedProjects, persistExpandedProjects, toggleProject } =
    useProjectExpansion(activeProject, projectByName);
  const {
    query,
    setQuery,
    normalizedQuery,
    userNames,
    activeNodes,
    visibleNodes,
    projectSort,
    projectOrder,
    visibleProjects,
  } = useExplorerFilter(
    graph,
    projects,
    activeProject,
    projectByName,
    expandedProjects,
  );
  const {
    selectedNodeIDs,
    setSelectedNodeIDs,
    toggleNodeSelection,
    selectedProjectNames,
    setSelectedProjectNames,
    toggleProjectSelection,
  } = useExplorerSelection(
    graph?.nodes,
    projects,
    visibleNodes,
    visibleProjects,
  );

  const transfer = useProjectTransfer({
    graph,
    activeProject,
    expandedProjects,
    persistExpandedProjects,
    setProjectTransferNotice,
  });
  const create = useProjectCreate({
    projectByName,
    expandedProjects,
    persistExpandedProjects,
    setProjectTransferNotice,
  });
  const rename = useProjectRename({
    projectByName,
    expandedProjects,
    persistExpandedProjects,
    setProjectTransferNotice,
  });
  const saveAs = useSaveAs({
    activeProject,
    projectByName,
    expandedProjects,
    persistExpandedProjects,
    setProjectTransferNotice,
  });
  const picker = useWorkspacePicker({ setProjectTransferNotice });
  const {
    projectDropTarget,
    setProjectDropTarget,
    projectDropEdge,
    setProjectDropEdge,
    moveProjectTo,
    moveProjectAcrossWorkspace,
    reorderProject,
  } = useProjectDrag({
    workspace,
    switchingWorkspace,
    setSwitchingWorkspace,
    expandedProjects,
    persistExpandedProjects,
    setProjectTransferNotice,
    projects,
    projectSort,
    projectOrder,
    setProjectSort: (sort) => updateUIPreference("explorerProjectSort", sort),
  });

  const workspaceName =
    workspace.split(/[\\/]/).filter(Boolean).at(-1) || "workspace";

  /**
   * The menu's half of a move: ask for a destination, then reuse the same
   * moveProjectTo the drag drop calls.
   *
   * A project cannot land inside itself or anything under it, so both are kept
   * out of the picker — the server would refuse it, but offering a choice that
   * can only fail is worse than not offering it. The moves run in sequence
   * rather than together: each one renames a directory the next one's path may
   * be expressed against, and the notice each writes would otherwise arrive in
   * an order that does not match what happened.
   */
  const moveProjectsInteractively = async (target: ProjectMenuTarget) => {
    const moving = target.names.length > 0 ? target.names : [target.name];
    const parent = await pickProject({
      title: moving.length > 1 ? `搬移 ${moving.length} 個項目` : `搬移「${target.label}」`,
      description: "選擇要搬去的位置。內容不會變動，只是換一個位置。",
      confirmLabel: "搬移",
      includeFolders: true,
      rootLabel: workspaceName,
      exclude: projects
        .filter((project) =>
          moving.some(
            (name) => project.name === name || project.name.startsWith(name + "/"),
          ),
        )
        .map((project) => project.name),
    });
    if (parent === null) return;
    for (const name of moving) {
      await moveProjectTo(name, parent === "." ? "" : parent);
    }
  };
  const workspaceRoots =
    workspaces.length > 0
      ? workspaces
      : workspace
        ? [
            {
              path: workspace,
              label: workspaceName,
              active: true,
              projects: projects.length,
            },
          ]
        : [];

  useEffect(() => {
    setValidationExpanded(issues.length > 0);
  }, [activeProject, issues.length > 0]);

  const toggleNodes = () => {
    updateUIPreference("explorerNodesExpanded", !nodesExpanded);
  };

  const toggleWorkspaceExpanded = () => {
    updateUIPreference("explorerWorkspaceExpanded", !workspaceExpanded);
  };

  const openProject = async (name: string) => {
    if (switchingProject) return;
    const next = new Set(expandedProjects);
    next.add(name);
    persistExpandedProjects(next);
    setSwitchingProject(name);
    try {
      await switchProject(name);
    } finally {
      setSwitchingProject(null);
    }
  };

  const openFolderInExplorer = async (path: string, label: string) => {
    if (!path || folderOpenBusy) return;
    setFolderOpenBusy(path);
    try {
      await api.openFolder(path);
      setProjectTransferNotice(`已在檔案總管開啟：${label}`);
    } catch (error) {
      setProjectTransferNotice(`無法開啟資料夾：${(error as Error).message}`);
      reportError(error);
    } finally {
      setFolderOpenBusy(null);
    }
  };

  return (
    <aside
      className="sidebar explorer-sidebar"
      onContextMenu={(event) => {
        const target = event.target as Element;
        // Rows with their own menus (projects, node files) stop propagation;
        // form fields keep the native menu for copy/paste.
        if (target.closest("input, textarea, select, .lane-context-menu")) return;
        event.preventDefault();
        setTreeContextMenu({ x: event.clientX, y: event.clientY });
      }}
    >
      <section className="explorer">
        <div className="explorer-heading">
          <div>
            <h3>專案總管</h3>
            <span className="explorer-workspace-label">{workspaceName}</span>
          </div>
          <span className="explorer-project-count">{projects.length} 個專案</span>
        </div>
        {canEdit && (
        <div className="explorer-create-actions" aria-label="專案與工作區工具">
          <button
            type="button"
            className="primary"
            onClick={() => create.beginProjectCreate({ mode: "project", parent: "" })}
          >
            <IconPlus size={13} />
            新專案
          </button>
          <button
            type="button"
            onClick={() =>
              create.beginProjectCreate({
                mode: "project",
                parent: activeProject,
              })
            }
            disabled={!activeProject}
          >
            <IconFolder size={13} />
            新增子專案
          </button>
          <label className="explorer-sort">
            <span className="visually-hidden">專案排序方式</span>
            <select
              value={projectSort}
              aria-label="專案排序方式"
              onChange={(event) =>
                updateUIPreference(
                  "explorerProjectSort",
                  event.target.value as ProjectSort,
                )
              }
            >
              {(Object.keys(PROJECT_SORT_LABELS) as ProjectSort[]).map((sort) => (
                <option key={sort} value={sort}>
                  {PROJECT_SORT_LABELS[sort]}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => transfer.markdownImportInputRef.current?.click()}
            disabled={transfer.markdownImportBusy || !activeProject}
            title="匯入 Markdown 檔案為目前專案的節點"
          >
            <IconImport size={13} />
            {transfer.markdownImportBusy ? "匯入中…" : "匯入 MD"}
          </button>
          <button
            type="button"
            className="workspace-connect-action"
            onClick={picker.openWorkspacePicker}
            disabled={picker.importPathBusy}
            title="啟動後連接另一個本機或已掛載的雲端工作區"
          >
            <IconFolderOpen size={13} />
            連接工作區
          </button>
        </div>
        )}
        {canEdit && (
          <>
            <ProjectTransferBar
              transfer={transfer}
              activeProject={activeProject}
              setProjectTransferNotice={setProjectTransferNotice}
              openSaveAs={saveAs.openSaveAs}
              saveAsBusy={saveAs.saveAsBusy}
            />
            <ProjectCreatePanel
              create={create}
              projects={projects}
              projectByName={projectByName}
            />
            <ProjectRenamePanel rename={rename} />
            <SaveAsPanel saveAs={saveAs} activeProject={activeProject} />
            <WorkspacePickerPanel picker={picker} />
          </>
        )}
        {projectTransferNotice && (
          <div className="project-transfer-notice" role="status">
            {projectTransferNotice}
          </div>
        )}
        <input
          className="explorer-search"
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜尋專案／目前節點"
          aria-label="搜尋專案或目前節點"
        />

      <WorkspaceTree
        workspace={workspace}
        workspaceRoots={workspaceRoots}
        switchingWorkspace={switchingWorkspace}
        setSwitchingWorkspace={setSwitchingWorkspace}
        switchWorkspace={switchWorkspace}
        workspaceExpanded={workspaceExpanded}
        toggleWorkspaceExpanded={toggleWorkspaceExpanded}
        projects={projects}
        visibleProjects={visibleProjects}
        activeProject={activeProject}
        switchingProject={switchingProject}
        openProject={openProject}
        toggleProject={toggleProject}
        expandedProjects={expandedProjects}
        beginProjectCreate={create.beginProjectCreate}
        moveProjectTo={moveProjectTo}
        moveProjectAcrossWorkspace={moveProjectAcrossWorkspace}
        projectDropTarget={projectDropTarget}
        setProjectDropTarget={setProjectDropTarget}
        projectDropEdge={projectDropEdge}
        setProjectDropEdge={setProjectDropEdge}
        reorderProject={reorderProject}
        setProjectContextMenu={setProjectContextMenu}
        selectedProjectNames={selectedProjectNames}
        toggleProjectSelection={toggleProjectSelection}
        selectedNodeIDs={selectedNodeIDs}
        toggleNodeSelection={toggleNodeSelection}
        statuses={statuses}
        customStatuses={customStatuses}
        issues={issues}
        userNames={userNames}
        activeNodes={activeNodes}
        visibleNodes={visibleNodes}
        nodesExpanded={nodesExpanded}
        toggleNodes={toggleNodes}
        normalizedQuery={normalizedQuery}
        activeTab={activeTab}
        openTab={openTabAndCollapse}
        setNodeContextMenu={setNodeContextMenu}
        nodeFolders={nodeFolders}
        nodeFolderOf={nodeFolderOf}
        createNodeFolder={(path) => void createNodeFolder(path).catch(reportError)}
        renameNodeFolder={(path, name) =>
          void renameNodeFolder(path, name).catch(reportError)
        }
        deleteNodeFolder={(path) => void deleteNodeFolder(path).catch(reportError)}
        moveNodeFolder={(path, parent) =>
          void moveNodeFolder(path, parent).catch(reportError)
        }
        moveNodesToFolder={(ids, folder) =>
          void moveNodesToFolder(ids, folder).catch(reportError)
        }
        trash={trash}
        restoreTrash={restoreTrash}
        validationExpanded={validationExpanded}
        setValidationExpanded={setValidationExpanded}
      />
      </section>

      {treeContextMenu && (
        <TreeContextMenu
          menu={treeContextMenu}
          onClose={() => setTreeContextMenu(null)}
          workspace={workspace}
          workspaceName={workspaceName}
          workspaceRoots={workspaceRoots}
          activeProject={activeProject}
          openFolderInExplorer={openFolderInExplorer}
          folderOpenBusy={folderOpenBusy}
          beginProjectCreate={create.beginProjectCreate}
          openWorkspacePicker={picker.openWorkspacePicker}
          importPathBusy={picker.importPathBusy}
          transfer={transfer}
          setProjectTransferNotice={setProjectTransferNotice}
        />
      )}

      {nodeContextMenu && (
        <NodeContextMenu
          menu={nodeContextMenu}
          onClose={() => setNodeContextMenu(null)}
          openTab={openTabAndCollapse}
          clearNodeSelection={() => setSelectedNodeIDs([])}
        />
      )}

      {projectContextMenu && (
        <ProjectContextMenu
          menu={projectContextMenu}
          onClose={() => setProjectContextMenu(null)}
          activeProject={activeProject}
          expandedProjects={expandedProjects}
          persistExpandedProjects={persistExpandedProjects}
          clearProjectSelection={() => setSelectedProjectNames([])}
          setProjectTransferNotice={setProjectTransferNotice}
          openFolderInExplorer={openFolderInExplorer}
          folderOpenBusy={folderOpenBusy}
          exportProjectArchive={transfer.exportProjectArchive}
          projectTransferBusy={transfer.projectTransferBusy}
          projectRenameBusy={rename.projectRenameBusy}
          beginProjectCreate={create.beginProjectCreate}
          transfer={transfer}
          onRename={(target) => {
            rename.beginProjectRename(target);
            setProjectContextMenu(null);
          }}
          onMove={(target) => {
            setProjectContextMenu(null);
            void moveProjectsInteractively(target);
          }}
        />
      )}

      <StatusLegend />
    </aside>
  );
}
