import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  GlobalRunError,
  runFinal,
  type Run,
  type RunAsk,
  type Runner,
} from "../../api";
import { useAgents } from "../../agents/agentStore";
import { useViewer } from "../../components/SignIn";
import { useI18n } from "../../i18n";
import { useEdition } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { selectRunById, selectRunChain, useRuns } from "../runStore";
import { canEndRun, deriveComposerState, type ComposerState } from "./RunComposer";
import { buildRunLines, type RunDialogLine } from "./runLines";

/** Cap on how many turns of a chain are watched simultaneously to bound memory. */
const MAX_WATCHED_TURNS = 10;

const FOLLOW_UP_ERROR_KEYS: Record<string, string> = {
  CONTINUED: "runDialog.err.continued",
  IN_USE: "runDialog.err.inUse",
  WORK_GONE: "runDialog.err.gone",
  RUNNER_OUTDATED: "runDialog.err.outdated",
  RUN_LIVE: "runDialog.err.live",
  PR_ENDED: "runDialog.err.prEnded",
  BRANCH_MOVED: "runDialog.err.branchMoved",
  PR_MOVED: "runDialog.err.prMoved",
  MERGE_PENDING: "runDialog.err.mergePending",
  TURN_RUNNING: "runDialog.err.turnRunning",
};

