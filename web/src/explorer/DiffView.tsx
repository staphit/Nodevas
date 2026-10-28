import { useMemo, useState } from "react";
import { api } from "../api";
import { explorerApi, type AgentItem } from "../api/explorer";
import type { Run } from "../api/runs";
import { useI18n } from "../i18n";
import { DiffTable, readDiffLayout } from "../runs/DiffViewer";
import { parsePatch } from "../runs/patch";
import { clockText } from "./explorerFormat";
import { useExplorer, type ExplorerView } from "./explorerStore";
import { useLoad } from "./useExplorerData";

export interface DiffViewProps {
  view: Extract<ExplorerView, { kind: "diff" }>;
}

/** What a diff view shows, whichever side served it. */
interface Shown {
  title: string;
  chip?: { text: string; cls: string };
  sub?: string;
  patch: string;
  truncated: boolean;
  /** The run behind it, when it is known. */
  runId?: string;
  /** A commit's author and time, to find the agent that made it. */
  author?: string;
  at?: string;
  /** The stat the hub counted, when it did (a run's). */
  stat?: { files: number; add: number; del: number };
}

function ciChip(run: Run | null): Shown["chip"] {
  switch (run?.ciStatus) {
    case "success":
      return { text: "explorer.git.ci.pass", cls: "ok" };
    case "failure":
    case "error":
      return { text: "explorer.git.ci.fail", cls: "bad" };
    case undefined:
    case "":
      return undefined;
    default:
      return { text: "explorer.git.ci.pending", cls: "" };
  }
}

async function load(view: DiffViewProps["view"]): Promise<Shown> {
  if (view.source === "run") {
    const [changesRes, run] = await Promise.all([
      api.getRunChanges(view.runId),
      api.getRun(view.runId).catch(() => null),
    ]);
    // The patch rides on the changes; an older hub put it beside them.
    const loose = changesRes as unknown as { patch?: string; patchTruncated?: boolean };
    const changes = changesRes.changes;
    return {
      title: run?.branch || run?.nodeId || view.runId,
      chip: ciChip(run),
      patch: changes?.patch ?? loose.patch ?? "",
      truncated: Boolean(changes?.patchTruncated ?? loose.patchTruncated),
      runId: view.runId,
      stat: changes
        ? { files: changes.files?.length ?? 0, add: changes.additions, del: changes.deletions }
        : undefined,
    };
  }
  if (view.source === "workspace") {
    const commit = await explorerApi.workspaceCommit(view.sha);
    return {
      title: commit.subject,
      sub: `${commit.sha.slice(0, 8)} · ${commit.author}`,
      patch: commit.patch,
      truncated: commit.truncated,
      author: commit.author,
      at: commit.at,
    };
  }
  const diff = await explorerApi.repoDiff(view.runnerId, view.repoId, view.ref);
  return {
    title: view.ref ? view.ref.slice(0, 12) : "",
    patch: diff.patch,
    truncated: diff.truncated,
  };
}

/** The agent run that most likely made a commit: one that touched its files
 * and finished closest to it. A person's commit names no agent. */
function agentFor(items: AgentItem[], shown: Shown, paths: string[]): AgentItem | undefined {
  const author = (shown.author ?? "").toLowerCase();
  const byAgent = author.startsWith("run:") || items.some((item) => item.agent && author === item.agent.toLowerCase());
  if (!byAgent) return undefined;
  const at = Date.parse(shown.at ?? "");
  const touched = new Set(paths);
  let best: AgentItem | undefined;
  let bestGap = Infinity;
  for (const item of items) {
    if (item.kind !== "run") continue;
    if (touched.size > 0 && !item.files.some((file) => touched.has(file.path))) continue;
    const end = Date.parse(item.finishedAt || item.startedAt);
    const gap = Number.isFinite(at) && Number.isFinite(end) ? Math.abs(at - end) : Number.MAX_SAFE_INTEGER;
    if (gap < bestGap) {
      best = item;
      bestGap = gap;
    }
  }
  return best;
}

/** A run's, a workspace commit's or a repo's patch. */
export default function DiffView({ view }: DiffViewProps) {
  const { t } = useI18n();
  const open = useExplorer((s) => s.open);
  const [note, setNote] = useState<string | null>(null);
  const loaded = useLoad(() => load(view), JSON.stringify(view));
  const shown = loaded.data;
  const files = useMemo(() => (shown ? parsePatch(shown.patch) : []), [shown]);
  const layout = useMemo(() => readDiffLayout(), []);

  const stat = shown?.stat ?? {
    files: files.length,
    add: files.reduce((sum, file) => sum + file.additions, 0),
    del: files.reduce((sum, file) => sum + file.deletions, 0),
  };

  const whichAgent = async () => {
    setNote(null);
    if (!shown) return;
    if (shown.runId) {
      open({ kind: "agent", id: `run:${shown.runId}` });
      return;
    }
    try {
      const agents = await explorerApi.agents();
      const found = agentFor([...agents.live, ...agents.recent], shown, files.map((file) => file.path));
      if (found) open({ kind: "agent", id: found.id });
      else setNote(t("explorer.git.byPerson"));
    } catch (error) {
      setNote(error instanceof Error ? error.message : String(error));
    }
  };

  const title =
    view.source === "repo" && !view.ref
      ? t("explorer.git.uncommitted", { repo: view.repoName || view.repoId })
      : shown?.title ?? "";

  return (
    <div className="ex-view">
      <div className="ex-mhead">
        <h1>{title}</h1>
        {shown?.chip && <span className={`ex-chip ${shown.chip.cls}`}>{t(shown.chip.text)}</span>}
        {shown && <span className="ex-chip">{t(stat.files === 1 ? "explorer.git.statOne" : "explorer.git.statMany", stat)}</span>}
        {shown?.at && <span className="ex-crumb">{clockText(shown.at)}</span>}
        {shown && view.source !== "repo" && (
          <button type="button" className="ex-btn" onClick={() => void whichAgent()}>
            {t("explorer.git.whichAgent")}
          </button>
        )}
      </div>
      <div className="ex-mbody">
        {note && (
          <p className="ex-note" role="status">
            {note}
          </p>
        )}
        {shown?.sub && <p className="ex-crumb">{shown.sub}</p>}
        {loaded.error != null && (
          <p className="ex-note">{loaded.error instanceof Error ? loaded.error.message : String(loaded.error)}</p>
        )}
        {shown?.truncated && <p className="ex-note">{t("diff.truncatedNoPr")}</p>}
        {files.map((file) => (
          <div key={file.path} className="ex-diff-file">
            <div className="ex-crumb">{file.path}</div>
            {file.binary ? <p className="ex-note">{t("diff.binary")}</p> : <DiffTable file={file} layout={layout} />}
          </div>
        ))}
      </div>
    </div>
  );
}
