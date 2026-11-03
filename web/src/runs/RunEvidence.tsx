// Agent roles in a node's runs: the role each run acted in, the red/green
// evidence of the repo's test command, the executor diff guard and a judge's
// checks. Read-only: the hub records all of it, nobody edits it here.

import { useEffect, useId, useRef, useState } from "react";

import type { CheckResult, GuardReport, Run, VerifyResult } from "../api";
import { useI18n } from "../i18n";
import { roleNameById } from "../roles/roleLabels";
import { useApp } from "../store";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** "Tester", "Executor"…: the role a run acted in; nothing for a legacy run. */
export function RoleBadge({ role }: { role?: string }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  if (!role) return null;
  return <span className={`run-role run-role-${role}`}>{roleNameById(graph, role, t)}</span>;
}

/** "Red: tests failed (exit 1)", "Green: tests passed (exit 0)"… */
export function verifyLabel(verify: VerifyResult, t: Translate): string {
  if (verify.noCommand) return t("evidence.noCommand");
  const phase = verify.phase === "red" || verify.phase === "green" || verify.phase === "goal" ? verify.phase : "other";
  const failed = verify.exitCode !== 0;
  return t(`evidence.${phase}.${failed ? "failed" : "passed"}`, { exit: verify.exitCode });
}

/** "4.2 s", "1 min 5 s". */
export function durationText(ms: number, t: Translate): string {
  const seconds = Math.max(0, ms) / 1000;
  if (seconds < 60) return t("evidence.seconds", { value: seconds.toFixed(1) });
  const minutes = Math.floor(seconds / 60);
  return t("evidence.minutes", { minutes, seconds: Math.round(seconds - minutes * 60) });
}

/** A verify run's result as a button; it opens the evidence. */
export function VerifyEvidence({ verify }: { verify: VerifyResult }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  const label = verifyLabel(verify, t);
  // Whether the result is what the phase wanted: red wants failing tests.
  const good = verify.passed && !verify.noCommand;

  useEffect(() => {
    if (!open) return;
    closeRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <span className="run-evidence">
      <button
        type="button"
        className={`run-evidence-button is-${verify.phase} ${good ? "is-good" : "is-bad"}`}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {label}
      </button>
      {open && (
        <div className="run-evidence-pop" role="dialog" aria-labelledby={titleId}>
          <div className="run-evidence-head">
            <strong id={titleId}>{t("evidence.title", { label })}</strong>
            <button
              ref={closeRef}
              type="button"
              className="run-link-button"
              aria-label={t("common.close")}
              onClick={() => setOpen(false)}
            >
              ×
            </button>
          </div>
          <dl className="run-meta">
            <div>
              <dt>{t("evidence.exit")}</dt>
              <dd>{verify.exitCode}</dd>
            </div>
            <div>
              <dt>{t("evidence.duration")}</dt>
              <dd>{durationText(verify.durationMs, t)}</dd>
            </div>
            {verify.head && (
              <div>
                <dt>{t("evidence.head")}</dt>
                <dd>
                  <code title={verify.head}>{verify.head.slice(0, 7)}</code>
                </dd>
              </div>
            )}
            <div>
              <dt>{t("evidence.verdict")}</dt>
              <dd>{t(good ? "evidence.asWanted" : "evidence.notAsWanted")}</dd>
            </div>
          </dl>
          {verify.output ? (
            <pre className="run-evidence-output">{verify.output}</pre>
          ) : (
            <p className="run-hint">{t("evidence.noOutput")}</p>
          )}
        </div>
      )}
    </span>
  );
}

/** The executor diff guard: the Tester's files and whether the executor
 * changed them (or test infrastructure), and who signed it off. */
export function GuardView({ guard }: { guard: GuardReport }) {
  const { t } = useI18n();
  const touched = new Set(guard.touched ?? []);
  const files = guard.testFiles ?? [];
  const sensitive = guard.sensitive ?? [];
  const violation = files.some((file) => touched.has(file)) || sensitive.length > 0;
  return (
    <div className={`run-guard${violation ? " is-violation" : ""}`} role="group" aria-label={t("evidence.guard")}>
      <strong className="run-guard-title">{t("evidence.guard")}</strong>
      <span className="run-guard-state">
        {violation
          ? guard.approved
            ? t("evidence.guardApproved", { by: guard.approvedBy || "—" })
            : t("evidence.guardViolation")
          : t("evidence.guardClean")}
      </span>
      {files.length > 0 && (
        <ul className="run-guard-files">
          {files.map((file) => (
            <li key={file} className={touched.has(file) ? "is-touched" : undefined}>
              <code>{file}</code>
              {touched.has(file) && <span className="run-guard-mark">{t("evidence.changedByExecutor")}</span>}
            </li>
          ))}
        </ul>
      )}
      {sensitive.length > 0 && (
        <>
          <span className="run-guard-sub">{t("evidence.sensitive")}</span>
          <ul className="run-guard-files">
            {sensitive.map((file) => (
              <li key={file} className="is-touched">
                <code>{file}</code>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

/** A judge's verdict on each criterion. */
export function JudgeChecks({ checks }: { checks: CheckResult[] }) {
  const { t } = useI18n();
  if (checks.length === 0) return null;
  return (
    <ol className="run-checks" aria-label={t("evidence.checks")}>
      {checks.map((check) => (
        <li key={check.id} className={check.pass ? "is-pass" : "is-fail"}>
          <code>{check.id}</code> {t(check.pass ? "evidence.checkPass" : "evidence.checkFail")}
          {check.note ? ` — ${check.note}` : ""}
        </li>
      ))}
    </ol>
  );
}

/** Whether a run carries role evidence worth a footer in its row. */
export function runHasEvidence(run: Run): boolean {
  return Boolean(run.verify);
}
