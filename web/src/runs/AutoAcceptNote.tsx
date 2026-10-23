import type { Runner } from "../api";
import { useI18n } from "../i18n";
import { formatClock } from "../stepup/StepUpDialog";
import { useStepUp } from "../stepup/stepUpStore";
import { autoAcceptOutcome } from "./autoAccept";

/**
 * The dispatch dialog's last word before sending: whether the chosen runner
 * starts this run on its own, would once the dispatcher verifies it is them,
 * or waits for someone at it to accept.
 */
export function AutoAcceptNote({
  runner,
  repoId,
  agent,
  mode = "",
  onVerify,
}: {
  runner: Runner;
  repoId: string;
  agent: string;
  mode?: string;
  onVerify: () => void;
}) {
  const { t } = useI18n();
  const stepUp = useStepUp((s) => s.status);
  const policy = runner.info?.autoAccept;
  const outcome = autoAcceptOutcome(policy, stepUp, { repoId, agent, mode });
  const repo = runner.info?.repos?.find((item) => item.id === repoId)?.name || repoId;
  const values = { runner: runner.name, repo, agent };

  if (outcome.kind === "auto") {
    const text =
      outcome.stepUp === "active"
        ? t("dispatch.autoAccept.active", { ...values, time: formatClock(outcome.until) || "—" })
        : t(`dispatch.autoAccept.${outcome.stepUp}`, values);
    return (
      <p className="run-note run-auto-note is-auto" data-testid="dispatch-auto-accept" data-outcome="auto">
        {text}
      </p>
    );
  }

  if (outcome.kind === "verify") {
    return (
      <p className="run-note run-auto-note is-verify" data-testid="dispatch-auto-accept" data-outcome="verify">
        <span>{t("dispatch.autoAccept.verify", values)}</span>{" "}
        <button type="button" className="run-auto-verify" onClick={onVerify}>
          {t("dispatch.autoAccept.verifyButton")}
        </button>
      </p>
    );
  }

  // Manual, or still asking the hub about step-up: the run waits for the
  // runner either way until the answer says otherwise.
  const reason =
    outcome.kind === "manual" && policy ? t(`dispatch.autoAccept.reason.${outcome.reason}`, values) : "";
  return (
    <p
      className="run-note run-auto-note"
      data-testid="dispatch-auto-accept"
      data-outcome={outcome.kind}
    >
      {t("runs.acceptNote")}
      {reason && <span className="run-auto-reason">{reason}</span>}
    </p>
  );
}
