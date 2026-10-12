import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { AuthError, api, onUnauthorized, type Actor } from "../api";
import { IconSignOut } from "../icons";
import { LANGUAGE_OPTIONS, useI18n } from "../i18n";

/**
 * ViewerContext carries who this browser is signed in as.
 *
 * AuthGate resolved it already, and every screen that has to know whether to
 * offer an edit needs it, so it is published here rather than threaded through
 * the tree. A null actor is a loopback server, where there is no account and
 * the OS decided who may connect.
 */
const ViewerContext = createContext<Actor | null>(null);

/** Ends the session. A no-op where there is no session to end. */
const SignOutContext = createContext<() => void>(() => {});

/** Who this browser is, or null on a loopback server. */
export function useViewer(): Actor | null {
  return useContext(ViewerContext);
}

/**
 * Whether this browser may change anything.
 *
 * This is a UI question only. The server refuses a visitor's writes on its own
 * — see withVisitorReadOnly — and would keep refusing them if every check that
 * uses this hook were deleted. What the hook is for is not offering an action
 * that is going to be refused.
 */
export function useCanEdit(): boolean {
  return useViewer()?.role !== "visitor";
}

/**
 * AuthGate decides whether the app or a sign-in form is shown.
 *
 * A loopback server has no accounts, so the gate resolves to "local" and gets
 * out of the way — the single-user experience is unchanged. A networked server
 * answers /api/auth/status with mode "accounts", and every API call that comes
 * back 401 puts the form back up without a reload.
 */
export function AuthGate({ children }: { children: React.ReactNode }) {
  const { t } = useI18n();
  const [mode, setMode] = useState<"loading" | "local" | "accounts" | "unknown">(
    "loading",
  );
  const [actor, setActor] = useState<Actor | null>(null);
  const [visitorEnabled, setVisitorEnabled] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    api
      .getAuthStatus()
      .then((status) => {
        if (cancelled) return;
        setMode(status.mode === "accounts" ? "accounts" : "local");
        setActor(status.authenticated ? status.actor : null);
        setVisitorEnabled(status.visitor === true);
      })
      .catch(() => {
        // A failure here says nothing about who is signed in, so it must not be
        // read as "no accounts on this server". The endpoint is public, but a
        // rejected Host header, a proxy in the way or a half-started server all
        // fail exactly the same way — and treating that as a loopback install
        // would put the board on screen for someone who has proved nothing.
        // Nothing they could then do would work, since every API call is still
        // refused, so the honest answer is to say the server cannot be reached.
        if (!cancelled) setMode("unknown");
      });
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  useEffect(() => onUnauthorized(() => setActor(null)), []);

  /**
   * Drop the session, then the actor.
   *
   * The local state is cleared whatever the request did: a failed logout means
   * the cookie may outlive the click, which the server settles at the next
   * request, and leaving the app on screen would read as a button that does
   * nothing.
   */
  const signOut = useCallback(() => {
    void api
      .logout()
      .catch(() => undefined)
      .finally(() => setActor(null));
  }, []);

  if (mode === "loading") {
    return <div className="signin-loading">{t("auth.loading")}</div>;
  }
  if (mode === "unknown") {
    return (
      <div className="signin-loading" role="alert">
        <p>{t("auth.connectionFailed")}</p>
        <button
          type="button"
          onClick={() => {
            setMode("loading");
            setAttempt((count) => count + 1);
          }}
        >
          {t("auth.retry")}
        </button>
      </div>
    );
  }
  if (mode === "accounts" && actor === null) {
    return <SignIn onSignedIn={setActor} visitor={visitorEnabled} />;
  }
  return (
    <ViewerContext.Provider value={actor}>
      <SignOutContext.Provider value={signOut}>
        {children}
      </SignOutContext.Provider>
    </ViewerContext.Provider>
  );
}

/**
 * A standing reminder that nothing on this screen will save.
 *
 * It is not dismissible — a visitor who forgets and types into a node would
 * otherwise find out from a failed request, which reads as a broken server
 * rather than as the rule it is. It used to be a full-width strip above the
 * app, but the fact costs a phone a whole row it cannot spare, so it is now a
 * pill in the top bar: still always there, one tap or hover from the full
 * sentence. The top bar renders it (see MainApp) so it sits with the other
 * session-wide facts; it knows the role itself so a caller cannot forget the
 * condition.
 */
