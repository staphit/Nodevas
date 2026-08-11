/**
 * The right-click menu on a node file row [B-06].
 *
 * The menu acts on the whole tree selection when the click landed inside it, so
 * every action here has to cope with more than one node.
 */

import { useEffect, useState } from "react";
import { IconDoc, IconPages, IconTrash } from "../../icons";
import { reportError, useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";

export type NodeMenuTarget = {
  nodeId: string;
  title: string;
  /** Same rule as the project menu: one node, or the whole selection. */
  ids: string[];
  x: number;
  y: number;
};

export function NodeContextMenu({
  menu,
  onClose,
  openTab,
  clearNodeSelection,
}: {
  menu: NodeMenuTarget;
  onClose: () => void;
  openTab: (id: string) => Promise<void>;
  clearNodeSelection: () => void;
}) {
  const duplicateNode = useApp((state) => state.duplicateNode);
  const deleteNodes = useApp((state) => state.deleteNodes);
  const [nodeMenuBusy, setNodeMenuBusy] = useState(false);

  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      const target = event.target as Element | null;
      if (!target?.closest(".node-context-menu")) onClose();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("pointerdown", closeOutside);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [menu]);

  // Deleting from the tree uses the same single-request path as the canvas.
  const removeSelectedNodes = async (ids: string[]) => {
    if (ids.length === 0 || nodeMenuBusy) return;
    const many = ids.length > 1;
    const title = menu.title;
    const confirmed = await confirmAction({
      title: many ? `刪除 ${ids.length} 個節點` : `刪除「${title}」？`,
      description: many
        ? `確定將選取的 ${ids.length} 個節點移到垃圾桶？一次撤銷即可全部復原。`
        : "節點會移到垃圾桶，可隨時復原。",
      confirmLabel: "移到垃圾桶",
      tone: "danger",
    });
    if (!confirmed) return;
    setNodeMenuBusy(true);
    onClose();
    try {
      await deleteNodes(ids);
      clearNodeSelection();
    } catch (error) {
      reportError(error);
    } finally {
      setNodeMenuBusy(false);
    }
  };

  return (
    <div
      className="project-context-menu node-context-menu"
      role="menu"
      aria-label={`${menu.title} 檔案操作`}
      style={{
        left: Math.max(8, Math.min(menu.x, window.innerWidth - 282)),
        top: Math.max(8, Math.min(menu.y, window.innerHeight - 210)),
      }}
      onContextMenu={(event) => event.preventDefault()}
    >
      <div className="project-context-heading">
        <IconDoc size={14} />
        <span>
          <b>
            {menu.ids.length > 1
              ? `${menu.ids.length} 個節點`
              : menu.title}
          </b>
          <small>
            {menu.ids.length > 1
              ? menu.ids.join("、")
              : "節點文件"}
          </small>
        </span>
      </div>
      {/* Opening or copying only makes sense for one node, so a batch
          selection shows just the action that does. */}
      {menu.ids.length === 1 && (
        <>
          <button
            type="button"
            role="menuitem"
            disabled={nodeMenuBusy}
            onClick={() => {
              void openTab(menu.nodeId).catch(reportError);
              onClose();
            }}
          >
            <span className="project-context-icon">
              <IconDoc size={14} />
            </span>
            <span>
              <b>開啟</b>
              <small>在資訊欄開啟文件</small>
            </span>
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={nodeMenuBusy}
            onClick={() => {
              setNodeMenuBusy(true);
              void duplicateNode(menu.nodeId)
                .then((newID) => void openTab(newID).catch(reportError))
                .catch(reportError)
                .finally(() => {
                  setNodeMenuBusy(false);
                  onClose();
                });
            }}
          >
            <span className="project-context-icon">
              <IconPages size={14} />
            </span>
            <span>
              <b>建立副本</b>
              <small>複製節點與文件內容</small>
            </span>
          </button>
          <div className="project-context-separator" />
        </>
      )}
      <button
        type="button"
        role="menuitem"
        className="danger"
        disabled={nodeMenuBusy}
        onClick={() => void removeSelectedNodes(menu.ids)}
      >
        <span className="project-context-icon">
          <IconTrash size={14} />
        </span>
        <span>
          <b>
            {menu.ids.length > 1
              ? `刪除 ${menu.ids.length} 個節點`
              : "刪除"}
          </b>
          <small>
            {menu.ids.length > 1
              ? "一次移到垃圾桶，一次撤銷即可全部復原"
              : "移到垃圾桶，可從側欄還原"}
          </small>
        </span>
      </button>
    </div>
  );
}
