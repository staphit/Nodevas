import { useCallback, useEffect, useState } from "react";
import type { NotifySettings } from "../api";
import { remoteScope, useApp, useOperationPending } from "../store";
import { IconBell, IconClose } from "../icons";

const LEAD_PRESETS: { minutes: number; label: string }[] = [
  { minutes: 30, label: "30 分鐘前" },
  { minutes: 60, label: "1 小時前" },
  { minutes: 180, label: "3 小時前" },
  { minutes: 720, label: "12 小時前" },
  { minutes: 1440, label: "1 天前" },
  { minutes: 2880, label: "2 天前" },
  { minutes: 4320, label: "3 天前" },
  { minutes: 10080, label: "7 天前" },
];

/**
 * Workspace-wide deadline email settings. The reminder itself is sent by the
 * Go server once per (project, node, deadline); this dialog only configures
 * lead time, SMTP transport, and recipient emails.
 */
export function NotifySettingsModal({ onClose }: { onClose: () => void }) {
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
        text: error instanceof Error ? error.message : "載入設定失敗",
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
        ? { text: "設定已儲存。", kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  }, [saveNotifySettings, settings]);

  const sendTest = useCallback(async () => {
    if (!settings) return;
    setMessage(null);
    const result = await sendNotifyTest(settings);
    setMessage(
      result.ok
        ? { text: "測試信已送出，請檢查收件匣。", kind: "ok" }
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
        aria-label="截止提醒設定"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconBell size={17} />
          </span>
          <div>
            <h2>截止提醒</h2>
            <p>節點到達「死線」（時間軸規劃的死線日）前，寄 email 提醒負責人。提醒由本機伺服器寄出，每個死線日期只寄一次。</p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label="關閉">
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
              啟用截止前 email 提醒
            </label>

            <div className="notify-field">
              <label htmlFor="notify-lead">提前多久提醒</label>
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
                      {preset.label}
                    </option>
                  ))}
                  <option value="custom">自訂</option>
                </select>
                <input
                  type="number"
                  min={1}
                  max={86400}
                  value={settings.leadMinutes}
                  aria-label="自訂提前分鐘數"
                  onChange={(event) => {
                    const minutes = Number(event.target.value);
                    if (Number.isFinite(minutes) && minutes > 0)
                      patch({ leadMinutes: Math.floor(minutes) });
                  }}
                />
                <span>分鐘</span>
              </div>
            </div>

            <div className="notify-grid">
              <div className="notify-field">
                <label htmlFor="notify-host">SMTP 主機</label>
                <input
                  id="notify-host"
                  value={settings.smtpHost}
                  placeholder="例如 smtp.gmail.com"
                  onChange={(event) => patch({ smtpHost: event.target.value.trim() })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-port">連接埠</label>
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
                <label htmlFor="notify-user">帳號</label>
                <input
                  id="notify-user"
                  value={settings.smtpUser}
                  autoComplete="off"
                  onChange={(event) => patch({ smtpUser: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-pass">密碼／應用程式密碼</label>
                <input
                  id="notify-pass"
                  type="password"
                  value={settings.smtpPass}
                  autoComplete="new-password"
                  placeholder={hasPassword ? "已儲存，留空即不變更" : ""}
                  onChange={(event) => patch({ smtpPass: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-from">寄件人</label>
                <input
                  id="notify-from"
                  value={settings.from}
                  placeholder="you@example.com"
                  onChange={(event) => patch({ from: event.target.value.trim() })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="notify-default-to">預設收件人</label>
                <input
                  id="notify-default-to"
                  value={settings.defaultTo}
                  placeholder="負責人沒填信箱時寄到這裡"
                  onChange={(event) => patch({ defaultTo: event.target.value.trim() })}
                />
              </div>
            </div>

            {users.length > 0 && (
              <div className="notify-users">
                <label>負責人信箱（目前專案）</label>
                {users.map((user) => (
                  <div key={user.id} className="notify-user-row">
                    <span>{user.name}</span>
                    <input
                      key={`${user.id}:${user.email ?? ""}`}
                      defaultValue={user.email ?? ""}
                      placeholder="未填則寄到預設收件人"
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
            <p>{message ? message.text : "載入中…"}</p>
          </div>
        )}

        <footer>
          <button type="button" disabled={busy || !settings} onClick={sendTest}>
            寄測試信
          </button>
          <button type="button" className="primary" disabled={busy || !settings} onClick={save}>
            儲存
          </button>
        </footer>
      </div>
    </div>
  );
}