export function VisitorBadge() {
  const { t } = useI18n();
  const viewer = useViewer();
  if (viewer?.role !== "visitor") return null;
  return (
    <span
      className="visitor-badge"
      role="status"
      title={t("auth.visitorTitle")}
    >
      {t("auth.visitorBadge")}
    </span>
  );
}

/**
 * The way out, as one topbar icon beside the other session-wide actions.
 *
 * It renders nothing on a loopback server: there is no session there, and a
 * button that ends nothing is worse than no button. Who is signed in lives in
 * the tooltip rather than in a strip of its own — the name is something you
 * check occasionally, not something worth a row of the window forever.
 */
export function useSignOut(): () => void {
  return useContext(SignOutContext);
}

export function SignOutButton({ className = "icon-btn" }: { className?: string }) {
  const { t } = useI18n();
  const actor = useViewer();
  const signOut = useSignOut();
  if (!actor) return null;
  const label = t("topbar.signOut", { name: actor.name || actor.role });
  return (
    <button type="button" className={className} onClick={signOut} title={label} aria-label={label}>
      <IconSignOut size={16} />
    </button>
  );
}

/** The server gives a passcode five minutes; the countdown mirrors that so the
 * user can see whether it is still worth typing the one in their inbox. */
const OTP_TTL_MS = 5 * 60 * 1000;

/** The server also refuses a resend for this long per account; keep the UI
 * from offering a request that is guaranteed to receive HTTP 429. */
const OTP_RESEND_COOLDOWN_MS = 30 * 1000;

/** The passcode is a fixed-width alphanumeric string. Whitespace is dropped
 * before counting, since a code copied out of a mail often carries some; case
 * and everything else about validity are the server's call. */
const OTP_LENGTH = 8;

function compactCode(value: string): string {
  return value.replace(/\s+/g, "");
}

/** Turns a failure into something worth reading. Throttling and a missing mail
 * transport get their own wording because the server's raw text for those is
 * often an English identifier that means nothing to the person reading it. */
function describe(failure: unknown, t: (key: string) => string): string {
  if (failure instanceof AuthError) {
    // A message with no CJK in it is a machine string, not a sentence for a
    // reader of this UI, so it is replaced rather than shown.
    const readable = /[一-鿿]/.test(failure.message);
    if (failure.status === 429 && !readable) return t("auth.tooManyAttempts");
    if (failure.status === 503 && !readable) {
      return t("auth.mailUnavailable");
    }
    return failure.message;
  }
  return failure instanceof Error ? failure.message : t("auth.signInFailed");
}

