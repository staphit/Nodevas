import { useState } from "react";
import { lifecycleStatusUsage, type CustomLifecycleStatus } from "../../domain";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import { StatusShape } from "../../statusTheme";
import type { StatusDefinition } from "../../types";
import { IconPlus, IconTrash } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import { ColorField, EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";

const SHAPES: { value: StatusDefinition["shape"]; labelKey: string }[] = [
  { value: "circle", labelKey: "statusEditor.shape.circle" },
  { value: "square", labelKey: "statusEditor.shape.square" },
  { value: "diamond", labelKey: "statusEditor.shape.diamond" },
  { value: "triangle", labelKey: "statusEditor.shape.triangle" },
  { value: "dash", labelKey: "statusEditor.shape.dash" },
];

/**
 * The lifecycle-status vocabulary [B-05].
 *
 * Definitions live either in the workspace file or in this graph, so every
 * edit first has to work out which file owns the one being touched.
 */
export function StatusVocabularyEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const runState = useApp((s) => s.runState);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);
  const workspaceStatuses = useApp((s) => s.workspaceStatuses);
  const saveWorkspaceStatuses = useApp((s) => s.saveWorkspaceStatuses);

  const [statusLabel, setStatusLabel] = useState("");
  const [statusColor, setStatusColor] = useState("#8b7cf6");
  const [statusShape, setStatusShape] = useState<StatusDefinition["shape"]>("circle");
  const [statusSettled, setStatusSettled] = useState(false);

  // The board reads the merged list the server hands back; editing targets
  // whichever file actually owns a definition.
  const lifecycleDefinitions = graph?.ui?.customStatuses ?? [];
  const sharedIDs = new Set(workspaceStatuses.map((definition) => definition.id));
  const isShared = (id: StatusDefinition["id"]) => sharedIDs.has(id);
  const nextSharedID = (): StatusDefinition["id"] => {
    const used = new Set(lifecycleDefinitions.map((definition) => definition.id));
    for (let index = 1; index <= 10_000; index++) {
      const candidate: StatusDefinition["id"] = `custom-status-${index}`;
      if (!used.has(candidate)) return candidate;
    }
    return `custom-status-${crypto.randomUUID()}`;
  };
  const writeShared = async (
    next: StatusDefinition[],
    done: string,
  ): Promise<boolean> => {
    notify.onError(null);
    notify.onNotice(null);
    try {
      await saveWorkspaceStatuses(next);
      notify.onNotice(done);
      return true;
    } catch (error) {
      notify.onError((error as Error).message || t("statusEditor.saveFailed"));
      return false;
    }
  };
  /** Edits a state wherever it lives: the workspace file or this graph. */
  const editLifecycleStatus = async (
    definition: StatusDefinition,
    patch: Partial<Omit<StatusDefinition, "id">>,
  ) => {
    if (isShared(definition.id)) {
      await writeShared(
        workspaceStatuses.map((item) =>
          item.id === definition.id ? { ...item, ...patch } : item,
        ),
        t("statusEditor.updated", { label: patch.label ?? definition.label }),
      );
      return;
    }
    await runSettingsCommand(
      notify,
      updateWorkflowDefinition({
        type: "workflow.updateLifecycleStatus",
        id: definition.id as CustomLifecycleStatus,
        patch,
      }),
    );
  };

  const addLifecycleStatus = async () => {
    const label = statusLabel.trim();
    if (!label) {
      notify.onError(t("statusEditor.nameRequired"));
      return;
    }
    if (
      lifecycleDefinitions.some(
        (definition) =>
          definition.label.localeCompare(label, undefined, {
            sensitivity: "accent",
          }) === 0,
      )
    ) {
      notify.onError(t("statusEditor.duplicate", { label }));
      return;
    }
    // New states are always shared: a per-project state is what made people
    // retype the same vocabulary in every project.
    const ok = await writeShared(
      [
        ...workspaceStatuses,
        {
          id: nextSharedID(),
          label,
          color: statusColor,
          shape: statusShape,
          ...(statusSettled ? { settled: true } : {}),
        },
      ],
      t("statusEditor.added", { label }),
    );
    if (ok) setStatusLabel("");
  };

  const removeLifecycleStatus = async (definition: StatusDefinition) => {
    const usage = lifecycleStatusUsage(
      statuses,
      runState,
      definition.id as CustomLifecycleStatus,
    );
    const confirmed = await confirmAction({
      title: t("statusEditor.deleteTitle", { label: definition.label }),
      description:
        usage.currentNodes.length || usage.events
          ? t("statusEditor.deleteDescriptionUsed", {
              nodes: usage.currentNodes.length,
              events: usage.events,
            })
          : t("statusEditor.deleteDescriptionUnused"),
      confirmLabel: t("statusEditor.deleteDefinition"),
      tone: "danger",
    });
    if (!confirmed) return;
    if (isShared(definition.id)) {
      await writeShared(
        workspaceStatuses.filter((item) => item.id !== definition.id),
        t("statusEditor.deletedShared", { label: definition.label }),
      );
      return;
    }
    await runSettingsCommand(
      notify,
      updateWorkflowDefinition({
        type: "workflow.removeLifecycleStatus",
        id: definition.id as CustomLifecycleStatus,
      }),
      t("statusEditor.deleted", { label: definition.label }),
    );
  };

  return (
    <section className="settings-section">
      <p className="settings-hint">
        {t("statusEditor.hint")}
      </p>
      <ul className="settings-list">
        {lifecycleDefinitions.map((definition) => {
          const usage = lifecycleStatusUsage(
            statuses,
            runState,
            definition.id as CustomLifecycleStatus,
          );
          return (
            <li key={definition.id}>
              <StatusShape status={definition.id} definitions={lifecycleDefinitions} />
              <input
                defaultValue={definition.label}
                aria-label={t("statusEditor.nameAria", { label: definition.label })}
                onBlur={(event) => {
                  const label = event.target.value.trim();
                  if (!label || label === definition.label) return;
                  void editLifecycleStatus(definition, { label });
                }}
              />
              <ColorField
                value={definition.color}
                label={t("statusEditor.colorAria", { label: definition.label })}
                onCommit={(color) => void editLifecycleStatus(definition, { color })}
              />
              <select
                value={definition.shape}
                aria-label={t("statusEditor.shapeAria", { label: definition.label })}
                onChange={(event) =>
                  void editLifecycleStatus(definition, {
                    shape: event.target.value as StatusDefinition["shape"],
                  })
                }
              >
                {SHAPES.map((shape) => (
                  <option key={shape.value} value={shape.value}>
                    {t(shape.labelKey)}
                  </option>
                ))}
              </select>
              <label className="settings-flag">
                <input
                  type="checkbox"
                  checked={definition.settled === true}
                  aria-label={t("statusEditor.settledAria", { label: definition.label })}
                  onChange={(event) =>
                    void editLifecycleStatus(definition, {
                      settled: event.target.checked,
                    })
                  }
                />
                {t("statusEditor.settled")}
              </label>
              <small className="settings-usage">
                {isShared(definition.id)
                  ? t("statusEditor.workspaceShared")
                  : t("statusEditor.projectOnly")} · {t("statusEditor.usage", {
                  nodes: usage.currentNodes.length,
                  events: usage.events,
                })}
              </small>
              <button
                type="button"
                className="danger"
                aria-label={t("statusEditor.deleteAria", { label: definition.label })}
                onClick={() => void removeLifecycleStatus(definition)}
              >
                <IconTrash size={13} />
              </button>
            </li>
          );
        })}
      </ul>
      {lifecycleDefinitions.length === 0 && (
        <EmptyState
          title={t("statusEditor.emptyTitle")}
          description={t("statusEditor.emptyDescription")}
        />
      )}
      <div className="settings-create">
        <input
          value={statusLabel}
          placeholder={t("statusEditor.newPlaceholder")}
          aria-label={t("statusEditor.newNameAria")}
          onChange={(event) => setStatusLabel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void addLifecycleStatus();
          }}
        />
        <input
          type="color"
          value={statusColor}
          aria-label={t("statusEditor.newColorAria")}
          onChange={(event) => setStatusColor(event.target.value)}
        />
        <select
          value={statusShape}
          aria-label={t("statusEditor.newShapeAria")}
          onChange={(event) =>
            setStatusShape(event.target.value as StatusDefinition["shape"])
          }
        >
          {SHAPES.map((shape) => (
            <option key={shape.value} value={shape.value}>
              {t(shape.labelKey)}
            </option>
          ))}
        </select>
        <label className="settings-flag">
          <input
            type="checkbox"
            checked={statusSettled}
            aria-label={t("statusEditor.newSettledAria")}
            onChange={(event) => setStatusSettled(event.target.checked)}
          />
          {t("statusEditor.settled")}
        </label>
        <button type="button" className="primary" onClick={() => void addLifecycleStatus()}>
          <IconPlus size={13} />
          {t("statusEditor.add")}
        </button>
      </div>
    </section>
  );
}
