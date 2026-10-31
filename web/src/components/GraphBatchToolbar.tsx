/**
 * Multi-selection actions on the canvas [B-06].
 *
 * Shown for two or more nodes only: one node has its drawer and its own menu,
 * and a bar of batch actions over a single card only repeated them. The row
 * keeps what is done to a whole selection — mark, assign, send elsewhere,
 * delete, let go — and the two rarer ones (duplicate, group background) wait
 * in its `⋯`.
 */

import { reportError, useApp } from "../store";
import type { Graph, Status, StatusDefinition } from "../types";
import type { GraphSelection } from "./LaneView";
import { confirmAction } from "./ConfirmDialog";
import { nodeLabels, sendNodesToProject } from "./canvas/nodeTransfer";
import { OverflowMenu } from "./menus/OverflowMenu";
import { localizedStatusLabel, useI18n } from "../i18n";

/** The assignee option that clears the field: "" is the select's placeholder. */
const UNASSIGNED = "__unassigned__";

export interface GraphBatchToolbarProps {
  graph: Graph | null;
  customStatuses: StatusDefinition[];
  selectableStatuses: Status[];
  selectedIDs: string[];
  shortcutBusy: boolean;
  setShortcutBusy: (busy: boolean) => void;
  applyBatchStatus: (status: Status) => void;
  applyBatchAssignee: (assignee: string) => void;
  createGroupFromSelection: () => void;
  duplicateNode: (id: string) => Promise<string>;
  deleteNodes: (ids: string[]) => Promise<void>;
  setGraphSelection: (selection: GraphSelection) => void;
  onSelectedNodeChange?: (nodeId: string | null) => void;
}

