/**
 * What the copilot shows while it has no model to answer with yet: this
 * person's runner (which holds the key for them) is offline or has none, the
 * browser's built-in model still has to be downloaded, or, in automatic mode,
 * nothing free is available and the person is told how to get one.
 */

import { useEffect, useId, useState } from "react";
import { useI18n } from "../i18n";
import { IconClose } from "../icons";
import { useCopilot, type ServerState } from "./copilotStore";
import { downloadBrowserModel, refreshCopilotStatus } from "./session";

export const RUNNER_SETUP_COMMANDS = [
  "nodevas runner set copilotProvider anthropic",
  "nodevas runner copilot-key",
  "nodevas runner",
];

/** The model `ollama pull` suggests: small, and good at tool calls. */
export const OLLAMA_PULL_COMMAND = "ollama pull qwen3:8b";

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

/** The free options, in the order automatic mode tries them. */
export function FreeModelSteps() {
  const { t } = useI18n();
  return (
    <ul className="copilot-runner-steps copilot-free-options">
      <li>{t("copilot.setup.browser")}</li>
      <li>
        {t("copilot.setup.ollama")}
        <code>{OLLAMA_PULL_COMMAND}</code>
      </li>
      <li>{t("copilot.setup.key")}</li>
    </ul>
  );
}

function DownloadModel() {
  const { t } = useI18n();
  const download = useCopilot((s) => s.download);
  const percent = download.progress === null ? null : Math.round(download.progress * 100);
  return (
    <div className="copilot-download">
      <button type="button" disabled={download.active} onClick={() => void downloadBrowserModel()}>
        {download.active ? t("copilot.download.downloading") : t("copilot.download.button")}
      </button>
      {download.active && percent !== null && (
        <div className="copilot-download-progress">
          <progress max={100} value={percent} aria-label={t("copilot.download.progress")} />
          <span>{t("copilot.download.percent", { percent })}</span>
        </div>
      )}
      {download.error && (
        <p className="copilot-runner-reason" role="alert">
          {t("copilot.download.failed", { error: download.error })}
        </p>
      )}
    </div>
  );
}

const TITLE_KEYS: Partial<Record<ServerState, string>> = {
  needsRunner: "copilot.runner.title",
  needsDownload: "copilot.download.title",
  needsSetup: "copilot.setup.title",
};

/** The floating button's label while the copilot waits for a model. */
export const OPEN_KEYS: Partial<Record<ServerState, string>> = {
  needsRunner: "copilot.runner.open",
  needsDownload: "copilot.download.open",
  needsSetup: "copilot.setup.open",
};

export function RunnerHelp({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const headingId = useId();
  const serverState = useCopilot((s) => s.serverState);
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
            {t(TITLE_KEYS[serverState] ?? "copilot.runner.title")}
          </h2>
          <button type="button" className="copilot-icon-btn" onClick={onClose} aria-label={t("copilot.close")}>
            <IconClose size={16} />
          </button>
        </div>
      </header>
      <div className="copilot-runner-body">
        {serverState === "needsDownload" ? (
          <>
            <p>{t("copilot.download.body")}</p>
            <DownloadModel />
          </>
        ) : serverState === "needsSetup" ? (
          <>
            <p>{t("copilot.setup.body")}</p>
            <FreeModelSteps />
            <p className="copilot-runner-note">{t("copilot.setup.runner")}</p>
            <RunnerSetupSteps />
          </>
        ) : (
          <>
            <p>{t("copilot.runner.body")}</p>
            {reason && <p className="copilot-runner-reason">{reason}</p>}
            <RunnerSetupSteps />
          </>
        )}
        <button
          type="button"
          disabled={checking}
          onClick={async () => {
            setChecking(true);
            try {
              await refreshCopilotStatus(true);
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
