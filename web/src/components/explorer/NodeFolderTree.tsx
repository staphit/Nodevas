/**
 * Node folders in the explorer [B-06].
 *
 * Folders group node documents on disk and nothing more: the graph never
 * mentions them, so dragging a node between folders changes where the file
 * lives and leaves every edge, requirement and logic gate exactly as it was.
 * That is worth stating in the UI too, which is why the drop hints talk about
 * files rather than relationships.
 *
 * Presentational, like the rest of the tree: the folder list, the assignments
 * and every action arrive as props. The only state it owns is what a folder
 * row is doing right now — being renamed, or waiting for a new child's name.
 */

import { useState, type ReactNode } from "react";
import type { GraphNode } from "../../types";
import { IconFolder, IconFolderOpen, IconPlus, IconTrash } from "../../icons";
import { useI18n } from "../../i18n";

/** Drag payload keys, distinct from the project tree's own. */
export const NODE_DRAG_TYPE = "application/x-nodevas-nodes";
export const FOLDER_DRAG_TYPE = "application/x-nodevas-node-folder";

export interface NodeFolderTreeProps {
  /** Every folder path under nodes/, parents first. */
  folders: string[];
  /** Node id → folder path; nodes at the root are absent. */
  folderOf: Record<string, string>;
  /** The nodes the search filter left visible, already ordered. */
  visibleNodes: GraphNode[];
  /** While searching, folders are ignored and every hit is listed flat. */
  searching: boolean;
  /** Renders one node row; owned by the tree so the row markup stays in one place. */
  renderNode: (node: GraphNode) => ReactNode;
  createFolder: (path: string) => void;
  renameFolder: (path: string, name: string) => void;
  deleteFolder: (path: string) => void;
  moveFolder: (path: string, parent: string) => void;
  moveNodes: (ids: string[], folder: string) => void;
}

/** Direct children of a folder; "" is the root of nodes/. */
function childrenOf(folders: string[], parent: string): string[] {
  const prefix = parent ? `${parent}/` : "";
  return folders.filter(
    (folder) =>
      folder.startsWith(prefix) &&
      folder.length > prefix.length &&
      !folder.slice(prefix.length).includes("/"),
  );
}

function isInside(candidate: string, folder: string): boolean {
  return candidate === folder || candidate.startsWith(`${folder}/`);
}

function folderName(path: string): string {
  const cut = path.lastIndexOf("/");
  return cut === -1 ? path : path.slice(cut + 1);
}

/** Reads the dragged node ids, tolerating a payload from an older tab. */
function draggedNodeIDs(data: string): string[] {
  try {
    const parsed = JSON.parse(data);
    return Array.isArray(parsed) ? parsed.filter((id) => typeof id === "string") : [];
  } catch {
    return data ? [data] : [];
  }
}

