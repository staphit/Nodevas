// "Take a look" and "Apply to my folder": a finished run's result in plain
// words, for the single-user edition (resultState.ts). Station's lists
// show it to every single user, the workspace panel only in Simple mode.
// Apply is the owner's local merge with its gates as they are: the panel's
// workspace view is read when Apply is pressed (never when a row appears, so
// a long list costs no runner calls), a refusal is said in a plain sentence
// that always says nothing was changed, and the runner's own words wait
// under "Show details".

import { useEffect, useRef, useState } from "react";
import { api, BusyError, type BusyResponse, type OnBusy, type Run } from "../api";
import { useViewer } from "../components/SignIn";
import { useI18n } from "../i18n";
import { useEdition, useLocalRunner } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useSimpleMode } from "../simple/simpleMode";
import { usePreference } from "../store";
import { confirmApply } from "./confirmWorkspace";
import { BusyChoice } from "./coord/BusyChoice";
import { useCoordStore } from "./coord/coordStore";
import { mapFollowUpError } from "./dialog/followUpErrors";
import { applyRefusalKey, lookAt, resultState, type LookOutcome } from "./resultState";
import { followUpMode } from "./RunModePicker";
import { selectRunById, useRuns } from "./runStore";
import { useWorkspace, workspaceCodeText } from "./workspaceStore";

type Translate = ReturnType<typeof useI18n>["t"];

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * The whole task a run's work started from: its chain root's first "You"
 * line (seq 0, hub-written), from the run store when it is loaded, else read
 * once; the title (its first line only) when neither is there.
 */
export async function originalTask(run: Run): Promise<string> {
  const root = run.workRoot || run.id;
  const fromEvents = (events: readonly { type: string; text?: string }[] | undefined) =>
    events?.find((event) => event.type === "user" && event.text?.trim())?.text?.trim() ?? "";
  const held = fromEvents(useRuns.getState().eventsByRun[root]);
  if (held) return held;
  try {
    const page = await api.getRunEvents(root, 0, 1);
    const read = fromEvents(page.events ?? []);
    if (read) return read;
  } catch {
    // Fall back to what the list already knows.
  }
  return run.title || run.prompt || "";
}

/** What "Take a look" said it did, in the reader's words; "" for nothing. */
function lookText(t: Translate, look: LookOutcome | null): string {
  switch (look?.kind) {
    case "blocked":
      return t("result.look.blocked");
    case "folder":
      return t("result.look.folder");
    case "off":
      return t("result.look.off");
    case "gone":
      return t("result.look.gone");
    case "failed":
      return t("result.look.failed", { reason: look.reason });
    default:
      return "";
  }
}

/** Why the last apply did not happen: a code and the technical words. */
interface Refusal {
  code: string;
  detail: string;
}

