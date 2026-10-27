/**
 * Node metadata [B-04/B-06].
 *
 * Writes `graph.yaml` through the typed node commands. Every field commits on
 * blur/change, and the panel reports whether that write landed. The title and
 * the assignee are read every time a node is opened, so they live in the
 * panel's header; the rest sits behind "More properties".
 */

import { useCallback, useEffect, useRef, useState } from "react";
import type { NodeMetadataPatch } from "../../domain/graph/node";
import { IconTrash } from "../../icons";
import {
  nodeById,
  reportError,
  useApp,
  type CommandResult,
} from "../../store";
import type { NodeLinkRef } from "../../types";
import { NodeLinkPicker } from "../NodeLinkPicker";
import { confirmAction } from "../ConfirmDialog";
import { NodeAppearance } from "./NodeAppearance";
import { useI18n } from "../../i18n";
import { isKnownKind, normalizeKind } from "../../domain/graph/nodeTypes";
import { nodeTypeLabel, useNodeTypes } from "../../nodeTypeTheme";

/** Reports a command that failed; a success needs no word of its own. */
function reportCommand(run: Promise<CommandResult>) {
  void run.then((result) => {
    if (!result.ok) reportError(new Error(result.message));
  });
}

/**
 * The node's title, edited where it is read: the panel's heading. Commits on
 * blur like every other field; Enter commits, Escape puts it back.
 */
export function NodeTitleField({
  id,
  className = "drawer-title-input",
}: {
  id: string;
  className?: string;
}) {
  const { t } = useI18n();
  const title = useApp((state) => nodeById(state.graph, id)?.title ?? "");
  const updateNodeMetadata = useApp((state) => state.updateNodeMetadata);
  return (
    <input
      key={`${id}:${title}`}
      id={`title-${id}`}
      className={className}
      defaultValue={title}
      placeholder={id}
      aria-label={t("node.meta.title")}
      onBlur={(event) => {
        if (event.target.value === title) return;
        reportCommand(updateNodeMetadata(id, { title: event.target.value }));
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter") event.currentTarget.blur();
        if (event.key === "Escape") {
          // The panel closes on Escape; inside the field it only means
          // "never mind".
          event.nativeEvent.stopImmediatePropagation();
          event.currentTarget.value = title;
          event.currentTarget.blur();
        }
      }}
    />
  );
}

/** Who the node is assigned to: a known user, or a new name typed in. */
export function AssigneeField({ id }: { id: string }) {
  const { t } = useI18n();
  const users = useApp((state) => state.graph?.users);
  const assigneeId = useApp((state) => nodeById(state.graph, id)?.assignee);
  const updateNode = useApp((state) => state.updateNode);
  const assignedUserName = (users ?? []).find((user) => user.id === assigneeId)?.name ?? "";
  const [assignee, setAssignee] = useState(assignedUserName);
  useEffect(() => setAssignee(assignedUserName), [assignedUserName]);
  // Escape blurs to leave the field, and the blur would commit what was
  // typed: the reset to the saved name has not rendered yet. The flag tells
  // that blur to drop it instead.
  const cancelledRef = useRef(false);

  const commitAssignee = () => {
    if (cancelledRef.current) {
      cancelledRef.current = false;
      return;
    }
    const name = assignee.trim();
    if (name === assignedUserName) return;
    reportCommand(updateNode({ type: "node.assignByName", nodeId: id, name }));
  };

  return (
    <>
      <input
        id={`assignee-${id}`}
        className="assignee-input"
        list={`assignee-options-${id}`}
        value={assignee}
        aria-label={t("node.meta.assignee")}
        placeholder={t("drawer.assigneePlaceholder")}
        title={t("node.meta.assigneePlaceholder")}
        onChange={(event) => setAssignee(event.target.value)}
        onFocus={() => {
          cancelledRef.current = false;
        }}
        onBlur={commitAssignee}
        onKeyDown={(event) => {
          if (event.key === "Enter") event.currentTarget.blur();
          if (event.key === "Escape") {
            event.nativeEvent.stopImmediatePropagation();
            cancelledRef.current = true;
            setAssignee(assignedUserName);
            event.currentTarget.blur();
          }
        }}
      />
      <datalist id={`assignee-options-${id}`}>
        {(users ?? []).map((user) => (
          <option key={user.id} value={user.name} />
        ))}
      </datalist>
    </>
  );
}

/**
 * Everything about a node that is not read every time it is opened: type,
 * entry, priority, tags, links, card appearance, write access and deletion.
 */
