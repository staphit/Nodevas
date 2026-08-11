/**
 * Timeline cell context menu [B-06].
 *
 * Schedules an expected milestone on the day that was right-clicked, including
 * creating a project-wide milestone type on first use.
 */

import { planStatusLabel } from "../../plan";
import type { Graph, PlanStatus, PlanStatusDefinition } from "../../types";
import type { LaneContextMenu } from "../LaneView";

export interface PlanMenuProps {
  contextMenu: Extract<LaneContextMenu, { kind: "plan" }>;
  nodeTitle: (id: string | undefined) => string;
  graph: Graph | null;
  planStatusDefinitions: PlanStatusDefinition[];
  planNote: string;
  setPlanNote: (note: string) => void;
  planTime: string;
  setPlanTime: (time: string) => void;
  planCustomLabel: string;
  setPlanCustomLabel: (label: string) => void;
  planError: string | null;
  setPlanError: (error: string | null) => void;
  upsertPlan: (nodeId: string, date: string, status: PlanStatus) => void;
  removePlan: (nodeId: string, status: PlanStatus) => void;
  addCustomPlanStatusFromMenu: (nodeId: string, date: string) => void;
}

export function PlanMenu({
  contextMenu,
  nodeTitle,
  graph,
  planStatusDefinitions,
  planNote,
  setPlanNote,
  planTime,
  setPlanTime,
  planCustomLabel,
  setPlanCustomLabel,
  planError,
  setPlanError,
  upsertPlan,
  removePlan,
  addCustomPlanStatusFromMenu,
}: PlanMenuProps) {
  return (
    <>
      <div className="lane-context-title">
        {nodeTitle(contextMenu.nodeId)} · {contextMenu.date}
      </div>
      <label className="lane-context-field">
        規劃註解
        <input
          value={planNote}
          placeholder="例如：等待設計確認"
          onChange={(event) => {
            setPlanNote(event.target.value);
            setPlanError(null);
          }}
        />
      </label>
      <label className="lane-context-field">
        時間（選填，精確到分鐘）
        <input
          type="time"
          value={planTime}
          onChange={(event) => {
            setPlanTime(event.target.value);
            setPlanError(null);
          }}
        />
      </label>
      <button
        type="button"
        role="menuitem"
        onClick={() => upsertPlan(contextMenu.nodeId, contextMenu.date, "started")}
      >
        設為開始
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => upsertPlan(contextMenu.nodeId, contextMenu.date, "done")}
      >
        設為死線
      </button>
      {planStatusDefinitions.map((definition) => (
        <button
          type="button"
          role="menuitem"
          key={definition.id}
          onClick={() =>
            upsertPlan(
              contextMenu.nodeId,
              contextMenu.date,
              definition.id,
            )
          }
        >
          設為{definition.label}
        </button>
      ))}
      <div className="lane-context-field plan-custom-add">
        <input
          value={planCustomLabel}
          placeholder="新自訂狀態，例如：送審"
          onChange={(event) => {
            setPlanCustomLabel(event.target.value);
            setPlanError(null);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              addCustomPlanStatusFromMenu(contextMenu.nodeId, contextMenu.date);
            }
          }}
        />
        <button
          type="button"
          role="menuitem"
          onClick={() =>
            addCustomPlanStatusFromMenu(contextMenu.nodeId, contextMenu.date)
          }
        >
          ＋新增並設為此狀態
        </button>
      </div>
      {planError && <div className="lane-context-error">{planError}</div>}
      {(graph?.ui?.plans?.[contextMenu.nodeId] ?? [])
        .filter((plan) => plan.date === contextMenu.date)
        .map((plan) => (
          <button
            type="button"
            role="menuitem"
            className="danger"
            key={`remove-${plan.status}`}
            onClick={() => removePlan(contextMenu.nodeId, plan.status)}
          >
            移除{planStatusLabel(plan.status, planStatusDefinitions)}
          </button>
        ))}
    </>
  );
}
