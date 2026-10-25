/**
 * What the copilot shows while its model calls wait for this person's runner:
 * the key for them lives on their own PC, which is offline or has none yet.
 */

import { useEffect, useId, useState } from "react";
import { useI18n } from "../i18n";
import { IconClose } from "../icons";
import { useCopilot } from "./copilotStore";
import { refreshCopilotStatus } from "./session";

export const RUNNER_SETUP_COMMANDS = [
  "nodevas runner set copilotProvider anthropic",
  "nodevas runner copilot-key",
  "nodevas runner",
];

export function RunnerSetupSteps() {
  const { t } = useI18n();
  return (
    <>
      <ol className="copilot-runner-steps">
        <li>
          {t("copilot.runner.stepProvider")}
          <code>{RUNNER_SETUP_COMMANDS[0]}</code>
        </li>
        <li>
          {t("copilot.runner.stepKey")}
          <code>{RUNNER_SETUP_COMMANDS[1]}</code>
        </li>
        <li>
          {t("copilot.runner.stepStart")}
          <code>{RUNNER_SETUP_COMMANDS[2]}</code>
        </li>
      </ol>
      <p className="copilot-runner-note">{t("copilot.runner.keyStaysHere")}</p>
    </>
  );
}

export function RunnerHelp({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const headingId = useId();
  const reason = useCopilot((s) => s.status?.reason);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <aside className="copilot-panel copilot-runner-help" role="dialog" aria-modal="false" aria-labelledby={headingId}>
      <header className="copilot-header">
        <div className="copilot-header-row">
          <h2 id={headingId} className="copilot-heading">
            {t("copilot.runner.title")}
          </h2>
          <button type="button" className="copilot-icon-btn" onClick={onClose} aria-label={t("copilot.close")}>
            <IconClose size={16} />
          </button>
        </div>
      </header>
      <div className="copilot-runner-body">
        <p>{t("copilot.runner.body")}</p>
        {reason && <p className="copilot-runner-reason">{reason}</p>}
        <RunnerSetupSteps />
        <button
          type="button"
          disabled={checking}
          onClick={async () => {
            setChecking(true);
            try {
              await refreshCopilotStatus();
            } finally {
              setChecking(false);
            }
          }}
        >
          {t("copilot.runner.checkAgain")}
        </button>
      </div>
    </aside>
  );
}
