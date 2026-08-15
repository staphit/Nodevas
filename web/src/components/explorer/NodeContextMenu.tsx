/**
 * The right-click menu on a node file row [B-06].
 *
 * The menu acts on the whole tree selection when the click landed inside it, so
 * every action here has to cope with more than one node.
 */

import { useEffect, useState } from "react";
import { IconDoc, IconPages, IconTrash } from "../../icons";
import { useI18n } from "../../i18n";
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
  const { t } = useI18n();
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
      title: many
        ? t("explorer.deleteNodesTitleMany", { count: ids.length })
        : t("explorer.deleteNodeTitle", { title }),
      description: many
        ? t("explorer.deleteNodesDescriptionMany", { count: ids.length })
        : t("explorer.deleteNodeDescription"),
      confirmLabel: t("explorer.moveToTrash"),
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
      aria-label={t("explorer.nodeActions", { title: menu.title })}
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
              ? t("explorer.nodeCount", { count: menu.ids.length })
              : menu.title}
          </b>
          <small>
            {menu.ids.length > 1
              ? menu.ids.join(", ")
              : t("explorer.nodeDocument")}
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
              <b>{t("explorer.open")}</b>
              <small>{t("explorer.openInInspector")}</small>
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
              <b>{t("explorer.duplicate")}</b>
              <small>{t("explorer.duplicateHint")}</small>
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
              ? t("explorer.deleteNodesAction", { count: menu.ids.length })
              : t("explorer.delete")}
          </b>
          <small>
            {menu.ids.length > 1
              ? t("explorer.deleteNodesHint")
              : t("explorer.deleteNodeHint")}
          </small>
        </span>
      </button>
    </div>
  );
}
