import { useState } from "react";
import { api } from "../api";
import { explorerApi, type AgentItem, type RepoSummary, type SessionAction } from "../api/explorer";
import type { Run, RunEvent } from "../api/runs";
import { openNodeDetail } from "../components/drawer/openNodeDetail";
import { useI18n } from "../i18n";
import { formatTokens } from "../usage/usageFormat";
import { clockText, costText, durationText, roleText, stateText } from "./explorerFormat";
import { useExplorer } from "./explorerStore";
import { useLoad } from "./useExplorerData";

export interface AgentViewProps {
  id: string;
}

type Dot = "txt" | "tool" | "red" | "green";

interface TimelineEntry {
  key: string;
  at: string;
  dot: Dot;
  text: string;
  sub?: string;
}

const EXCERPT = 240;
const excerpt = (text: string) => (text.length > EXCERPT ? `${text.slice(0, EXCERPT)}…` : text);

function findItem(data: { live: AgentItem[]; external: AgentItem[]; recent: AgentItem[] } | null, id: string) {
  if (!data) return undefined;
  return [...data.live, ...data.external, ...data.recent].find((item) => item.id === id);
}

/** A run's events as timeline entries; text and tool calls, errors red. */
function runEntries(events: RunEvent[]): TimelineEntry[] {
  const out: TimelineEntry[] = [];
  for (const event of events) {
    if (event.type === "text" && event.text) {
      out.push({ key: `e${event.seq}`, at: event.at, dot: "txt", text: excerpt(event.text) });
    } else if (event.type === "tool_call") {
      out.push({ key: `e${event.seq}`, at: event.at, dot: "tool", text: event.tool ?? "", sub: event.toolId });
    } else if (event.type === "tool_result" && event.isError) {
      out.push({ key: `e${event.seq}`, at: event.at, dot: "red", text: excerpt(event.text ?? ""), sub: event.tool });
    } else if (event.type === "error") {
      out.push({ key: `e${event.seq}`, at: event.at, dot: "red", text: excerpt(event.text ?? "") });
    }
  }
  return out;
}

/** The repo a run's file lives in: the run's own repo when the run is known,
 * else the runner's only one. */
function repoFor(item: AgentItem, run: Run | undefined, repos: RepoSummary[]): RepoSummary | undefined {
  if (run) {
    const exact = repos.find((repo) => repo.runnerId === run.runnerId && repo.repoId === run.repoId);
    if (exact) return exact;
  }
  return repos.find((repo) => repo.runnerName === item.runnerName);
}

