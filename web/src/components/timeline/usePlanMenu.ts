/**
 * The plan menu: what a right-click on a timeline cell can schedule.
 *
 * Scheduling an existing milestone, defining a brand-new custom status on the
 * spot, and clearing one all edit the same node's plan, and all report their
 * refusals into the menu rather than as a toast.
 */

import { useState } from "react";
import { localizePlanOrderError, useI18n } from "../../i18n";
import { nextCustomPlanStatusID, planOrderError } from "../../plan";
import type { Graph, PlanStatus, PlanStatusDefinition } from "../../types";
import type { PlanCommand, WorkflowCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import type { LaneContextMenu } from "../LaneView";

export function usePlanMenu({
  graph,
  planStatusDefinitions,
  updatePlan,
  updateWorkflowDefinition,
  setContextMenu,
}: {
  graph: Graph | null;
  planStatusDefinitions: PlanStatusDefinition[];
  updatePlan: (command: PlanCommand) => Promise<CommandResult>;
  updateWorkflowDefinition: (command: WorkflowCommand) => Promise<CommandResult>;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}) {
  const { t, language } = useI18n();
  const [planNote, setPlanNote] = useState("");
  const [planTime, setPlanTime] = useState("");
  const [planCustomLabel, setPlanCustomLabel] = useState("");
  const [planError, setPlanError] = useState<string | null>(null);

  const upsertPlan = (nodeId: string, date: string, status: PlanStatus) => {
    const current = graph?.ui?.plans?.[nodeId] ?? [];
    const error = planOrderError(current, status, date);
    if (error) {
      setPlanError(localizePlanOrderError(error, language));
      return;
    }
    const note = planNote.trim() || undefined;
    const time = planTime.trim() || undefined;
    void updatePlan({
      type: "plan.upsert",
      nodeId,
      milestone: { type: status, date, time, note },
    }).then((result) => {
      if (!result.ok) setPlanError(result.message);
    });
    setContextMenu(null);
  };

  // Creates a project-wide custom plan status and immediately schedules it on
  // the menu's date, so new statuses never exist without a first use.
  const addCustomPlanStatusFromMenu = (nodeId: string, date: string) => {
    const label = planCustomLabel.trim();
    if (!label) {
      setPlanError(t("planMenu.customNameRequired"));
      return;
    }
    const duplicate = planStatusDefinitions.some(
      (definition) =>
        definition.label.trim().toLocaleLowerCase() === label.toLocaleLowerCase(),
    );
    if (duplicate) {
      setPlanError(t("planMenu.duplicateCustomName"));
      return;
    }
    const definition: PlanStatusDefinition = {
      id: nextCustomPlanStatusID(planStatusDefinitions),
      label,
    };
    const note = planNote.trim() || undefined;
    const time = planTime.trim() || undefined;
    // Define the type first, then schedule it: a definition that nothing uses
    // is confusing, and the two writes are separately undoable.
    void updateWorkflowDefinition({
      type: "workflow.addMilestoneType",
      label: definition.label,
      id: definition.id,
    })
      .then((defined) => {
        if (!defined.ok) return defined;
        return updatePlan({
          type: "plan.upsert",
          nodeId,
          milestone: { type: definition.id, date, time, note },
        });
      })
      .then((result) => {
        if (!result.ok) setPlanError(result.message);
      });
    setPlanCustomLabel("");
    setContextMenu(null);
  };

  const openPlanMenu = (
    event: React.MouseEvent,
    nodeId: string,
    date: string,
    note = "",
    time = "",
  ) => {
    event.preventDefault();
    event.stopPropagation();
    setPlanNote(note);
    setPlanTime(time);
    setPlanError(null);
    setContextMenu({ kind: "plan", x: event.clientX, y: event.clientY, nodeId, date });
  };

  const removePlan = (nodeId: string, status: PlanStatus) => {
    void updatePlan({ type: "plan.remove", nodeId, milestoneType: status }).then(
      (result) => {
        if (!result.ok) setPlanError(result.message);
      },
    );
    setContextMenu(null);
  };

  return {
    planNote,
    setPlanNote,
    planTime,
    setPlanTime,
    planCustomLabel,
    setPlanCustomLabel,
    planError,
    setPlanError,
    upsertPlan,
    addCustomPlanStatusFromMenu,
    openPlanMenu,
    removePlan,
  };
}
