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
  const [mode, setMode] = useState<"loading" | "local" | "accounts" | "unknown">(
    "loading",
  );
  const [actor, setActor] = useState<Actor | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    api
      .getAuthStatus()
      .then((status) => {
        if (cancelled) return;
        setMode(status.mode === "accounts" ? "accounts" : "local");
        setActor(status.authenticated ? status.actor : null);
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
    return <div className="signin-loading">載入中…</div>;
  }
  if (mode === "unknown") {
    return (
      <div className="signin-loading" role="alert">
        <p>無法連線到伺服器，因此無法確認登入狀態。</p>
        <button
          type="button"
          onClick={() => {
            setMode("loading");
            setAttempt((count) => count + 1);
          }}
        >
          重試
        </button>
      </div>
    );
  }
  if (mode === "accounts" && actor === null) {
    return <SignIn onSignedIn={setActor} />;
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
  const viewer = useViewer();
  if (viewer?.role !== "visitor") return null;
  return (
    <span
      className="visitor-badge"
      role="status"
      title="訪客模式：唯讀。可以瀏覽、複製與儲存可見的文件和附件，但無法編輯、整案匯出或使用管理／主機功能。"
    >
      訪客 · 唯讀
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
  const actor = useViewer();
  const signOut = useSignOut();
  if (!actor) return null;
  const label = `登出（${actor.name || actor.role}）`;
  return (
    <button type="button" className={className} onClick={signOut} title={label} aria-label={label}>
      <IconSignOut size={16} />
    </button>
  );
}

/** The server gives a passcode five minutes; the countdown mirrors that so the
 * user can see whether it is still worth typing the one in their inbox. */
const OTP_TTL_MS = 5 * 60 * 1000;

/** The passcode is a fixed-width alphanumeric string. This is the only shape
 * check done here: everything else about validity is the server's call. */
const OTP_LENGTH = 8;

/**
 * The same sentence is shown whether or not the PIN exists. The server answers
 * 202 either way for exactly this reason — a message that distinguished the two
 * would turn the form into a PIN oracle. It is also what a visitor sees, whose
 * passcode is fixed and was never mailed; saying anything else here would tell
 * an unauthenticated caller which kind of PIN they just typed.
 */
const OTP_SENT_NOTICE = "若這組 PIN 有效，驗證碼已寄出";

/** Turns a failure into something worth reading. Throttling and a missing mail
 * transport get their own wording because the server's raw text for those is
 * often an English identifier that means nothing to the person reading it. */
function describe(failure: unknown): string {
  if (failure instanceof AuthError) {
    // A message with no CJK in it is a machine string, not a sentence for a
    // reader of this UI, so it is replaced rather than shown.
    const readable = /[一-鿿]/.test(failure.message);
    if (failure.status === 429 && !readable) return "嘗試次數過多，請稍後再試";
    if (failure.status === 503 && !readable) {
      return "此伺服器尚未設定郵件服務，請聯絡管理員";
    }
    return failure.message;
  }
  return failure instanceof Error ? failure.message : "登入失敗";
}

function formatRemaining(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

/**
 * Two-factor sign-in on one screen: a PIN the administrator handed over out of
 * band, and a one-time passcode mailed to the address bound to it. There is no
 * signup, no password and no recovery path — an administrator issues both
 * halves.
 *
 * Both fields are present from the start rather than in sequence. The visitor
 * credential is the reason: its passcode is fixed and never mailed, so a form
 * that only revealed the passcode field after a successful send would have no
 * way to show it to the one person who does not need a send. Everyone else
 * loses nothing — the passcode field simply sits empty until the mail lands.
 */
export function SignIn({ onSignedIn }: { onSignedIn: (actor: Actor) => void }) {
  const [pin, setPin] = useState("");
  const [otp, setOtp] = useState("");
  const [busy, setBusy] = useState<"" | "send" | "login">("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [expiresAt, setExpiresAt] = useState(0);
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

  const sendOtp = useCallback(async () => {
    if (busy !== "" || pin.trim() === "") return;
    setBusy("send");
    setError("");
    // A fresh passcode invalidates the previous one and every session opened
    // with it, so anything already typed is cleared rather than left looking
    // usable.
    setOtp("");
    try {
      await api.requestOtp(pin.trim());
      setExpiresAt(Date.now() + OTP_TTL_MS);
      setNotice(OTP_SENT_NOTICE);
      otpField.current?.focus();
    } catch (failure) {
      setError(describe(failure));
    } finally {
      setBusy("");
    }
  }, [busy, pin]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const code = otp.trim();
    if (busy !== "" || pin.trim() === "" || code.length !== OTP_LENGTH) return;
    setBusy("login");
    setError("");
    try {
      const result = await api.login(pin.trim(), code);
      // Nothing needs the PIN after this, and holding it in memory only widens
      // what a later bug could leak. It is never written to storage or the URL.
      setPin("");
      setOtp("");
      onSignedIn(result.actor);
    } catch (failure) {
      setError(describe(failure));
    } finally {
      setBusy("");
    }
  };

  const remaining = expiresAt - now;
  const expired = expiresAt !== 0 && remaining <= 0;

  return (
    <div className="signin">
      <form className="signin-form" onSubmit={submit}>
        <h1>Nodevas</h1>
        <p className="signin-hint">
          請輸入管理員提供的 PIN，按「寄送驗證碼」後填入信箱收到的驗證碼。
        </p>

        <label htmlFor="signin-pin">PIN</label>
        <div className="signin-pin-row">
          <input
            id="signin-pin"
            type="password"
            value={pin}
            autoComplete="off"
            autoFocus
            onChange={(event) => setPin(event.target.value)}
            /* Enter in the PIN field means "send it" while there is no
             * passcode to submit. Letting it submit the form instead would do
             * nothing at all — the submit guard requires a full passcode — and
             * a key that silently does nothing reads as a broken form. */
            onKeyDown={(event) => {
              if (event.key !== "Enter" || otp.trim() !== "") return;
              event.preventDefault();
              void sendOtp();
            }}
          />
          <button
            type="button"
            onClick={sendOtp}
            disabled={busy !== "" || pin.trim() === ""}
          >
            {busy === "send" ? "寄送中…" : "寄送驗證碼"}
          </button>
        </div>

        <label htmlFor="signin-otp">驗證碼</label>
        <input
          id="signin-otp"
          ref={otpField}
          className="signin-otp"
          /* Uppercased for display by text-transform in .signin-otp rather
           * than here: rewriting the value would also rewrite the state, and
           * the server is to receive what was typed, only trimmed. */
          value={otp}
          inputMode="text"
          autoComplete="one-time-code"
          autoCapitalize="characters"
          autoCorrect="off"
          spellCheck={false}
          maxLength={OTP_LENGTH}
          onChange={(event) => setOtp(event.target.value)}
        />

        {notice && (
          <p className="signin-notice" role="status">
            {notice}
          </p>
        )}
        {expiresAt !== 0 && (
          <p className="signin-countdown">
            {expired
              ? "驗證碼已失效，請重新寄送"
              : `驗證碼將在 ${formatRemaining(remaining)} 後失效`}
          </p>
        )}
        {error && (
          <p className="signin-error" role="alert">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={
            busy !== "" || pin.trim() === "" || otp.trim().length !== OTP_LENGTH
          }
        >
          {busy === "login" ? "登入中…" : "登入"}
        </button>
        <p className="signin-warning">
          重新寄送會產生新的驗證碼，並且會將這組 PIN 目前所有的登入工作階段登出。
        </p>
      </form>
    </div>
  );
}
