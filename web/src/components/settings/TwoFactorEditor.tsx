import { useCallback, useEffect, useState, type FormEvent } from "react";
import { AuthError, api, type TotpEnrollment, type TotpState } from "../../api";
import { IconCopy } from "../../icons";
import { useI18n } from "../../i18n";
import { StepUpDialog } from "../../stepup/StepUpDialog";
import { compactSecondFactor, isSecondFactorCode } from "../secondFactor";
import type { SettingsNotify } from "./notify";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** A refused code in words; anything else keeps the hub's sentence. */
function codeError(t: Translate, failure: unknown): string {
  if (failure instanceof AuthError) {
    if (failure.status === 401) return t("auth.totp.wrongCode");
    if (failure.status === 429) return t("auth.totp.throttled");
    if (failure.status === 0) return t("auth.totp.offline");
    return failure.message;
  }
  return failure instanceof Error ? failure.message : String(failure);
}

/** The setup key in groups of four, as authenticator apps print it. */
export function groupKey(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, "$1 ");
}

function formatDay(iso: string | undefined): string {
  if (!iso) return "";
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleDateString();
}

type View =
  | { kind: "idle" }
  | { kind: "enroll"; enrollment: TotpEnrollment }
  | { kind: "codes"; codes: string[] }
  | { kind: "change"; action: "disable" | "regenerate" };

/**
 * Two-factor authentication for the signed-in account: an authenticator app
 * beside the mailed passcode. Enrolling takes a fresh step-up, a scan (or the
 * key typed by hand) and one code from the app; the recovery codes are then
 * shown once. Turning it off takes a current code.
 */
