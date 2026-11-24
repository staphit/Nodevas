import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  BusyError,
  GlobalRunError,
  runFinal,
  type BusyOption,
  type BusyResponse,
  type OnBusy,
  type Run,
  type RunAsk,
  type Runner,
} from "../../api";
import { useAgents } from "../../agents/agentStore";
import { useViewer } from "../../components/SignIn";
import { useI18n } from "../../i18n";
import { useEdition } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { usePreference } from "../../store";
import { followUpMode } from "../RunModePicker";
import { selectRunById, selectRunChain, useRuns } from "../runStore";
import { mapFollowUpError } from "./followUpErrors";
import { canEndRun, deriveComposerState, type ComposerState } from "./RunComposer";
import { buildRunLines, type RunDialogLine } from "./runLines";

/** Cap on how many turns of a chain are watched simultaneously to bound memory. */
const MAX_WATCHED_TURNS = 10;

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
  /** A "new work" start the hub answered BUSY: the choice to show. */
  busy: BusyResponse | null;
  /** Starts the busy-refused new work again with the person's choice; true once started. */
  chooseBusy: (choice: BusyOption | string) => Promise<boolean>;
  cancelBusy: () => void;
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
  const [busy, setBusy] = useState<{ response: BusyResponse; text: string } | null>(null);
  const modePref = usePreference("runMode");
  // A reply queued behind a still-running turn: the view stays on that turn
  // (its Stop stays in reach) and moves to the reply once the turn ends.
  const queuedReplyRef = useRef<string | null>(null);

  useEffect(() => {
    setActiveId(runId);
    setError(null);
    setBusy(null);
    queuedReplyRef.current = null;
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

  // The turn a reply was queued behind has ended: show the reply's turn.
  const activeFinal = Boolean(run && runFinal(run.status));
  useEffect(() => {
    const queued = queuedReplyRef.current;
    if (!queued || !activeFinal) return;
    queuedReplyRef.current = null;
    setActiveId(queued);
  }, [activeFinal]);

  // §8.3: a follow-up or new work uses the first start's rule, read for the
  // prior run's runner and agent (and the saved preference).
  const modeFor = useCallback(
    (prior: Run, queued: boolean) =>
      followUpMode(
        {
          runner,
          agent: runner?.info?.agents?.find((item) => item.kind === prior.agent),
          viewer,
          edition,
        },
        modePref,
        queued,
      ),
    [runner, viewer, edition, modePref],
  );

  const startNewWork = useCallback(
    async (prior: Run, text: string, onBusy?: OnBusy, holderIds?: string[]): Promise<boolean> => {
      const mode = modeFor(prior, false);
      try {
        const created = await api.createGlobalRun({
          runnerId: prior.runnerId,
          repoId: prior.repoId,
          agent: prior.agent,
          model: prior.model || undefined,
          ...(mode ? { mode } : {}),
          base: prior.base || undefined,
          task: text,
          openPR: !prior.noPR,
          simple,
          ...(onBusy ? { onBusy } : {}),
          ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
        });
        setBusy(null);
        putRun(created);
        setActiveId(created.id);
        onRunCreated?.(created);
        return true;
      } catch (err) {
        if (err instanceof BusyError) {
          // The folder is busy: the person chooses (queue, take over, both).
          setBusy({ response: err.busy, text });
          return false;
        }
        throw err;
      }
    },
    [modeFor, simple, putRun, onRunCreated],
  );

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
          return await startNewWork(targetRun, text);
        }

        // follow_up, follow_up_pr, reset_at, background_queue. A reply that
        // waits (behind a running turn, or for a usage limit to reset) is a
        // queued reply: always background (§8.3).
        const queued = composerState.kind === "background_queue" || composerState.kind === "reset_at";
        const mode = modeFor(targetRun, queued);
        const created = await api.followUpGlobalRun(targetRun.id, {
          message: text,
          ...(mode ? { mode } : {}),
        });
        putRun(created);
        if (runFinal(targetRun.status)) {
          setActiveId(created.id);
          void loadThread(created.id).catch(() => undefined);
        } else {
          // Stay on the running turn (its Stop stays here); the queued reply
          // shows as a pending line and takes over when the turn ends.
          queuedReplyRef.current = created.id;
          void loadThread(targetRun.id).catch(() => undefined);
        }
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
    [targetRun, composerState, simple, t, putRun, loadThread, onRunCreated, modeFor, startNewWork],
  );

  const chooseBusy = useCallback(
    async (choice: BusyOption | string): Promise<boolean> => {
      if (!busy || !targetRun) return false;
      const holderIds = (busy.response.holders ?? []).map((holder) => holder.runId);
      setSubmitting(true);
      setError(null);
      try {
        return await startNewWork(targetRun, busy.text, choice as OnBusy, holderIds);
      } catch (err) {
        setBusy(null);
        setError(mapFollowUpError(err, t, simple));
        return false;
      } finally {
        setSubmitting(false);
      }
    },
    [busy, targetRun, startNewWork, t, simple],
  );

  const cancelBusy = useCallback(() => setBusy(null), []);

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
      if (queuedReplyRef.current === targetRunId) queuedReplyRef.current = null;
      await cancelRun(targetRunId);
      // The view goes back to the latest turn that is still there, never the
      // withdrawn one.
      const remaining = chain.filter((item) => item.id !== targetRunId);
      const withdrawn = chain.find((item) => item.id === targetRunId);
      const next = remaining.length > 0 ? remaining[remaining.length - 1].id : withdrawn?.continueOf || activeId;
      if (next && next !== activeId) setActiveId(next);
      if (isGlobal && next) {
        void loadThread(next).catch(() => undefined);
      }
    },
    [cancelRun, chain, isGlobal, activeId, loadThread],
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
    busy: busy?.response ?? null,
    chooseBusy,
    cancelBusy,
    submitText,
    endRun,
    stopRun,
    withdrawRun,
    focusLatestTurn,
  };
}
