import { useState } from "react";
import { milestoneTypeUsage } from "../../domain";
import { useApp } from "../../store";
import { IconPlus, IconTrash } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import { EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";
import { localizedPlanStatusLabel, useI18n } from "../../i18n";

/**
 * The milestone-type vocabulary [B-05].
 *
 * These classify the *expected* plan and live in `graph.yaml`, which is why
 * they are a separate domain from the lifecycle states next door.
 */
export function MilestoneTypeEditor({ notify }: { notify: SettingsNotify }) {
  const graph = useApp((s) => s.graph);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);
  const { t, language } = useI18n();

  const [milestoneLabel, setMilestoneLabel] = useState("");

  const milestoneDefinitions = graph?.ui?.planStatuses ?? [];

  const addMilestoneType = async () => {
    const label = milestoneLabel.trim();
    if (!label) {
      notify.onError(t("settings.milestoneNameRequired"));
      return;
    }
    const ok = await runSettingsCommand(
      notify,
      updateWorkflowDefinition({ type: "workflow.addMilestoneType", label }),
      t("settings.milestoneAdded", { label }),
    );
    if (ok) setMilestoneLabel("");
  };

  const removeMilestoneType = async (id: `custom-${string}`, label: string) => {
    const usage = graph ? milestoneTypeUsage(graph, id) : { nodes: [], milestones: 0 };
    const keepScheduled =
      usage.milestones > 0
        ? await confirmAction({
            title: t("settings.deleteMilestoneTitle", { label }),
            description: t("settings.deleteMilestoneDescription", {
              nodes: usage.nodes.length,
              milestones: usage.milestones,
            }),
            confirmLabel: t("settings.deleteMilestoneWithPlans"),
            tone: "danger",
          })
        : true;
    if (usage.milestones > 0 && !keepScheduled) {
      await runSettingsCommand(
        notify,
        updateWorkflowDefinition({ type: "workflow.removeMilestoneType", id }),
        t("settings.milestoneDefinitionRemovedKeepPlans", { count: usage.milestones }),
      );
      return;
    }
    if (!keepScheduled) return;
    await runSettingsCommand(
      notify,
      updateWorkflowDefinition({
        type: "workflow.removeMilestoneType",
        id,
        removeScheduled: usage.milestones > 0,
      }),
      t("settings.milestoneRemoved", { label }),
    );
  };

  return (
    <section className="settings-section">
      <p className="settings-hint">
        {t("settings.milestoneIntro")}
      </p>
      <ul className="settings-list">
        {milestoneDefinitions.map((definition) => {
          const usage = graph
            ? milestoneTypeUsage(graph, definition.id)
            : { nodes: [], milestones: 0 };
          return (
            <li key={definition.id}>
              <input
                defaultValue={definition.label}
                aria-label={t("settings.milestoneNameAria", { label: definition.label })}
                onBlur={(event) => {
                  const label = event.target.value.trim();
                  if (!label || label === definition.label) return;
                  void runSettingsCommand(
                    notify,
                    updateWorkflowDefinition({
                      type: "workflow.updateMilestoneType",
                      id: definition.id,
                      label,
                    }),
                  );
                }}
              />
              <small className="settings-usage">
                {t("settings.milestoneUsage", { milestones: usage.milestones, nodes: usage.nodes.length })}
              </small>
              <button
                type="button"
                className="danger"
                aria-label={t("settings.deleteMilestoneAria", { label: definition.label })}
                onClick={() => void removeMilestoneType(definition.id, definition.label)}
              >
                <IconTrash size={13} />
              </button>
            </li>
          );
        })}
      </ul>
      {milestoneDefinitions.length === 0 && (
        <EmptyState
          title={t("settings.noMilestones")}
          description={t("settings.builtinMilestones", {
            started: localizedPlanStatusLabel("started", [], language),
            done: localizedPlanStatusLabel("done", [], language),
          })}
        />
      )}
      <div className="settings-create">
        <input
          value={milestoneLabel}
          placeholder={t("settings.newMilestonePlaceholder")}
          aria-label={t("settings.newMilestoneAria")}
          onChange={(event) => setMilestoneLabel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void addMilestoneType();
          }}
        />
        <button type="button" className="primary" onClick={() => void addMilestoneType()}>
          <IconPlus size={13} />
          {t("settings.addMilestone")}
        </button>
      </div>
    </section>
  );
}
