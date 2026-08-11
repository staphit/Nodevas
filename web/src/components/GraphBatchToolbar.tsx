/** Multi-selection actions on the canvas [B-06]. */

import { reportError, useApp } from "../store";
import { statusTheme } from "../statusTheme";
import type { Graph, Status, StatusDefinition } from "../types";
import { confirmAction } from "./ConfirmDialog";
import { nodeLabels, sendNodesToProject } from "./canvas/nodeTransfer";

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
      title: `刪除 ${selectedIDs.length} 個節點`,
      description:
        `確定將選取的 ${selectedIDs.length} 個節點移到垃圾桶？` +
        "一次撤銷即可全部復原，也可以從側欄逐一還原。",
      confirmLabel: "移到垃圾桶",
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
      <b>{selectedIDs.length} 個節點</b>
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
        建立副本
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title="以選取節點的範圍建立群組底圖色塊"
        onClick={createGroupFromSelection}
      >
        建立群組底圖
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title="把選取的節點連同文件、附件、子頁面複製到另一個專案（Ctrl+C 可先暫存，切換專案後貼上）"
        onClick={() => void transferSelection("copy")}
      >
        複製到專案…
      </button>
      <button
        type="button"
        disabled={shortcutBusy}
        title="把選取的節點移到另一個專案，來源節點會進入垃圾桶（Ctrl+X 可先暫存）"
        onClick={() => void transferSelection("cut")}
      >
        移動到專案…
      </button>
      {clipboard && (
        <span className="graph-batch-clipboard">
          剪貼簿：{clipboard.mode === "cut" ? "已剪下" : "已複製"}{" "}
          {clipboard.ids.length} 個節點（{clipboard.project}）
          <button
            type="button"
            title="清空節點剪貼簿"
            onClick={() => clearNodeClipboard()}
          >
            清除
          </button>
        </span>
      )}
      <label>
        狀態
        <select
          defaultValue=""
          disabled={shortcutBusy}
          onChange={(event) => {
            if (event.target.value) applyBatchStatus(event.target.value as Status);
            event.target.value = "";
          }}
        >
          <option value="">批次切換…</option>
          {selectableStatuses.map((status) => (
            <option key={status} value={status}>
              {statusTheme(status, customStatuses).label}
            </option>
          ))}
        </select>
      </label>
      <label>
        負責人
        <select
          defaultValue=""
          onChange={(event) => applyBatchAssignee(event.target.value)}
        >
          <option value="">尚未指派</option>
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
        title="把選取的節點移到垃圾桶（也可以按 Delete）"
        onClick={() => void removeSelection()}
      >
        刪除
      </button>
    </div>
  );
}
