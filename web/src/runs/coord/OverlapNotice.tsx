import { useState } from "react";
import { runFinal, type Run, type RunOverlap } from "../../api";
import { formatList, useI18n } from "../../i18n";

export interface OverlapNoticeProps {
  overlaps: readonly RunOverlap[] | null | undefined;
  run: Run;
  simple?: boolean;
  /** Pre-fills the interactive terminal composer with overlap.tellText. */
  onTell?: (text: string) => void;
  /** Opens/pre-fills the follow-up composer for a background global run. */
  onOpenFollowUp?: (text: string) => void;
}

export function OverlapNotice({
  overlaps,
  run,
  simple = false,
  onTell,
  onOpenFollowUp,
}: OverlapNoticeProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);

  const list = overlaps ?? [];
  // Security guard (§C.10): hidden overlaps never expose any field beyond hidden=true.
  const hiddenList = list.filter((item) => item.hidden);
  // Weak add-only overlaps (strongCount === 0) never trigger a notice on their own.
  const ownStrongList = list.filter((item) => !item.hidden && (item.strongCount ?? 0) > 0);
  const hasWeakAddOnly = list.some(
    (item) => !item.hidden && (item.pathCount ?? (item.paths?.length ?? 0)) > (item.strongCount ?? 0),
  );

  if (hiddenList.length === 0 && ownStrongList.length === 0) {
    return null;
  }

  // Collect strong paths from own overlaps only.
  const strongPaths: string[] = [];
  for (const item of ownStrongList) {
    const strongCount = item.strongCount ?? 0;
    const paths = (item.paths ?? []).slice(0, strongCount);
    for (const path of paths) {
      if (path && !strongPaths.includes(path)) {
        strongPaths.push(path);
      }
    }
  }

  // "Tell it" appears only for global runs the viewer may type into or follow up (§C.8).
  let tellAction: (() => void) | null = null;
  if (run.global && strongPaths.length > 0) {
    const tellText = t("overlap.tellText", { files: formatList(strongPaths) });
    const isLiveInteractive = run.mode === "interactive" && !runFinal(run.status);
    if (isLiveInteractive) {
      if (!run.viewOnly && onTell) {
        tellAction = () => onTell(tellText);
      }
    } else {
      const closed =
        Boolean(run.continuedBy) ||
        run.status === "declined" ||
        run.workspace === "discarded" ||
        run.workspace === "merged_local" ||
        run.prState === "merged" ||
        run.prState === "closed" ||
        run.merge?.status === "pending";
      const target = onOpenFollowUp ?? onTell;
      if (!closed && target) {
        tellAction = () => target(tellText);
      }
    }
  }

  if (simple) {
    return (
      <div className="coord-overlap is-simple" role="status">
        <div className="coord-overlap-bar">
          <span className="coord-overlap-title">{t("overlap.simple.title")}</span>
          <span className="coord-overlap-hint">{t("overlap.simple.hint")}</span>
          {tellAction && (
            <button type="button" className="coord-overlap-tell" onClick={tellAction}>
              {t("overlap.tell")}
            </button>
          )}
        </div>
      </div>
    );
  }

  // Hidden-only overlap: one non-expandable line with no paths and no count.
  if (ownStrongList.length === 0) {
    return (
      <div className="coord-overlap is-hidden" role="status">
        <div className="coord-overlap-bar">
          <span className="coord-overlap-title">{t("overlap.hidden")}</span>
        </div>
      </div>
    );
  }

  return (
    <div className={`coord-overlap${expanded ? " is-expanded" : ""}`} role="status">
      <div className="coord-overlap-bar">
        <button
          type="button"
          className="coord-overlap-toggle"
          aria-expanded={expanded}
          onClick={() => setExpanded((prev) => !prev)}
        >
          <span className="coord-overlap-title">{t("overlap.title")}</span>
        </button>
        {tellAction && (
          <button type="button" className="coord-overlap-tell" onClick={tellAction}>
            {t("overlap.tell")}
          </button>
        )}
      </div>
      {expanded && (
        <div className="coord-overlap-details">
          {ownStrongList.map((item, index) => {
            const strongCount = item.strongCount ?? 0;
            const itemPaths = (item.paths ?? []).slice(0, strongCount);
            const isKept = item.status === "succeeded";
            const summary = isKept
              ? t("overlap.kept", { count: strongCount })
              : t("overlap.mine", {
                  title: item.title || item.nodeId || item.runId || "",
                  count: strongCount,
                });
            return (
              <div key={item.runId ?? index} className="coord-overlap-item">
                <div className="coord-overlap-item-head">{summary}</div>
                {itemPaths.length > 0 && (
                  <ul className="coord-overlap-paths">
                    {itemPaths.map((path) => (
                      <li key={path} className="coord-overlap-path">
                        <code>{path}</code>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            );
          })}
          {hasWeakAddOnly && <div className="coord-overlap-weak">{t("overlap.addOnly")}</div>}
          {hiddenList.length > 0 && (
            <div className="coord-overlap-hidden-note">{t("overlap.hidden")}</div>
          )}
        </div>
      )}
    </div>
  );
}
