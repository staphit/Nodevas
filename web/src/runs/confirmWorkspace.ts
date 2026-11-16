import type { Run, RunWorkspaceInfo } from "../api";
import { confirmAction } from "../components/ConfirmDialog";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** Asks before a local merge, naming the branch, the base, where the runner
 * merges (the owner's clean checkout or a temporary worktree) and that
 * nothing is pushed. */
export function confirmMergeLocal(
  t: Translate,
  run: Pick<Run, "branch" | "base">,
  info: RunWorkspaceInfo | null | undefined,
): Promise<boolean> {
  const values = { branch: run.branch, base: run.base };
  // "Clean" only when the runner just said so; it checks again either way.
  const where =
    info?.baseCheckout === "repo"
      ? t(info.baseClean ? "runWorkspace.merge.confirmRepo" : "runWorkspace.merge.confirmRepoDirty", values)
      : info?.baseCheckout === "none"
        ? t("runWorkspace.merge.confirmTemp", values)
        : "";
  return confirmAction({
    title: t("runWorkspace.merge.confirmTitle", values),
    description: [where, t("runWorkspace.merge.confirmAfter", values)].filter(Boolean).join(" "),
    confirmLabel: t("runWorkspace.merge.confirm"),
    cancelLabel: t("common.cancel"),
  });
}

/** Asks before a result is applied (the plain local merge): which folder it
 * goes into, by name, else "your folder", and that nothing leaves this
 * computer. */
export function confirmApply(t: Translate, folder: string): Promise<boolean> {
  return confirmAction({
    title: t("result.apply.confirmTitle"),
    description: t("result.apply.confirmBody", { folder: folder || t("result.yourFolder") }),
    confirmLabel: t("result.apply.confirm"),
    cancelLabel: t("common.cancel"),
  });
}

/** Asks before discarding a run's work, listing what goes: the worktree with
 * its uncommitted files, the branch, an open PR and its remote branch. Only
 * what the runner will really remove is promised. */
export function confirmDiscard(
  t: Translate,
  run: Pick<Run, "branch" | "prState" | "prNumber" | "prUrl" | "kind" | "commit">,
  info: RunWorkspaceInfo | null | undefined,
): Promise<boolean> {
  // The runner deletes the branch, and closes the PR, of an implement or
  // test run only: a review or revise run's PR belongs to another run.
  const ownKind = !run.kind || run.kind === "test";
  const dropsBranch = ownKind && Boolean(run.branch) && (info ? info.ownBranch && info.branchExists : true);
  // The runner closes only a PR it watches; without its word, that is said
  // as a maybe, never as a promise.
  const openPR = ownKind && Boolean(run.prUrl) && run.prState === "open";
  const closesPR = openPR && Boolean(info?.watchedPr);
  const mayClosePR = openPR && !info;
  const moved = dropsBranch && Boolean(info?.branchHead) && Boolean(run.commit) && info?.branchHead !== run.commit;
  const lines = [
    // Without the runner's word the worktree may or may not be there; a
    // count it could not take (no answer, or git failed) is never shown as 0.
    info?.exists === false
      ? ""
      : info && info.uncommitted >= 0
        ? t("runWorkspace.discard.worktree", { count: info.uncommitted })
        : t("runWorkspace.discard.worktreeUnknown"),
    dropsBranch ? t("runWorkspace.discard.branch", { branch: run.branch }) : "",
    moved ? t("runWorkspace.discard.moved", { branch: run.branch }) : "",
    closesPR ? t("runWorkspace.discard.pr", { number: run.prNumber ?? "" }) : "",
    mayClosePR ? t("runWorkspace.discard.prMaybe", { number: run.prNumber ?? "" }) : "",
    t("runWorkspace.discard.final"),
  ];
  return confirmAction({
    title: t("runWorkspace.discard.confirmTitle"),
    description: lines.filter(Boolean).join(" "),
    confirmLabel: t("runWorkspace.discard.confirm"),
    cancelLabel: t("runWorkspace.keep"),
    tone: "danger",
  });
}
