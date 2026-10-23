import type { Run } from "../api";
import { useI18n } from "../i18n";
import { formatCount, runBadgeKind, runHasChanges, safeExternalUrl } from "./runFormat";
import { useRuns } from "./runStore";

const PR_STATES = new Set(["open", "merged", "closed"]);
const CI_STATES = new Set(["pending", "success", "failure"]);
const CI_MARK: Record<string, string> = { pending: "…", success: "✓", failure: "✗" };

/** "+120 −8" with an accessible reading. */
export function LineCounts({ additions, deletions }: { additions: number; deletions: number }) {
  const { t } = useI18n();
  return (
    <span
      className="run-lines"
      title={t("runs.lineCounts", {
        additions: formatCount(additions),
        deletions: formatCount(deletions),
      })}
    >
      <span className="run-lines-add">+{formatCount(additions)}</span>{" "}
      <span className="run-lines-del">−{formatCount(deletions)}</span>
    </span>
  );
}

export function filesLabel(t: (key: string, values?: Record<string, string | number>) => string, count: number) {
  return count === 1 ? t("runs.filesOne") : t("runs.filesMany", { count: formatCount(count) });
}

/** "PR #42" linking out, coloured by its state. */
export function PRChip({ run }: { run: Run }) {
  const { t } = useI18n();
  const href = safeExternalUrl(run.prUrl);
  if (!href) return null;
  const state = PR_STATES.has(run.prState ?? "") ? (run.prState as string) : "open";
  const label = run.prNumber ? t("runs.pr", { number: run.prNumber }) : "PR";
  return (
    <a
      className={`run-pr run-pr-${state}`}
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      title={t("runs.prTitle", { number: run.prNumber ?? "", state: t(`runs.prState.${state}`) })}
      onClick={(event) => event.stopPropagation()}
    >
      {label}
      <span className="run-pr-state">{t(`runs.prState.${state}`)}</span>
    </a>
  );
}

/** "CI ✓" / "CI ✗" / "CI …", or nothing before any check reports. */
export function CIBadge({ status }: { status: string | undefined }) {
  const { t } = useI18n();
  if (!status || !CI_STATES.has(status)) return null;
  return (
    <span
      className={`run-ci run-ci-${status}`}
      role="img"
      aria-label={t(`runs.ci.${status}`)}
      title={t(`runs.ci.${status}`)}
    >
      CI {CI_MARK[status]}
    </span>
  );
}

/**
 * A run's changes in one line: "+120 −8 · 5 files · PR #42 · CI ✓", and on a
 * second, muted line why no pull request was opened. Nothing while the run
 * has reported neither changes nor a PR.
 */
export function RunChangesSummary({ run, className = "" }: { run: Run; className?: string }) {
  const { t } = useI18n();
  const changed = runHasChanges(run);
  const pr = Boolean(safeExternalUrl(run.prUrl));
  if (!changed && !pr && !run.prError) return null;
  return (
    <div className={`run-changes ${className}`.trim()}>
      {(changed || pr) && (
        <span className="run-changes-line">
          {changed && (
            <>
              <LineCounts additions={run.additions ?? 0} deletions={run.deletions ?? 0} />
              <span className="run-changes-files">{filesLabel(t, run.filesChanged ?? 0)}</span>
            </>
          )}
          <PRChip run={run} />
          {pr && <CIBadge status={run.ciStatus} />}
        </span>
      )}
      {run.prError && !pr && (
        <span className="run-pr-error" title={run.prError}>
          {t("runs.noPr", { error: run.prError })}
        </span>
      )}
    </div>
  );
}

/** The latest run's state on a graph card: a dot and a few words. */
export function RunCardBadge({ nodeId }: { nodeId: string }) {
  const { t } = useI18n();
  const run = useRuns((s) => s.latestByNode[nodeId]);
  if (!run) return null;
  const kind = runBadgeKind(run);
  if (!kind) return null;
  const agent = run.model ? `${run.agent} · ${run.model}` : run.agent;
  const pr = run.prNumber ? t("runs.pr", { number: run.prNumber }) : "PR";
  const text =
    kind === "ci-failure"
      ? `${pr} · CI ✗`
      : kind === "pr-merged" || kind === "pr-open"
        ? pr
        : run.model || run.agent;
  const title = t(`runs.badge.${kind}`, { agent, number: run.prNumber ?? "" });
  return (
    <span className={`col-card-run col-card-run-${kind}`} title={title} aria-label={title}>
      <span className="col-card-run-dot" aria-hidden="true" />
      <span className="col-card-run-text">{text}</span>
    </span>
  );
}
