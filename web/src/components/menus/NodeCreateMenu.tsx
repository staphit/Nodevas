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
import { StatusShape, statusOptions } from "../../statusTheme";
import { localizedPlanStatusLabel, localizedStatusLabel, useI18n } from "../../i18n";
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
  nodeCreateWriteAccess: string;
  setNodeCreateWriteAccess: (writeAccess: string) => void;
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
  nodeCreateWriteAccess,
  setNodeCreateWriteAccess,
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
  const { t, language } = useI18n();
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
          <b>{t("nodeCreate.title")}</b>
          <small>{t("nodeCreate.locationHint")}</small>
        </span>
      </div>
      <label className="lane-context-field node-create-field">
        {t("nodeCreate.nodeTitle")}
        <input
          autoFocus
          value={nodeCreateTitle}
          onChange={(event) => {
            setNodeCreateTitle(event.target.value);
            setNodeCreateError(null);
          }}
          placeholder={t("nodeCreate.titlePlaceholder")}
          maxLength={256}
          disabled={nodeCreateBusy}
        />
      </label>
      <div className="node-create-id-hint">
        <span aria-hidden>#</span>
        {t("nodeCreate.idHint")}
      </div>
      <section className="node-create-plans">
        <div className="node-create-section-head">
          <span>
            <b>{t("nodeCreate.plannedDates")}</b>
            <small>{t("nodeCreate.plannedDatesHint")}</small>
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
            ＋ {t("nodeCreate.addCustomStatus")}
          </button>
        </div>
        <div className="node-create-plan-list">
          {[
            ...BUILTIN_PLAN_STATUSES,
            ...planStatusDefinitions.map((definition) => ({
              status: definition.id,
              label: definition.label,
            })),
          ].map(({ status }) => (
            <label className="node-create-plan-row" key={status}>
              <span>{localizedPlanStatusLabel(status, planStatusDefinitions, language)}</span>
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
                aria-label={t("nodeCreate.customStatusName")}
                placeholder={t("nodeCreate.statusNamePlaceholder")}
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
                aria-label={t("nodeCreate.customDateAria", {
                  label: draft.label || t("nodeCreate.customStatus"),
                })}
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
                aria-label={t("nodeCreate.removeCustomStatus")}
                title={t("common.remove")}
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
            <b>{t("nodeCreate.appearanceAndStatus")}</b>
            <small>{t("nodeCreate.appearanceHint")}</small>
          </span>
        </button>
        <div id={advancedID} hidden={!advancedOpen} className="node-create-advanced-body">
          <label className="lane-context-field node-create-field">
            {t("nodeCreate.actualStatus")}
            <select
              value={nodeCreateStatus}
              disabled={nodeCreateBusy}
              onChange={(event) => setNodeCreateStatus(event.target.value as Status | "")}
            >
              <option value="">{t("nodeCreate.defaultStatus")}</option>
              {statusOptions(customStatuses).map((candidate) => (
                <option key={candidate} value={candidate}>
                  {localizedStatusLabel(candidate, customStatuses)}
                </option>
              ))}
            </select>
          </label>
          <label className="lane-context-field node-create-field">
            {t("node.meta.writeAccess")}
            <select
              value={nodeCreateWriteAccess}
              disabled={nodeCreateBusy}
              onChange={(event) => setNodeCreateWriteAccess(event.target.value)}
            >
              <option value="">{t("node.writeAccess.everyone")}</option>
              <option value="worker">{t("node.writeAccess.worker")}</option>
              <option value="orchestrator">{t("node.writeAccess.orchestrator")}</option>
              <option value="human-only">{t("node.writeAccess.humanOnly")}</option>
            </select>
          </label>
          <div className="node-create-status-preview">
            <StatusShape status={previewStatus} definitions={customStatuses} />
            <span>
              {t("nodeCreate.statusJournalHint")}
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
            previewTitle={nodeCreateTitle || t("nodeCreate.newNode")}
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
          {t("common.cancel")}
        </button>
        <button type="submit" className="primary" disabled={nodeCreateBusy}>
          {nodeCreateBusy ? t("nodeCreate.creating") : t("nodeCreate.create")}
        </button>
      </div>
    </form>
  );
}
