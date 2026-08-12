/**
 * Document outline panel.
 *
 * Lists the headings of the document being edited and jumps to one when it is
 * clicked. It reads the markdown source, so it works the same in live, source
 * and preview mode.
 */

import { useMemo } from "react";
import { outlineBaseLevel, parseOutline } from "../../editor/outline";
import { EmptyState } from "../InteractionPrimitives";
import { useI18n } from "../../i18n";

export function DocumentOutline({
  source,
  activeLine,
  onJump,
}: {
  source: string;
  /** Line the caret is on, so the reader can see where they are. */
  activeLine?: number;
  onJump: (entry: { line: number; from: number }) => void;
}) {
  const { t } = useI18n();
  const entries = useMemo(() => parseOutline(source), [source]);
  const base = useMemo(() => outlineBaseLevel(entries), [entries]);

  // The current heading is the last one at or above the caret.
  const currentLine = useMemo(() => {
    if (activeLine === undefined) return null;
    let current: number | null = null;
    for (const entry of entries) {
      if (entry.line <= activeLine) current = entry.line;
      else break;
    }
    return current;
  }, [entries, activeLine]);

  return (
    <nav className="doc-outline" aria-label={t("editor.outline.aria")}>
      {entries.length === 0 ? (
        <EmptyState
          title={t("editor.outline.empty")}
          description={t("editor.outline.emptyHint")}
        />
      ) : (
        <ul>
          {entries.map((entry) => (
            <li key={`${entry.line}:${entry.text}`}>
              <button
                type="button"
                className={`doc-outline-item${entry.line === currentLine ? " current" : ""}`}
                style={{ paddingInlineStart: `${(entry.level - base) * 12 + 8}px` }}
                data-level={entry.level}
                onClick={() => onJump(entry)}
                title={t("editor.outline.jump", { title: entry.text })}
              >
                {entry.text}
              </button>
            </li>
          ))}
        </ul>
      )}
    </nav>
  );
}
