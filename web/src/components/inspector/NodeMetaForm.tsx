/**
 * Node metadata form [B-04/B-06].
 *
 * Writes `graph.yaml` through the typed node commands. Every field commits on
 * blur/change, and the section badge reports whether that write landed.
 */

import { useCallback, useEffect, useState } from "react";
import type { NodeMetadataPatch } from "../../domain/graph/node";
import { IconTrash } from "../../icons";
import {
  nodeById,
  operationScope,
  reportError,
  useApp,
  useOperation,
  type CommandResult,
} from "../../store";
import type { NodeLinkRef } from "../../types";
import { NodeLinkPicker } from "../NodeLinkPicker";
import { confirmAction } from "../ConfirmDialog";
import { OperationStatus } from "../InteractionPrimitives";

export function NodeMetaForm({ id }: { id: string }) {
  const graph = useApp((state) => state.graph);
  const updateNodeMetadata = useApp((state) => state.updateNodeMetadata);
  const updateNode = useApp((state) => state.updateNode);
  const deleteNode = useApp((state) => state.deleteNode);
  const openNodeLink = useApp((state) => state.openNodeLink);
  const activeProject = useApp((state) => state.activeProject);
  const metadataOperation = useOperation(operationScope.node(id));

  const node = nodeById(graph, id);
  const users = graph?.users ?? [];
  const assignedUserName = users.find((user) => user.id === node?.assignee)?.name ?? "";
  const [assignee, setAssignee] = useState(assignedUserName);
  useEffect(() => setAssignee(assignedUserName), [assignedUserName]);

  // The list is edited locally so a label can be typed without a write per
  // keystroke; every structural change commits immediately.
  const [draftLinks, setDraftLinks] = useState<NodeLinkRef[] | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const links = draftLinks ?? node?.links ?? [];
  // Every graph reload hands back a fresh array even when the links are
  // untouched, so the draft is compared by content instead: a background
  // refresh must not throw away a label someone is halfway through typing,
  // while a genuine change from elsewhere still wins and is shown.
  const linksSignature = (node?.links ?? [])
    .map((link) => JSON.stringify([link.project ?? "", link.node, link.label]))
    .join("|");
  useEffect(() => setDraftLinks(null), [id, linksSignature]);

  const reportCommand = useCallback((run: Promise<CommandResult>) => {
    void run.then((result) => {
      if (!result.ok) reportError(new Error(result.message));
    });
  }, []);

  const commitMeta = useCallback(
    (field: "title" | "kind" | "priority", value: string) => {
      reportCommand(updateNodeMetadata(id, { [field]: value } as NodeMetadataPatch));
    },
    [id, reportCommand, updateNodeMetadata],
  );

  const setDraftLabel = (index: number, label: string) => {
    setDraftLinks(
      links.map((link, item) => (item === index ? { ...link, label } : link)),
    );
  };

  const commitLinks = useCallback(
    (next: NodeLinkRef[]) => {
      setDraftLinks(next);
      reportCommand(updateNode({ type: "node.setLinks", nodeId: id, links: next }));
    },
    [id, reportCommand, updateNode],
  );

  const commitTags = useCallback(
    (value: string) => {
      const tags = value
        .split(/[,，]/)
        .map((tag) => tag.trim())
        .filter(Boolean)
        .slice(0, 64);
      reportCommand(updateNodeMetadata(id, { tags }));
    },
    [id, reportCommand, updateNodeMetadata],
  );


  // The automatic answer is shown inside the "自動" option so switching away
  // from it is an informed choice rather than a guess.
  const autoEntry = !(graph?.edges ?? []).some((edge) => edge.to === id);
  const entryOverride = graph?.ui?.entryOverrides?.[id];
  const entryChoice =
    typeof entryOverride === "boolean" ? (entryOverride ? "yes" : "no") : "auto";

  const commitEntryOverride = useCallback(
    (choice: string) => {
      reportCommand(
        updateNode({
          type: "node.setEntryOverride",
          nodeId: id,
          value: choice === "auto" ? undefined : choice === "yes",
        }),
      );
    },
    [id, reportCommand, updateNode],
  );

  const commitAssignee = useCallback(() => {
    const name = assignee.trim();
    if (name === assignedUserName) return;
    reportCommand(updateNode({ type: "node.assignByName", nodeId: id, name }));
  }, [assignee, assignedUserName, id, reportCommand, updateNode]);

  return (
      <section className="meta-form" aria-labelledby={`meta-heading-${id}`}>
        <div className="section-head">
          <h3 id={`meta-heading-${id}`}>基本資料</h3>
          <span className="section-hint">寫入 graph.yaml，離開欄位即儲存</span>
          <OperationStatus
            status={metadataOperation.status}
            message={
              metadataOperation.status === "error" ||
              metadataOperation.status === "conflict"
                ? metadataOperation.message
                : undefined
            }
          />
        </div>
        <div className="meta-row">
          <label htmlFor={`title-${id}`}>標題</label>
          <input
            id={`title-${id}`}
            key={`${id}:${node?.title ?? ""}`}
            defaultValue={node?.title ?? ""}
            onBlur={(e) => commitMeta("title", e.target.value)}
          />
          <label htmlFor={`kind-${id}`}>類型</label>
          <select
            id={`kind-${id}`}
            value={node?.kind ?? "task"}
            onChange={(e) => commitMeta("kind", e.target.value)}
          >
            {(
              [
                ["task", "任務"],
                ["scene", "場景"],
                ["event", "事件"],
                ["choice", "抉擇"],
                ["gate", "閘門"],
                ["start", "起點"],
                ["end", "終點"],
              ] as const
            ).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          <label htmlFor={`entry-${id}`}>起點</label>
          <select
            id={`entry-${id}`}
            value={entryChoice}
            title="自動判定是「沒有任何節點指向它」。孤立節點不一定是起點，這裡可以自己決定。"
            onChange={(event) => commitEntryOverride(event.target.value)}
          >
            <option value="auto">自動{autoEntry ? "（是起點）" : "（不是起點）"}</option>
            <option value="yes">是起點</option>
            <option value="no">不是起點</option>
          </select>
          <label htmlFor={`priority-${id}`}>優先度</label>
          <select
            id={`priority-${id}`}
            value={node?.priority ?? ""}
            onChange={(event) => commitMeta("priority", event.target.value)}
          >
            <option value="">未設定</option>
            <option value="urgent">緊急</option>
            <option value="high">高</option>
            <option value="medium">中</option>
            <option value="low">低</option>
          </select>
        </div>
        <div className="meta-row">
          <label htmlFor={`assignee-${id}`}>負責人</label>
          <input
            id={`assignee-${id}`}
            className="assignee-input"
            list={`assignee-options-${id}`}
            value={assignee}
            placeholder="尚未指派（選擇或輸入新使用者）"
            onChange={(event) => setAssignee(event.target.value)}
            onBlur={commitAssignee}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.currentTarget.blur();
              if (event.key === "Escape") {
                setAssignee(assignedUserName);
                event.currentTarget.blur();
              }
            }}
          />
          <datalist id={`assignee-options-${id}`}>
            {users.map((user) => (
              <option key={user.id} value={user.name} />
            ))}
          </datalist>
        </div>
        <div className="meta-row">
          <label htmlFor={`tags-${id}`}>標籤</label>
          <input
            id={`tags-${id}`}
            key={`${id}:${(node?.tags ?? []).join(",")}`}
            defaultValue={(node?.tags ?? []).join(", ")}
            placeholder="以逗號分隔，例如：前端, 發布"
            onBlur={(event) => commitTags(event.target.value)}
          />
        </div>
        <div className="meta-row meta-row-links">
          <label>連結</label>
          <div className="node-links-editor">
            {links.length === 0 && (
              <p className="settings-hint">
                還沒有連結。加入後，在內容中輸入 <code>/名稱</code> 就能插入。
              </p>
            )}
            {links.map((link, index) => (
              <div className="node-link-chip" key={`${link.project ?? ""}/${link.node}`}>
                <input
                  value={link.label}
                  aria-label={`連結 ${index + 1} 名稱`}
                  onChange={(event) =>
                    setDraftLabel(index, event.target.value)
                  }
                  onBlur={() => commitLinks(links)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") event.currentTarget.blur();
                  }}
                />
                <button
                  type="button"
                  className="node-link-chip-target"
                  title="開啟這個節點"
                  onClick={() =>
                    void openNodeLink({
                      project: link.project ?? activeProject,
                      nodeId: link.node,
                    }).catch(reportError)
                  }
                >
                  {link.project && link.project !== activeProject
                    ? `${link.project} / ${link.node}`
                    : link.node}
                </button>
                <button
                  type="button"
                  className="danger"
                  aria-label={`移除連結 ${link.label}`}
                  onClick={() =>
                    commitLinks(links.filter((_, item) => item !== index))
                  }
                >
                  <IconTrash size={12} />
                </button>
              </div>
            ))}
            <button type="button" onClick={() => setPickerOpen(true)}>
              ＋ 新增連結
            </button>
          </div>
        </div>
        {pickerOpen && (
          <NodeLinkPicker
            excludeNodeId={id}
            onClose={() => setPickerOpen(false)}
            onPick={(target) => {
              setPickerOpen(false);
              const exists = links.some(
                (link) =>
                  link.node === target.nodeId &&
                  (link.project ?? activeProject) === target.project,
              );
              if (exists) return;
              commitLinks([
                ...links,
                {
                  label: target.title || target.nodeId,
                  node: target.nodeId,
                  ...(target.project && target.project !== activeProject
                    ? { project: target.project }
                    : {}),
                },
              ]);
            }}
          />
        )}
        <div className="meta-row">
          <button
            className="danger"
            onClick={async () => {
              const confirmed = await confirmAction({
                title: "刪除節點",
                description: `確定刪除 ${id}？節點會移到垃圾桶，可隨時復原。`,
                confirmLabel: "移到垃圾桶",
                tone: "danger",
              });
              if (confirmed) void deleteNode(id).catch(reportError);
            }}
          >
            <IconTrash size={13} />
            刪除節點
          </button>
        </div>
      </section>
  );
}
