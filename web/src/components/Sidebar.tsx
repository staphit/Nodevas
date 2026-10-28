import { useCallback, useEffect, useId, useMemo, useState } from "react";
import {
  IconCompass,
  IconFolder,
  IconFolderOpen,
  IconImport,
  IconMore,
  IconPlus,
} from "../icons";
import { OPEN_START_SCREEN_EVENT, useNewProjectRequest } from "./StartScreen";
import { api } from "../api";
import { reportError, useApp, usePreference } from "../store";
import { useI18n } from "../i18n";
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
import {
  NodeContextMenu,
  type NodeMenuTarget,
} from "./explorer/NodeContextMenu";
import {
  ProjectContextMenu,
  type ProjectMenuTarget,
} from "./explorer/ProjectContextMenu";
import { pickProject } from "./ProjectPickerDialog";
import { useViewer } from "./SignIn";

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
  const { t } = useI18n();
  const graph = useApp((state) => state.graph);
  const statuses = useApp((state) => state.statuses);
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
  const toolsExpanded = usePreference("explorerToolsExpanded");
  // Copying an example in creates a project, an administrator's call on a
  // team server; a standalone install has no viewer and may.
  const viewer = useViewer();
  const canCreateProjects = !viewer || viewer.role === "admin";
  const toolsId = useId();
  const workspaceExpanded = usePreference("explorerWorkspaceExpanded");
  // The notice line says what the last project action did, so it belongs to
  // the project open when it was said: opening another (a switch, an example)
  // clears it. Actions that open a project set it once that one is open.
  const [projectNotice, setProjectNotice] = useState<{
    text: string;
    project: string;
  } | null>(null);
  const setProjectTransferNotice = useCallback(
    (text: string | null) =>
      setProjectNotice(
        text === null ? null : { text, project: useApp.getState().activeProject },
      ),
    [],
  );
  const projectTransferNotice =
    projectNotice?.project === activeProject ? projectNotice.text : null;
  useEffect(() => {
    if (projectNotice && projectNotice.project !== activeProject) setProjectNotice(null);
  }, [projectNotice, activeProject]);
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
    activeTab,
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

  // "Start with a blank project" from the start screen, in a workspace that
  // already has work in it: the same form the New project button opens.
  const newProjectRequested = useNewProjectRequest((state) => state.pending);
  const beginProjectCreate = create.beginProjectCreate;
  useEffect(() => {
    if (!newProjectRequested) return;
    useNewProjectRequest.getState().set(false);
    beginProjectCreate({ mode: "project", parent: "" });
  }, [newProjectRequested, beginProjectCreate]);

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
      title:
        moving.length > 1
          ? t("sidebar.moveTitlePlural", { count: String(moving.length) })
          : t("sidebar.moveTitle", { label: target.label }),
      description: t("sidebar.moveDescription"),
      confirmLabel: t("sidebar.move"),
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
      setProjectTransferNotice(t("sidebar.folderOpened", { label }));
    } catch (error) {
      setProjectTransferNotice(t("sidebar.folderOpenFailed", { error: (error as Error).message }));
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
            <h3>{t("sidebar.title")}</h3>
            <span className="explorer-workspace-label">{workspaceName}</span>
          </div>
          <span className="explorer-project-count">
            {t("sidebar.projectCount", { count: String(projects.length) })}
          </span>
          {/* Folding the explorer lives with the explorer; the board's header
              has the matching » to bring it back. */}
          <button
            type="button"
            className="explorer-collapse"
            onClick={() => {
              updateUIPreference("explorerCollapsed", true);
              // The explorer, this button with it, is about to unmount; the
              // board header's » is the way back, so the keyboard waits there.
              requestAnimationFrame(() =>
                document.querySelector<HTMLElement>(".board-explorer-toggle")?.focus(),
              );
            }}
            title={t("board.collapseExplorer")}
            aria-label={t("board.collapseExplorer")}
            aria-expanded
            aria-controls="project-explorer"
          >
            «
          </button>
        </div>
        {/* One action up front — a first project is all a new workspace needs —
            and the rest one press away. The fold is remembered, so someone
            who uses them keeps them open. */}
        <div className="explorer-primary-actions">
          <button
            type="button"
            className="primary"
            onClick={() => create.beginProjectCreate({ mode: "project", parent: "" })}
          >
            <IconPlus size={13} />
            {t("sidebar.newProject")}
          </button>
          <button
            type="button"
            className={`explorer-more-toggle${toolsExpanded ? " active" : ""}`}
            onClick={() => updateUIPreference("explorerToolsExpanded", !toolsExpanded)}
            aria-expanded={toolsExpanded}
            aria-controls={toolsId}
            title={t("sidebar.moreTools")}
            aria-label={t("sidebar.moreTools")}
          >
            <IconMore size={15} />
          </button>
        </div>
        {toolsExpanded && (
        <div id={toolsId} className="explorer-create-actions" aria-label={t("sidebar.tools")}>
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
            {t("sidebar.newSubproject")}
          </button>
          <label className="explorer-sort">
            <span className="visually-hidden">{t("sidebar.sortProjects")}</span>
            <select
              value={projectSort}
              aria-label={t("sidebar.sortProjects")}
              onChange={(event) =>
                updateUIPreference(
                  "explorerProjectSort",
                  event.target.value as ProjectSort,
                )
              }
            >
              {(Object.keys(PROJECT_SORT_LABELS) as ProjectSort[]).map((sort) => (
                <option key={sort} value={sort}>
                  {t(PROJECT_SORT_LABELS[sort])}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => transfer.markdownImportInputRef.current?.click()}
            disabled={transfer.markdownImportBusy || !activeProject}
            title={t("sidebar.importMdTitle")}
          >
            <IconImport size={13} />
            {transfer.markdownImportBusy ? t("sidebar.importingMd") : t("sidebar.importMd")}
          </button>
          <button
            type="button"
            className="workspace-connect-action"
            onClick={picker.openWorkspacePicker}
            disabled={picker.importPathBusy}
            title={t("sidebar.connectWorkspaceTitle")}
          >
            <IconFolderOpen size={13} />
            {t("sidebar.connectWorkspace")}
          </button>
          {canCreateProjects && (
          <button
            type="button"
            className="explorer-example-action"
            onClick={() => window.dispatchEvent(new Event(OPEN_START_SCREEN_EVENT))}
            title={t("sidebar.openExampleTitle")}
          >
            <IconCompass size={13} />
            {t("sidebar.openExample")}
          </button>
          )}
        </div>
        )}
        <ProjectTransferBar
          showActions={toolsExpanded}
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
          placeholder={t("sidebar.searchPlaceholder")}
          aria-label={t("sidebar.searchLabel")}
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
