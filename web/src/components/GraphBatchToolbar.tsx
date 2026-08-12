/** Multi-selection actions on the canvas [B-06]. */

import { reportError, useApp } from "../store";
import type { Graph, Status, StatusDefinition } from "../types";
import { confirmAction } from "./ConfirmDialog";
import { nodeLabels, sendNodesToProject } from "./canvas/nodeTransfer";
import { localizedStatusLabel, useI18n } from "../i18n";

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
  setGraphSelection: (selection: { kind: "node"; id: string } | null) => void;
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
  const clipboard = useApp((state) => state.nodeClipboard);
  const clearNodeClipboard = useApp((state) => state.clearNodeClipboard);

  const transferSelection = async (mode: "copy" | "cut") => {
    setShortcutBusy(true);
    try {
      const result = await sendNodesToProject(
        selectedIDs,
        nodeLabels(graph, selectedIDs),
        mode,
      );
      if (result && mode === "cut") {
        setGraphSelection(null);
        onSelectedNodeChange?.(null);
      }
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
      setGraphSelection(null);
      onSelectedNodeChange?.(null);
    } catch (error) {
      reportError(error);
    } finally {
      setShortcutBusy(false);
    }
  };
  return (
    <div className="graph-batch-toolbar">
      <b>{t("batch.selectedNodes", { count: String(selectedIDs.length) })}</b>
      <button
        type="button"
        disabled={selectedIDs.length !== 1 || shortcutBusy}
        onClick={() => {
          const id = selectedIDs[0];
          if (!id) return;
          setShortcutBusy(true);
          void duplicateNode(id)
            .then((newID) => {
              setGraphSelection({ kind: "node", id: newID });
              onSelectedNodeChange?.(newID);
            })
            .catch(reportError)
            .finally(() => setShortcutBusy(false));
        }}
      >
        {t("batch.duplicate")}
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title={t("batch.groupBackgroundTitle")}
        onClick={createGroupFromSelection}
      >
        {t("batch.groupBackground")}
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title={t("batch.copyProjectTitle")}
        onClick={() => void transferSelection("copy")}
      >
        {t("batch.copyProject")}
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title={t("batch.moveProjectTitle")}
        onClick={() => void transferSelection("cut")}
      >
        {t("batch.moveProject")}
      </button>
      {clipboard && (
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
      )}
      <label>
        {t("batch.status")}
        <select
          defaultValue=""
          disabled={shortcutBusy}
          onChange={(event) => {
            if (event.target.value) applyBatchStatus(event.target.value as Status);
            event.target.value = "";
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
        {t("batch.assignee")}
        <select
          defaultValue=""
          onChange={(event) => applyBatchAssignee(event.target.value)}
        >
          <option value="">{t("graphTools.unassigned")}</option>
          {(graph?.users ?? []).map((user) => (
            <option key={user.id} value={user.id}>
              {user.name}
            </option>
          ))}
        </select>
      </label>
      <button
        type="button"
        className="danger"
        disabled={shortcutBusy}
        title={t("batch.deleteTitle")}
        onClick={() => void removeSelection()}
      >
        {t("batch.delete")}
      </button>
    </div>
  );
}
