/**
 * A draft's nodes, in order: kind, title (double-click to rename), what each
 * waits for, and the marks the checks and a refine left on it. Every change
 * goes through `onEdit` as a pure draft edit.
 */

import { useState, type KeyboardEvent } from "react";
import type { CopilotDraft, CopilotDraftExtras, CopilotDraftNode } from "../../api";
import { safeCssColor } from "../../cssColor";
import { nodeTypeById } from "../../domain/graph/nodeTypes";
import { formatList, useI18n } from "../../i18n";
import { IconClose, IconWarning } from "../../icons";
import { nodeTypeLabel } from "../../nodeTypeTheme";
import type { Graph, NodeType } from "../../types";
import {
  addAfter,
  changeKind,
  existingIdOf,
  existingRef,
  isExistingRef,
  linkInsteadOfDuplicate,
  removeAfter,
  MAX_DRAFT_NODES,
  removeNode,
  renameNode,
  takesAcceptance,
  type DiffMark,
  type Draft,
} from "./draftStore";

/** A big board offers its first nodes in the picker; the canvas reaches the rest. */
const MAX_BOARD_CHOICES = 300;

/** One mark on a node: a check's finding, by code (never the model's English). */
export interface NodeMark {
  code: string;
  severity: "error" | "warning";
  ref?: string;
  existingId?: string;
  title?: string;
}

export function KindGlyph({ type, large }: { type: NodeType; large?: boolean }) {
  const color = safeCssColor(type.color);
  return (
    <span
      className={`draft-kind-glyph${large ? " is-large" : ""}`}
      data-shape={type.shape}
      data-kind={type.id}
      style={color ? { ["--draft-kind-color" as string]: color } : undefined}
      aria-hidden
    />
  );
}

function TitleEditor({
  title,
  readOnly,
  onRename,
}: {
  title: string;
  readOnly: boolean;
  onRename: (title: string) => void;
}) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(title);
  if (editing) {
    const commit = () => {
      setEditing(false);
      if (value.trim() && value.trim() !== title) onRename(value);
    };
    const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.nativeEvent.isComposing) return;
      if (event.key === "Enter") {
        event.preventDefault();
        commit();
      } else if (event.key === "Escape") {
        // The panel closes on Escape; here it only ends the rename.
        event.preventDefault();
        event.stopPropagation();
        setEditing(false);
        setValue(title);
      }
    };
    return (
      <input
        className="draft-title-input"
        aria-label={t("draft.renameLabel")}
        value={value}
        maxLength={60}
        autoFocus
        onChange={(event) => setValue(event.target.value)}
        onBlur={commit}
        onKeyDown={onKeyDown}
      />
    );
  }
  return (
    <span
      className="draft-title"
      title={readOnly ? undefined : t("draft.rename")}
      onDoubleClick={() => {
        if (readOnly) return;
        setValue(title);
        setEditing(true);
      }}
    >
      {title}
    </span>
  );
}

/**
 * A plan step's read-only chips: the planner's tags (the goal tag, on every
 * step, left out) and acceptance criteria, greyed when the step's type takes
 * none and Create would leave them out.
 */
function PlanChips({
  extras,
  goalTag,
  usesAcceptance,
}: {
  extras: CopilotDraftExtras | undefined;
  goalTag?: string;
  usesAcceptance: boolean;
}) {
  const { t, language } = useI18n();
  const tags = (extras?.tags ?? []).filter((tag) => tag !== goalTag);
  const acceptance = extras?.acceptance ?? [];
  if (!tags.length && !acceptance.length) return null;
  return (
    <div className="draft-extras">
      {tags.length > 0 && (
        <span className="draft-chip" data-chip="tags" title={formatList(tags, language)}>
          {t("draft.plan.tags", { n: tags.length })}
        </span>
      )}
      {acceptance.length > 0 && (
        <span
          className={`draft-chip${usesAcceptance ? "" : " is-unused"}`}
          data-chip="acceptance"
          title={
            usesAcceptance
              ? acceptance.join("\n")
              : `${t("draft.plan.acceptanceUnused")}\n${acceptance.join("\n")}`
          }
        >
          {t("draft.plan.acceptance", { n: acceptance.length })}
        </span>
      )}
    </div>
  );
}