export function GraphBatchToolbar({
  graph,
  customStatuses,
  selectableStatuses,
  selectedIDs,
  shortcutBusy,
  setShortcutBusy,
  applyBatchStatus,
  applyBatchAssignee,
  createGroupFromSelection,
  duplicateNode,
  deleteNodes,
  setGraphSelection,
  onSelectedNodeChange,
}: GraphBatchToolbarProps) {
  const { t } = useI18n();

  const clearSelection = () => {
    setGraphSelection(null);
    onSelectedNodeChange?.(null);
  };

  const transferSelection = async (mode: "copy" | "cut") => {
    setShortcutBusy(true);
    try {
      const result = await sendNodesToProject(
        selectedIDs,
        nodeLabels(graph, selectedIDs),
        mode,
      );
      if (result && mode === "cut") clearSelection();
    } catch (error) {
      reportError(error);
    } finally {
      setShortcutBusy(false);
    }
  };

  // One copy per selected node, in order, and the copies become the selection
  // so the next action lands on what was just made.
  const duplicateSelection = async () => {
    setShortcutBusy(true);
    try {
      const copies: string[] = [];
      for (const id of selectedIDs) copies.push(await duplicateNode(id));
      setGraphSelection(
        copies.length === 1 ? { kind: "node", id: copies[0] } : { kind: "nodes", ids: copies },
      );
      onSelectedNodeChange?.(copies.length === 1 ? copies[0] : null);
    } catch (error) {
      reportError(error);
    } finally {
      setShortcutBusy(false);
    }
  };

  const removeSelection = async () => {
    const confirmed = await confirmAction({
      title: t("batch.deleteConfirmTitle", { count: String(selectedIDs.length) }),
      description:
        t("batch.deleteConfirmDescription", { count: String(selectedIDs.length) }),
      confirmLabel: t("batch.moveToTrash"),
      tone: "danger",
    });
    if (!confirmed) return;
    setShortcutBusy(true);
    try {
      await deleteNodes(selectedIDs);
      clearSelection();
    } catch (error) {
      reportError(error);
    } finally {
      setShortcutBusy(false);
    }
  };

  return (
    <div
      className="graph-batch-toolbar"
      role="toolbar"
      aria-label={t("batch.selectedNodes", { count: String(selectedIDs.length) })}
    >
      <b>{t("batch.selectedNodes", { count: String(selectedIDs.length) })}</b>
      {/* Both selects act on choice and fall back to their placeholder, so the
          same value can be applied twice and the row never shows a stale pick. */}
      <label>
        {t("board.batch.markStatus")}
        <select
          value=""
          disabled={shortcutBusy}
          onChange={(event) => {
            if (event.target.value) applyBatchStatus(event.target.value as Status);
          }}
        >
          <option value="">{t("batch.statusChange")}</option>
          {selectableStatuses.map((status) => (
            <option key={status} value={status}>
              {localizedStatusLabel(status, customStatuses)}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t("board.batch.assign")}
        <select
          value=""
          disabled={shortcutBusy}
          onChange={(event) => {
            const value = event.target.value;
            if (!value) return;
            applyBatchAssignee(value === UNASSIGNED ? "" : value);
          }}
        >
          <option value="">{t("board.batch.choose")}</option>
          <option value={UNASSIGNED}>{t("graphTools.unassigned")}</option>
          {(graph?.users ?? []).map((user) => (
            <option key={user.id} value={user.id}>
              {user.name}
            </option>
          ))}
        </select>
      </label>
      <OverflowMenu
        className="board-overflow"
        triggerClassName="graph-batch-menu-trigger"
        placement="fixed"
        align="start"
        label={t("board.batch.transfer")}
        trigger={<span>{t("board.batch.transfer")} ▾</span>}
        items={[
          {
            id: "move",
            label: t("batch.moveProject"),
            title: t("batch.moveProjectTitle"),
            disabled: shortcutBusy,
            onSelect: () => void transferSelection("cut"),
          },
          {
            id: "copy",
            label: t("batch.copyProject"),
            title: t("batch.copyProjectTitle"),
            disabled: shortcutBusy,
            onSelect: () => void transferSelection("copy"),
          },
        ]}
      />
      <button
        type="button"
        className="danger"
        disabled={shortcutBusy}
        title={t("batch.deleteTitle")}
        onClick={() => void removeSelection()}
      >
        {t("batch.delete")}
      </button>
      <OverflowMenu
        className="board-overflow"
        triggerClassName="graph-batch-menu-trigger"
        placement="fixed"
        label={t("board.batch.more")}
        items={[
          {
            id: "duplicate",
            label: t("batch.duplicate"),
            disabled: shortcutBusy,
            onSelect: () => void duplicateSelection(),
          },
          {
            id: "group",
            label: t("batch.groupBackground"),
            title: t("batch.groupBackgroundTitle"),
            disabled: shortcutBusy,
            onSelect: createGroupFromSelection,
          },
        ]}
      />
      <NodeClipboardNotice />
      <span className="graph-batch-spacer" aria-hidden />
      <button type="button" onClick={clearSelection}>
        {t("board.batch.clearSelection")}
      </button>
    </div>
  );
}

/**
 * What Ctrl+C / Ctrl+X is holding, so a paste in another project is expected.
 *
 * Inline in the batch toolbar while two or more nodes are selected; on its
 * own as a thin `strip` otherwise, because what was copied stays armed after
 * the selection that copied it is gone.
 */
export function NodeClipboardNotice({ strip = false }: { strip?: boolean }) {
  const { t } = useI18n();
  const clipboard = useApp((state) => state.nodeClipboard);
  const clearNodeClipboard = useApp((state) => state.clearNodeClipboard);
  if (!clipboard) return null;
  const notice = (
    <span className="graph-batch-clipboard">
      {t("batch.clipboard", {
        action: clipboard.mode === "cut" ? t("batch.cut") : t("batch.copied"),
        count: String(clipboard.ids.length),
        project: clipboard.project,
      })}{" "}
      <button
        type="button"
        title={t("batch.clearClipboard")}
        onClick={() => clearNodeClipboard()}
      >
        {t("batch.clear")}
      </button>
    </span>
  );
  return strip ? <div className="graph-clipboard-strip">{notice}</div> : notice;
}
