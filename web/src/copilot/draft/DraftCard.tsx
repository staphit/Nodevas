/**
 * A draft in the transcript: what the copilot proposes, editable until it is
 * created or discarded. The open, newest draft is the active one (its ghosts
 * are on the board); older, created and discarded drafts fold to a line,
 * except an open coding agent's plan, which stays live until it is created
 * or discarded.
 *
 * Edits go to the copilot store's `drafts` map, never to the transcript's
 * items, so editing does not move the transcript.
 */

import { useEffect, useMemo, useState } from "react";
import type { CopilotDraft, CopilotDraftIssue, CopilotStatus } from "../../api";
import { nodeTypeById, nodeTypesForLayout } from "../../domain/graph/nodeTypes";
import { formatList, useI18n } from "../../i18n";
import type { Runner } from "../../api";
import { useEdition } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { usePreference, useApp } from "../../store";
import { activeThemePack, useThemePacks } from "../../theme-pack/themePackStore";
import { browserModelLanguages } from "../browserModel";
import { currentDraft, isRefining, useCopilot, type DraftItem } from "../copilotStore";
import { modelLabel } from "../modelLabel";
import {
  checkLocally,
  createDraft,
  discardDraft,
  editDraft,
  isBlocking,
  liveServerIssues,
  planWithAgent,
  refreshCanWrite,
} from "./draftApi";
import { DraftList, issueText, KindGlyph, type NodeMark } from "./DraftList";
import { KeptList } from "./KeptList";
import { countLinks, diffDrafts, goalTagOf, maxNodesOf, mentionsCode, mergeMany, type DiffMark } from "./draftStore";
import { useDraftUi } from "./draftUi";

/** More nodes than this, and the coding agent's plan is the suggestion. */
const DEEPER_NODE_COUNT = 15;

/**
 * Whether a coding agent could plan it now: some runner is online with a
 * repo to read and an agent to run. Without one the button would only start
 * a breakdown that cannot run.
 */
export function canPlanWithAgent(runners: readonly Runner[]): boolean {
  return runners.some(
    (runner) =>
      runner.online &&
      (runner.info?.repos?.length ?? 0) > 0 &&
      (runner.info?.agents ?? []).some((agent) => agent.installed !== false),
  );
}

/** Whether a theme pack dresses this viewer's screens. */
export function useThemeActive(): boolean {
  const picked = usePreference("themePack");
  return useThemePacks((state) =>
    state.error === null && activeThemePack(state.packs, state.defaultId, picked) !== undefined,
  );
}

function marksFor(
  draft: CopilotDraft,
  serverIssues: CopilotDraftIssue[],
  edited: boolean,
  maxNodes: number,
): { byKey: Record<string, NodeMark[]>; general: NodeMark[] } {
  const byKey: Record<string, NodeMark[]> = {};
  const general: NodeMark[] = [];
  const seen = new Set<string>();
  const add = (key: string | undefined, mark: NodeMark) => {
    const id = `${key ?? ""}:${mark.code}:${mark.ref ?? ""}`;
    if (seen.has(id)) return;
    seen.add(id);
    if (key) (byKey[key] ??= []).push(mark);
    else general.push(mark);
  };
  for (const issue of liveServerIssues(serverIssues, draft, edited)) {
    add(issue.key, {
      code: issue.code,
      severity: issue.severity === "error" ? "error" : "warning",
      existingId: issue.existingId,
      title: issue.title,
    });
  }
  for (const issue of checkLocally(draft, undefined, maxNodes)) {
    // The server already named the same problem without the ref.
    if (issue.key && seen.has(`${issue.key}:${issue.code}:`)) continue;
    add(issue.key, { code: issue.code, severity: isBlocking(issue) ? "error" : "warning", ref: issue.ref });
  }
  return { byKey, general };
}

