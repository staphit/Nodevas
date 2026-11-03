import { useEffect } from "react";
import { explorerApi, type AgentItem } from "../api/explorer";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { costText, roleText, stateText } from "./explorerFormat";
import { useExplorer } from "./explorerStore";
import { useLoad } from "./useExplorerData";

/** How often the list refreshes while it is shown. */
const REFRESH_MS = 15_000;

const ICON: Record<string, string> = {
  running: "▶",
  succeeded: "✓",
  failed: "✗",
  cancelled: "■",
  online: "⌁",
  offline: "⌁",
};

/** Running, external and recently finished agents. */
export default function AgentsTab() {
  const { t } = useI18n();
  const view = useExplorer((s) => s.view);
  const open = useExplorer((s) => s.open);
  const project = useApp((s) => s.activeProject);
  const agents = useLoad(() => explorerApi.agents(), `agents:${project}`);
  const { reload } = agents;
  useEffect(() => {
    const timer = window.setInterval(reload, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [reload]);

  const row = (item: AgentItem) => {
    const on = view?.kind === "agent" && view.id === item.id;
    const meta = [stateText(item.state, t)];
    if (item.costUsd > 0 || item.kind === "run") meta.push(costText(item, t));
    return (
      <button
        key={item.id}
        type="button"
        className={`ex-row ex-agent${on ? " on" : ""}`}
        onClick={() => open({ kind: "agent", id: item.id })}
      >
        <span className="ex-ico" aria-hidden="true">
          {ICON[item.state] ?? "•"}
        </span>
        <span className="ex-name">
          <span className="ex-who">{item.agent}</span>
          {item.nodeTitle && (
            <>
              {" · "}
              <span>{item.nodeTitle}</span>
            </>
          )}
        </span>
        {item.kind === "run" && item.role && <span className="ex-chip role">{roleText(item.role, t)}</span>}
        <span className="ex-meta">{meta.join(" · ")}</span>
      </button>
    );
  };

  const data = agents.data;
  const groups: [string, AgentItem[]][] = data
    ? [
        ["explorer.agent.live", data.live],
        ["explorer.agent.external", data.external],
        ["explorer.agent.recent", data.recent],
      ]
    : [];
  const empty = data && groups.every(([, items]) => items.length === 0);

  return (
    <div className="ex-pane">
      <div className="ex-phead">
        <h2>{t("explorer.tab.agent")}</h2>
      </div>
      <div className="ex-plist">
        {groups
          .filter(([, items]) => items.length > 0)
          .map(([key, items]) => (
            <section key={key} className="ex-root" aria-label={t(key)}>
              <div className="ex-sec">
                <span className="ex-sec-title">{t(key)}</span>
              </div>
              {items.map(row)}
            </section>
          ))}
        {empty && <p className="ex-note">{t("explorer.agent.none")}</p>}
        {agents.error != null && <p className="ex-note">{(agents.error as Error).message ?? String(agents.error)}</p>}
      </div>
    </div>
  );
}
