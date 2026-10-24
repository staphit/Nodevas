import { useCallback, useEffect, useState } from "react";
import type { CopilotSettings, CopilotStatus } from "../../api/copilot";
import { RequestError } from "../../api/http";
import { RunnerSetupSteps } from "../../copilot/RunnerHelp";
import { fetchCopilotStatus, loadCopilotSettings, saveCopilotKeySource } from "../../copilot/session";
import { useI18n } from "../../i18n";
import type { SettingsNotify } from "./notify";

type KeySourceChoice = "auto" | "runner" | "server";

/**
 * Where the voice copilot's API key lives. On a runner (the person's own PC)
 * the server never holds a model credential; a key on the server is one an
 * intruder there could take. Everyone sees where their calls run; only an
 * administrator changes it.
 */
export function CopilotEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<CopilotStatus | null>(null);
  const [settings, setSettings] = useState<CopilotSettings | null>(null);
  const [admin, setAdmin] = useState<boolean | null>(null);
  const [choice, setChoice] = useState<KeySourceChoice>("auto");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await fetchCopilotStatus());
    } catch (error) {
      setStatus(null);
      if (!(error instanceof RequestError && error.status === 404)) {
        notify.onError(error instanceof Error ? error.message : String(error));
      }
    }
    try {
      const loaded = await loadCopilotSettings();
      setSettings(loaded);
      setChoice((loaded.keySource as KeySourceChoice | undefined) ?? "auto");
      setAdmin(true);
    } catch {
      // 403 for everyone but an administrator; the status still shows.
      setAdmin(false);
    }
  }, [notify]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    try {
      const saved = await saveCopilotKeySource(choice);
      setSettings(saved);
      notify.onNotice(t("copilot.settings.saved"));
      setStatus(await fetchCopilotStatus());
    } catch (error) {
      notify.onError(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  const onRunner = status?.keySource === "runner";
  const serverKey = settings?.effectiveKeySource === "server" && settings.hasApiKey;

  return (
    <section className="settings-section copilot-settings">
      <p className="settings-hint">{t("copilot.settings.intro")}</p>

      <div className="settings-row" data-testid="copilot-key-status">
        <span>{t("copilot.settings.callsRun")}</span>
        <b>
          {status === null
            ? t("copilot.settings.unavailable")
            : onRunner
              ? status.runner
                ? t("copilot.settings.onRunner", { runner: status.runner, model: status.model })
                : t("copilot.settings.waitingRunner")
              : t("copilot.settings.onServer", { model: status.model })}
        </b>
      </div>
      {status?.reason && !status.needsRunner && <p className="settings-hint">{status.reason}</p>}
      {onRunner && !status?.runner && <RunnerSetupSteps />}

      {admin && (
        <>
          <div className="settings-row">
            <label htmlFor="copilot-key-source">{t("copilot.settings.keySource")}</label>
            <select
              id="copilot-key-source"
              value={choice}
              onChange={(event) => setChoice(event.target.value as KeySourceChoice)}
            >
              <option value="runner">{t("copilot.settings.source.runner")}</option>
              <option value="auto">{t("copilot.settings.source.auto")}</option>
              <option value="server">{t("copilot.settings.source.server")}</option>
            </select>
            <button type="button" disabled={saving || choice === (settings?.keySource ?? "auto")} onClick={() => void save()}>
              {t("copilot.settings.save")}
            </button>
          </div>
          <p className="settings-hint">{t(`copilot.settings.source.${choice}.hint`)}</p>
          {serverKey && (
            <p className="copilot-key-warning" role="note">
              {t("copilot.settings.serverKeyWarning")}
            </p>
          )}
        </>
      )}
      {admin === false && <p className="settings-hint">{t("copilot.settings.adminOnly")}</p>}
    </section>
  );
}