export function TwoFactorEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const [state, setState] = useState<TotpState | null>(null);
  const [view, setView] = useState<View>({ kind: "idle" });
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [needsStepUp, setNeedsStepUp] = useState(false);
  const [stepUpOpen, setStepUpOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    try {
      setState(await api.getTotp());
    } catch (failure) {
      notify.onError(failure instanceof Error ? failure.message : String(failure));
    }
  }, [notify]);

  useEffect(() => {
    void load();
  }, [load]);

  const reset = (next: View) => {
    setView(next);
    setCode("");
    setError("");
  };

  const begin = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    setNeedsStepUp(false);
    try {
      reset({ kind: "enroll", enrollment: await api.beginTotp() });
    } catch (failure) {
      if (failure instanceof AuthError && failure.code === "STEP_UP_REQUIRED") setNeedsStepUp(true);
      else setError(codeError(t, failure));
    } finally {
      setBusy(false);
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    const typed = compactSecondFactor(code);
    setBusy(true);
    setError("");
    try {
      if (view.kind === "enroll") {
        const result = await api.confirmTotp(typed);
        reset({ kind: "codes", codes: result.recoveryCodes });
        notify.onNotice(t("auth.totp.turnedOn"));
      } else if (view.kind === "change" && view.action === "disable") {
        await api.disableTotp(typed);
        reset({ kind: "idle" });
        notify.onNotice(t("auth.totp.turnedOff"));
      } else if (view.kind === "change") {
        const result = await api.regenerateRecoveryCodes(typed);
        reset({ kind: "codes", codes: result.recoveryCodes });
      }
      await load();
    } catch (failure) {
      setError(codeError(t, failure));
    } finally {
      setBusy(false);
    }
  };

  const copyCodes = async (codes: string[]) => {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  // Enrollment confirms with the app's six digits only; turning it off also
  // takes a recovery code, and a new set takes the app's code.
  const codeReady =
    view.kind === "enroll" || (view.kind === "change" && view.action === "regenerate")
      ? /^\d{6}$/.test(compactSecondFactor(code))
      : isSecondFactorCode(code);

  const codeField = (label: string) => (
    <label className="twofactor-field">
      <span>{label}</span>
      <input
        value={code}
        inputMode="numeric"
        autoComplete="one-time-code"
        autoCapitalize="characters"
        autoCorrect="off"
        spellCheck={false}
        maxLength={24}
        aria-invalid={error ? true : undefined}
        onChange={(event) => setCode(event.target.value)}
      />
    </label>
  );

  return (
    <section className="settings-section twofactor" aria-labelledby="twofactor-title">
      <h3 id="twofactor-title">{t("auth.totp.title")}</h3>
      <p className="settings-hint">{t("auth.totp.intro")}</p>

      {state === null && <p className="settings-hint">{t("auth.totp.loading")}</p>}

      {state !== null && view.kind !== "codes" && (
        <p className="twofactor-status" data-testid="twofactor-status">
          {state.enabled
            ? t("auth.totp.statusOn", { time: formatDay(state.enabledAt) })
            : t("auth.totp.statusOff")}
          {state.enabled && (
            <small> · {t("auth.totp.recoveryLeft", { count: state.recoveryCodesLeft })}</small>
          )}
        </p>
      )}

      {state !== null && !state.enabled && view.kind === "idle" && (
        <div className="twofactor-actions">
          <button type="button" onClick={() => void begin()} disabled={busy}>
            {busy ? t("auth.totp.starting") : t("auth.totp.setUp")}
          </button>
          {needsStepUp && (
            <>
              <span className="settings-hint" role="status">
                {t("auth.totp.needsStepUp")}
              </span>
              <button type="button" onClick={() => setStepUpOpen(true)}>
                {t("auth.totp.verifyFirst")}
              </button>
            </>
          )}
        </div>
      )}

      {view.kind === "enroll" && (
        <form className="twofactor-enroll" onSubmit={(event) => void submit(event)}>
          <p className="settings-hint">{t("auth.totp.scanHint")}</p>
          <img className="twofactor-qr" src={view.enrollment.qr} alt={t("auth.totp.qrAlt")} />
          <div className="twofactor-key">
            <span>{t("auth.totp.manualKey")}</span>
            <code data-testid="twofactor-key">{groupKey(view.enrollment.secret)}</code>
            <small>{t("auth.totp.accountLabel", { account: view.enrollment.account })}</small>
          </div>
          {codeField(t("auth.totp.codeLabel"))}
          <div className="twofactor-actions">
            <button type="submit" className="primary" disabled={busy || !codeReady}>
              {busy ? t("auth.totp.confirming") : t("auth.totp.confirm")}
            </button>
            <button type="button" onClick={() => reset({ kind: "idle" })} disabled={busy}>
              {t("auth.totp.cancel")}
            </button>
          </div>
        </form>
      )}

      {view.kind === "codes" && (
        <div className="twofactor-codes">
          <b>{t("auth.totp.recoveryTitle")}</b>
          <p className="settings-hint">{t("auth.totp.recoveryHint")}</p>
          <ol data-testid="twofactor-recovery">
            {view.codes.map((entry) => (
              <li key={entry}>
                <code>{entry}</code>
              </li>
            ))}
          </ol>
          <div className="twofactor-actions">
            <button type="button" onClick={() => void copyCodes(view.codes)}>
              <IconCopy size={13} /> {copied ? t("auth.totp.copied") : t("auth.totp.copy")}
            </button>
            <button
              type="button"
              className="primary"
              onClick={() => {
                setCopied(false);
                reset({ kind: "idle" });
              }}
            >
              {t("auth.totp.saved")}
            </button>
          </div>
        </div>
      )}

      {state?.enabled && view.kind === "idle" && (
        <div className="twofactor-actions">
          <button type="button" onClick={() => reset({ kind: "change", action: "regenerate" })}>
            {t("auth.totp.regenerate")}
          </button>
          <button type="button" onClick={() => reset({ kind: "change", action: "disable" })}>
            {t("auth.totp.disable")}
          </button>
        </div>
      )}

      {view.kind === "change" && (
        <form className="twofactor-enroll" onSubmit={(event) => void submit(event)}>
          <p className="settings-hint">
            {view.action === "disable" ? t("auth.totp.disableHint") : t("auth.totp.regenerateHint")}
          </p>
          {codeField(t("auth.totp.changeCodeLabel"))}
          <div className="twofactor-actions">
            <button type="submit" className="primary" disabled={busy || !codeReady}>
              {view.action === "disable"
                ? busy
                  ? t("auth.totp.disabling")
                  : t("auth.totp.disable")
                : busy
                  ? t("auth.totp.regenerating")
                  : t("auth.totp.regenerate")}
            </button>
            <button type="button" onClick={() => reset({ kind: "idle" })} disabled={busy}>
              {t("auth.totp.cancel")}
            </button>
          </div>
        </form>
      )}

      {error && (
        <p className="twofactor-error" role="alert">
          {error}
        </p>
      )}

      {stepUpOpen && (
        <StepUpDialog
          onClose={() => {
            setStepUpOpen(false);
            setNeedsStepUp(false);
          }}
        />
      )}
    </section>
  );
}
