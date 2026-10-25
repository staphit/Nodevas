import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { createPortal } from "react-dom";
import { api, AuthError, type RunnerAccessTokenStatus, type RunnerLinkResponse } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { IconClose, IconComputer, IconCopy } from "../icons";
import { useI18n } from "../i18n";
import { formatClock, StepUpDialog } from "../stepup/StepUpDialog";
import { stepUpActive, useStepUp } from "../stepup/stepUpStore";
import { useRunners } from "./runnerStore";

/** How long a device link stays usable; mirrors runnerwire.LinkTTLSeconds. */
export const LINK_TTL_MINUTES = 10;

export function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className="runner-copy"
      aria-label={label}
      title={label}
      onClick={() => {
        void navigator.clipboard
          ?.writeText(value)
          .then(() => setCopied(true))
          .catch(() => setCopied(false));
      }}
    >
      <IconCopy size={13} />
      <span>{copied ? t("runners.copied") : t("runners.copy")}</span>
    </button>
  );
}

function message(failure: unknown): string {
  return failure instanceof Error ? failure.message : String(failure);
}

/**
 * Runs an action that needs a fresh step-up: straight away when this browser
 * is verified (or the hub has no step-up), otherwise after the step-up dialog
 * closes verified. `ask` forces the dialog, for when the hub refused anyway.
 */
function useFreshStepUp() {
  const [open, setOpen] = useState(false);
  const pending = useRef<(() => void) | null>(null);
  const ask = useCallback((action: () => void) => {
    pending.current = action;
    setOpen(true);
  }, []);
  const run = useCallback(
    (action: () => void) => {
      const status = useStepUp.getState().status;
      if (status?.required && !stepUpActive(status)) ask(action);
      else action();
    },
    [ask],
  );
  const close = useCallback(() => {
    setOpen(false);
    const action = pending.current;
    pending.current = null;
    if (action && stepUpActive(useStepUp.getState().status)) action();
  }, []);
  return { run, ask, open, dialog: open ? <StepUpDialog onClose={close} /> : null };
}

/** A 403 that a fresh step-up would cure: true when, asked again, the hub
 * says this browser is not stepped up. */
async function needsStepUp(failure: unknown): Promise<boolean> {
  if (!(failure instanceof AuthError) || failure.status !== 403) return false;
  await useStepUp.getState().load();
  const status = useStepUp.getState().status;
  return Boolean(status?.required) && !stepUpActive(status);
}

/**
 * "Link a new device": name it, verify it's you, and get a one-time link the
 * new machine's `nodevas runner login --link` turns into a runner. The link
 * lives only in this dialog's state and is gone once it closes.
 */
