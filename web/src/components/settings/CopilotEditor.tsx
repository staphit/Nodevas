import { useCallback, useEffect, useState } from "react";
import type { CopilotSettings, CopilotStatus } from "../../api/copilot";
import { RequestError } from "../../api/http";
import { modelLabel } from "../../copilot/modelLabel";
import { FreeModelSteps, RunnerSetupSteps } from "../../copilot/RunnerHelp";
import {
  fetchCopilotStatus,
  loadCopilotSettings,
  saveCopilotKeySource,
  saveCopilotProvider,
} from "../../copilot/session";
import { useI18n } from "../../i18n";
import type { SettingsNotify } from "./notify";

type KeySourceChoice = "auto" | "runner" | "server";
type ProviderChoice = "auto" | "anthropic" | "openai";

/** The administrator's provider choice; automatic stays automatic even when a key resolved it. */
function providerChoice(settings: CopilotSettings): ProviderChoice {
  if (settings.automatic || !settings.provider || settings.provider === "auto") return "auto";
  return settings.provider === "openai" ? "openai" : "anthropic";
}

/**
 * Which model the voice copilot uses and where its API key lives. Automatic
 * (the default) needs no key: the browser's built-in model or a local
 * server. On a runner (the person's own PC) the server never holds a model
 * credential; a key on the server is one an intruder there could take.
 * Everyone sees where their calls run; only an administrator changes it.
 */
export function CopilotEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<CopilotStatus | null>(null);
  const [settings, setSettings] = useState<CopilotSettings | null>(null);
  const [admin, setAdmin] = useState<boolean | null>(null);
  const [choice, setChoice] = useState<KeySourceChoice>("auto");
  const [provider, setProvider] = useState<ProviderChoice>("auto");
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
      setProvider(providerChoice(loaded));
      setAdmin(true);
    } catch {
      // 403 for everyone but an administrator; the status still shows.
      setAdmin(false);
    }
  }, [notify]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async (change: () => Promise<CopilotSettings>) => {
    setSaving(true);
    try {
      const saved = await change();
      setSettings(saved);
      setProvider(providerChoice(saved));
      notify.onNotice(t("copilot.settings.saved"));
      setStatus(await fetchCopilotStatus());
    } catch (error) {
      notify.onError(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  const host = status?.modelHost ?? status?.keySource;
  const onRunner = host === "runner";
  const serverKey = settings?.effectiveKeySource === "server" && settings.hasApiKey;

  let callsRun: string;
  if (status === null) callsRun = t("copilot.settings.unavailable");
  else if (status.provider === "browser" || host === "browser") callsRun = t("copilot.settings.inBrowser");
  else if (status.needsSetup) callsRun = t("copilot.settings.noFreeModel");
  else if (status.localServer) callsRun = t("copilot.settings.local", { label: modelLabel(status, t) });
  else if (onRunner)
    callsRun = status.runner
      ? t("copilot.settings.onRunner", { runner: status.runner, model: status.model })
      : t("copilot.settings.waitingRunner");
  else callsRun = t("copilot.settings.onServer", { model: status.model });

  const savedProvider = settings ? providerChoice(settings) : "auto";

  return (
    <section className="settings-section copilot-settings">
      <p className="settings-hint">{t("copilot.settings.intro")}</p>

      <div className="settings-row" data-testid="copilot-key-status">
        <span>{t("copilot.settings.callsRun")}</span>
        <b>{callsRun}</b>
      </div>
      {status?.reason && !status.needsRunner && !status.needsSetup && (
        <p className="settings-hint">{status.reason}</p>
      )}
      {status?.needsSetup && <FreeModelSteps />}
      {onRunner && !status?.runner && !status?.needsSetup && <RunnerSetupSteps />}

      {admin && (
        <>
          <div className="settings-row">
            <label htmlFor="copilot-provider">{t("copilot.settings.provider")}</label>
            <select
              id="copilot-provider"
              value={provider}
              onChange={(event) => setProvider(event.target.value as ProviderChoice)}
            >
              <option value="auto">{t("copilot.settings.provider.auto")}</option>
              <option value="anthropic">{t("copilot.settings.provider.anthropic")}</option>
              <option value="openai">{t("copilot.settings.provider.openai")}</option>
            </select>
            <button
              type="button"
              disabled={saving || provider === savedProvider}
              onClick={() => void save(() => saveCopilotProvider(provider))}
            >
              {t("copilot.settings.save")}
            </button>
          </div>
          <p className="settings-hint">{t(`copilot.settings.provider.${provider}.hint`)}</p>
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
            <button
              type="button"
              disabled={saving || choice === (settings?.keySource ?? "auto")}
              onClick={() => void save(() => saveCopilotKeySource(choice))}
            >
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
