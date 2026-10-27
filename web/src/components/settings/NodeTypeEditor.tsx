import { useState } from "react";
import { NODE_COMPLETIONS, NODE_SHAPES, nodeTypeUsage } from "../../domain/graph/nodeTypes";
import type { WorkflowCommand } from "../../domain/commands";
import { useApp } from "../../store";
import { IconPlus, IconTrash } from "../../icons";
import { useI18n } from "../../i18n";
import { NodeTypeChip, nodeTypeLabel, useNodeTypes } from "../../nodeTypeTheme";
import type {
  AgentMode,
  CustomNodeTypeId,
  NodeCompletion,
  NodeShape,
  NodeType,
  NodeTypeDef,
} from "../../types";
import { confirmAction } from "../ConfirmDialog";
import { ColorField } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";

/** Shown in the colour picker while a type has no colour of its own. */
const NEUTRAL_SWATCH = "#8b949e";

const AGENT_MODES: AgentMode[] = ["none", "dispatch", "autopilot"];

/** A new custom type starts as plain work a person finishes. */
const PLAIN_PRESET: Omit<NodeTypeDef, "id" | "label"> = {
  shape: "round",
  agentMode: "none",
  completion: "anyone",
  countsInProgress: true,
};
/** Reference material, like Note: no status, outside the flow. */
const REFERENCE_PRESET: Omit<NodeTypeDef, "id" | "label"> = {
  shape: "dashed",
  agentMode: "none",
  completion: "none",
  countsInProgress: false,
};

/**
 * The project's node types [B-05]: what each kind of node does and how it
 * looks by default.
 *
 * Built-in types keep their behaviour — only their shape and colour change
 * here. A custom type carries every attribute a built-in does: a name, a
 * shape and colour, whether an agent may work it, who completes it and
 * whether it counts as work. The list lives in `graph.yaml → node_types`, so
 * the whole team shares it.
 */