export function DraftCard({
  item,
  active,
  prompt = "",
  themed: themedProp,
}: {
  item: DraftItem;
  /** The open draft the board previews and a refine sends. */
  active: boolean;
  /** What the person asked for, to tell whether it names code. */
  prompt?: string;
  /** Overrides the theme-pack check (tests). */
  themed?: boolean;
}) {
  const { t, language } = useI18n();
  const data = item.data;
  const edited = useCopilot((state) => state.drafts[item.id]);
  const base = useCopilot((state) =>
    data?.refineOf ? currentDraft(state, data.refineOf) : null,
  );
  const running = useCopilot((state) => state.running);
  // A refine of this draft is running: what lands replaces it, so it waits.
  const refining = useCopilot((state) => isRefining(state, item.id));
  const storedEdited = data?.edited === true;
  const kept = useDraftUi((state) => state.kept[item.id]);
  const runners = useRunners((state) => state.runners);
  const runnersLoaded = useRunners((state) => state.loaded);
  const hub = useEdition() === "team";
  const graph = useApp((state) => state.graph);
  const project = useApp((state) => state.activeProject);
  const unreadable = useApp((state) => state.graphUnreadable !== null);
  const check = useDraftUi((state) => state.checks[item.id]);
  const busy = useDraftUi((state) => state.busy[item.id]);
  const canWrite = useDraftUi((state) =>
    state.canWrite?.project === project ? state.canWrite.value : null,
  );
  const themeActive = useThemeActive();
  const themed = themedProp ?? themeActive;
  const [expanded, setExpanded] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());

  const open = data?.state === "open";
  // A newer draft supersedes a copilot draft (it is refined into the next
  // one), never a coding agent's plan: that is the only copy of its run's
  // plan, and its goal stays waiting on it (the copilot refuses the
  // autopilot, a revision or another breakdown there) until it is created or
  // discarded. So an open plan draft keeps its buttons wherever it sits.
  const superseded = open && !active && !data?.origin;
  const live = open && !superseded;

  useEffect(() => {
    if (live && canWrite === null) void refreshCanWrite(project);
  }, [live, canWrite, project]);

  // The coding agent's button waits on the runner list.
  useEffect(() => {
    if (live && !data?.origin && !runnersLoaded) void useRunners.getState().refresh();
  }, [live, data?.origin, runnersLoaded]);

  const draft = edited ?? data?.draft ?? null;
  const maxNodes = data ? maxNodesOf(data) : undefined;
  const nodeTypes = useMemo(() => nodeTypesForLayout(graph), [graph]);
  const marks = useMemo(
    () =>
      draft && data
        ? marksFor(draft, data.issues ?? [], edited !== undefined || storedEdited, maxNodesOf(data))
        : { byKey: {}, general: [] },
    // graph: the client checks read the board's ids and types.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [draft, data, edited, storedEdited, graph],
  );
  const diff = useMemo<{ marks: Record<string, DiffMark>; removed: string[] }>(() => {
    if (!base || !data) return { marks: {}, removed: [] };
    const result = diffDrafts(base, data.draft);
    return { marks: result.marks, removed: result.removed.map((node) => node.title) };
  }, [base, data]);

  if (!data || !draft) {
    return <div className="copilot-notice">{item.text || t("draft.unreadable")}</div>;
  }

  const count = draft.nodes.length;
  const links = countLinks(draft);
  // A coding agent's plan names who planned it; the copilot's own, its model.
  const origin = data.origin;
  const model = origin
    ? [origin.agent, origin.model].filter(Boolean).join(" · ")
    : modelLabel({ provider: data.provider, model: data.model } as CopilotStatus, t);
  const goalTitle = origin
    ? origin.goalTitle || graph?.nodes?.find((node) => node.id === origin.goalId)?.title || origin.goalId
    : "";
  const planExtras = origin ? (data.extras ?? {}) : undefined;
  const goalTag = origin ? goalTagOf(origin.goalId) : undefined;
  const blocking =
    Object.values(marks.byKey).some((list) => list.some((mark) => mark.severity === "error")) ||
    marks.general.some((mark) => mark.severity === "error");
  const noWrite = unreadable || canWrite === false;
  const createDisabled =
    !live || refining || blocking || check?.status === "error" || noWrite || busy !== undefined;
  const agentReady = !origin && canPlanWithAgent(runners);
  const deeper =
    agentReady && (count > DEEPER_NODE_COUNT || Boolean(draft.needsCodeKnowledge) || mentionsCode(prompt));
  const smallModel =
    data.provider === "browser" &&
    language === "zh-TW" &&
    browserModelLanguages().every((code) => code.toLowerCase().startsWith("en"));
  const stateLabel = !open ? t(`draft.state.${data.state}`) : superseded ? t("draft.state.superseded") : "";

  const header = (
    <div className="draft-head">
      <span className="draft-heading">{t("draft.title", { n: count, e: links })}</span>
      {model && (
        <span className="draft-model" title={t("draft.byModel", { model })}>
          {model}
        </span>
      )}
      {stateLabel && <span className={`draft-state is-${open ? "superseded" : data.state}`}>{stateLabel}</span>}
      {origin && (
        <span className="draft-origin">
          {t("draft.plan.origin", { agent: origin.agent || t("draft.plan.agentUnknown"), goal: goalTitle })}
        </span>
      )}
    </div>
  );

  if (!live) {
    return (
      <section className="draft-card is-folded" data-draft-entry={item.id} data-state={data.state}>
        {header}
        {(draft.summary || item.text) && <p className="draft-summary">{draft.summary || item.text}</p>}
        <button type="button" className="draft-text-btn" onClick={() => setExpanded(!expanded)} aria-expanded={expanded}>
          {expanded ? t("draft.hide") : t("draft.show")}
        </button>
        {expanded && (
          <DraftList
            draft={draft}
            graph={graph}
            nodeTypes={nodeTypes}
            marks={{}}
            diff={{}}
            readOnly
            selected={new Set()}
            onToggleSelect={() => undefined}
            onEdit={() => undefined}
            extras={planExtras}
            goalTag={goalTag}
            maxNodes={maxNodes}
          />
        )}
        {data.state === "committed" && kept && kept.length > 0 && (
          <div className="draft-kept-block">
            <p className="draft-note">{t("draft.kept.card")}</p>
            <KeptList nodes={kept} />
          </div>
        )}
      </section>
    );
  }

  const onEdit = (update: (current: CopilotDraft) => CopilotDraft) => {
    editDraft(item.id, update);
    // Merged or removed keys are no longer there to be selected.
    setSelected(new Set());
  };
  const toggleSelect = (key: string) => {
    const next = new Set(selected);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setSelected(next);
  };
  const mergeKeys = draft.nodes.map((node) => node.key).filter((key) => selected.has(key));
  const showList = !themed || listOpen;

  return (
    <section className={`draft-card${themed ? " is-themed" : ""}`} data-draft-entry={item.id} data-state="open">
      {header}
      {draft.summary && <p className="draft-summary">{draft.summary}</p>}
      {origin && <p className="draft-note">{t("draft.plan.note")}</p>}
      {refining && (
        <p className="draft-note" role="status">
          {t("draft.refining")}
        </p>
      )}
      {smallModel && <p className="draft-note">{t("draft.smallModel")}</p>}
      {data.capped && <p className="draft-note">{t("draft.capped", { max: maxNodesOf(data) })}</p>}
      {(draft.questions?.length ?? 0) > 0 && (
        <div className="draft-questions">
          <span className="draft-questions-label">{t("draft.questions")}</span>
          {draft.questions!.map((question) => (
            <button
              key={question}
              type="button"
              className="draft-question"
              onClick={() => useCopilot.getState().fillComposer(question)}
            >
              {question}
            </button>
          ))}
        </div>
      )}
      {themed && (
        <div className="draft-glyphs">
          <span className="draft-glyph-row" aria-hidden>
            {draft.nodes.map((node) => (
              <KindGlyph key={node.key} type={nodeTypeById(graph, node.kind)} large />
            ))}
          </span>
          <span className="draft-glyph-count">{t("draft.glyphCount", { n: count })}</span>
          <button
            type="button"
            className="draft-text-btn"
            aria-expanded={listOpen}
            onClick={() => setListOpen(!listOpen)}
          >
            {listOpen ? t("draft.collapseList") : t("draft.expandList")}
          </button>
        </div>
      )}
      {showList && (
        <DraftList
          draft={draft}
          graph={graph}
          nodeTypes={nodeTypes}
          marks={marks.byKey}
          diff={diff.marks}
          readOnly={refining}
          selected={selected}
          onToggleSelect={toggleSelect}
          onEdit={onEdit}
          extras={planExtras}
          goalTag={goalTag}
          maxNodes={maxNodes}
        />
      )}
      {diff.removed.length > 0 && (
        <p className="draft-removed">
          <span className="draft-diff draft-diff-removed" aria-hidden>
            −
          </span>
          {t("draft.diff.removed", { titles: formatList(diff.removed, language) })}
        </p>
      )}
      {marks.general.map((mark) => (
        <p key={mark.code} className={`draft-issue is-${mark.severity}`} role="alert" data-issue={mark.code}>
          {issueText(mark, t, maxNodes)}
        </p>
      ))}
      {check?.status === "error" && (
        <p className="draft-issue is-error" role="alert">
          {t("draft.checkFailed", { message: check.message })}
        </p>
      )}
      {check?.status === "checking" && (
        <p className="draft-checking" role="status">
          {t("draft.status.checking")}
        </p>
      )}
      {mergeKeys.length >= 2 && (
        <div className="draft-merge-bar">
          <button
            type="button"
            className="draft-secondary"
            onClick={() => onEdit((current) => mergeMany(current, mergeKeys))}
          >
            {t("draft.mergeCount", { n: mergeKeys.length })}
          </button>
        </div>
      )}
      {noWrite && <p className="draft-note is-warn">{t("draft.noWrite")}</p>}
      <div className="draft-actions">
        <button
          type="button"
          className="draft-primary"
          disabled={createDisabled}
          onClick={() => void createDraft(item.id)}
        >
          {busy === "creating" ? t("draft.creating") : t("draft.create", { n: count })}
        </button>
        <button
          type="button"
          className="draft-secondary"
          disabled={busy !== undefined || refining}
          onClick={() => void discardDraft(item.id)}
        >
          {t("draft.discard")}
        </button>
      </div>
      {/* A coding agent's plan already is the deeper path; it needs a runner that can take it. */}
      {agentReady && (
        <button
          type="button"
          className={`draft-deeper${deeper ? " is-suggested" : ""}`}
          onClick={() => void planWithAgent(item.id)}
          disabled={running}
        >
          {t("draft.deeper")}
        </button>
      )}
      {deeper && <p className="draft-deeper-hint">{t("draft.deeperHint")}</p>}
      {agentReady && hub && <p className="draft-deeper-hint">{t("draft.deeperHub")}</p>}
    </section>
  );
}