function mapFollowUpError(
  error: unknown,
  t: (key: string, values?: Record<string, string | number>) => string,
  simple: boolean,
): string {
  if (error instanceof GlobalRunError) {
    if (simple && error.code === "WORK_GONE") return t("runDialog.simple.err.gone");
    const key = FOLLOW_UP_ERROR_KEYS[error.code];
    if (key) return t(key);
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

export interface UseRunDialogOptions {
  runId: string;
  simple?: boolean;
  subscribed?: boolean;
  terminalReadonly?: boolean;
  screenAsks?: boolean;
  onRunCreated?: (run: Run) => void;
}

export interface UseRunDialogResult {
  run: Run | undefined;
  latestRun: Run | undefined;
  runner: Runner | undefined;
  chain: Run[];
  lines: RunDialogLine[];
  composerState: ComposerState | null;
  canEnd: boolean;
  submitting: boolean;
  ending: boolean;
  stopping: boolean;
  error: string | null;
  submitText: (text: string) => Promise<boolean>;
  endRun: () => Promise<void>;
  stopRun: () => Promise<void>;
  withdrawRun: (targetRunId: string) => Promise<void>;
  focusLatestTurn: () => void;
}

/**
 * Drives RunDialog (§3.1): loads the global turn chain via `GET …/thread`,
 * ref-counted `watchRun` for the turns in view, derives composer state, and
 * wires follow-up / new-work / ask-answer / End / Stop / Withdraw actions.
 */
export function useRunDialog({
  runId,
  simple = false,
  subscribed = true,
  terminalReadonly = false,
  screenAsks = false,
  onRunCreated,
}: UseRunDialogOptions): UseRunDialogResult {
  const { t } = useI18n();
  const edition = useEdition();
  const viewer = useViewer();
  const runners = useRunners((s) => s.runners);

  const [activeId, setActiveId] = useState(runId);
  const [submitting, setSubmitting] = useState(false);
  const [ending, setEnding] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setActiveId(runId);
    setError(null);
  }, [runId]);

  const run = useRuns((s) => selectRunById(s, activeId));
  const globalById = useRuns((s) => s.globalById);
  const runsByNode = useRuns((s) => s.runsByNode);
  const activeRuns = useRuns((s) => s.activeRuns);
  const latestByNode = useRuns((s) => s.latestByNode);
  const threadsByRoot = useRuns((s) => s.threadsByRoot);
  const chain = useMemo(
    () =>
      selectRunChain(
        { globalById, runsByNode, activeRuns, latestByNode, threadsByRoot },
        activeId,
      ),
    [globalById, runsByNode, activeRuns, latestByNode, threadsByRoot, activeId],
  );
  const eventsByRun = useRuns((s) => s.eventsByRun);
  const asksByRun = useRuns((s) => s.asksByRun);
  const stopping = useRuns((s) => Boolean(s.cancelling[activeId]));
  const watchRun = useRuns((s) => s.watchRun);
  const loadThread = useRuns((s) => s.loadThread);
  const putRun = useRuns((s) => s.putRun);
  const cancelRun = useRuns((s) => s.cancelRun);

  const isGlobal = Boolean(run?.global);

  // Load the full follow-up chain for global runs.
  useEffect(() => {
    if (!subscribed || !activeId || !isGlobal) return;
    let cancelled = false;
    void loadThread(activeId).catch(() => {
      if (!cancelled) {
        /* fallback to single run already in store */
      }
    });
    return () => {
      cancelled = true;
    };
  }, [subscribed, activeId, isGlobal, loadThread]);

  // Ref-counted watch for turns in the displayed chain (capped for memory).
  // Diff against active watches so growing a chain never unwatches/evicts
  // turns that are already loaded.
  const watchedIds = useMemo(() => {
    if (!subscribed) return [] as string[];
    const list = chain.length > 0 ? chain.map((item) => item.id) : activeId ? [activeId] : [];
    return list.length > MAX_WATCHED_TURNS ? list.slice(list.length - MAX_WATCHED_TURNS) : list;
  }, [subscribed, chain, activeId]);

  const watchedKey = watchedIds.join(",");
  const activeWatchesRef = useRef<Map<string, () => void>>(new Map());

  useEffect(() => {
    const nextIds = new Set(watchedKey ? watchedKey.split(",") : []);
    const active = activeWatchesRef.current;

    for (const [id, unwatch] of active) {
      if (!nextIds.has(id)) {
        active.delete(id);
        unwatch();
      }
    }
    for (const id of nextIds) {
      if (!active.has(id)) {
        active.set(id, watchRun(id));
      }
    }
  }, [watchedKey, watchRun]);

  useEffect(
    () => () => {
      const active = activeWatchesRef.current;
      for (const unwatch of active.values()) unwatch();
      active.clear();
    },
    [],
  );

  const latestRun = chain.length > 0 ? chain[chain.length - 1] : run;
  const targetRun = run ?? latestRun;

  const runner = useMemo(
    () => (targetRun ? runners.find((r) => r.id === targetRun.runnerId) : undefined),
    [runners, targetRun],
  );

  const lines = useMemo<RunDialogLine[]>(() => {
    const turns = chain.length > 0 ? chain : targetRun ? [targetRun] : [];
    if (turns.length === 0) return [];
    return buildRunLines(
      turns.map((turn) => ({
        run: turn,
        events: eventsByRun[turn.id] ?? [],
        asks: asksByRun[turn.id] ?? [],
      })),
    );
  }, [chain, targetRun, eventsByRun, asksByRun]);

  const pendingAsks = useMemo<RunAsk[]>(() => {
    if (!targetRun) return [];
    return (asksByRun[targetRun.id] ?? []).filter((ask) => ask.status === "pending");
  }, [targetRun, asksByRun]);

  const composerState = useMemo(() => {
    if (!targetRun) return null;
    return deriveComposerState({
      run: targetRun,
      viewer,
      runner,
      edition,
      simple,
      terminalReadonly,
      screenAsks,
      pendingAsks,
    });
  }, [targetRun, viewer, runner, edition, simple, terminalReadonly, screenAsks, pendingAsks]);

  const canEnd = useMemo(
    () => (targetRun ? canEndRun(targetRun, viewer, runner, edition) : false),
    [targetRun, viewer, runner, edition],
  );

  const focusLatestTurn = useCallback(() => {
    if (latestRun && latestRun.id !== activeId) {
      setActiveId(latestRun.id);
    }
  }, [latestRun, activeId]);

  const submitText = useCallback(
    async (text: string): Promise<boolean> => {
      if (!targetRun || !composerState) return false;
      setSubmitting(true);
      setError(null);
      try {
        if (composerState.kind === "ask_question" && composerState.askId) {
          const ok = await useAgents.getState().answerAsk(composerState.askId, {
            decision: "answer",
            answer: text,
          });
          if (!ok) {
            setError(useAgents.getState().error || t("game.dialog.answerFailed"));
            return false;
          }
          return true;
        }

        if (composerState.kind === "new_work") {
          const created = await api.createGlobalRun({
            runnerId: targetRun.runnerId,
            repoId: targetRun.repoId,
            agent: targetRun.agent,
            model: targetRun.model || undefined,
            base: targetRun.base || undefined,
            task: text,
            openPR: !targetRun.noPR,
            simple,
          });
          putRun(created);
          setActiveId(created.id);
          onRunCreated?.(created);
          return true;
        }

        // follow_up, follow_up_pr, reset_at, background_queue
        const created = await api.followUpGlobalRun(targetRun.id, {
          message: text,
        });
        putRun(created);
        setActiveId(created.id);
        void loadThread(created.id).catch(() => undefined);
        onRunCreated?.(created);
        return true;
      } catch (err) {
        if (err instanceof GlobalRunError && err.code === "CONTINUED" && err.latestId) {
          setActiveId(err.latestId);
          void loadThread(err.latestId).catch(() => undefined);
        }
        setError(mapFollowUpError(err, t, simple));
        return false;
      } finally {
        setSubmitting(false);
      }
    },
    [targetRun, composerState, simple, t, putRun, loadThread, onRunCreated],
  );

  const endRun = useCallback(async () => {
    if (!targetRun || ending) return;
    setEnding(true);
    setError(null);
    try {
      const updated = await api.endRun(targetRun.id);
      if (updated?.id) putRun(updated);
    } catch (err) {
      setError(mapFollowUpError(err, t, simple));
    } finally {
      setEnding(false);
    }
  }, [targetRun, ending, putRun, t, simple]);

  const stopRun = useCallback(async () => {
    if (!targetRun || runFinal(targetRun.status)) return;
    setError(null);
    await cancelRun(targetRun.id);
  }, [targetRun, cancelRun]);

  const withdrawRun = useCallback(
    async (targetRunId: string) => {
      setError(null);
      await cancelRun(targetRunId);
      if (isGlobal && activeId) {
        void loadThread(activeId).catch(() => undefined);
      }
    },
    [cancelRun, isGlobal, activeId, loadThread],
  );

  return {
    run: targetRun,
    latestRun,
    runner,
    chain,
    lines,
    composerState,
    canEnd,
    submitting,
    ending,
    stopping,
    error,
    submitText,
    endRun,
    stopRun,
    withdrawRun,
    focusLatestTurn,
  };
}
