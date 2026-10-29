import { useState } from "react";
import { useI18n } from "../../i18n";
import { reportError, useApp } from "../../store";

/**
 * The hub's GET /api/graph refusal for a graph.yaml that does not parse or
 * validate (store/graph_unreadable.go): "graph.yaml: line 3 has an error. Fix
 * the file, then reload. (yaml: ...)", in the reader's language, sometimes
 * behind 'load project "x": '. Split into the hub's sentence, which already
 * carries the advice, and the parser's or validator's own words.
 */
const HUB_FRAME = /^([^(（]*[.。]) ?[(（]([\s\S]*)[)）]$/;

export function splitUnreadableGraph(message: string): {
  sentence: string | null;
  detail: string;
} {
  const said = /(?:^|[:：] ?)graph\.yaml: ([\s\S]*)$/.exec(message)?.[1].trim() ?? message.trim();
  const framed = HUB_FRAME.exec(said);
  if (framed) return { sentence: framed[1].trim(), detail: framed[2].trim() };
  return { sentence: null, detail: said };
}

/**
 * In place of the board while graph.yaml cannot be read: the reason, how to
 * get out of it, and no way to create a node — a write would go over the
 * file the reader has to fix. It goes away by itself on the next load that
 * reads (the watcher's graph-changed), or from its own button. It lies over
 * the board rather than replacing it, so the board keeps its scroll.
 */
export function UnreadableGraphState({ message }: { message: string }) {
  const { t } = useI18n();
  const loadAll = useApp((s) => s.loadAll);
  const [busy, setBusy] = useState(false);
  const { sentence, detail } = splitUnreadableGraph(message);
  const reload = () => {
    setBusy(true);
    void loadAll()
      .catch(reportError)
      .finally(() => setBusy(false));
  };
  return (
    <div className="board-unreadable">
      <section className="ui-empty-state board-unreadable-state" role="alert">
        <span className="ui-empty-state-mark" aria-hidden="true" />
        <div>
          <b>{t("lane.unreadableTitle")}</b>
          <p>{sentence ?? t("lane.unreadableAdvice")}</p>
          {detail && <code className="board-unreadable-detail">{detail}</code>}
        </div>
        <div className="ui-empty-state-action">
          <button type="button" onClick={reload} disabled={busy}>
            {t("lane.unreadableReload")}
          </button>
        </div>
      </section>
    </div>
  );
}