export function ResultActions({
  run: given,
  compact = false,
  onStartNewWork,
}: {
  run: Run;
  compact?: boolean;
  /** Opens the caller's own composer prefilled with the whole task (the
   * global list's row); without it the task opens in a box here. */
  onStartNewWork?: (task: string) => void;
}) {
  const { t } = useI18n();
  const local = useLocalRunner();
  const simple = useSimpleMode();
  const viewer = useViewer();
  const edition = useEdition();
  const modePref = usePreference("runMode");
  // The run store's copy is the fresher one: an apply lands there first.
  const stored = useRuns((s) => selectRunById(s, given.id));
  const run = stored ?? given;
  const contest = useCoordStore((s) => (run.contestId ? s.contests[run.contestId]?.contest : undefined));
  const state = resultState(run, local?.id, { contest });
  // Only an apply is this component's: a discard pressed beside it (the
  // global list) is neither "Applying…" nor an apply refused.
  const applying = useWorkspace((s) => s.acting[run.id] === "merge");
  const ownRefusal = useWorkspace((s) => s.actionKinds[run.id] === "merge");
  const storeCode = useWorkspace((s) => (ownRefusal ? s.actionCodes[run.id] : undefined));
  const storeText = useWorkspace((s) => (ownRefusal ? s.actionErrors[run.id] : undefined));
  const [gate, setGate] = useState("");
  const [checking, setChecking] = useState(false);
  const [looking, setLooking] = useState(false);
  const [look, setLook] = useState<LookOutcome | null>(null);
  const [hiddenAfter, setHiddenAfter] = useState(false);
  const [details, setDetails] = useState(false);
  const [folderError, setFolderError] = useState("");
  const [startingNew, setStartingNew] = useState(false);
  const [newWorkError, setNewWorkError] = useState("");
  const [newWorkText, setNewWorkText] = useState<string | null>(null);
  const [newWorkBusy, setNewWorkBusy] = useState<BusyResponse | null>(null);
  const runner = useRunners((s) => s.runners.find((item) => item.id === run.runnerId));
  const live = useRef(true);
  // Set again on every mount: StrictMode (dev) unmounts and mounts once more,
  // and a ref left false would stop every answer from showing.
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  if (state === "none") return null;

  const className = `result-actions${compact ? " is-compact" : ""}`;

  // Earlier turns of a follow-up chain never act directly; the latest turn does.
  if (state === "continued") {
    return (
      <div className={className}>
        <p className="result-actions-note" role="status">
          {t(simple ? "runDialog.simple.continuedBelow" : "runDialog.continuedBelow")}
        </p>
      </div>
    );
  }

  // Merged or closed PR: offer starting new work with the same task.
  if (state === "pr_ended") {
    // Never starts on its own: the whole task opens in a composer first, so
    // the person sees (and can change) what the new work will be told.
    const onOpenNewWork = async () => {
      if (startingNew) return;
      setNewWorkError("");
      setStartingNew(true);
      const task = await originalTask(run);
      if (!live.current) return;
      setStartingNew(false);
      if (onStartNewWork) {
        onStartNewWork(task);
        return;
      }
      setNewWorkText(task);
    };
    // §8.3: the first start's rule, read for this run's runner and agent.
    const mode = followUpMode(
      {
        runner,
        agent: runner?.info?.agents?.find((item) => item.kind === run.agent),
        viewer,
        edition,
      },
      modePref,
    );
    const onStart = async (onBusy?: OnBusy, holderIds?: string[]) => {
      const task = (newWorkText ?? "").trim();
      if (!task || startingNew) return;
      setStartingNew(true);
      setNewWorkError("");
      try {
        const created = await api.createGlobalRun({
          runnerId: run.runnerId,
          repoId: run.repoId,
          agent: run.agent,
          model: run.model || undefined,
          ...(mode ? { mode } : {}),
          base: run.base || undefined,
          task,
          openPR: !run.noPR,
          simple,
          ...(onBusy ? { onBusy } : {}),
          ...(holderIds && holderIds.length > 0 ? { holderIds } : {}),
        });
        if (!live.current) return;
        setNewWorkBusy(null);
        setNewWorkText(null);
        if (created?.id) useRuns.getState().putRun(created);
      } catch (error) {
        if (!live.current) return;
        if (error instanceof BusyError) {
          setNewWorkBusy(error.busy);
          return;
        }
        setNewWorkBusy(null);
        setNewWorkError(mapFollowUpError(error, t, simple));
      } finally {
        if (live.current) setStartingNew(false);
      }
    };
    return (
      <div className={className}>
        <p className="result-actions-note">{t(simple ? "runDialog.simple.err.prEnded" : "runDialog.err.prEnded")}</p>
        {newWorkText === null ? (
          <div className="result-actions-buttons">
            <button
              type="button"
              className="result-btn is-primary"
              disabled={startingNew}
              onClick={() => void onOpenNewWork()}
            >
              {t("runDialog.newWork")}
            </button>
          </div>
        ) : (
          <div className="result-actions-new-work">
            <textarea
              className="run-dlg-textarea"
              rows={3}
              value={newWorkText}
              aria-label={t("result.newWork.label")}
              disabled={startingNew}
              onChange={(event) => setNewWorkText(event.target.value)}
            />
            {newWorkBusy && (
              <BusyChoice
                busy={newWorkBusy}
                simple={simple}
                maxConcurrent={runner?.info?.maxConcurrent}
                onChoose={(choice) => {
                  const holderIds = (newWorkBusy.holders ?? []).map((holder) => holder.runId);
                  void onStart(choice as OnBusy, holderIds);
                }}
                onCancel={() => setNewWorkBusy(null)}
              />
            )}
            <div className="result-actions-buttons">
              <button
                type="button"
                className="result-btn"
                disabled={startingNew}
                onClick={() => {
                  setNewWorkText(null);
                  setNewWorkBusy(null);
                  setNewWorkError("");
                }}
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                className="result-btn is-primary"
                disabled={startingNew || !newWorkText.trim()}
                onClick={() => void onStart()}
              >
                {t("result.newWork.start")}
              </button>
            </div>
          </div>
        )}
        {newWorkError && (
          <p className="result-actions-note is-error" role="alert">
            {newWorkError}
          </p>
        )}
      </div>
    );
  }

  const values = { base: run.base };
  const action = run.workspaceAction;
  const failed =
    action?.action === "merge_local" && (action.status === "failed" || action.status === "expired") ? action : null;
  let refusal: Refusal | null = null;
  if (gate) {
    refusal = { code: gate, detail: workspaceCodeText(gate, values) };
  } else if (storeCode || storeText) {
    refusal = { code: storeCode ?? "", detail: storeText ?? "" };
  } else if (failed) {
    const code = failed.status === "expired" ? "expired" : (failed.code ?? "");
    refusal = { code, detail: [workspaceCodeText(code, values), failed.reason].filter(Boolean).join(" · ") };
  }
  const busy = state === "applying" || applying;

  const onLook = async () => {
    setLooking(true);
    setLook(null);
    const outcome = await lookAt(run);
    if (!live.current) return;
    setLooking(false);
    setLook(outcome);
  };

  const onApply = async () => {
    setGate("");
    setDetails(false);
    setChecking(true);
    const workspace = useWorkspace.getState();
    await workspace.loadView(run.id);
    if (!live.current) return;
    setChecking(false);
    const view = useWorkspace.getState().views[run.id];
    // The panel's gates, read now: a result that cannot be applied says why
    // before anything is asked.
    if (view && view.merge.mode !== "local") {
      setGate(view.merge.mode === "pr" ? "has_pr" : view.merge.blocked || "refused");
      return;
    }
    if (!(await confirmApply(t, run.repoName))) return;
    // Applied in a temporary copy: the person's folder shows another
    // version, so the new files are not visible there yet.
    if (live.current) setHiddenAfter(view?.info?.baseCheckout === "none");
    await useWorkspace.getState().mergeLocal(run);
  };

  const repoPath = local?.info?.repos?.find((repo) => repo.id === run.repoId)?.path ?? "";
  const onOpenFolder = async () => {
    setFolderError("");
    try {
      await api.openFolder(repoPath);
    } catch (error) {
      if (live.current) setFolderError(t("result.openFolder.failed", { reason: message(error) }));
    }
  };

  if (state === "applied") {
    return (
      <div className={className}>
        <p className="result-actions-done" role="status">
          {t("result.apply.done")}
        </p>
        {hiddenAfter && <p className="result-actions-note">{t("result.apply.doneHidden")}</p>}
        {repoPath && (
          <div className="result-actions-buttons">
            <button type="button" className="result-btn" onClick={() => void onOpenFolder()}>
              {t("result.openFolder")}
            </button>
          </div>
        )}
        {folderError && (
          <p className="result-actions-note is-error" role="alert">
            {folderError}
          </p>
        )}
      </div>
    );
  }

  // Undecided contest entry: Apply stays locked until the person picks a winner.
  if (state === "contest_locked") {
    const contestState = contest?.state || "running";
    const statusKey =
      contestState === "running" ||
      contestState === "judging" ||
      contestState === "ready" ||
      contestState === "decided" ||
      contestState === "cancelled"
        ? `contest.state.${contestState}`
        : "contest.state.running";
    return (
      <div className={className}>
        <div className="result-actions-buttons">
          <button type="button" className="result-btn" disabled={looking} onClick={() => void onLook()}>
            {looking ? t("result.look.opening") : t("result.look")}
          </button>
          <button type="button" className="result-btn is-primary" disabled>
            {t("result.apply")}
          </button>
        </div>
        <p className="result-actions-note" role="status">
          {t(statusKey)}
        </p>
      </div>
    );
  }

  const isTurnRunning = refusal?.code.toLowerCase() === "turn_running";
  const key = refusal ? (isTurnRunning ? "runDialog.err.turnRunning" : applyRefusalKey(refusal.code)) : "";
  const other = key === "result.refuse.other";
  const said = lookText(t, look);
  const page = look?.kind === "page" || look?.kind === "blocked" ? look.url : "";

  return (
    <div className={className}>
      <div className="result-actions-buttons">
        <button type="button" className="result-btn" disabled={looking} onClick={() => void onLook()}>
          {looking ? t("result.look.opening") : t("result.look")}
        </button>
        {busy ? (
          <span className="result-actions-busy" role="status" aria-live="polite">
            <span className="run-merge-spinner" aria-hidden="true" />
            {t("result.apply.busy")}
          </span>
        ) : (
          <button type="button" className="result-btn is-primary" disabled={checking} onClick={() => void onApply()}>
            {t("result.apply")}
          </button>
        )}
      </div>
      {said && <p className="result-actions-note">{said}</p>}
      {page && (
        <a className="result-actions-link" href={page} target="_blank" rel="noopener noreferrer">
          {t("result.look.link")}
        </a>
      )}
      {refusal && !busy && (
        <div className="result-actions-note is-error" role="alert">
          <span>{t(key, { reason: refusal.detail || refusal.code })}</span>
          {!other && !isTurnRunning && refusal.detail && (
            <>
              {details ? (
                <small className="result-actions-details">{refusal.detail}</small>
              ) : (
                <button type="button" className="result-actions-more" onClick={() => setDetails(true)}>
                  {t("result.details")}
                </button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

export default ResultActions;