/** One agent: cards, files touched, event timeline. */
export default function AgentView({ id }: AgentViewProps) {
  const { t } = useI18n();
  const open = useExplorer((s) => s.open);
  const [note, setNote] = useState<string | null>(null);
  // The agent and its timeline arrive together, so the view never shows an
  // agent with an empty log that is still on its way.
  const loaded = useLoad(async () => {
    const item = findItem(await explorerApi.agents(), id);
    if (!item) return { item, events: [] as RunEvent[], actions: [] as SessionAction[] };
    const [events, actions] = await Promise.all([
      item.kind === "run" && item.runId
        ? api.getRunEvents(item.runId).then((r) => r.events ?? [], () => [] as RunEvent[])
        : Promise.resolve([] as RunEvent[]),
      item.kind === "session" && item.sessionId
        ? explorerApi.sessionActions(item.sessionId).catch(() => [] as SessionAction[])
        : Promise.resolve([] as SessionAction[]),
    ]);
    return { item, events, actions };
  }, `agent:${id}`);
  const item = loaded.data?.item;
  const runId = item?.kind === "run" ? item.runId ?? "" : "";
  const repos = useLoad(() => explorerApi.repos(), "repos");
  const runs = useLoad(() => (runId ? api.listProjectRuns() : Promise.resolve(null)), `runs:${runId}`);

  if (!loaded.data) {
    return loaded.error ? <p className="ex-note">{String((loaded.error as Error).message ?? loaded.error)}</p> : null;
  }
  if (!item) return <p className="ex-note">{t("explorer.agent.gone")}</p>;

  const run = runs.data?.runs?.find((candidate) => candidate.id === runId);
  const timeline: TimelineEntry[] =
    item.kind === "run"
      ? runEntries(loaded.data.events)
      : loaded.data.actions.map((action, i) => ({
          key: `a${i}`,
          at: action.at,
          dot: action.action === "doing" || action.action === "changes" ? "tool" : "txt",
          text: t(`explorer.action.${action.action}`, {
            doing: action.detail,
            to: action.detail,
            question: action.detail,
            count: action.detail,
            tokens: action.detail,
          }),
        }));
  if (item.testState) {
    timeline.push({
      key: "test",
      at: item.finishedAt || "",
      dot: item.testState,
      text: t(`warroom.test.${item.testState}`),
    });
  }

  const openFile = (path: string) => {
    setNote(null);
    const repo = repoFor(item, run, repos.data ?? []);
    if (repo) {
      if (!repo.online) {
        setNote(t("explorer.agent.repoOffline"));
        return;
      }
      open({ kind: "file", source: "repo", runnerId: repo.runnerId, runnerName: repo.runnerName, repoId: repo.repoId, path });
      return;
    }
    open({ kind: "file", source: "workspace", path });
  };

  const verdict = item.verdict;
  const title = item.nodeTitle ? `${item.agent} · ${item.nodeTitle}` : item.agent;
  const stateCls = item.state === "failed" ? " bad" : item.state === "running" || item.state === "online" ? " run" : " ok";

  return (
    <div className="ex-view">
      <div className="ex-mhead">
        <h1>{title}</h1>
        {item.kind === "run" && item.role && <span className="ex-chip role">{roleText(item.role, t)}</span>}
        <span className={`ex-chip${stateCls}`}>{stateText(item.state, t)}</span>
        {item.nodeId && (
          <button type="button" className="ex-btn" onClick={() => openNodeDetail(item.nodeId)}>
            {t("explorer.agent.openNode")}
          </button>
        )}
      </div>
      <div className="ex-mbody">
        <div className="ex-grid">
          <div className="ex-card">
            <span className="ex-lbl">{t("explorer.agent.tokens")}</span>
            <span className="ex-big">
              {formatTokens(item.tokens.input)} / {formatTokens(item.tokens.output)}
            </span>
            {item.tokens.cacheRead > 0 && (
              <span className="ex-lbl">{t("explorer.agent.cache", { count: formatTokens(item.tokens.cacheRead) })}</span>
            )}
          </div>
          <div className="ex-card">
            <span className="ex-lbl">{t("explorer.agent.cost")}</span>
            <span className="ex-big">{costText(item, t)}</span>
            {item.model && <span className="ex-lbl">{item.model}</span>}
          </div>
          <div className="ex-card">
            <span className="ex-lbl">{t("explorer.agent.time")}</span>
            <span className="ex-big">{durationText(item.startedAt, item.finishedAt) || "—"}</span>
            {item.runnerName && <span className="ex-lbl">{item.runnerName}</span>}
          </div>
          {verdict && (
            <div className="ex-card">
              <span className="ex-lbl">{t("explorer.agent.verdict")}</span>
              <span className={`ex-big ${verdict.outcome === "pass" ? "is-ok" : "is-bad"}`}>{t(`verdict.${verdict.outcome}`)}</span>
              <span className="ex-chips">
                <span className="ex-chip">{t(`verdict.severity.${verdict.severity}`)}</span>
                {verdict.reasons.map((reason) => (
                  <span key={reason} className="ex-chip">
                    {t(`verdict.reason.${reason}`)}
                  </span>
                ))}
              </span>
            </div>
          )}
        </div>

        <section className="ex-block" aria-label={t("explorer.agent.filesTouched")}>
          <h2>{t("explorer.agent.filesTouched")}</h2>
          {note && (
            <p className="ex-note" role="status">
              {note}
            </p>
          )}
          {item.files.length === 0 ? (
            <p className="ex-empty">{t("explorer.agent.noFiles")}</p>
          ) : (
            <div className="ex-files">
              {item.files.map((file) => (
                <button key={file.path} type="button" className="ex-fileline" onClick={() => openFile(file.path)}>
                  <span className="ex-mark">{file.mark === "read" ? t("explorer.agent.read") : file.mark}</span>
                  <span>{file.path}</span>
                </button>
              ))}
            </div>
          )}
        </section>

        <section className="ex-block" aria-label={t("explorer.agent.events")}>
          <h2>{t("explorer.agent.events")}</h2>
          {timeline.length === 0 ? (
            <p className="ex-empty">{t("explorer.agent.noEvents")}</p>
          ) : (
            <div className="ex-tl">
              {timeline.map((entry) => (
                <div key={entry.key} className="ev">
                  <span className="t">{clockText(entry.at)}</span>
                  <span className={`dot is-${entry.dot}`} aria-hidden="true" />
                  <div className="d">
                    <div>{entry.text}</div>
                    {entry.sub && <div className="x">{entry.sub}</div>}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
