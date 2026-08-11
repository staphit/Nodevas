/**
 * Node creation form [B-06].
 *
 * Opened from the toolbar button or the canvas menu. Expected milestones can be
 * scheduled while creating, so a new node arrives with its plan already set.
 *
 * Actual status and card appearance can be set here too, but folded away. Most
 * nodes are made by typing a title and pressing Enter, and putting six more
 * controls between the title field and the submit button would make the common
 * case pay for the rare one. The fold remembers its answer, so the rare case
 * only has to open it once.
 */

import { useId, type PointerEvent as ReactPointerEvent } from "react";
import { BUILTIN_PLAN_STATUSES } from "../../plan";
import { useApp, usePreference } from "../../store";
import { StatusShape, statusOptions, statusTheme } from "../../statusTheme";
import type {
  NodeStyle,
  PlanStatus,
  PlanStatusDefinition,
  Status,
  StatusDefinition,
} from "../../types";
import {
  AppearanceControls,
  mergeNodeStyle,
} from "../inspector/AppearanceControls";
import type { LaneContextMenu } from "../LaneView";

export interface NodeCreateMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "node-create" }>;
  planStatusDefinitions: PlanStatusDefinition[];
  /** Workspace statuses, so the picker offers exactly what the rest of the app does. */
  customStatuses: StatusDefinition[];
  nodeCreateTitle: string;
  setNodeCreateTitle: (title: string) => void;
  nodeCreateError: string | null;
  setNodeCreateError: (error: string | null) => void;
  nodeCreateBusy: boolean;
  nodeCreateStatus: Status | "";
  setNodeCreateStatus: (status: Status | "") => void;
  nodeCreateStyle: NodeStyle;
  setNodeCreateStyle: (update: (current: NodeStyle) => NodeStyle) => void;
  nodePlanDates: Partial<Record<PlanStatus, string>>;
  setNodePlanDates: (
    update: (
      current: Partial<Record<PlanStatus, string>>,
    ) => Partial<Record<PlanStatus, string>>,
  ) => void;
  nodeCustomPlanDrafts: { key: number; label: string; date: string }[];
  setNodeCustomPlanDrafts: (
    update: (
      current: { key: number; label: string; date: string }[],
    ) => { key: number; label: string; date: string }[],
  ) => void;
  customPlanDraftKey: { current: number };
  addGraphNode: (menu: Extract<LaneContextMenu, { kind: "node-create" }>) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  /** Makes the heading a handle for dragging the whole panel. */
  dragHandleProps: { onPointerDown: (event: ReactPointerEvent) => void };
}

