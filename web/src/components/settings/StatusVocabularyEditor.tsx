import { useState } from "react";
import { lifecycleStatusUsage, type CustomLifecycleStatus } from "../../domain";
import { useApp } from "../../store";
import { StatusShape } from "../../statusTheme";
import type { StatusDefinition } from "../../types";
import { IconPlus, IconTrash } from "../../icons";
import { confirmAction } from "../ConfirmDialog";
import { ColorField, EmptyState } from "../InteractionPrimitives";
import { runSettingsCommand, type SettingsNotify } from "./notify";

const SHAPES: { value: StatusDefinition["shape"]; label: string }[] = [
  { value: "circle", label: "圓形" },
  { value: "square", label: "方形" },
  { value: "diamond", label: "菱形" },
  { value: "triangle", label: "三角形" },
  { value: "dash", label: "橫線" },
];

/**
 * The lifecycle-status vocabulary [B-05].
 *
 * Definitions live either in the workspace file or in this graph, so every
 * edit first has to work out which file owns the one being touched.
 */
export function StatusVocabularyEditor({ notify }: { notify: SettingsNotify }) {
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
      notify.onError((error as Error).message || "儲存失敗。");
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
        `已更新「${patch.label ?? definition.label}」`,
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
      notify.onError("請輸入狀態名稱。");
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
      notify.onError(`已存在名稱「${label}」。`);
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
      `已新增「${label}」（工作區共用）`,
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
      title: `刪除實際狀態「${definition.label}」`,
      description:
        usage.currentNodes.length || usage.events
          ? `目前有 ${usage.currentNodes.length} 個節點處於此狀態，歷史中有 ${usage.events} 筆相關紀錄。` +
            "刪除只移除定義，journal 不會被改寫；既有紀錄會顯示為未知狀態。"
          : "尚無節點或歷史使用此狀態。",
      confirmLabel: "刪除定義",
      tone: "danger",
    });
    if (!confirmed) return;
    if (isShared(definition.id)) {
      await writeShared(
        workspaceStatuses.filter((item) => item.id !== definition.id),
        `已刪除「${definition.label}」（工作區共用）`,
      );
      return;
    }
    await runSettingsCommand(
      notify,
      updateWorkflowDefinition({
        type: "workflow.removeLifecycleStatus",
        id: definition.id as CustomLifecycleStatus,
      }),
      `已刪除「${definition.label}」`,
    );
  };

  return (
    <section className="settings-section">
      <p className="settings-hint">
        自訂狀態存在工作區的 <code>.vised/workflow.json</code>，
        同一個工作區的所有專案共用，不必逐一重建。
        實際狀態記在 <code>run/journal.jsonl</code>。內建狀態不可刪除；
        自訂狀態刪除後，歷史紀錄仍保留。勾選「視為完結」的狀態和
        「完成」「略過」一樣，不會擋住後續節點。
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
                aria-label={`${definition.label} 名稱`}
                onBlur={(event) => {
                  const label = event.target.value.trim();
                  if (!label || label === definition.label) return;
                  void editLifecycleStatus(definition, { label });
                }}
              />
              <ColorField
                value={definition.color}
                label={`${definition.label} 顏色`}
                onCommit={(color) => void editLifecycleStatus(definition, { color })}
              />
              <select
                value={definition.shape}
                aria-label={`${definition.label} 圖形`}
                onChange={(event) =>
                  void editLifecycleStatus(definition, {
                    shape: event.target.value as StatusDefinition["shape"],
                  })
                }
              >
                {SHAPES.map((shape) => (
                  <option key={shape.value} value={shape.value}>
                    {shape.label}
                  </option>
                ))}
              </select>
              <label className="settings-flag">
                <input
                  type="checkbox"
                  checked={definition.settled === true}
                  aria-label={`${definition.label} 視為完結`}
                  onChange={(event) =>
                    void editLifecycleStatus(definition, {
                      settled: event.target.checked,
                    })
                  }
                />
                視為完結
              </label>
              <small className="settings-usage">
                {isShared(definition.id) ? "工作區共用" : "僅此專案"} ·
                使用中 {usage.currentNodes.length} · 歷史 {usage.events}
              </small>
              <button
                type="button"
                className="danger"
                aria-label={`刪除 ${definition.label}`}
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
          title="尚無自訂實際狀態"
          description="內建狀態已可涵蓋大部分流程；需要專屬階段時再新增。"
        />
      )}
      <div className="settings-create">
        <input
          value={statusLabel}
          placeholder="新狀態名稱"
          aria-label="新狀態名稱"
          onChange={(event) => setStatusLabel(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void addLifecycleStatus();
          }}
        />
        <input
          type="color"
          value={statusColor}
          aria-label="新狀態顏色"
          onChange={(event) => setStatusColor(event.target.value)}
        />
        <select
          value={statusShape}
          aria-label="新狀態圖形"
          onChange={(event) =>
            setStatusShape(event.target.value as StatusDefinition["shape"])
          }
        >
          {SHAPES.map((shape) => (
            <option key={shape.value} value={shape.value}>
              {shape.label}
            </option>
          ))}
        </select>
        <label className="settings-flag">
          <input
            type="checkbox"
            checked={statusSettled}
            aria-label="新狀態視為完結"
            onChange={(event) => setStatusSettled(event.target.checked)}
          />
          視為完結
        </label>
        <button type="button" className="primary" onClick={() => void addLifecycleStatus()}>
          <IconPlus size={13} />
          新增狀態
        </button>
      </div>
    </section>
  );
}