export function DraftList({
  draft,
  graph,
  nodeTypes,
  marks,
  diff,
  readOnly,
  selected,
  onToggleSelect,
  onEdit,
  extras,
  goalTag,
  maxNodes = MAX_DRAFT_NODES,
}: {
  draft: Draft;
  graph: Graph | null;
  nodeTypes: NodeType[];
  marks: Record<string, NodeMark[]>;
  diff: Record<string, DiffMark>;
  readOnly: boolean;
  selected: ReadonlySet<string>;
  onToggleSelect: (key: string) => void;
  onEdit: (update: (draft: CopilotDraft) => CopilotDraft) => void;
  /** A coding agent's plan: what the planner wrote per step, by key. */
  extras?: Record<string, CopilotDraftExtras>;
  /** The plan's goal tag, which every step carries and the chips leave out. */
  goalTag?: string;
  /** The draft's node cap, for the issue texts. */
  maxNodes?: number;
}) {
  const { t } = useI18n();
  const boardNodes = graph?.nodes ?? [];
  const titleOfRef = (ref: string) => {
    if (isExistingRef(ref)) {
      const id = existingIdOf(ref);
      return boardNodes.find((node) => node.id === id)?.title || id;
    }
    return draft.nodes.find((node) => node.key === ref)?.title || ref;
  };

  const row = (node: CopilotDraftNode, index: number) => {
    const type = nodeTypeById(graph, node.kind);
    const nodeMarks = marks[node.key] ?? [];
    const mark = diff[node.key];
    const after = node.after ?? [];
    const draftChoices = draft.nodes.filter(
      (other) => other.key !== node.key && !after.includes(other.key),
    );
    const boardChoices = boardNodes
      .filter((item) => !after.includes(existingRef(item.id)))
      .slice(0, MAX_BOARD_CHOICES);
    return (
      <li
        key={node.key}
        className={`draft-node${mark ? ` is-${mark}` : ""}${nodeMarks.some((item) => item.severity === "error") ? " has-error" : ""}`}
        data-draft-key={node.key}
      >
        <div className="draft-node-row">
          {!readOnly && (
            <input
              type="checkbox"
              className="draft-node-select"
              checked={selected.has(node.key)}
              onChange={() => onToggleSelect(node.key)}
              aria-label={t("draft.select", { title: node.title })}
            />
          )}
          <span className="draft-node-index" aria-hidden>
            {index + 1}
          </span>
          <span className="draft-kind" title={t("draft.kind", { type: nodeTypeLabel(type, t) })}>
            <KindGlyph type={type} />
            {!readOnly && (
              <select
                className="draft-kind-select"
                aria-label={t("draft.kind", { type: nodeTypeLabel(type, t) })}
                value={type.id}
                onChange={(event) => {
                  const kind = event.target.value;
                  onEdit((current) => changeKind(current, node.key, kind));
                }}
              >
                {nodeTypes.map((option) => (
                  <option key={option.id} value={option.id}>
                    {nodeTypeLabel(option, t)}
                  </option>
                ))}
              </select>
            )}
          </span>
          <TitleEditor
            key={node.title}
            title={node.title}
            readOnly={readOnly}
            onRename={(title) => onEdit((current) => renameNode(current, node.key, title))}
          />
          {mark && (
            <span className={`draft-diff draft-diff-${mark}`}>
              {mark === "added" ? "+" : "~"}
              <span className="draft-sr">{t(`draft.diff.${mark}`)}</span>
            </span>
          )}
          {nodeMarks.length > 0 && (
            <span className="draft-node-warn" title={nodeMarks.map((item) => issueText(item, t, maxNodes)).join("\n")}>
              <IconWarning size={14} />
            </span>
          )}
          {!readOnly && (
            <button
              type="button"
              className="draft-icon-btn draft-remove"
              onClick={() => onEdit((current) => removeNode(current, node.key))}
              aria-label={t("draft.removeTitle", { title: node.title })}
              title={t("draft.remove")}
            >
              <IconClose size={12} />
            </button>
          )}
        </div>
        {extras && (
          <PlanChips
            extras={extras[node.key]}
            goalTag={goalTag}
            usesAcceptance={takesAcceptance(node.kind, nodeTypes)}
          />
        )}
        {(after.length > 0 || !readOnly) && (
          <div className="draft-after">
            {after.map((ref) => (
              <span key={ref} className={`draft-chip${isExistingRef(ref) ? " is-existing" : ""}`}>
                {t("draft.after", { title: titleOfRef(ref) })}
                {!readOnly && (
                  <button
                    type="button"
                    className="draft-chip-x"
                    onClick={() => onEdit((current) => removeAfter(current, node.key, ref))}
                    aria-label={t("draft.afterRemove", { title: titleOfRef(ref) })}
                  >
                    <IconClose size={10} />
                  </button>
                )}
              </span>
            ))}
            {!readOnly && (draftChoices.length > 0 || boardChoices.length > 0) && (
              <select
                className="draft-after-picker"
                aria-label={t("draft.afterAdd")}
                value=""
                onChange={(event) => {
                  const target = event.target.value;
                  if (target) onEdit((current) => addAfter(current, node.key, target));
                }}
              >
                <option value="">{t("draft.afterAdd")}</option>
                {draftChoices.length > 0 && (
                  <optgroup label={t("draft.afterPicker.draft")}>
                    {draftChoices.map((other) => (
                      <option key={other.key} value={other.key}>
                        {other.title}
                      </option>
                    ))}
                  </optgroup>
                )}
                {boardChoices.length > 0 && (
                  <optgroup label={t("draft.afterPicker.board")}>
                    {boardChoices.map((item) => (
                      <option key={item.id} value={existingRef(item.id)}>
                        {item.title || item.id}
                      </option>
                    ))}
                  </optgroup>
                )}
              </select>
            )}
          </div>
        )}
        {nodeMarks.length > 0 && (
          <ul className="draft-issues">
            {nodeMarks.map((item, markIndex) => (
              <li key={`${item.code}-${markIndex}`} className={`draft-issue is-${item.severity}`} data-issue={item.code}>
                {item.code === "duplicate_existing" && item.existingId ? (
                  <>
                    <span>{t("draft.dup", { title: item.title || titleOfRef(existingRef(item.existingId)) })}</span>
                    {!readOnly && (
                      <button
                        type="button"
                        className="draft-link-btn"
                        onClick={() =>
                          onEdit((current) => linkInsteadOfDuplicate(current, node.key, item.existingId!))
                        }
                      >
                        {t("draft.dupLink")}
                      </button>
                    )}
                  </>
                ) : (
                  <span>{issueText(item, t, maxNodes)}</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </li>
    );
  };

  return (
    <ol className="draft-list" aria-label={t("draft.list")}>
      {draft.nodes.map(row)}
    </ol>
  );
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

const KNOWN_ISSUES = new Set([
  "empty_draft",
  "too_many",
  "bad_key",
  "duplicate_key",
  "empty_title",
  "unknown_kind",
  "unknown_after",
  "self_after",
  "cycle",
  "unknown_ref",
  "duplicate_existing",
  "duplicate_in_draft",
  "body_clipped",
]);

/** An issue in the reader's language, by its code (`max`: the draft's node cap). */
export function issueText(
  issue: { code: string; title?: string },
  t: Translate,
  max: number = MAX_DRAFT_NODES,
): string {
  if (!KNOWN_ISSUES.has(issue.code)) return t("draft.issue.other");
  return t(`draft.issue.${issue.code}`, { title: issue.title ?? "", max });
}
