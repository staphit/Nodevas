import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Run } from "../api";
import { IconCheck, IconClose, IconFork } from "../icons";
import { useI18n } from "../i18n";
import { safeExternalUrl } from "./runFormat";
import { useRuns, type MergeRefusal } from "./runStore";

/** Why the Merge button is off, when it is shown but cannot be pressed. */
export type MergeBlock = "ci-pending" | "ci-failure" | "ci-none" | "no-head";

/**
 * What the merge control offers for a run:
 * - "none": no open PR to merge (none at all, or merged/closed already);
 * - "pending": an approval is waiting for the runner;
 * - "ready": open, CI green, head known — the button works;
 * - "blocked": open, but CI is not green yet (or the head is unknown).
 * A draft PR is not known here; the hub refuses it and says so.
 */
export type MergeGate =
  | { kind: "none" }
  | { kind: "pending" }
  | { kind: "ready" }
  | { kind: "blocked"; reason: MergeBlock };

/** Mirrors runnerwire.MergeRequestTTLSeconds. */
export const MERGE_TTL_MS = 15 * 60 * 1000;

/** How long until a pending merge goes stale, in ms; 0 once it has, null
 * when the run has no pending merge. The hub expires one lazily (on the next
 * approval, runner connect or run read), so the page cannot wait for it. */
export function pendingMergeLeft(run: Run, now = Date.now()): number | null {
  const merge = run.merge;
  if (merge?.status !== "pending") return null;
  const at = Date.parse(merge.createdAt);
  if (!Number.isFinite(at)) return null;
  return Math.max(0, at + MERGE_TTL_MS - now);
}

export function mergeGate(run: Run, now = Date.now()): MergeGate {
  if (run.merge?.status === "pending" && pendingMergeLeft(run, now) !== 0) {
    return { kind: "pending" };
  }
  if (!safeExternalUrl(run.prUrl) || run.prState !== "open") return { kind: "none" };
  if (run.ciStatus === "failure") return { kind: "blocked", reason: "ci-failure" };
  if (run.ciStatus === "pending") return { kind: "blocked", reason: "ci-pending" };
  if (run.ciStatus !== "success") return { kind: "blocked", reason: "ci-none" };
  if (!run.prHeadSha) return { kind: "blocked", reason: "no-head" };
  return { kind: "ready" };
}

/** The first seven characters, as git shows a commit. */
export function shortSha(sha: string | undefined): string {
  return (sha ?? "").slice(0, 7);
}

// The diff dialog closes on Escape from a capture listener on window, which
// runs before this dialog's own; it checks this to leave the Escape to us.
let openConfirms = 0;
export function mergeConfirmOpen(): boolean {
  return openConfirms > 0;
}

/**
 * Names exactly what is about to be merged — PR, repository, branch → base and
 * the head commit the approver looked at — before the approval is posted.
 * Portalled so it stacks over the diff dialog and the phone's Drawer sheet.
 */
