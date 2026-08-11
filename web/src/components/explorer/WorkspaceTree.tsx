/**
 * Workspace tree [B-06].
 *
 * The explorer's tree: workspace roots → projects → the active project's nodes
 * and trash. Presentational apart from reading nothing itself — every action
 * and every piece of state arrives as a prop, so the sidebar keeps ownership of
 * drag state and context menus.
 */

import type { CSSProperties } from "react";
import type { ProjectEntry, TrashItem, WorkspaceEntry } from "../../state/types";
import type { GraphNode, Issue, Status, StatusDefinition } from "../../types";
import {
  IconCheck,
  IconDoc,
  IconFolder,
  IconFolderOpen,
  IconPlus,
  IconRestore,
  IconTrash,
  IconWarning,
} from "../../icons";
import { reportError } from "../../store";
import { StatusShape } from "../../statusTheme";
import { NODE_DRAG_TYPE, NodeFolderTree } from "./NodeFolderTree";
import type { ProjectDropEdge } from "./useProjectDrag";

/**
 * How much of a row's height each edge claims. A quarter top and bottom leaves
 * the middle half for the existing reparenting drop, which is the harder target
 * to hit precisely and the more destructive of the two.
 */
const EDGE_ZONE = 0.25;

/** Which half of a row the pointer is nearer, or null while it is in the middle. */
function dropEdgeAt(row: HTMLElement, clientY: number): boolean | null {
  const rect = row.getBoundingClientRect();
  const offset = (clientY - rect.top) / (rect.height || 1);
  if (offset <= EDGE_ZONE) return true;
  if (offset >= 1 - EDGE_ZONE) return false;
  return null;
}

/** Mirrors the sidebar's own menu/creation shapes. */
export type ProjectCreateTarget = { mode: "project" | "folder"; parent: string };

export type ProjectContextMenu = {
  name: string;
  label: string;
  path: string;
  isFolder: boolean;
  childCount: number;
  /** Every project the menu acts on: the right-clicked one, or the whole
   * Ctrl-selection when the click landed inside it. */
  names: string[];
  x: number;
  y: number;
};

/** What the pointer's modifier keys mean for a tree selection. */
export type SelectionModifiers = { additive: boolean; range: boolean };

export type NodeContextMenu = {
  nodeId: string;
  title: string;
  /** Same rule as the project menu: one node, or the whole selection. */
  ids: string[];
  x: number;
  y: number;
};

export interface WorkspaceTreeProps {
  workspace: string;
  workspaceRoots: WorkspaceEntry[];
  switchingWorkspace: string | null;
  setSwitchingWorkspace: (path: string | null) => void;
  switchWorkspace: (path: string) => Promise<void>;
  /** Whether the open workspace shows its project list. */
  workspaceExpanded: boolean;
  toggleWorkspaceExpanded: () => void;

  /**
   * Ctrl/⌘ click builds these, Shift extends from the last click, and a plain
   * click collapses them to one item.
   */
  selectedProjectNames: string[];
  toggleProjectSelection: (name: string, modifiers: SelectionModifiers) => void;
  selectedNodeIDs: string[];
  toggleNodeSelection: (id: string, modifiers: SelectionModifiers) => void;

  projects: ProjectEntry[];
  visibleProjects: ProjectEntry[];
  activeProject: string;
  switchingProject: string | null;
  openProject: (name: string) => Promise<void>;
  toggleProject: (name: string) => void;
  expandedProjects: Set<string>;
  beginProjectCreate: (target: ProjectCreateTarget) => void;
  moveProjectTo: (name: string, newParent: string) => Promise<void>;
  moveProjectAcrossWorkspace: (
    name: string,
    targetWorkspace: string,
    targetLabel: string,
  ) => Promise<void>;
  projectDropTarget: string | null;
  setProjectDropTarget: (
    update: string | null | ((current: string | null) => string | null),
  ) => void;
  /**
   * Manual ordering [B-03]. A drop on the middle of a row still reparents; only
   * the top and bottom edges reorder, and only among siblings.
   */
  projectDropEdge: ProjectDropEdge | null;
  setProjectDropEdge: (edge: ProjectDropEdge | null) => void;
  reorderProject: (
    moved: string,
    target: string,
    before: boolean,
  ) => Promise<void>;
  setProjectContextMenu: (menu: ProjectContextMenu | null) => void;

  statuses: Record<string, Status>;
  customStatuses: StatusDefinition[];
  issues: Issue[];
  userNames: Map<string, string>;
  activeNodes: GraphNode[];
  visibleNodes: GraphNode[];
  nodesExpanded: boolean;
  toggleNodes: () => void;
  normalizedQuery: string;
  activeTab: string | null;
  openTab: (id: string) => Promise<void>;
  setNodeContextMenu: (menu: NodeContextMenu | null) => void;

