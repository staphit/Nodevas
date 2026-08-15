import { useCallback, useEffect, useState } from "react";
import type { NotifySettings } from "../api";
import { remoteScope, useApp, useOperationPending } from "../store";
import { IconBell, IconClose } from "../icons";
import { useI18n } from "../i18n";

const LEAD_PRESETS: { minutes: number; key: string }[] = [
  { minutes: 30, key: "30m" },
  { minutes: 60, key: "1h" },
  { minutes: 180, key: "3h" },
  { minutes: 720, key: "12h" },
  { minutes: 1440, key: "1d" },
  { minutes: 2880, key: "2d" },
  { minutes: 4320, key: "3d" },
  { minutes: 10080, key: "7d" },
];

/**
 * Workspace-wide deadline email settings. The reminder itself is sent by the
 * Go server once per (project, node, deadline); this dialog only configures
 * lead time, SMTP transport, and recipient emails.
 */
export function NotifySettingsModal({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const updateNode = useApp((s) => s.updateNode);
  const stored = useApp((s) => s.notifySettings);
  // The server never sends the stored password back, so the field starts
  // empty and an empty field means "keep the one already saved".
  const hasPassword = useApp((s) => s.notifyHasPassword);
  const refreshNotifySettings = useApp((s) => s.refreshNotifySettings);
  const saveNotifySettings = useApp((s) => s.saveNotifySettings);
  const sendNotifyTest = useApp((s) => s.sendNotifyTest);
  const busy = useOperationPending(remoteScope.notify());
  // The form's own copy: the store holds what the server has, this holds what
  // the user is in the middle of typing.
  const [settings, setSettings] = useState<NotifySettings | null>(null);
  const [message, setMessage] = useState<{ text: string; kind: "ok" | "error" } | null>(null);

  useEffect(() => {
    void refreshNotifySettings().catch((error: unknown) =>
      setMessage({
        text: error instanceof Error ? error.message : t("notify.loadFailed"),
        kind: "error",
      }),
    );
  }, [refreshNotifySettings]);

  useEffect(() => {
    if (stored) setSettings(stored);
  }, [stored]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const patch = useCallback((partial: Partial<NotifySettings>) => {
    setSettings((value) => (value ? { ...value, ...partial } : value));
  }, []);

  const save = useCallback(async () => {
    if (!settings) return;
    setMessage(null);
    const result = await saveNotifySettings(settings);
    setMessage(
      result.ok
        ? { text: t("notify.saved"), kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  }, [saveNotifySettings, settings]);

  const sendTest = useCallback(async () => {
    if (!settings) return;
    setMessage(null);
    const result = await sendNotifyTest(settings);
    setMessage(
      result.ok
        ? { text: t("notify.testSent"), kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  }, [sendNotifyTest, settings]);

  const commitUserEmail = useCallback(
    (userID: string, email: string) => {
      void updateNode({ type: "node.setUserEmail", userId: userID, email }).then(
        (result) => {
          if (!result.ok) setMessage({ text: result.message, kind: "error" });
        },
      );
    },
    [updateNode],
  );

  const users = graph?.users ?? [];
  const leadIsPreset = settings
    ? LEAD_PRESETS.some((preset) => preset.minutes === settings.leadMinutes)
    : true;

  return (
    <div className="confirm-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog notify-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={t("notify.title")}
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconBell size={17} />
          </span>
          <div>
            <h2>{t("notify.title")}</h2>
            <p>{t("notify.description")}</p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("common.close")}>
            <IconClose size={14} />
          </button>
        </header>

        {settings ? (
          <div className="notify-form">
            <label className="notify-toggle">
              <input
                type="checkbox"
                checked={settings.enabled}
                onChange={(event) => patch({ enabled: event.target.checked })}
              />
              {t("notify.enabled")}
            </label>

            <div className="notify-field">
              <label htmlFor="notify-lead">{t("notify.leadTime")}</label>
              <div className="notify-lead">
                <select
                  id="notify-lead"
                  value={leadIsPreset ? String(settings.leadMinutes) : "custom"}
                  onChange={(event) => {
                    if (event.target.value !== "custom")
                      patch({ leadMinutes: Number(event.target.value) });
                  }}
                >
                  {LEAD_PRESETS.map((preset) => (
                    <option key={preset.minutes} value={preset.minutes}>
                      {t(`notify.lead.${preset.key}`)}
                    </option>
                  ))}
                  <option value="custom">{t("notify.custom")}</option>
                </select>
                <input
                  type="number"
                  min={1}
                  max={86400}
                  value={settings.leadMinutes}
                  aria-label={t("notify.customMinutes")}
                  onChange={(event) => {
                    const minutes = Number(event.target.value);
                    if (Number.isFinite(minutes) && minutes > 0)
                      patch({ leadMinutes: Math.floor(minutes) });
                  }}
                />
                <span>{t("notify.minutes")}</span>
              </div>
            </div>

            <div className="notify-grid">
              <div className="notify-field">
                <label htmlFor="notify-host">{t("notify.smtpHost")}</label>
                <input
                  id="notify-host"
                  value={settings.smtpHost}
                  placeholder={t("notify.smtpHostPlaceholder")}
                  onChange={(event) => patch({ smtpHost: event.target.value.trim() })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-port">{t("notify.port")}</label>
                <input
                  id="notify-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={settings.smtpPort}
                  onChange={(event) => patch({ smtpPort: Number(event.target.value) || 0 })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-user">{t("notify.username")}</label>
                <input
                  id="notify-user"
                  value={settings.smtpUser}
                  autoComplete="off"
                  onChange={(event) => patch({ smtpUser: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-pass">{t("notify.password")}</label>
                <input
                  id="notify-pass"
                  type="password"
                  value={settings.smtpPass}
                  autoComplete="new-password"
                  placeholder={hasPassword ? t("notify.passwordSaved") : ""}
                  onChange={(event) => patch({ smtpPass: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-from">{t("notify.from")}</label>
                <input
                  id="notify-from"
                  value={settings.from}
                  placeholder="you@example.com"
                  onChange={(event) => patch({ from: event.target.value.trim() })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-default-to">{t("notify.defaultRecipient")}</label>
                <input
                  id="notify-default-to"
                  value={settings.defaultTo}
                  placeholder={t("notify.defaultRecipientPlaceholder")}
                  onChange={(event) => patch({ defaultTo: event.target.value.trim() })}
                />
              </div>
            </div>

            {users.length > 0 && (
              <div className="notify-users">
                <label>{t("notify.userEmails")}</label>
                {users.map((user) => (
                  <div key={user.id} className="notify-user-row">
                    <span>{user.name}</span>
                    <input
                      key={`${user.id}:${user.email ?? ""}`}
                      defaultValue={user.email ?? ""}
                      placeholder={t("notify.userEmailPlaceholder")}
                      onBlur={(event) => commitUserEmail(user.id, event.target.value)}
                    />
                  </div>
                ))}
              </div>
            )}

            {message && (
              <div className={`notify-message ${message.kind}`} role="status">
                {message.text}
              </div>
            )}
          </div>
        ) : (
          <div className="notify-form">
            <p>{message ? message.text : t("common.loading")}</p>
          </div>
        )}

        <footer>
          <button type="button" disabled={busy || !settings} onClick={sendTest}>
            {t("notify.sendTest")}
          </button>
          <button type="button" className="primary" disabled={busy || !settings} onClick={save}>
            {t("common.save")}
          </button>
        </footer>
      </div>
    </div>
  );
}