export function NodeCreateMenu({
  contextMenu,
  planStatusDefinitions,
  customStatuses,
  nodeCreateTitle,
  setNodeCreateTitle,
  nodeCreateError,
  setNodeCreateError,
  nodeCreateBusy,
  nodeCreateStatus,
  setNodeCreateStatus,
  nodeCreateStyle,
  setNodeCreateStyle,
  nodePlanDates,
  setNodePlanDates,
  nodeCustomPlanDrafts,
  setNodeCustomPlanDrafts,
  customPlanDraftKey,
  addGraphNode,
  setContextMenu,
  dragHandleProps,
}: NodeCreateMenuProps) {
  const updateUIPreference = useApp((state) => state.updateUIPreference);
  const advancedOpen = usePreference("nodeCreateAdvanced");
  const advancedID = useId();
  const previewStatus: Status = nodeCreateStatus || "ready";

  return (
    <form
      className="node-create-form"
      onSubmit={(event) => {
        event.preventDefault();
        void addGraphNode(contextMenu);
      }}
    >
      {/* The form is tall enough to cover the spot it is about to create a node
          in, so its heading doubles as a handle for moving it aside. Dragging
          only moves the panel: the node still lands on the cell that was
          clicked, which is what the subtitle promises. */}
      <div className="node-create-heading" {...dragHandleProps}>
        <span className="node-create-icon" aria-hidden>＋</span>
        <span>
          <b>新增節點</b>
          <small>建立在目前點選的位置</small>
        </span>
      </div>
      <label className="lane-context-field node-create-field">
        節點標題
        <input
          autoFocus
          value={nodeCreateTitle}
          onChange={(event) => {
            setNodeCreateTitle(event.target.value);
            setNodeCreateError(null);
          }}
          placeholder="例如：第一章，進入王都"
          maxLength={256}
          disabled={nodeCreateBusy}
        />
      </label>
      <div className="node-create-id-hint">
        <span aria-hidden>#</span>
        ID 將由系統自動產生，不需要手動管理
      </div>
      <section className="node-create-plans">
        <div className="node-create-section-head">
          <span>
            <b>預期日期</b>
            <small>選填，不會改變實際狀態</small>
          </span>
          <button
            type="button"
            onClick={() =>
              setNodeCustomPlanDrafts((current) => [
                ...current,
                {
                  key: ++customPlanDraftKey.current,
                  label: "",
                  date: "",
                },
              ])
            }
            disabled={
              nodeCreateBusy ||
              planStatusDefinitions.length + nodeCustomPlanDrafts.length >= 64
            }
          >
            ＋ 自訂狀態
          </button>
        </div>
        <div className="node-create-plan-list">
          {[
            ...BUILTIN_PLAN_STATUSES,
            ...planStatusDefinitions.map((definition) => ({
              status: definition.id,
              label: definition.label,
            })),
          ].map(({ status, label }) => (
            <label className="node-create-plan-row" key={status}>
              <span>{label}</span>
              <input
                type="date"
                value={nodePlanDates[status] ?? ""}
                onChange={(event) =>
                  setNodePlanDates((current) => ({
                    ...current,
                    [status]: event.target.value,
                  }))
                }
                disabled={nodeCreateBusy}
              />
            </label>
          ))}
          {nodeCustomPlanDrafts.map((draft) => (
            <div className="node-create-custom-plan" key={draft.key}>
              <input
                aria-label="自訂狀態名稱"
                placeholder="狀態名稱"
                value={draft.label}
                maxLength={128}
                disabled={nodeCreateBusy}
                onChange={(event) =>
                  setNodeCustomPlanDrafts((current) =>
                    current.map((item) =>
                      item.key === draft.key
                        ? { ...item, label: event.target.value }
                        : item,
                    ),
                  )
                }
              />
              <input
                aria-label={`${draft.label || "自訂狀態"}預期日期`}
                type="date"
                value={draft.date}
                disabled={nodeCreateBusy}
                onChange={(event) =>
                  setNodeCustomPlanDrafts((current) =>
                    current.map((item) =>
                      item.key === draft.key
                        ? { ...item, date: event.target.value }
                        : item,
                    ),
                  )
                }
              />
              <button
                type="button"
                aria-label="移除自訂狀態"
                title="移除"
                disabled={nodeCreateBusy}
                onClick={() =>
                  setNodeCustomPlanDrafts((current) =>
                    current.filter((item) => item.key !== draft.key),
                  )
                }
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </section>
      {/* Folded by default: everything above is the fast path, everything
          inside is optional and can equally be set after the node exists. */}
      <section className="node-create-advanced">
        <button
          type="button"
          className="node-create-disclosure"
          aria-expanded={advancedOpen}
          aria-controls={advancedID}
          onClick={() => updateUIPreference("nodeCreateAdvanced", !advancedOpen)}
        >
          <span className="node-create-disclosure-mark" aria-hidden>
            {advancedOpen ? "▾" : "▸"}
          </span>
          <span>
            <b>外觀與狀態</b>
            <small>選填，之後也能在節點編輯中修改</small>
          </span>
        </button>
        <div id={advancedID} hidden={!advancedOpen} className="node-create-advanced-body">
          <label className="lane-context-field node-create-field">
            實際狀態
            <select
              value={nodeCreateStatus}
              disabled={nodeCreateBusy}
              onChange={(event) => setNodeCreateStatus(event.target.value as Status | "")}
            >
              <option value="">預設（未開始）</option>
              {statusOptions(customStatuses).map((candidate) => (
                <option key={candidate} value={candidate}>
                  {statusTheme(candidate, customStatuses).label}
                </option>
              ))}
            </select>
          </label>
          <div className="node-create-status-preview">
            <StatusShape status={previewStatus} definitions={customStatuses} />
            <span>
              建立後寫入 run/journal.jsonl，與之後手動改狀態同一條紀錄
            </span>
          </div>
          <AppearanceControls
            idPrefix={`node-create${advancedID}`}
            style={nodeCreateStyle}
            onChange={(patch) =>
              setNodeCreateStyle((current) => mergeNodeStyle(current, patch))
            }
            status={previewStatus}
            customStatuses={customStatuses}
            previewTitle={nodeCreateTitle || "新節點"}
            previewAssignee="尚未指派"
            disabled={nodeCreateBusy}
          />
        </div>
      </section>
      {nodeCreateError && <div className="lane-context-error">{nodeCreateError}</div>}
      <div className="node-create-actions">
        <button
          type="button"
          onClick={() => setContextMenu(null)}
          disabled={nodeCreateBusy}
        >
          取消
        </button>
        <button type="submit" className="primary" disabled={nodeCreateBusy}>
          {nodeCreateBusy ? "建立中…" : "建立節點"}
        </button>
      </div>
    </form>
  );
}