export function NodeFolderTree({
  folders,
  folderOf,
  visibleNodes,
  searching,
  renderNode,
  createFolder,
  renameFolder,
  deleteFolder,
  moveFolder,
  moveNodes,
}: NodeFolderTreeProps) {
  const { t } = useI18n();
  // Collapsed rather than expanded, so a folder created during this session is
  // open without having to be added to anything.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  /** Folder whose new child is being named; "" is a new top-level folder. */
  const [creatingIn, setCreatingIn] = useState<string | null>(null);
  const [draft, setDraft] = useState("");

  const nodesIn = (folder: string) =>
    visibleNodes.filter((node) => (folderOf[node.id] ?? "") === folder);

  const beginCreate = (parent: string) => {
    setCreatingIn(parent);
    setDraft("");
    setCollapsed((current) => {
      if (!parent || !current.has(parent)) return current;
      const next = new Set(current);
      next.delete(parent);
      return next;
    });
  };

  const commitCreate = (parent: string) => {
    const name = draft.trim();
    setCreatingIn(null);
    setDraft("");
    if (name) createFolder(parent ? `${parent}/${name}` : name);
  };

  const commitRename = (path: string) => {
    const name = draft.trim();
    setRenaming(null);
    setDraft("");
    if (name && name !== folderName(path)) renameFolder(path, name);
  };

  /** Shared drop behaviour: a folder row and the root row accept the same things. */
  const dropProps = (folder: string) => ({
    onDragOver: (event: React.DragEvent) => {
      const types = event.dataTransfer.types;
      if (!types.includes(NODE_DRAG_TYPE) && !types.includes(FOLDER_DRAG_TYPE)) return;
      event.preventDefault();
      event.stopPropagation();
      event.dataTransfer.dropEffect = "move";
      setDropTarget(folder);
    },
    onDragLeave: () =>
      setDropTarget((current) => (current === folder ? null : current)),
    onDrop: (event: React.DragEvent) => {
      event.preventDefault();
      event.stopPropagation();
      setDropTarget(null);
      const nodePayload = event.dataTransfer.getData(NODE_DRAG_TYPE);
      if (nodePayload) {
        const ids = draggedNodeIDs(nodePayload);
        if (ids.length) moveNodes(ids, folder);
        return;
      }
      const dragged = event.dataTransfer.getData(FOLDER_DRAG_TYPE);
      // Dropping a folder into itself or its own subtree would orphan it.
      if (dragged && !isInside(folder, dragged)) moveFolder(dragged, folder);
    },
  });

  const nameInput = (onCommit: () => void, onCancel: () => void, label: string) => (
    <input
      className="tree-folder-input"
      autoFocus
      value={draft}
      aria-label={label}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={onCommit}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          onCommit();
        }
        if (event.key === "Escape") {
          event.preventDefault();
          onCancel();
        }
      }}
    />
  );

  const renderFolder = (path: string, depth: number): ReactNode => {
    const open = !collapsed.has(path);
    const contents = nodesIn(path);
    const subfolders = childrenOf(folders, path);
    return (
      <li key={path} className="node-folder" role="treeitem" aria-expanded={open}>
        <div
          className={`tree-folder-row node-folder-row${
            dropTarget === path ? " drop-target" : ""
          }`}
          style={{ paddingInlineStart: `${depth * 12}px` }}
          draggable={renaming !== path}
          onDragStart={(event) => {
            event.stopPropagation();
            event.dataTransfer.setData(FOLDER_DRAG_TYPE, path);
            event.dataTransfer.effectAllowed = "move";
          }}
          onDragEnd={() => setDropTarget(null)}
          {...dropProps(path)}
          title={t("explorer.folderDropHint", { path })}
        >
          <button
            type="button"
            className="tree-folder-toggle"
            onClick={() =>
              setCollapsed((current) => {
                const next = new Set(current);
                if (next.has(path)) next.delete(path);
                else next.add(path);
                return next;
              })
            }
            aria-label={`${open ? t("explorer.collapse") : t("explorer.expand")} ${folderName(path)}`}
          >
            <span className={`tree-chevron${open ? " open" : ""}`}>
              {open ? "⌄" : "›"}
            </span>
          </button>
          {open ? <IconFolderOpen size={13} /> : <IconFolder size={13} />}
          {renaming === path ? (
            nameInput(
              () => commitRename(path),
              () => {
                setRenaming(null);
                setDraft("");
              },
              t("explorer.folderName"),
            )
          ) : (
            <button
              type="button"
              className="node-folder-name"
              onDoubleClick={() => {
                setRenaming(path);
                setDraft(folderName(path));
              }}
              onClick={() =>
                setCollapsed((current) => {
                  const next = new Set(current);
                  if (next.has(path)) next.delete(path);
                  else next.add(path);
                  return next;
                })
              }
              title={t("explorer.doubleClickRename")}
            >
              {folderName(path)}
            </button>
          )}
          <span className="tree-count">{contents.length}</span>
          <button
            type="button"
            className="project-tree-add"
            onClick={() => beginCreate(path)}
            aria-label={t("explorer.addChildFolderAria", { label: folderName(path) })}
            title={t("explorer.newFolderHere")}
          >
            <IconPlus size={12} />
          </button>
          <button
            type="button"
            className="node-folder-delete"
            onClick={() => deleteFolder(path)}
            aria-label={t("explorer.deleteFolderAria", { label: folderName(path) })}
            title={t("explorer.deleteFolderTitle")}
          >
            <IconTrash size={12} />
          </button>
        </div>
        {open && (
          <ul className="node-list tree-node-list" role="group">
            {creatingIn === path && (
              <li className="node-folder-new">
                {nameInput(
                  () => commitCreate(path),
                  () => {
                    setCreatingIn(null);
                    setDraft("");
                  },
                  t("explorer.newFolderName"),
                )}
              </li>
            )}
            {subfolders.map((child) => renderFolder(child, depth + 1))}
            {contents.map((node) => renderNode(node))}
            {contents.length === 0 && subfolders.length === 0 && creatingIn !== path && (
              <li className="tree-empty">{t("explorer.emptyFolderShort")}</li>
            )}
          </ul>
        )}
      </li>
    );
  };

  // A search is about finding a node, not about where it is filed, so the
  // folders step out of the way and every hit is listed flat.
  if (searching) {
    return (
      <ul className="node-list tree-node-list">
        {visibleNodes.map((node) => renderNode(node))}
        {visibleNodes.length === 0 && <li className="tree-empty">{t("explorer.noMatchingNodes")}</li>}
      </ul>
    );
  }

  const rootNodes = nodesIn("");
  const rootFolders = childrenOf(folders, "");
  return (
    <div
      className={`node-folder-root${dropTarget === "" ? " drop-target" : ""}`}
      {...dropProps("")}
    >
      <div className="node-folder-actions">
        <button
          type="button"
          className="node-folder-create"
          onClick={() => beginCreate("")}
          title={t("explorer.newFolderTitle")}
        >
          <IconPlus size={12} />
          {t("explorer.newFolderHere")}
        </button>
      </div>
      <ul className="node-list tree-node-list">
        {creatingIn === "" && (
          <li className="node-folder-new">
            {nameInput(
              () => commitCreate(""),
              () => {
                setCreatingIn(null);
                setDraft("");
              },
              t("explorer.newFolderName"),
            )}
          </li>
        )}
        {rootFolders.map((folder) => renderFolder(folder, 0))}
        {rootNodes.map((node) => renderNode(node))}
        {rootNodes.length === 0 && rootFolders.length === 0 && (
          <li className="tree-empty">{t("explorer.emptyFolderShort")}</li>
        )}
      </ul>
    </div>
  );
}

export { childrenOf as folderChildren, isInside as folderContains };