  /**
   * Folders that group node files [B-06]. They organise the explorer only —
   * none of these actions reads or writes the graph.
   */
  nodeFolders: string[];
  nodeFolderOf: Record<string, string>;
  createNodeFolder: (path: string) => void;
  renameNodeFolder: (path: string, name: string) => void;
  deleteNodeFolder: (path: string) => void;
  moveNodeFolder: (path: string, parent: string) => void;
  moveNodesToFolder: (ids: string[], folder: string) => void;

  trash: TrashItem[];
  restoreTrash: (file: string) => Promise<void>;
  validationExpanded: boolean;
  setValidationExpanded: (open: boolean) => void;
}

export function WorkspaceTree({
  workspace,
  workspaceRoots,
  switchingWorkspace,
  setSwitchingWorkspace,
  switchWorkspace,
  workspaceExpanded,
  toggleWorkspaceExpanded,
  projects,
  visibleProjects,
  activeProject,
  switchingProject,
  openProject,
  toggleProject,
  expandedProjects,
  beginProjectCreate,
  moveProjectTo,
  moveProjectAcrossWorkspace,
  projectDropTarget,
  setProjectDropTarget,
  projectDropEdge,
  setProjectDropEdge,
  reorderProject,
  setProjectContextMenu,
  selectedProjectNames,
  toggleProjectSelection,
  selectedNodeIDs,
  toggleNodeSelection,
  statuses,
  customStatuses,
  issues,
  userNames,
  activeNodes,
  visibleNodes,
  nodesExpanded,
  toggleNodes,
  normalizedQuery,
  activeTab,
  openTab,
  setNodeContextMenu,
  nodeFolders,
  nodeFolderOf,
  createNodeFolder,
  renameNodeFolder,
  deleteNodeFolder,
  moveNodeFolder,
  moveNodesToFolder,
  trash,
  restoreTrash,
  validationExpanded,
  setValidationExpanded,
}: WorkspaceTreeProps) {
  return (
    <div className="workspace-tree" role="tree" aria-label="工作區檔案樹">
      {workspaceRoots.map((root) => {
        const rootActive = root.path === workspace || root.active;
        // Only the open workspace has a project list to show, so only it can
        // be expanded; the others stay closed until they are opened.
        const expanded = rootActive && workspaceExpanded;
        const switching = switchingWorkspace === root.path;
        return (
          <div className="workspace-root-group" key={root.path}>
            <div
              className={`workspace-root${rootActive ? " active" : ""}${
                projectDropTarget ===
                (rootActive ? "." : `workspace:${root.path}`)
                  ? " drop-target"
                  : ""
              }`}
              role="treeitem"
              tabIndex={0}
              aria-expanded={expanded}
              aria-busy={switching}
              title={
                rootActive
                  ? `${root.path}（點擊收合或展開；可把子專案拖到這裡移到最上層）`
                  : `${root.path}（點擊開啟；拖入專案可搬到此工作區）`
              }
              onClick={() => {
                if (rootActive) {
                  toggleWorkspaceExpanded();
                  return;
                }
                if (switchingWorkspace) return;
                // Opening a workspace should show it, even if the previous one
                // was left collapsed.
                if (!workspaceExpanded) toggleWorkspaceExpanded();
                setSwitchingWorkspace(root.path);
                void switchWorkspace(root.path)
                  .catch(reportError)
                  .finally(() => setSwitchingWorkspace(null));
              }}
              onKeyDown={(event) => {
                if (event.key !== "Enter" && event.key !== " ") return;
                event.preventDefault();
                event.currentTarget.click();
              }}
              onDragOver={(event) => {
                if (!event.dataTransfer.types.includes("application/x-nodevas-project"))
                  return;
                event.preventDefault();
                event.dataTransfer.dropEffect = "move";
                setProjectDropTarget(
                  rootActive ? "." : `workspace:${root.path}`,
                );
              }}
              onDragLeave={() =>
                setProjectDropTarget((target) =>
                  target === (rootActive ? "." : `workspace:${root.path}`)
                    ? null
                    : target,
                )
              }
              onDrop={(event) => {
                event.preventDefault();
                event.stopPropagation();
                const name = event.dataTransfer.getData(
                  "application/x-nodevas-project",
                );
                if (rootActive) {
                  void moveProjectTo(name, "");
                } else {
                  void moveProjectAcrossWorkspace(name, root.path, root.label);
                }
              }}
            >
              <span className={`tree-chevron${expanded ? " open" : ""}`}>
                {expanded ? "⌄" : "›"}
              </span>
              {rootActive ? (
                <IconFolderOpen size={15} />
              ) : (
                <IconFolder size={15} />
              )}
              <b>{root.label}</b>
              <span className="tree-count">{root.projects}</span>
              {switching && <span className="tree-loading">…</span>}
            </div>

            {expanded && <ul className="project-tree">
        {visibleProjects.map((project) => {
          const active = project.name === activeProject;
          const expanded = expandedProjects.has(project.name);
          const switching = switchingProject === project.name;
          // Only the open project has its nodes loaded, so expanding any other
          // one can reveal nothing but its sub-projects. Without a row saying
          // so, the chevron looks broken.
          const hasChildProjects = projects.some(
            (candidate) => candidate.parent === project.name,
          );
          const revealsNothing = expanded && !hasChildProjects && !active;
          return (
            <li
              className={`project-tree-item${active ? " active" : ""}`}
              key={project.name}
              role="treeitem"
              aria-expanded={expanded}
              style={
                {
                  "--project-depth": project.depth,
                } as CSSProperties
              }
            >
              <div
                className={`project-tree-row${
                  projectDropTarget === project.name ? " drop-target" : ""
                }${selectedProjectNames.includes(project.name) ? " selected" : ""}${
                  projectDropEdge?.name === project.name
                    ? projectDropEdge.before
                      ? " drop-before"
                      : " drop-after"
                    : ""
                }`}
                title={`${project.path}（右鍵開啟專案選單；拖到列上搬移層級，拖到列的上下緣調整順序，Alt + ↑／↓ 也可以）`}
                draggable
                onDragStart={(event) => {
                  event.dataTransfer.setData(
                    "application/x-nodevas-project",
                    project.name,
                  );
                  event.dataTransfer.effectAllowed = "move";
                }}
                onDragEnd={() => {
                  setProjectDropTarget(null);
                  setProjectDropEdge(null);
                }}
                onDragOver={(event) => {
                  if (
                    !event.dataTransfer.types.includes("application/x-nodevas-project")
                  )
                    return;
                  event.preventDefault();
                  event.stopPropagation();
                  event.dataTransfer.dropEffect = "move";
                  const before = dropEdgeAt(event.currentTarget, event.clientY);
                  if (before === null) {
                    setProjectDropEdge(null);
                    setProjectDropTarget(project.name);
                    return;
                  }
                  setProjectDropTarget(null);
                  setProjectDropEdge({ name: project.name, before });
                }}
                onDragLeave={() => {
                  setProjectDropTarget((target) =>
                    target === project.name ? null : target,
                  );
                  if (projectDropEdge?.name === project.name)
                    setProjectDropEdge(null);
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  const name = event.dataTransfer.getData(
                    "application/x-nodevas-project",
                  );
                  const before = dropEdgeAt(event.currentTarget, event.clientY);
                  if (!name || name === project.name) {
                    setProjectDropTarget(null);
                    setProjectDropEdge(null);
                    return;
                  }
                  if (before === null) void moveProjectTo(name, project.name);
                  else void reorderProject(name, project.name, before);
                }}
                onContextMenu={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  // Right-clicking inside a multi-selection acts on all of it;
                  // right-clicking outside one narrows the selection first.
                  const inSelection = selectedProjectNames.includes(project.name);
                  const names =
                    inSelection && selectedProjectNames.length > 1
                      ? selectedProjectNames
                      : [project.name];
                  if (names.length === 1) {
                    toggleProjectSelection(project.name, {
                      additive: false,
                      range: false,
                    });
                  }
                  setProjectContextMenu({
                    name: project.name,
                    label: project.label,
                    path: project.path,
                    isFolder: Boolean(project.isFolder),
                    childCount: projects.filter((candidate) =>
                      candidate.name.startsWith(`${project.name}/`),
                    ).length,
                    names,
                    x: event.clientX,
                    y: event.clientY,
                  });
                }}
              >
                <button
                  type="button"
                  className="project-tree-toggle"
                  onClick={() => toggleProject(project.name)}
                  aria-label={`${expanded ? "收合" : "展開"} ${project.label}`}
                >
                  <span className={`tree-chevron${expanded ? " open" : ""}`}>
                    {expanded ? "⌄" : "›"}
                  </span>
                </button>
                {expanded ? (
                  <IconFolderOpen size={14} />
                ) : (
                  <IconFolder size={14} />
                )}
                <button
                  type="button"
                  className="project-tree-open"
                  onClick={(event) => {
                    // Ctrl/⌘ and Shift pick projects for a batch action
                    // instead of opening one.
                    const additive = event.ctrlKey || event.metaKey;
                    if (additive || event.shiftKey) {
                      toggleProjectSelection(project.name, {
                        additive,
                        range: event.shiftKey,
                      });
                      return;
                    }
                    toggleProjectSelection(project.name, {
                      additive: false,
                      range: false,
                    });
                    if (project.isFolder) {
                      toggleProject(project.name);
                      return;
                    }
                    void openProject(project.name).catch(reportError);
                  }}
                  onKeyDown={(event) => {
                    // Dragging is pointer-only, so the same reordering has to be
                    // reachable from the row that already holds keyboard focus.
                    if (!event.altKey) return;
                    if (event.key !== "ArrowUp" && event.key !== "ArrowDown")
                      return;
                    const up = event.key === "ArrowUp";
                    const siblings = visibleProjects.filter(
                      (candidate) =>
                        (candidate.parent ?? "") === (project.parent ?? ""),
                    );
                    const at = siblings.findIndex(
                      (candidate) => candidate.name === project.name,
                    );
                    const neighbour = siblings[at + (up ? -1 : 1)];
                    if (!neighbour) return;
                    event.preventDefault();
                    void reorderProject(project.name, neighbour.name, up);
                  }}
                  disabled={Boolean(switchingProject) && !project.isFolder}
                  aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                  title={
                    project.isFolder
                      ? "工作區目錄（分組用，不可開啟）"
                      : "Ctrl／⌘ + 點擊多選，Shift + 點擊選取範圍，右鍵批次操作，Alt + ↑／↓ 調整順序"
                  }
                >
                  <span className="project-tree-name">{project.label}</span>
                  {project.isFolder && (
                    <span className="current-project-tag folder-tag">目錄</span>
                  )}
                  {active && <span className="current-project-tag">目前</span>}
                </button>
                {!project.isFolder && (
                  <span className="tree-count">{project.nodes}</span>
                )}
                <button
                  type="button"
                  className="project-tree-add"
                  onClick={() =>
                    beginProjectCreate({
                      mode: "project",
                      parent: project.name,
                    })
                  }
                  aria-label={`在 ${project.label} 新增子專案`}
                  title="新增子專案"
                >
                  <IconPlus size={12} />
                </button>
                {switching && <span className="tree-loading">…</span>}
              </div>

              {revealsNothing && (
                <div className="project-tree-children" role="group">
                  {project.isFolder ? (
                    <div className="tree-folder-row tree-empty-row">
                      <span className="tree-indent" />
                      <span>空目錄（可把專案拖進來）</span>
                    </div>
                  ) : (
                    <button
                      type="button"
                      className="tree-folder-row tree-empty-row"
                      onClick={() =>
                        void openProject(project.name).catch(reportError)
                      }
                      disabled={Boolean(switchingProject)}
                      title="節點只在開啟的專案載入"
                    >
                      <span className="tree-indent" />
                      <span>開啟此專案以檢視節點</span>
                    </button>
                  )}
                </div>
              )}

              {active && expanded && (
                <div className="project-tree-children" role="group">
                  <button
                    type="button"
                    className="tree-folder-row"
                    onClick={toggleNodes}
                    aria-expanded={nodesExpanded}
                  >
                    <span className={`tree-chevron${nodesExpanded ? " open" : ""}`}>
                      {nodesExpanded ? "⌄" : "›"}
                    </span>
                    {nodesExpanded ? (
                      <IconFolderOpen size={13} />
                    ) : (
                      <IconFolder size={13} />
                    )}
                    <span>節點</span>
                    <span className="tree-count">{activeNodes.length}</span>
                  </button>

                  {nodesExpanded && (
                    <NodeFolderTree
                      folders={nodeFolders}
                      folderOf={nodeFolderOf}
                      visibleNodes={visibleNodes}
                      searching={Boolean(normalizedQuery)}
                      createFolder={createNodeFolder}
                      renameFolder={renameNodeFolder}
                      deleteFolder={deleteNodeFolder}
                      moveFolder={moveNodeFolder}
                      moveNodes={moveNodesToFolder}
                      renderNode={(node) => {
                        const status = statuses[node.id] ?? "ready";
                        return (
                          <li key={node.id}>
                            <button
                              type="button"
                              className={`${activeTab === node.id ? "active" : ""}${
                                selectedNodeIDs.includes(node.id) ? " selected" : ""
                              }`}
                              draggable
                              onDragStart={(event) => {
                                // Dragging one of several selected nodes moves
                                // the whole selection, matching the project tree.
                                const ids = selectedNodeIDs.includes(node.id)
                                  ? selectedNodeIDs
                                  : [node.id];
                                event.dataTransfer.setData(
                                  NODE_DRAG_TYPE,
                                  JSON.stringify(ids),
                                );
                                event.dataTransfer.effectAllowed = "move";
                              }}
                              onClick={(event) => {
                                const additive = event.ctrlKey || event.metaKey;
                                if (additive || event.shiftKey) {
                                  toggleNodeSelection(node.id, {
                                    additive,
                                    range: event.shiftKey,
                                  });
                                  return;
                                }
                                toggleNodeSelection(node.id, {
                                  additive: false,
                                  range: false,
                                });
                                void openTab(node.id).catch(reportError);
                              }}
                              onContextMenu={(event) => {
                                event.preventDefault();
                                event.stopPropagation();
                                const inSelection = selectedNodeIDs.includes(node.id);
                                const ids =
                                  inSelection && selectedNodeIDs.length > 1
                                    ? selectedNodeIDs
                                    : [node.id];
                                if (ids.length === 1) {
                                  toggleNodeSelection(node.id, {
                                    additive: false,
                                    range: false,
                                  });
                                }
                                setNodeContextMenu({
                                  nodeId: node.id,
                                  title: node.title || node.id,
                                  ids,
                                  x: event.clientX,
                                  y: event.clientY,
                                });
                              }}
                              title={`${
                                node.title || node.id
                              }（Ctrl／⌘ + 點擊多選，Shift + 點擊選取範圍，右鍵開啟檔案選單）`}
                            >
                              <span className="tree-indent" />
                              <IconDoc size={13} />
                              <span className="node-list-copy">
                                <span className="node-list-title">
                                  {node.title || node.id}
                                </span>
                                <span
                                  className={`node-list-assignee${
                                    node.assignee ? "" : " unassigned"
                                  }`}
                                >
                                  {node.id}.md ·{" "}
                                  {(node.assignee &&
                                    userNames.get(node.assignee)) ||
                                    "尚未指派"}
                                </span>
                              </span>
                              <StatusShape
                                status={status}
                                definitions={customStatuses}
                              />
                            </button>
                          </li>
                        );
                      }}
                    />
                  )}

                  <details
                    className="tree-special-folder"
                    open={validationExpanded}
                    onToggle={(event) =>
                      setValidationExpanded(
                        (event.currentTarget as HTMLDetailsElement).open,
                      )
                    }
                  >
                    <summary>
                      <span className="tree-chevron">›</span>
                      <IconWarning size={12} />
                      <span>驗證</span>
                      <span className="tree-count">{issues.length}</span>
                    </summary>
                    {issues.length === 0 ? (
                      <div className="tree-ok-msg">
                        <IconCheck size={12} /> 沒有問題
                      </div>
                    ) : (
                      <ul className="issue-list">
                        {issues.map((issue, index) => (
                          <li
                            key={index}
                            className={`issue-${issue.severity}`}
                            onClick={() =>
                              issue.nodeId &&
                              void openTab(issue.nodeId).catch(reportError)
                            }
                          >
                            <IconWarning size={12} />
                            <span>
                              <b className="mono">{issue.nodeId ?? "graph"}</b>
                              {issue.field ? `.${issue.field}` : ""}: {issue.msg}
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </details>

                  {trash.length > 0 && (
                    <details className="tree-special-folder">
                      <summary>
                        <span className="tree-chevron">›</span>
                        <IconTrash size={12} />
                        <span>垃圾桶</span>
                        <span className="tree-count">{trash.length}</span>
                      </summary>
                      <ul className="trash-list">
                        {trash.map((item) => (
                          <li key={item.file}>
                            <span className="trash-id">
                              {item.kind === "page" ? (
                                <>
                                  <b>{item.title || item.nodeId}</b>
                                  <small>{item.parentId} 的子頁</small>
                                </>
                              ) : (
                                <span className="mono">{item.nodeId}.md</span>
                              )}
                            </span>
                            <button
                              type="button"
                              onClick={() =>
                                void restoreTrash(item.file).catch(reportError)
                              }
                              title="復原此節點"
                            >
                              <IconRestore size={12} />
                              復原
                            </button>
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                </div>
              )}
            </li>
          );
        })}
        {visibleProjects.length === 0 && (
          <li className="tree-empty project-empty">沒有符合的專案</li>
        )}
            </ul>}
          </div>
        );
      })}
    </div>
  );
}
