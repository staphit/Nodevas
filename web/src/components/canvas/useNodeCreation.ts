/**
 * The new-node form on the canvas.
 *
 * The menu collects a title and, optionally, a date for each planned
 * milestone — including custom statuses that do not exist yet. Creating the
 * node defines those statuses and schedules them in one go, and refuses
 * dates that would put the lifecycle out of order, so a node is never created
 * half-planned.
 *
 * Looks and actual status can be chosen here too, so a node does not have to be
 * created and then opened to be finished. They live in two different stores and
 * so travel by two different routes: the card style is graph state, and rides
 * along in the graph write `createNode` already performs for position and plan;
 * the actual status is an append to `run/journal.jsonl`, which no graph write
 * can carry, so it is a second call — deliberately after the node exists, and
 * deliberately non-fatal, because a node that was created but whose status did
 * not stick is recoverable, and one that was never created is not.
 */

import { useRef, useState } from "react";
import { localizePlanOrderError, useI18n } from "../../i18n";
import {
  BUILTIN_PLAN_STATUSES,
  nextCustomPlanStatusID,
  planOrderError,
} from "../../plan";
import type {
  NodeStyle,
  PlanMilestone,
  PlanStatus,
  PlanStatusDefinition,
  Status,
} from "../../types";
import type { LaneContextMenu } from "../LaneView";

export function useNodeCreation({
  planStatusDefinitions,
  createNode,
  setLifecycleStatus,
  setGraphNotice,
  setContextMenu,
}: {
  planStatusDefinitions: PlanStatusDefinition[];
  createNode: (
    node: { title: string; kind: string; writeAccess?: string },
    options: {
      openDocument: boolean;
      position: { x: number; y: number };
      plans: PlanMilestone[];
      planStatuses?: PlanStatusDefinition[];
      style?: NodeStyle;
    },
  ) => Promise<string>;
  setLifecycleStatus: (
    nodeId: string,
    status: Status,
    note?: string,
  ) => Promise<{ ok: boolean; message?: string }>;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
}) {
  const { t, language } = useI18n();
  const [nodeCreateTitle, setNodeCreateTitle] = useState("");
  const [nodeCreateError, setNodeCreateError] = useState<string | null>(null);
  const [nodeCreateBusy, setNodeCreateBusy] = useState(false);
  // Empty means "leave the node at its default state", which is what pressing
  // Enter on a title alone must keep doing.
  const [nodeCreateStatus, setNodeCreateStatus] = useState<Status | "">("");
  // Empty means "everyone may write", the default a new node keeps.
  const [nodeCreateWriteAccess, setNodeCreateWriteAccess] = useState("");
  const [nodeCreateStyle, setNodeCreateStyle] = useState<NodeStyle>({});
  const [nodePlanDates, setNodePlanDates] = useState<
    Partial<Record<PlanStatus, string>>
  >({});
  const customPlanDraftKey = useRef(0);
  const [nodeCustomPlanDrafts, setNodeCustomPlanDrafts] = useState<
    { key: number; label: string; date: string }[]
  >([]);

  const addGraphNode = async (
    menu: Extract<LaneContextMenu, { kind: "node-create" }>,
  ) => {
    if (nodeCreateBusy) return;
    setNodeCreateBusy(true);
    setNodeCreateError(null);
    try {
      const definitions: PlanStatusDefinition[] = [
        ...planStatusDefinitions.map((definition) => ({ ...definition })),
      ];
      const plans: PlanMilestone[] = [];
      for (const { status } of BUILTIN_PLAN_STATUSES) {
        const date = nodePlanDates[status];
        if (date) plans.push({ status, date });
      }
      for (const definition of planStatusDefinitions) {
        const date = nodePlanDates[definition.id];
        if (date) plans.push({ status: definition.id, date });
      }

      const usedLabels = new Set(
        definitions.map((definition) => definition.label.trim().toLocaleLowerCase()),
      );
      for (const draft of nodeCustomPlanDrafts) {
        const label = draft.label.trim();
        if (!label && !draft.date) continue;
        if (!label) throw new Error(t("nodeCreate.error.customNameRequired"));
        if (label.length > 128) throw new Error(t("nodeCreate.error.customNameTooLong"));
        const normalized = label.toLocaleLowerCase();
        if (usedLabels.has(normalized)) {
          throw new Error(t("nodeCreate.error.customNameDuplicate", { label }));
        }
        if (definitions.length >= 64) {
          throw new Error(t("nodeCreate.error.customStatusLimit"));
        }
        usedLabels.add(normalized);
        const definition: PlanStatusDefinition = {
          id: nextCustomPlanStatusID(definitions),
          label,
        };
        definitions.push(definition);
        if (draft.date) {
          plans.push({ status: definition.id, date: draft.date });
        }
      }

      for (const plan of plans) {
        const error = planOrderError(
          plans.filter((candidate) => candidate.status !== plan.status),
          plan.status,
          plan.date,
        );
        if (error) throw new Error(localizePlanOrderError(error, language));
      }
      plans.sort((a, b) => a.date.localeCompare(b.date));

      const id = await createNode(
        {
          title: nodeCreateTitle.trim(),
          kind: "task",
          ...(nodeCreateWriteAccess ? { writeAccess: nodeCreateWriteAccess } : {}),
        },
        {
          openDocument: false,
          position: { x: menu.col, y: menu.row },
          plans,
          planStatuses:
            definitions.length !== planStatusDefinitions.length
              ? definitions
              : undefined,
          style: Object.keys(nodeCreateStyle).length ? nodeCreateStyle : undefined,
        },
      );
      // The journal cannot be written by the graph save above, so this is a
      // second request. It is reported rather than thrown: the node is already
      // on the board, and closing the form on a failure would hide why the
      // state did not take.
      let statusWarning = "";
      if (nodeCreateStatus) {
        const applied = await setLifecycleStatus(id, nodeCreateStatus, t("nodeCreate.creationNote"));
        if (!applied.ok) statusWarning = applied.message ?? "";
      }
      const createdText = plans.length
        ? t("nodeCreate.createdWithPlans", { id, count: plans.length })
        : t("nodeCreate.created", { id });
      setGraphNotice({
        text: statusWarning
          ? `${createdText} ${t("nodeCreate.statusWarning", { message: statusWarning })}`
          : createdText,
        kind: statusWarning ? "error" : "ok",
      });
      // A form that keeps the last node's colours would make the next node
      // inherit them by accident, so the draft resets with the form.
      setNodeCreateStatus("");
      setNodeCreateWriteAccess("");
      setNodeCreateStyle({});
      setContextMenu(null);
    } catch (error) {
      setNodeCreateError((error as Error).message || t("nodeCreate.failed"));
    } finally {
      setNodeCreateBusy(false);
    }
  };

  return {
    nodeCreateTitle,
    setNodeCreateTitle,
    nodeCreateError,
    setNodeCreateError,
    nodeCreateBusy,
    nodeCreateStatus,
    setNodeCreateStatus,
    nodeCreateWriteAccess,
    setNodeCreateWriteAccess,
    nodeCreateStyle,
    setNodeCreateStyle,
    nodePlanDates,
    setNodePlanDates,
    customPlanDraftKey,
    nodeCustomPlanDrafts,
    setNodeCustomPlanDrafts,
    addGraphNode,
  };
}
