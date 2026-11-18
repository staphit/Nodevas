import { useState } from "react";
import { api } from "../api";
import {
  requestSystemNotifications,
  systemNotifyPermission,
  type SystemNotifyPermission,
} from "../agents/systemNotify";
import { IconBell, IconClose } from "../icons";
import { useI18n } from "../i18n";
import { useSimpleMode } from "../simple/simpleMode";
import { useRunToasts } from "./runNotify";

/** run-notify toasts, bottom left (the agent network's sit bottom right).
 * Clicking one opens the node's Runs tab on that run. */
export function RunToasts() {
  const { t } = useI18n();
  const toasts = useRunToasts((s) => s.toasts);
  const open = useRunToasts((s) => s.open);
  const dismiss = useRunToasts((s) => s.dismiss);
  if (toasts.length === 0) return null;
  return (
    <div className="run-toasts" role="status" aria-live="polite">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`run-toast is-${toast.kind}`}
          data-run-id={toast.runId}
          data-kind={toast.kind}
        >
          <button
            type="button"
            className="run-toast-body"
            onClick={() => open(toast.id)}
            title={t("runNotify.open")}
          >
            <span className="run-toast-dot" aria-hidden="true" />
            <span className="run-toast-text">
              <strong>{toast.title}</strong>
              {toast.body && <span>{toast.body}</span>}
            </span>
          </button>
          {toast.folder && (
            <button
              type="button"
              className="run-toast-action"
              onClick={() => {
                const folder = toast.folder ?? "";
                dismiss(toast.id);
                // A folder that will not open says nothing more here: the
                // result card still offers it, with the reason.
                void api.openFolder(folder).catch(() => undefined);
              }}
            >
              {t("result.openFolder")}
            </button>
          )}
          <button
            type="button"
            className="run-toast-close"
            onClick={() => dismiss(toast.id)}
            aria-label={t("runNotify.dismiss")}
          >
            <IconClose size={12} />
          </button>
        </div>
      ))}
    </div>
  );
}

/**
 * Whether run notifications may also reach the system tray. Permission is only
 * ever asked from this button: browsers penalise unprompted asks. Simple
 * (single-user edition) says what it tells you about in plain words: a Simple
 * run has no CI or merge to report.
 */
export function RunNotifySettings() {
  const { t } = useI18n();
  const simple = useSimpleMode();
  const [permission, setPermission] = useState<SystemNotifyPermission>(() =>
    systemNotifyPermission(),
  );
  if (permission === "unsupported") return null;
  return (
    <div className="run-notify-settings">
      <IconBell size={14} />
      <p>{t(simple ? `simple.notify.${permission}` : `runNotify.permission.${permission}`)}</p>
      {permission === "default" && (
        <button
          type="button"
          className="run-link-button"
          onClick={() => void requestSystemNotifications().then(setPermission)}
        >
          {t("runNotify.enable")}
        </button>
      )}
    </div>
  );
}