export function NodeMetaForm({ id }: { id: string }) {
  const { t } = useI18n();
  const graph = useApp((state) => state.graph);
  const updateNodeMetadata = useApp((state) => state.updateNodeMetadata);
  const updateNode = useApp((state) => state.updateNode);
  const deleteNode = useApp((state) => state.deleteNode);
  const openNodeLink = useApp((state) => state.openNodeLink);
  const activeProject = useApp((state) => state.activeProject);

  const node = nodeById(graph, id);

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

  const nodeTypes = useNodeTypes();
  const currentKind = normalizeKind(node?.kind) || "task";
  const legacyKind = isKnownKind(graph, currentKind) ? "" : currentKind;

  const commitMeta = useCallback(
    (field: "kind" | "priority" | "writeAccess", value: string) => {
      reportCommand(updateNodeMetadata(id, { [field]: value } as NodeMetadataPatch));
    },
    [id, updateNodeMetadata],
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
    [id, updateNode],
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
    [id, updateNodeMetadata],
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
    [id, updateNode],
  );

  return (
      <section className="meta-form" aria-label={t("drawer.moreProperties")}>
        <div className="meta-grid">
          <label htmlFor={`kind-${id}`}>{t("node.meta.kind")}</label>
          <select
            id={`kind-${id}`}
            value={currentKind}
            onChange={(e) => commitMeta("kind", e.target.value)}
          >
            <optgroup label={t("nodeType.builtin")}>
              {nodeTypes
                .filter((type) => type.builtin)
                .map((type) => (
                  <option key={type.id} value={type.id}>
                    {nodeTypeLabel(type, t)}
                  </option>
                ))}
            </optgroup>
            {nodeTypes.some((type) => !type.builtin) && (
              <optgroup label={t("nodeType.custom")}>
                {nodeTypes
                  .filter((type) => !type.builtin)
                  .map((type) => (
                    <option key={type.id} value={type.id}>
                      {type.label}
                    </option>
                  ))}
              </optgroup>
            )}
            {/* A free-form kind from an older file reads as Task but is shown
                as written, so changing it is a choice rather than an accident. */}
            {legacyKind && (
              <option value={legacyKind} disabled>
                {legacyKind}
              </option>
            )}
          </select>
          <label htmlFor={`entry-${id}`}>{t("node.meta.entry")}</label>
          <select
            id={`entry-${id}`}
            value={entryChoice}
            title={t("node.meta.entryTitle")}
            onChange={(event) => commitEntryOverride(event.target.value)}
          >
            <option value="auto">
              {autoEntry ? t("node.entry.autoStart") : t("node.entry.autoNotStart")}
            </option>
            <option value="yes">{t("node.entry.yes")}</option>
            <option value="no">{t("node.entry.no")}</option>
          </select>
          <label htmlFor={`priority-${id}`}>{t("node.meta.priority")}</label>
          <select
            id={`priority-${id}`}
            value={node?.priority ?? ""}
            onChange={(event) => commitMeta("priority", event.target.value)}
          >
            <option value="">{t("node.meta.unset")}</option>
            <option value="urgent">{t("node.priority.urgent")}</option>
            <option value="high">{t("node.priority.high")}</option>
            <option value="medium">{t("node.priority.medium")}</option>
            <option value="low">{t("node.priority.low")}</option>
          </select>
          <label htmlFor={`tags-${id}`}>{t("node.meta.tags")}</label>
          <input
            id={`tags-${id}`}
            key={`${id}:${(node?.tags ?? []).join(",")}`}
            defaultValue={(node?.tags ?? []).join(", ")}
            placeholder={t("node.meta.tagsPlaceholder")}
            onBlur={(event) => commitTags(event.target.value)}
          />
          <span className="meta-grid-label">{t("node.meta.links")}</span>
          <div className="node-links-editor">
            {links.length === 0 && (
              <p className="settings-hint">
                {t("node.meta.linksEmpty")}
              </p>
            )}
            {links.map((link, index) => (
              <div className="node-link-chip" key={`${link.project ?? ""}/${link.node}`}>
                <input
                  value={link.label}
                  aria-label={t("node.meta.linkName", { index: String(index + 1) })}
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
                  title={t("node.meta.openNode")}
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
                  aria-label={t("node.meta.removeLink", { label: link.label })}
                  onClick={() =>
                    commitLinks(links.filter((_, item) => item !== index))
                  }
                >
                  <IconTrash size={12} />
                </button>
              </div>
            ))}
            <button type="button" onClick={() => setPickerOpen(true)}>
              ＋ {t("node.meta.addLink")}
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
        {/* The card's look on the board: formerly a tab of its own. */}
        <details className="meta-appearance">
          <summary>{t("drawer.appearance")}</summary>
          <NodeAppearance id={id} />
        </details>
        <div className="meta-grid">
          <label htmlFor={`write-access-${id}`}>{t("node.meta.writeAccess")}</label>
          <select
            id={`write-access-${id}`}
            value={node?.writeAccess ?? ""}
            onChange={(event) => commitMeta("writeAccess", event.target.value)}
          >
            <option value="">{t("node.writeAccess.everyone")}</option>
            <option value="worker">{t("node.writeAccess.worker")}</option>
            <option value="orchestrator">{t("node.writeAccess.orchestrator")}</option>
            <option value="human-only">{t("node.writeAccess.humanOnly")}</option>
          </select>
        </div>
        <div className="meta-actions">
          <button
            type="button"
            className="danger"
            onClick={async () => {
              const confirmed = await confirmAction({
                title: t("node.meta.delete"),
                description: t("node.meta.deleteDescription", { id }),
                confirmLabel: t("batch.moveToTrash"),
                tone: "danger",
              });
              if (confirmed) void deleteNode(id).catch(reportError);
            }}
          >
            <IconTrash size={13} />
            {t("node.meta.delete")}
          </button>
        </div>
      </section>
  );
}
