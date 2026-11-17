/**
 * The nodes a batch undo kept on the board, by title, each with why and a
 * "Delete anyway" that goes through the normal delete (its usual checks, its own
 * undo step). The undo toast and the created card both show it.
 */

import { useState } from "react";
import { hasMessage, useI18n } from "../../i18n";
import { deleteKeptNode } from "./draftApi";
import type { KeptNode } from "./draftUi";

export function KeptList({ nodes }: { nodes: KeptNode[] }) {
  const { t, language } = useI18n();
  const [deleting, setDeleting] = useState<ReadonlySet<string>>(new Set());

  const reason = (code: string) => {
    const key = `draft.kept.reason.${code}`;
    return hasMessage(language, key) ? t(key) : t("draft.kept.reason.other");
  };

  const remove = async (id: string) => {
    setDeleting((current) => new Set(current).add(id));
    try {
      await deleteKeptNode(id);
    } finally {
      setDeleting((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  };

  return (
    <ul className="draft-kept-list">
      {nodes.map((node) => (
        <li key={node.id} className="draft-kept" data-kept-node={node.id}>
          <span className="draft-kept-title">{node.title}</span>
          <span className="draft-kept-reason">{reason(node.code)}</span>
          <button
            type="button"
            className="draft-text-btn draft-kept-delete"
            disabled={deleting.has(node.id)}
            aria-label={t("draft.kept.deleteLabel", { title: node.title })}
            onClick={() => void remove(node.id)}
          >
            {t("draft.kept.deleteAnyway")}
          </button>
        </li>
      ))}
    </ul>
  );
}