function formatRemaining(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

/**
 * Passwordless sign-in: an email address, then a one-time passcode mailed to
 * it. There is no signup, no password and no recovery path — an administrator
 * registers the address, and the mailbox is the credential.
 *
 * The form has two steps, because the passcode only exists once it has been
 * sent: the email step asks for an address, the code step takes the passcode
 * and lets the user go back to fix a mistyped address. The same sentence is
 * shown after a send whether or not the address is registered — the server
 * answers 202 either way, and a message that distinguished the two would turn
 * the form into an account oracle.
 *
 * The shared visitor credential (a visitor PIN plus a fixed passcode that is
 * never mailed) has its own form behind a separate entry, offered only when the
 * server says visitor access is enabled.
 */
export function SignIn({
  onSignedIn,
  visitor = false,
}: {
  onSignedIn: (actor: Actor) => void;
  /** Whether the server has visitor access enabled. */
  visitor?: boolean;
}) {
  const { language, setLanguage, t } = useI18n();
  const [view, setView] = useState<"email" | "code" | "visitor">("email");
  const [email, setEmail] = useState("");
  const [otp, setOtp] = useState("");
  const [visitorPin, setVisitorPin] = useState("");
  const [visitorPasscode, setVisitorPasscode] = useState("");
  const [busy, setBusy] = useState<"" | "send" | "login">("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [expiresAt, setExpiresAt] = useState(0);
  // The server's cooldown is per account, so it is remembered together with
  // the address it applies to: going back to fix a typo must not block the
  // first send to the corrected address.
  const [cooldown, setCooldown] = useState({ email: "", until: 0 });
  const [now, setNow] = useState(() => Date.now());
  const otpField = useRef<HTMLInputElement>(null);

  // A ticking clock only while a passcode is outstanding; there is nothing to
  // count down before one is requested and an idle interval would just wake
  // the tab.
  useEffect(() => {
    if (expiresAt === 0) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [expiresAt]);

  // Focus follows every send, or the keyboard user has to hunt for the field
  // the code they were just sent belongs in.
  useEffect(() => {
    if (view === "code") otpField.current?.focus();
  }, [view, expiresAt]);

  const address = email.trim();
  const resendWait =
    cooldown.email === address ? cooldown.until - Date.now() : 0;
  const resendBlocked = resendWait > 0;

  const sendOtp = async () => {
    if (busy !== "" || address === "" || resendBlocked) return;
    setBusy("send");
    setError("");
    // A fresh passcode invalidates the previous one, so anything already typed
    // is cleared rather than left looking usable.
    setOtp("");
    try {
      await api.requestOtp(address);
      const sentAt = Date.now();
      setExpiresAt(sentAt + OTP_TTL_MS);
      setCooldown({ email: address, until: sentAt + OTP_RESEND_COOLDOWN_MS });
      setNotice(t("auth.codeSent"));
      setView("code");
    } catch (failure) {
      setError(describe(failure, t));
    } finally {
      setBusy("");
    }
  };

  const login = async () => {
    const code = compactCode(otp);
    if (busy !== "" || address === "" || code.length !== OTP_LENGTH) return;
    setBusy("login");
    setError("");
    try {
      const result = await api.login(address, code);
      setOtp("");
      onSignedIn(result.actor);
    } catch (failure) {
      setError(describe(failure, t));
    } finally {
      setBusy("");
    }
  };

  const visitorSignIn = async () => {
    const pin = visitorPin.trim();
    const passcode = visitorPasscode.trim();
    if (busy !== "" || pin === "" || passcode === "") return;
    setBusy("login");
    setError("");
    try {
      const result = await api.visitorLogin(pin, passcode);
      // Nothing needs the shared secrets after this, and holding them in
      // memory only widens what a later bug could leak. They are never written
      // to storage or the URL.
      setVisitorPin("");
      setVisitorPasscode("");
      onSignedIn(result.actor);
    } catch (failure) {
      setError(describe(failure, t));
    } finally {
      setBusy("");
    }
  };

  /** Enter submits whichever step is on screen: on the email step that is the
   * send, so the key never silently does nothing. */
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (view === "email") void sendOtp();
    else if (view === "code") void login();
    else void visitorSignIn();
  };

  const changeEmail = () => {
    setView("email");
    setOtp("");
    setNotice("");
    setError("");
    setExpiresAt(0);
  };

  const openVisitor = () => {
    setView("visitor");
    setError("");
    setNotice("");
  };

  const backToEmail = () => {
    setVisitorPin("");
    setVisitorPasscode("");
    setView(expiresAt !== 0 ? "code" : "email");
    setError("");
  };

  const remaining = expiresAt - now;
  const expired = expiresAt !== 0 && remaining <= 0;

  return (
    <div className="signin">
      <form className="signin-form" onSubmit={submit}>
        <label className="signin-language">
          <span>{t("language.label")}</span>
          <select
            value={language}
            aria-label={t("language.label")}
            onChange={(event) => setLanguage(event.target.value as typeof language)}
          >
            {LANGUAGE_OPTIONS.map((option) => (
              <option key={option} value={option}>
                {t(`language.option.${option}`)}
              </option>
            ))}
          </select>
        </label>
        <h1>Nodevas</h1>

        {view === "visitor" ? (
          <>
            <p className="signin-hint">{t("auth.visitorHint")}</p>
            <label htmlFor="signin-visitor-pin">{t("auth.visitorPin")}</label>
            <input
              id="signin-visitor-pin"
              type="password"
              value={visitorPin}
              autoComplete="off"
              autoFocus
              onChange={(event) => setVisitorPin(event.target.value)}
            />
            <label htmlFor="signin-visitor-passcode">
              {t("auth.visitorPasscode")}
            </label>
            <input
              id="signin-visitor-passcode"
              type="password"
              value={visitorPasscode}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              onChange={(event) => setVisitorPasscode(event.target.value)}
            />
          </>
        ) : (
          <>
            <p className="signin-hint">{t("auth.signInHint")}</p>
            <label htmlFor="signin-email">{t("auth.email")}</label>
            {view === "email" ? (
              <div className="signin-pin-row">
                <input
                  id="signin-email"
                  type="email"
                  value={email}
                  inputMode="email"
                  autoComplete="email"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  autoFocus
                  onChange={(event) => setEmail(event.target.value)}
                />
                <button
                  type="submit"
                  disabled={busy !== "" || address === "" || resendBlocked}
                >
                  {busy === "send" ? t("auth.sendingCode") : t("auth.sendCode")}
                </button>
              </div>
            ) : (
              <div className="signin-pin-row">
                {/* Read-only rather than gone: the user checks the address the
                 * code went to, and leaves it through the button beside it. */}
                <input id="signin-email" type="email" value={email} readOnly />
                <button type="button" onClick={changeEmail} disabled={busy !== ""}>
                  {t("auth.changeEmail")}
                </button>
              </div>
            )}

            {view === "code" && (
              <>
                <label htmlFor="signin-otp">{t("auth.verificationCode")}</label>
                <div className="signin-pin-row">
                  <input
                    id="signin-otp"
                    ref={otpField}
                    className="signin-otp"
                    /* Uppercased for display by text-transform in .signin-otp
                     * rather than here: the server normalises case itself. */
                    value={otp}
                    inputMode="text"
                    autoComplete="one-time-code"
                    autoCapitalize="characters"
                    autoCorrect="off"
                    spellCheck={false}
                    /* Room for a code pasted with spaces in it; the length that
                     * matters is counted with whitespace dropped. */
                    maxLength={OTP_LENGTH * 2}
                    onChange={(event) => setOtp(event.target.value)}
                  />
                  <button
                    type="button"
                    onClick={() => void sendOtp()}
                    disabled={busy !== "" || resendBlocked}
                  >
                    {busy === "send"
                      ? t("auth.sendingCode")
                      : resendBlocked
                        ? t("auth.resendIn", { seconds: Math.ceil(resendWait / 1000) })
                        : t("auth.resendCode")}
                  </button>
                </div>
              </>
            )}
          </>
        )}

        {notice && view === "code" && (
          <p className="signin-notice" role="status">
            {notice}
          </p>
        )}
        {view === "code" && expiresAt !== 0 && (
          <p className="signin-countdown">
            {expired
              ? t("auth.codeExpired")
              : t("auth.codeExpiresIn", { time: formatRemaining(remaining) })}
          </p>
        )}
        {error && (
          <p className="signin-error" role="alert">
            {error}
          </p>
        )}

        {view === "code" && (
          <button
            type="submit"
            disabled={busy !== "" || compactCode(otp).length !== OTP_LENGTH}
          >
            {busy === "login" ? t("auth.signingIn") : t("auth.signIn")}
          </button>
        )}
        {view === "visitor" && (
          <button
            type="submit"
            disabled={
              busy !== "" || visitorPin.trim() === "" || visitorPasscode.trim() === ""
            }
          >
            {busy === "login" ? t("auth.signingIn") : t("auth.signIn")}
          </button>
        )}
        {view !== "visitor" && (
          <p className="signin-warning">{t("auth.signInWarning")}</p>
        )}

        {view === "visitor" ? (
          <button type="button" className="signin-switch" onClick={backToEmail}>
            {t("auth.backToEmail")}
          </button>
        ) : (
          visitor && (
            <button type="button" className="signin-switch" onClick={openVisitor}>
              {t("auth.visitorEntry")}
            </button>
          )
        )}
      </form>
    </div>
  );
}