export function MergeConfirmDialog({
  run,
  headSha,
  busy,
  onCancel,
  onConfirm,
}: {
  run: Run;
  /** The head as it was when the approver pressed Merge: the one posted. */
  headSha: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const latest = useRef({ onCancel, busy });
  latest.current = { onCancel, busy };

  useEffect(() => {
    openConfirms++;
    const previous = document.activeElement as HTMLElement | null;
    cancelRef.current?.focus();
    // Captured and stopped, so the diff dialog or Drawer underneath stays.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      if (!latest.current.busy) latest.current.onCancel();
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      openConfirms--;
      window.removeEventListener("keydown", onKey, true);
      previous?.focus?.();
    };
  }, []);

  const number = run.prNumber ?? "";
  const branch = run.branch || "";
  return createPortal(
    <div
      className="confirm-backdrop run-merge-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <section
        className="confirm-dialog run-merge-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconFork size={17} />
          </span>
          <div>
            <h2 id={titleId}>{t("merge.confirmTitle", { number })}</h2>
            <p>{t("merge.confirmDescription")}</p>
          </div>
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={onCancel}
            disabled={busy}
            aria-label={t("common.close")}
          >
            <IconClose size={14} />
          </button>
        </header>
        <dl className="run-merge-facts">
          <div>
            <dt>{t("merge.pullRequest")}</dt>
            <dd>{t("runs.pr", { number })}</dd>
          </div>
          <div>
            <dt>{t("runs.repo")}</dt>
            <dd>{run.repoName || run.repoId}</dd>
          </div>
          <div>
            <dt>{t("runs.branch")}</dt>
            <dd className="mono">
              {branch}
              {branch && run.base ? " → " : ""}
              {run.base}
            </dd>
          </div>
          <div>
            <dt>{t("merge.head")}</dt>
            <dd className="mono" title={headSha}>
              {shortSha(headSha)}
            </dd>
          </div>
        </dl>
        <p className="run-merge-method">{t("merge.method")}</p>
        <footer>
          <button type="button" ref={cancelRef} onClick={onCancel} disabled={busy}>
            {t("common.cancel")}
          </button>
          <button type="button" className="primary" onClick={onConfirm} disabled={busy}>
            {busy ? t("merge.approving") : t("merge.confirm", { number })}
          </button>
        </footer>
      </section>
    </div>,
    document.body,
  );
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

function refusalText(t: Translate, refusal: MergeRefusal): string {
  if (refusal.kind === "denied") return t("merge.denied", { message: refusal.message });
  if (refusal.kind === "precondition") {
    return refusal.message ? t("merge.refused", { message: refusal.message }) : t("merge.refusedGeneric");
  }
  return t("merge.error", { message: refusal.message });
}

/**
 * The run's merge approval: the latest merge's outcome and a Merge PR button,
 * or why it is off. Lives in the run's header and at the foot of its diff —
 * look at the changes, then approve, on a phone as much as anywhere.
 */
export function MergeControl({ run, className = "" }: { run: Run; className?: string }) {
  const { t } = useI18n();
  const merging = useRuns((s) => Boolean(s.merging[run.id]));
  const refusal = useRuns((s) => s.mergeErrors[run.id]);
  const mergeRun = useRuns((s) => s.mergeRun);
  // The head SHA the Merge button was pressed at. A push that lands while
  // the dialog is open must not change what the approval names; the hub
  // refuses a stale one with "the PR changed since you looked".
  const [confirmHead, setConfirmHead] = useState<string | null>(null);
  const reasonId = useId();
  // Re-render when a pending approval goes stale: nothing pushes that.
  const [, setTick] = useState(0);
  const left = pendingMergeLeft(run);
  const waiting = left !== null && left > 0;
  const pendingCreated = run.merge?.status === "pending" ? run.merge.createdAt : "";
  useEffect(() => {
    if (!waiting) return;
    const at = Date.parse(pendingCreated) + MERGE_TTL_MS;
    const timer = window.setTimeout(() => setTick((n) => n + 1), at - Date.now() + 1000);
    return () => window.clearTimeout(timer);
  }, [waiting, pendingCreated]);
  const gate = mergeGate(run);
  const merge = run.merge ?? null;

  let status: React.ReactNode = null;
  let tone = "";
  if (merge?.status === "pending" && left === 0) {
    // The hub expires it on the next approval; meanwhile it may be redone.
    tone = "failed";
    status = t("merge.stale");
  } else if (merge?.status === "pending") {
    tone = "pending";
    status = t("merge.pending");
  } else if (merge?.status === "merged") {
    tone = "merged";
    status = (
      <>
        <IconCheck size={13} /> {t("merge.merged", { number: run.prNumber ?? "" })}
      </>
    );
  } else if (merge?.status === "failed") {
    tone = "failed";
    status = merge.reason ? t("merge.failed", { reason: merge.reason }) : t("merge.failedNoReason");
  } else if (merge?.status === "expired") {
    tone = "failed";
    status = t("merge.expired");
  }

  // Nothing to say and nothing to press: no open PR and no merge on record.
  if (gate.kind === "none" && !status) return null;

  const blocked = gate.kind === "blocked" ? t(`merge.blocked.${gate.reason}`) : "";
  const showButton = gate.kind === "ready" || gate.kind === "blocked";
  const label = t("merge.button", { number: run.prNumber ?? "" });

  return (
    <div className={`run-merge ${className}`.trim()} data-merge={tone || gate.kind}>
      {(status || blocked) && (
        <div className="run-merge-text">
          {status && (
            <span
              className={`run-merge-status${tone ? ` is-${tone}` : ""}`}
              role="status"
              aria-live="polite"
            >
              {tone === "pending" && <span className="run-merge-spinner" aria-hidden="true" />}
              {status}
            </span>
          )}
          {blocked && (
            <span id={reasonId} className="run-merge-reason">
              {blocked}
            </span>
          )}
        </div>
      )}
      {showButton && (
        <button
          type="button"
          className="run-merge-button"
          disabled={gate.kind !== "ready" || merging}
          aria-describedby={blocked ? reasonId : undefined}
          title={blocked || undefined}
          onClick={() => setConfirmHead(run.prHeadSha ?? "")}
        >
          <IconFork size={13} />
          <span>{merging ? t("merge.approving") : label}</span>
        </button>
      )}
      {refusal && (
        <div className="notify-message error run-merge-error" role="alert">
          {refusalText(t, refusal)}
        </div>
      )}
      {confirmHead !== null && (
        <MergeConfirmDialog
          run={run}
          headSha={confirmHead}
          busy={merging}
          onCancel={() => setConfirmHead(null)}
          onConfirm={() => {
            void mergeRun(run.id, confirmHead).finally(() => setConfirmHead(null));
          }}
        />
      )}
    </div>
  );
}
