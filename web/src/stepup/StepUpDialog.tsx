import { useEffect, useRef, useState, type FormEvent } from "react";
import { createPortal } from "react-dom";
import { AuthError } from "../api";
import { IconClose, IconShield } from "../icons";
import { languageLocale, useI18n } from "../i18n";
import { stepUpActive, useStepUp } from "./stepUpStore";

/** The step-up passcode's length; whitespace in a pasted code is dropped. */
export const STEP_UP_CODE_LENGTH = 8;

/** The server refuses another mail for this long; the button waits it out. */
export const STEP_UP_RESEND_COOLDOWN_MS = 30 * 1000;

export function compactStepUpCode(value: string): string {
  // Upper-cased like the sign-in passcode; the hub ignores case anyway.
  return value.replace(/[\s-]+/g, "").toUpperCase();
}

/** "14:05" in the reader's language, or "" for an unparseable time. */
export function formatClock(iso: string | undefined): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(languageLocale(), {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date);
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** A refusal in words: a wrong code, throttling and a missing mail transport
 * each get their own sentence rather than the server's identifier. */
export function stepUpErrorText(t: Translate, failure: unknown, stage: "request" | "verify"): string {
  if (failure instanceof AuthError) {
    if (failure.status === 429) return t("stepUp.throttled");
    if (failure.status === 503) return t("stepUp.mailUnavailable");
    if (stage === "verify" && (failure.status === 400 || failure.status === 401)) {
      return t("stepUp.wrongCode");
    }
    if (failure.status === 0) return t("stepUp.offline");
    return failure.message;
  }
  return failure instanceof Error ? failure.message : String(failure);
}

/**
 * "Verify it's you": request a passcode by email, type it, and the session is
 * stepped up until the time shown. A runner that requires it auto-accepts
 * only dispatches from a stepped-up session.
 */
export function StepUpDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const status = useStepUp((s) => s.status);
  const requestCode = useStepUp((s) => s.requestCode);
  const verify = useStepUp((s) => s.verify);
  const [view, setView] = useState<"request" | "code" | "done">(() =>
    stepUpActive(useStepUp.getState().status) ? "done" : "request",
  );
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState<"" | "send" | "verify">("");
  const [error, setError] = useState("");
  const [cooldownUntil, setCooldownUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const codeField = useRef<HTMLInputElement>(null);

  useEffect(() => {
    void useStepUp.getState().load();
  }, []);

  // The fetch on open (or the window running out) can change the answer
  // under the first step; the code step is left alone mid-typing.
  const activeNow = stepUpActive(status);
  useEffect(() => {
    if (view === "request" && activeNow) setView("done");
    if (view === "done" && !activeNow && status !== undefined) setView("request");
  }, [activeNow, status, view]);

  // Ticks only while the resend button is waiting.
  useEffect(() => {
    if (cooldownUntil === 0) return;
    setNow(Date.now());
    const timer = window.setInterval(() => {
      const current = Date.now();
      setNow(current);
      if (current >= cooldownUntil) window.clearInterval(timer);
    }, 1000);
    return () => window.clearInterval(timer);
  }, [cooldownUntil]);

  useEffect(() => {
    if (view === "code") codeField.current?.focus();
  }, [view, cooldownUntil]);

  useEffect(() => {
    // Captured and stopped: the dialog underneath (dispatch, runners) closes
    // on Escape too, and one press should only close this one.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  const resendWait = cooldownUntil - now;
  const resendBlocked = resendWait > 0;
  const compact = compactStepUpCode(code);

  const send = async () => {
    if (busy !== "" || resendBlocked) return;
    setBusy("send");
    setError("");
    // A fresh passcode replaces the last one; what was typed is now useless.
    setCode("");
    try {
      await requestCode();
      setCooldownUntil(Date.now() + STEP_UP_RESEND_COOLDOWN_MS);
      setView("code");
    } catch (failure) {
      setError(stepUpErrorText(t, failure, "request"));
    } finally {
      setBusy("");
    }
  };

  const submitCode = async () => {
    if (busy !== "" || compact.length !== STEP_UP_CODE_LENGTH) return;
    setBusy("verify");
    setError("");
    try {
      await verify(compact);
      setCode("");
      setView("done");
    } catch (failure) {
      setError(stepUpErrorText(t, failure, "verify"));
      codeField.current?.select();
    } finally {
      setBusy("");
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (view === "request") void send();
    else if (view === "code") void submitCode();
    else onClose();
  };

  const until = formatClock(status?.until);
  const done = view === "done";

  return createPortal(
    <div className="confirm-backdrop stepup-backdrop" onClick={onClose}>
      <div
        className="confirm-dialog stepup-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="stepup-title"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconShield size={17} />
          </span>
          <div>
            <h2 id="stepup-title">{t("stepUp.title")}</h2>
            <p>{t("stepUp.description")}</p>
          </div>
          <button
            type="button"
            className="confirm-dialog-close"
            onClick={onClose}
            aria-label={t("common.close")}
          >
            <IconClose size={14} />
          </button>
        </header>

        <form id="stepup-form" className="stepup-body" onSubmit={submit}>
          {view === "request" && <p className="stepup-text">{t("stepUp.requestHint")}</p>}

          {view === "code" && (
            <>
              <p className="stepup-text" role="status">
                {t("stepUp.codeSent")}
              </p>
              <label htmlFor="stepup-code">{t("stepUp.codeLabel")}</label>
              <div className="stepup-row">
                <input
                  id="stepup-code"
                  ref={codeField}
                  className="stepup-code"
                  value={code}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  /* Room for a code pasted with spaces; the count that matters
                   * drops them. */
                  maxLength={STEP_UP_CODE_LENGTH * 2}
                  aria-invalid={error ? true : undefined}
                  aria-describedby={error ? "stepup-error" : undefined}
                  onChange={(event) => setCode(event.target.value)}
                />
                <button type="button" onClick={() => void send()} disabled={busy !== "" || resendBlocked}>
                  {busy === "send"
                    ? t("stepUp.sending")
                    : resendBlocked
                      ? t("stepUp.resendIn", { seconds: Math.ceil(resendWait / 1000) })
                      : t("stepUp.resend")}
                </button>
              </div>
            </>
          )}

          {done && (
            <p className="stepup-done" role="status" data-testid="stepup-done">
              {until ? t("stepUp.verifiedUntil", { time: until }) : t("stepUp.verified")}
            </p>
          )}

          {error && (
            <p id="stepup-error" className="stepup-error" role="alert">
              {error}
            </p>
          )}
        </form>

        <footer>
          {!done && (
            <button type="button" onClick={onClose}>
              {t("common.cancel")}
            </button>
          )}
          <button
            type="submit"
            form="stepup-form"
            className="primary"
            disabled={
              busy !== "" ||
              (view === "request" && resendBlocked) ||
              (view === "code" && compact.length !== STEP_UP_CODE_LENGTH)
            }
          >
            {view === "request"
              ? busy === "send"
                ? t("stepUp.sending")
                : t("stepUp.sendCode")
              : view === "code"
                ? busy === "verify"
                  ? t("stepUp.verifying")
                  : t("stepUp.verify")
                : t("stepUp.done")}
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