export function LinkDeviceDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<RunnerLinkResponse | null>(null);
  const stepUp = useFreshStepUp();
  const stepUpOpen = stepUp.open;

  useEffect(() => {
    void useStepUp.getState().load();
  }, []);

  useEffect(() => {
    // Captured and stopped, like the step-up dialog: the Runners dialog
    // underneath closes on Escape too. While the step-up dialog is up, the
    // press is its own.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || stepUpOpen) return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose, stepUpOpen]);

  const create = async () => {
    const label = name.trim();
    if (!label || busy) return;
    setBusy(true);
    setError("");
    try {
      setCreated(await api.createRunnerLink(label));
      void useRunners.getState().refreshIfLoaded();
    } catch (failure) {
      if (await needsStepUp(failure)) stepUp.ask(() => void create());
      else setError(message(failure));
    } finally {
      setBusy(false);
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (created) onClose();
    else stepUp.run(() => void create());
  };

  const hub = created?.hub || (typeof window === "undefined" ? "" : window.location.origin);
  const loginCommand = "nodevas runner login --link";
  const installCommand = `scripts/install.sh --hub ${hub} --link`;
  const until = formatClock(created?.expiresAt);

  return createPortal(
    <div className="confirm-backdrop runner-link-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog runner-link-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="runner-link-title"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconComputer size={17} />
          </span>
          <div>
            <h2 id="runner-link-title">{t("runners.link.title")}</h2>
            <p>{t("runners.link.description")}</p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("common.close")}>
            <IconClose size={14} />
          </button>
        </header>

        <form id="runner-link-form" className="runners-body" onSubmit={submit}>
          {!created && (
            <div className="runner-add">
              <label htmlFor="runner-link-name">{t("runners.name")}</label>
              <input
                id="runner-link-name"
                value={name}
                maxLength={64}
                autoFocus
                placeholder={t("runners.namePlaceholder")}
                onChange={(event) => setName(event.target.value)}
              />
              <span className="runner-hint">{t("runners.link.stepUpHint")}</span>
            </div>
          )}

          {created && (
            <section className="runner-token" aria-label={t("runners.link.linkLabel")}>
              <strong>{t("runners.link.readyTitle", { name: created.name })}</strong>
              <p className="runner-token-warning" role="alert">
                {t("runners.link.warning")}
              </p>
              <div className="runner-token-row">
                <input
                  readOnly
                  value={created.link}
                  aria-label={t("runners.link.linkLabel")}
                  data-testid="runner-link-value"
                  onFocus={(event) => event.currentTarget.select()}
                />
                <CopyButton value={created.link} label={t("runners.link.copyLink")} />
              </div>
              <span className="runner-hint" data-testid="runner-link-expiry">
                {until
                  ? t("runners.link.expiresAt", { time: until, minutes: LINK_TTL_MINUTES })
                  : t("runners.link.expiresIn", { minutes: LINK_TTL_MINUTES })}
              </span>
              <ol className="runner-steps">
                <li>
                  {t("runners.link.stepRun")}
                  <div className="runner-token-row">
                    <code>{loginCommand}</code>
                    <CopyButton value={loginCommand} label={t("runners.copyCommand")} />
                  </div>
                  <span className="runner-hint">{t("runners.link.stepInstall")}</span>
                  <div className="runner-token-row">
                    <code>{installCommand}</code>
                    <CopyButton value={installCommand} label={t("runners.copyCommand")} />
                  </div>
                </li>
                <li>{t("runners.link.stepPaste")}</li>
              </ol>
              {!created.access && <span className="runner-hint">{t("runners.link.noAccess")}</span>}
            </section>
          )}

          {error && (
            <p className="notify-message error" role="alert">
              {error}
            </p>
          )}
        </form>

        <footer>
          {!created && (
            <button type="button" onClick={onClose}>
              {t("common.cancel")}
            </button>
          )}
          <button
            type="submit"
            form="runner-link-form"
            className="primary"
            disabled={busy || (!created && !name.trim())}
          >
            {created ? t("runners.link.done") : busy ? t("runners.link.creating") : t("runners.link.create")}
          </button>
        </footer>
        {stepUp.dialog}
      </div>
    </div>,
    document.body,
  );
}

/**
 * The Cloudflare Access service token runners pass the edge with, stored on
 * the hub so device links can carry it. Administrators only: anyone else gets
 * a 403 on the first fetch and this renders nothing. The secret is
 * write-only; the hub never sends it back.
 */
export function RunnerAccessTokenSettings() {
  const { t } = useI18n();
  const [status, setStatus] = useState<RunnerAccessTokenStatus | null>(null);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const stepUp = useFreshStepUp();

  useEffect(() => {
    let live = true;
    api
      .getRunnerAccessToken()
      .then((next) => {
        if (!live) return;
        setStatus(next);
        setClientId(next.clientId);
      })
      .catch(() => {
        // Not an administrator, or a hub without device links: nothing to show.
      });
    return () => {
      live = false;
    };
  }, []);

  if (!status) return null;

  const save = async () => {
    if (busy || !clientId.trim() || !secret.trim()) return;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const next = await api.setRunnerAccessToken(clientId.trim(), secret.trim());
      setStatus(next);
      setSecret("");
      setSaved(true);
    } catch (failure) {
      if (await needsStepUp(failure)) stepUp.ask(() => void save());
      else setError(message(failure));
    } finally {
      setBusy(false);
    }
  };

  const clear = async () => {
    const ok = await confirmAction({
      title: t("runners.link.accessClearTitle"),
      description: t("runners.link.accessClearDescription"),
      confirmLabel: t("runners.link.accessClear"),
      tone: "danger",
    });
    if (!ok) return;
    const run = async () => {
      setBusy(true);
      setError("");
      setSaved(false);
      try {
        await api.clearRunnerAccessToken();
        setStatus({ clientId: "", set: false });
        setClientId("");
      } catch (failure) {
        if (await needsStepUp(failure)) stepUp.ask(() => void run());
        else setError(message(failure));
      } finally {
        setBusy(false);
      }
    };
    stepUp.run(() => void run());
  };

  return (
    <details className="runner-settings" data-testid="runner-access-token">
      <summary>{t("runners.link.accessTitle")}</summary>
      <form
        className="runner-add runner-access-form"
        onSubmit={(event) => {
          event.preventDefault();
          stepUp.run(() => void save());
        }}
      >
        <p className="runner-hint">{t("runners.link.accessHint")}</p>
        <p className="runner-hint" role="status">
          {status.set
            ? t("runners.link.accessSet", { clientId: status.clientId })
            : t("runners.link.accessNotSet")}
        </p>
        <label htmlFor="runner-access-id">{t("runners.link.accessClientId")}</label>
        <input
          id="runner-access-id"
          value={clientId}
          maxLength={256}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setClientId(event.target.value)}
        />
        <label htmlFor="runner-access-secret">{t("runners.link.accessClientSecret")}</label>
        <input
          id="runner-access-secret"
          type="password"
          value={secret}
          maxLength={1024}
          autoComplete="new-password"
          spellCheck={false}
          placeholder={status.set ? t("runners.link.accessSecretKept") : ""}
          onChange={(event) => setSecret(event.target.value)}
        />
        <div className="runner-token-row">
          <button type="submit" className="primary" disabled={busy || !clientId.trim() || !secret.trim()}>
            {t("runners.link.accessSave")}
          </button>
          {status.set && (
            <button type="button" disabled={busy} onClick={() => void clear()}>
              {t("runners.link.accessClear")}
            </button>
          )}
        </div>
        {saved && (
          <p className="runner-hint" role="status">
            {t("runners.link.accessSaved")}
          </p>
        )}
        {error && (
          <p className="notify-message error" role="alert">
            {error}
          </p>
        )}
      </form>
      {stepUp.dialog}
    </details>
  );
}
