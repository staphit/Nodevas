import { useState } from "react";
import { milestoneTypeUsage } from "../../domain";
import { useApp } from "../../store";
import { planStatusLabel } from "../../plan";
import { IconPlus, IconTrash } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import { EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";

/**
 * The milestone-type vocabulary [B-05].
 *
 * These classify the *expected* plan and live in `graph.yaml`, which is why
 * they are a separate domain from the lifecycle states next door.
 */
export function MilestoneTypeEditor({ notify }: { notify: SettingsNotify }) {
  const graph = useApp((s) => s.graph);
  const updateWorkflowDefinition = useApp((s) => s.updateWorkflowDefinition);

  const [milestoneLabel, setMilestoneLabel] = useState("");

  const milestoneDefinitions = graph?.ui?.planStatuses ?? [];

  const addMilestoneType = async () => {
    const label = milestoneLabel.trim();
    if (!label) {
      notify.onError("請輸入里程碑名稱。");
      return;
    }
    const ok = await runSettingsCommand(
      notify,
      updateWorkflowDefinition({ type: "workflow.addMilestoneType", label }),
      `已新增「${label}」`,
    );
    if (ok) setMilestoneLabel("");
  };

  const removeMilestoneType = async (id: `custom-${string}`, label: string) => {
    const usage = graph ? milestoneTypeUsage(graph, id) : { nodes: [], milestones: 0 };
    const keepScheduled =
      usage.milestones > 0
        ? await confirmAction({
            title: `刪除里程碑類型「${label}」`,
            description: `有 ${usage.nodes.length} 個節點、共 ${usage.milestones} 筆已排定的里程碑使用此類型。選擇「一併刪除」會移除那些排程；選擇取消則保留排程，只刪定義。`,
            confirmLabel: "一併刪除排程",
            tone: "danger",
          })
        : true;
    if (usage.milestones > 0 && !keepScheduled) {
      await runSettingsCommand(
        notify,
        updateWorkflowDefinition({ type: "workflow.removeMilestoneType", id }),
        `已刪除定義，保留 ${usage.milestones} 筆排程`,
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
      `已刪除「${label}」`,
    );
  };

  return (
    <section className="settings-section">
      <p className="settings-hint">
        里程碑類型是<b>預期計畫</b>的分類，存在 <code>graph.yaml</code>，
        與實際狀態互不影響。
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
                aria-label={`${definition.label} 名稱`}
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
                已排定 {usage.milestones} 筆 · {usage.nodes.length} 個節點
              </small>
              <button
                type="button"
                className="danger"
                aria-label={`刪除 ${definition.label}`}
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
          title="尚無自訂里程碑類型"
          description={`內建為「${planStatusLabel("started")}」與「${planStatusLabel("done")}」。`}
        />
      )}
      <div className="settings-create">
        <input
          value={milestoneLabel}
          placeholder="新里程碑名稱，例如：內審"
          aria-label="新里程碑名稱"
          onChange={(event) => setMilestoneLabel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void addMilestoneType();
          }}
        />
        <button type="button" className="primary" onClick={() => void addMilestoneType()}>
          <IconPlus size={13} />
          新增類型
        </button>
      </div>
    </section>
  );
}
