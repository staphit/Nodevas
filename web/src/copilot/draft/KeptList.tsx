/**
 * The nodes a batch undo kept on the board, by title, each with why and a
 * "Delete anyway" that goes through the normal delete (its usual checks, its own
 * undo step). The undo toast and the created card both show it.
 *
 * A deleted row takes its focused button with it, so focus moves on to the
 * row that took its place (or the one above); a keyboard user works through
 * the list without tabbing back in.
 */

import { useLayoutEffect, useRef, useState } from "react";
import { hasMessage, useI18n } from "../../i18n";
import { deleteKeptNode } from "./draftApi";
import type { KeptNode } from "./draftUi";

export function KeptList({ nodes }: { nodes: KeptNode[] }) {
  const { t, language } = useI18n();
  const [deleting, setDeleting] = useState<ReadonlySet<string>>(new Set());
  const list = useRef<HTMLUListElement>(null);
  /** The row a delete was pressed on, while focus should follow it. */
  const pressed = useRef<number | null>(null);

  useLayoutEffect(() => {
    const index = pressed.current;
    if (index === null || !list.current) return;
    pressed.current = null;
    // Only when the focus fell with the row: never pull it from elsewhere.
    const active = document.activeElement;
    if (active && active !== document.body && document.contains(active)) return;
    const buttons = list.current.querySelectorAll<HTMLButtonElement>("button.draft-kept-delete");
    if (buttons.length) buttons[Math.min(index, buttons.length - 1)].focus();
  }, [nodes]);

  const reason = (code: string) => {
    const key = `draft.kept.reason.${code}`;
    return hasMessage(language, key) ? t(key) : t("draft.kept.reason.other");
  };

  const remove = async (node: KeptNode, index: number) => {
    pressed.current = index;
    setDeleting((current) => new Set(current).add(node.id));
    try {
      await deleteKeptNode(node);
    } finally {
      setDeleting((current) => {
        const next = new Set(current);
        next.delete(node.id);
        return next;
      });
    }
  };

  return (
    <ul className="draft-kept-list" ref={list}>
      {nodes.map((node, index) => (
        <li key={node.id} className="draft-kept" data-kept-node={node.id}>
          <span className="draft-kept-title">{node.title}</span>
          <span className="draft-kept-reason">{reason(node.code)}</span>
          <button
            type="button"
            className="draft-text-btn draft-kept-delete"
            disabled={deleting.has(node.id)}
            aria-label={t("draft.kept.deleteLabel", { title: node.title })}
            onClick={() => void remove(node, index)}
          >
            {t("draft.kept.deleteAnyway")}
          </button>
        </li>
      ))}
    </ul>
  );
}
