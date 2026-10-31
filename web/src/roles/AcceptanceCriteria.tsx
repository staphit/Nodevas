// The node panel's acceptance criteria, at the top of the Content tab, for
// types agents work (dispatch and Autopilot). Writes go through a graph op
// "acceptance"; the hub decides who may write them (agents by role only, and
// nobody but a person once an Executor was dispatched and they locked).

import { useEffect, useId, useState, type KeyboardEvent } from "react";

import { hasAcceptance, MAX_ACCEPTANCE_ITEMS, MAX_ACCEPTANCE_TEXT, nextAcceptanceItemID } from "../domain/graph/acceptance";
import { nodeTypeOf } from "../domain/graph/nodeTypes";
import { requiresAcceptance, workflowForNode } from "../domain/graph/stages";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import type { AcceptanceItem } from "../types";

/** Event a dispatch dialog sends to bring the criteria into view. */
export const FOCUS_ACCEPTANCE_EVENT = "nodevas-focus-acceptance";

const NO_ITEMS: AcceptanceItem[] = [];

export function AcceptanceCriteria({ id }: { id: string }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const node = graph?.nodes?.find((candidate) => candidate.id === id);
  const items = node?.acceptance?.items ?? NO_ITEMS;
  const locked = Boolean(node?.acceptance?.locked);
  const by = node?.acceptance?.by ?? "";
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  const headingId = useId();
  const newId = useId();

  useEffect(() => {
    const focus = (event: Event) => {
      if ((event as CustomEvent<string>).detail !== id) return;
      document.getElementById(newId)?.focus();
    };
    window.addEventListener(FOCUS_ACCEPTANCE_EVENT, focus);
    return () => window.removeEventListener(FOCUS_ACCEPTANCE_EVENT, focus);
  }, [id, newId]);

  if (!node || !hasAcceptance(graph, node.kind)) return null;

  // Only work an Executor is dispatched to needs them first; a Goal's come
  // from QA or a person.
  const required =
    items.length === 0 &&
    nodeTypeOf(graph, node).agentMode === "dispatch" &&
    requiresAcceptance(workflowForNode(graph, id));

  /** Sends the whole new list; a locked list asks the person first. */
  const write = async (next: AcceptanceItem[]): Promise<boolean> => {
    if (locked && !window.confirm(t("acceptance.lockedConfirm"))) return false;
    setError("");
    const result = await useApp.getState().runGraphCommand({
      type: "node.setAcceptance",
      nodeId: id,
      acceptance: { items: next },
    });
    if (!result.ok) setError(result.message || t("acceptance.saveFailed"));
    return result.ok;
  };

  const add = async () => {
    const text = draft.trim();
    if (!text || items.length >= MAX_ACCEPTANCE_ITEMS) return;
    if (await write([...items, { id: nextAcceptanceItemID(items), text }])) setDraft("");
  };

  const move = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= items.length) return;
    const next = [...items];
    [next[index], next[target]] = [next[target], next[index]];
    void write(next);
  };

  const edit = (itemId: string, input: HTMLInputElement) => {
    const text = input.value.trim();
    const current = items.find((item) => item.id === itemId);
    if (!current || text === current.text) return;
    if (!text) {
      input.value = current.text;
      return;
    }
    void write(items.map((item) => (item.id === itemId ? { ...item, text } : item))).then((ok) => {
      if (!ok) input.value = current.text;
    });
  };

  const onItemKey = (itemId: string) => (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      edit(itemId, event.currentTarget);
    } else if (event.key === "Escape") {
      const current = items.find((item) => item.id === itemId);
      if (current) event.currentTarget.value = current.text;
    }
  };

  return (
    <section className="acceptance" role="region" aria-labelledby={headingId}>
      <header className="acceptance-head">
        <h3 id={headingId}>{t("acceptance.title")}</h3>
        {locked && (
          <span className="acceptance-lock" title={t("acceptance.lockedHint")}>
            {t("acceptance.locked")}
          </span>
        )}
        {by && <small className="acceptance-by">{t("acceptance.by", { by })}</small>}
      </header>
      {required && (
        <p className="acceptance-required" role="alert">
          {t("acceptance.required")}
        </p>
      )}
      {items.length > 0 && (
        <ol className="acceptance-list">
          {items.map((item, index) => (
            <li key={`${item.id}\u0000${item.text}`} className="acceptance-item">
              <span className="acceptance-id" aria-hidden="true">
                {item.id}
              </span>
              <input
                className="acceptance-text"
                defaultValue={item.text}
                maxLength={MAX_ACCEPTANCE_TEXT}
                aria-label={t("acceptance.item", { id: item.id })}
                onKeyDown={onItemKey(item.id)}
                onBlur={(event) => edit(item.id, event.currentTarget)}
              />
              <span className="acceptance-actions">
                <button
                  type="button"
                  className="acceptance-icon"
                  disabled={index === 0}
                  aria-label={t("acceptance.moveUp", { id: item.id })}
                  title={t("acceptance.moveUp", { id: item.id })}
                  onClick={() => move(index, -1)}
                >
                  ↑
                </button>
                <button
                  type="button"
                  className="acceptance-icon"
                  disabled={index === items.length - 1}
                  aria-label={t("acceptance.moveDown", { id: item.id })}
                  title={t("acceptance.moveDown", { id: item.id })}
                  onClick={() => move(index, 1)}
                >
                  ↓
                </button>
                <button
                  type="button"
                  className="acceptance-icon"
                  aria-label={t("acceptance.remove", { id: item.id })}
                  title={t("acceptance.remove", { id: item.id })}
                  onClick={() => void write(items.filter((candidate) => candidate.id !== item.id))}
                >
                  ×
                </button>
              </span>
            </li>
          ))}
        </ol>
      )}
      <form
        className="acceptance-add"
        onSubmit={(event) => {
          event.preventDefault();
          void add();
        }}
      >
        <input
          id={newId}
          value={draft}
          maxLength={MAX_ACCEPTANCE_TEXT}
          // The placeholder is the short ask, so it fits the drawer at any
          // text size; the example is said on hover.
          placeholder={t("acceptance.placeholder")}
          title={t("acceptance.placeholderHint")}
          aria-label={t("acceptance.new")}
          disabled={items.length >= MAX_ACCEPTANCE_ITEMS}
          onChange={(event) => setDraft(event.target.value)}
        />
        <button type="submit" disabled={!draft.trim() || items.length >= MAX_ACCEPTANCE_ITEMS}>
          {t("acceptance.add")}
        </button>
      </form>
      {error && (
        <p className="acceptance-error" role="status">
          {error}
        </p>
      )}
    </section>
  );
}
