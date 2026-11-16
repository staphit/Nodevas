// "Take a look" and "Apply to my folder": a finished run's result in plain
// words, for the single-user edition (resultActions.ts). Station's lists
// show it to every single user, the workspace panel only in Simple mode.
// Apply is the owner's local merge with its gates as they are: the panel's
// workspace view is read when Apply is pressed (never when a row appears, so
// a long list costs no runner calls), a refusal is said in a plain sentence
// that always says nothing was changed, and the runner's own words wait
// under "Show details".

import { useEffect, useRef, useState } from "react";
import { api, type Run } from "../api";
import { useI18n } from "../i18n";
import { useLocalRunner } from "../runners/edition";
import { confirmApply } from "./confirmWorkspace";
import { applyRefusalKey, lookAt, resultState, type LookOutcome } from "./resultState";
import { selectRunById, useRuns } from "./runStore";
import { useWorkspace, workspaceCodeText } from "./workspaceStore";

type Translate = ReturnType<typeof useI18n>["t"];

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
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

export function ResultActions({ run: given, compact = false }: { run: Run; compact?: boolean }) {
  const { t } = useI18n();
  const local = useLocalRunner();
  // The run store's copy is the fresher one: an apply lands there first.
  const stored = useRuns((s) => selectRunById(s, given.id));
  const run = stored ?? given;
  const state = resultState(run, local?.id);
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

  const className = `result-actions${compact ? " is-compact" : ""}`;

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

  const key = refusal ? applyRefusalKey(refusal.code) : "";
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
          {!other && refusal.detail && (
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