export function NodeTypeEditor({ notify }: { notify: SettingsNotify }) {
  const graph = useApp((s) => s.graph);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);
  const { t } = useI18n();
  const nodeTypes = useNodeTypes();
  const [newLabel, setNewLabel] = useState("");

  const run = (command: WorkflowCommand, done?: string) =>
    runSettingsCommand(notify, updateWorkflowDefinition(command), done);

  const update = (type: NodeType, patch: Partial<Omit<NodeTypeDef, "id">>) =>
    void run({ type: "workflow.updateNodeType", id: type.id, patch });

  const add = async (preset: Omit<NodeTypeDef, "id" | "label">) => {
    const label = newLabel.trim();
    if (!label) {
      notify.onError(t("settings.nodeTypeNameRequired"));
      return;
    }
    const ok = await run(
      { type: "workflow.addNodeType", definition: { ...preset, label } },
      t("settings.nodeTypeAdded", { label }),
    );
    if (ok) setNewLabel("");
  };

  const remove = async (type: NodeType) => {
    const count = graph ? nodeTypeUsage(graph, type.id).length : 0;
    if (count > 0) {
      const confirmed = await confirmAction({
        title: t("settings.nodeTypeDeleteTitle", { label: type.label }),
        description: t("settings.nodeTypeDeleteDescription", { count }),
        confirmLabel: t("settings.nodeTypeDeleteConfirm"),
        tone: "danger",
      });
      if (!confirmed) return;
    }
    await run(
      { type: "workflow.removeNodeType", id: type.id as CustomNodeTypeId },
      t("settings.nodeTypeRemoved", { label: type.label }),
    );
  };

  const setCompletion = (type: NodeType, completion: NodeCompletion) => {
    // A type that settles itself, or has no status, is never agent work; one
    // with no status is not work at all.
    const patch: Partial<NodeTypeDef> = { completion };
    if (completion === "auto" || completion === "none") patch.agentMode = "none";
    if (completion === "none") patch.countsInProgress = false;
    update(type, patch);
  };

  const behaviour = (type: NodeType) =>
    [
      t(`nodeType.agentMode.${type.agentMode}`),
      type.id === "goal" ? t("nodeType.done.goal") : t(`nodeType.completion.${type.completion}`),
      type.countsInProgress ? t("nodeType.counts") : t("nodeType.notCounted"),
    ].join(" · ");

  return (
    <section className="settings-section node-type-editor">
      <p className="settings-hint">{t("settings.nodeTypesIntro")}</p>
      <ul className="settings-list node-type-list">
        {nodeTypes.map((type) => {
          const label = nodeTypeLabel(type, t);
          const usage = graph ? nodeTypeUsage(graph, type.id).length : 0;
          const overriddenColor = type.builtin
            ? (graph?.nodeTypes ?? []).some((def) => def.id === type.id && def.color !== undefined)
            : Boolean(type.color);
          return (
            <li key={type.id} className="node-type-row" data-node-type={type.id}>
              <NodeTypeChip type={type} />
              {type.builtin ? (
                <span className="node-type-name" title={t(`nodeType.description.${type.id}`)}>
                  {label}
                </span>
              ) : (
                <input
                  key={`${type.id}:${type.label}`}
                  defaultValue={type.label}
                  maxLength={64}
                  aria-label={t("settings.nodeTypeNameAria", { label })}
                  onBlur={(event) => {
                    const next = event.target.value.trim();
                    if (!next || next === type.label) {
                      event.target.value = type.label;
                      return;
                    }
                    update(type, { label: next });
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") event.currentTarget.blur();
                  }}
                />
              )}
              <select
                value={type.shape}
                aria-label={t("settings.nodeTypeShape", { label })}
                onChange={(event) => update(type, { shape: event.target.value as NodeShape })}
              >
                {NODE_SHAPES.map((shape) => (
                  <option key={shape} value={shape}>
                    {t(`appearance.shape.${shape}`)}
                  </option>
                ))}
              </select>
              <ColorField
                value={type.color || NEUTRAL_SWATCH}
                label={t("settings.nodeTypeColor", { label })}
                onCommit={(color) => update(type, { color })}
              />
              <button
                type="button"
                className="node-type-clear"
                disabled={!overriddenColor}
                aria-label={t("settings.nodeTypeClearColor", { label })}
                title={t("settings.nodeTypeClearColor", { label })}
                onClick={() => update(type, { color: "" })}
              >
                ×
              </button>
              {usage > 0 && (
                <small className="settings-usage">{t("settings.nodeTypeUsage", { count: usage })}</small>
              )}
              {type.builtin ? (
                <small className="node-type-behaviour" title={t("settings.nodeTypeBuiltinFixed")}>
                  {behaviour(type)}
                </small>
              ) : (
                <div className="node-type-behaviour-fields">
                  <select
                    value={type.agentMode}
                    aria-label={t("settings.nodeTypeAgentMode", { label })}
                    disabled={type.completion === "auto" || type.completion === "none"}
                    onChange={(event) =>
                      update(type, { agentMode: event.target.value as AgentMode })
                    }
                  >
                    {AGENT_MODES.map((mode) => (
                      <option key={mode} value={mode}>
                        {t(`nodeType.agentMode.${mode}`)}
                      </option>
                    ))}
                  </select>
                  <select
                    value={type.completion}
                    aria-label={t("settings.nodeTypeCompletion", { label })}
                    onChange={(event) =>
                      setCompletion(type, event.target.value as NodeCompletion)
                    }
                  >
                    {NODE_COMPLETIONS.map((completion) => (
                      <option
                        key={completion}
                        value={completion}
                        // Autopilot hands the type to agents, so it must end
                        // with a run or a person.
                        disabled={
                          type.agentMode === "autopilot" &&
                          (completion === "auto" || completion === "none")
                        }
                      >
                        {t(`nodeType.completion.${completion}`)}
                      </option>
                    ))}
                  </select>
                  <label className="settings-flag">
                    <input
                      type="checkbox"
                      checked={type.countsInProgress}
                      disabled={type.completion === "none"}
                      onChange={(event) =>
                        update(type, { countsInProgress: event.target.checked })
                      }
                    />
                    {t("settings.nodeTypeCounts")}
                  </label>
                  <button
                    type="button"
                    className="danger"
                    aria-label={t("settings.nodeTypeDeleteAria", { label })}
                    onClick={() => void remove(type)}
                  >
                    <IconTrash size={13} />
                  </button>
                </div>
              )}
            </li>
          );
        })}
      </ul>
      <div className="settings-create">
        <input
          value={newLabel}
          maxLength={64}
          placeholder={t("settings.nodeTypeNamePlaceholder")}
          aria-label={t("settings.newNodeTypeAria")}
          onChange={(event) => setNewLabel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void add(PLAIN_PRESET);
          }}
        />
        <button type="button" className="primary" onClick={() => void add(PLAIN_PRESET)}>
          <IconPlus size={13} />
          {t("settings.nodeTypeAdd")}
        </button>
        <button type="button" onClick={() => void add(REFERENCE_PRESET)}>
          <IconPlus size={13} />
          {t("settings.nodeTypeAddReference")}
        </button>
      </div>
    </section>
  );
}
