import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthGate, SignIn, SignOutButton, VisitorBadge } from "./SignIn";
import { AuthError, api } from "../api";

/**
 * The component is the whole subject here, so the network is stubbed rather
 * than the fetch layer: what matters is which call the form makes with which
 * arguments, not how api.ts serialises it.
 */
vi.mock("../api", async () => {
  const actual = await vi.importActual<typeof import("../api")>("../api");
  return {
    ...actual,
    api: {
      requestOtp: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      getAuthStatus: vi.fn(),
    },
  };
});

const mocked = vi.mocked(api as unknown as {
  requestOtp: (pin: string) => Promise<{ ok: boolean }>;
  login: (pin: string, otp: string) => Promise<{ ok: boolean; actor: unknown }>;
  logout: () => Promise<{ ok: boolean }>;
  getAuthStatus: () => Promise<{
    mode: "local" | "accounts";
    authenticated: boolean;
    actor: unknown;
  }>;
});

/** Types a PIN and asks for a passcode. */
async function requestPasscode(
  user: ReturnType<typeof userEvent.setup>,
  pin = "4821",
) {
  await user.type(screen.getByLabelText("PIN"), pin);
  await user.click(screen.getByRole("button", { name: "寄送驗證碼" }));
}

describe("SignIn", () => {
  beforeEach(() => {
    mocked.requestOtp.mockResolvedValue({ ok: true });
    mocked.login.mockResolvedValue({ ok: true, actor: { name: "阿明" } });
  });

  afterEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    sessionStorage.clear();
  });

  /**
   * The visitor credential has a fixed passcode that is never mailed, so its
   * holder never presses 寄送驗證碼. A form that revealed the passcode field
   * only after a successful send would have nowhere to put their code.
   */
  it("offers both fields before anything is sent", () => {
    render(<SignIn onSignedIn={vi.fn()} />);

    expect(screen.getByLabelText("PIN")).toBeInTheDocument();
    const field = screen.getByLabelText("驗證碼");
    expect(field).toHaveAttribute("autocomplete", "one-time-code");
    expect(field).toHaveAttribute("inputmode", "text");
    expect(mocked.requestOtp).not.toHaveBeenCalled();
  });

  it("requests a passcode for the PIN and focuses the passcode field", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "4821");

    expect(mocked.requestOtp).toHaveBeenCalledWith("4821");
    // Focus follows the send, or the keyboard user has to hunt for the field
    // the code they were just sent belongs in.
    expect(screen.getByLabelText("驗證碼")).toHaveFocus();
  });

  /**
   * The server answers 202 for an unknown PIN precisely so the form cannot be
   * used to enumerate PINs. Any difference in wording here would undo that, so
   * the two renders are compared character for character.
   */
  it("says the same thing for a known and an unknown PIN", async () => {
    const user = userEvent.setup();
    const first = render(<SignIn onSignedIn={vi.fn()} />);
    await requestPasscode(user, "4821");
    const knownWording = screen.getByRole("status").textContent;
    first.unmount();

    render(<SignIn onSignedIn={vi.fn()} />);
    await requestPasscode(user, "0000");
    const unknownWording = screen.getByRole("status").textContent;

    expect(unknownWording).toBe(knownWording);
    expect(knownWording).toBe("若這組 PIN 有效，驗證碼已寄出");
  });

  it("signs in with the trimmed passcode the user typed", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} />);

    await requestPasscode(user, "4821");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));

    // Displayed uppercase for legibility, but the server is case-insensitive
    // and gets exactly what was typed.
    expect(mocked.login).toHaveBeenCalledWith("4821", "a7k2m9p4");
    expect(onSignedIn).toHaveBeenCalledWith({ name: "阿明" });
  });

  /**
   * A visitor types a passcode nobody sent them. Nothing in the form may
   * require a send first, or the shared credential cannot be used at all.
   */
  it("signs in with a passcode that was never requested", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} />);

    await user.type(screen.getByLabelText("PIN"), "777");
    await user.type(screen.getByLabelText("驗證碼"), "LOOKONLY");
    await user.click(screen.getByRole("button", { name: "登入" }));

    expect(mocked.requestOtp).not.toHaveBeenCalled();
    expect(mocked.login).toHaveBeenCalledWith("777", "LOOKONLY");
    expect(onSignedIn).toHaveBeenCalledTimes(1);
  });

  it("shows a rejected passcode as an alert and keeps the PIN", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    mocked.login.mockRejectedValue(new AuthError(401, "驗證碼不正確或已過期"));
    render(<SignIn onSignedIn={onSignedIn} />);

    await requestPasscode(user, "4821");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("驗證碼不正確或已過期");
    // Clearing the PIN would make the user retype it for what is very often a
    // mistyped passcode.
    expect(screen.getByLabelText("PIN")).toHaveValue("4821");
    expect(onSignedIn).not.toHaveBeenCalled();
  });

  // A raw English throttle string is not something this UI's reader can act on.
  it("rewrites an unhelpful 429 into readable advice", async () => {
    const user = userEvent.setup();
    mocked.requestOtp.mockRejectedValue(new AuthError(429, "rate limited"));
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "4821");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "嘗試次數過多，請稍後再試",
    );
  });

  it("asks for another passcode when 寄送驗證碼 is pressed again", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "4821");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "寄送驗證碼" }));

    expect(mocked.requestOtp).toHaveBeenCalledTimes(2);
    expect(mocked.requestOtp).toHaveBeenLastCalledWith("4821");
    // The old code is dead the moment a new one is issued, so leaving it in the
    // field would invite the user to submit something guaranteed to fail.
    expect(screen.getByLabelText("驗證碼")).toHaveValue("");
    // Every existing session for the PIN dies with the old passcode; the user
    // has to be told before pressing it, not after.
    expect(screen.getByText(/登出/)).toBeInTheDocument();
  });

  it("sends from the PIN field and signs in from the passcode field", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} />);

    // Enter with no passcode typed means "send it": submitting the form there
    // would be refused by the submit guard and look like a dead key.
    await user.type(screen.getByLabelText("PIN"), "4821{Enter}");
    expect(mocked.requestOtp).toHaveBeenCalledWith("4821");

    await user.keyboard("a7k2m9p4{Enter}");

    expect(mocked.login).toHaveBeenCalledWith("4821", "a7k2m9p4");
    expect(onSignedIn).toHaveBeenCalledTimes(1);
  });

  /**
   * The PIN is a long-lived administrator-issued secret with no rotation story,
   * so it must not survive the tab. Both stores are checked wholesale rather
   * than by key: a future refactor could persist it under any name.
   */
  it("leaves no trace of the PIN in web storage", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "4821");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));

    expect(mocked.login).toHaveBeenCalled();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(window.location.search).toBe("");
  });
});

describe("AuthGate", () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  /** A loopback server has no accounts, so a sign-out control there would end a
   * session that does not exist and lock nobody out of anything. */
  it("shows no sign-out control on a loopback server", async () => {
    mocked.getAuthStatus.mockResolvedValue({
      mode: "local",
      authenticated: false,
      actor: null,
    });

    render(
      <AuthGate>
        <SignOutButton />
        <p>app</p>
      </AuthGate>,
    );

    expect(await screen.findByText("app")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("signs the session out and puts the form back", async () => {
    const user = userEvent.setup();
    mocked.getAuthStatus.mockResolvedValue({
      mode: "accounts",
      authenticated: true,
      actor: { id: "u1", name: "阿明", role: "member" },
    });
    mocked.logout.mockResolvedValue({ ok: true });

    render(
      <AuthGate>
        <SignOutButton />
        <p>app</p>
      </AuthGate>,
    );

    // The signed-in name lives in the tooltip, not in a strip of its own.
    const button = await screen.findByRole("button", { name: "登出（阿明）" });
    await user.click(button);

    expect(mocked.logout).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("PIN")).toBeInTheDocument();
    expect(screen.queryByText("app")).toBeNull();
  });

  /**
   * A status call that fails says nothing about who is signed in. Reading it as
   * "this server has no accounts" would show the board to someone who has
   * proved nothing — and every request behind it would still be refused.
   */
  it("refuses to show the app when the status call fails", async () => {
    const user = userEvent.setup();
    mocked.getAuthStatus.mockRejectedValueOnce(new Error("network"));
    mocked.getAuthStatus.mockResolvedValueOnce({
      mode: "accounts",
      authenticated: false,
      actor: null,
    });

    render(
      <AuthGate>
        <p>app</p>
      </AuthGate>,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(/無法連線到伺服器/);
    expect(screen.queryByText("app")).toBeNull();

    await user.click(screen.getByRole("button", { name: "重試" }));
    expect(await screen.findByLabelText("PIN")).toBeInTheDocument();
  });

  /** The read-only rule is permanent, worn as a topbar pill rather than a strip. */
  it("gives a visitor the read-only notice and a way out", async () => {
    mocked.getAuthStatus.mockResolvedValue({
      mode: "accounts",
      authenticated: true,
      actor: { id: "visitor", name: "訪客", role: "visitor" },
    });

    render(
      <AuthGate>
        <VisitorBadge />
        <SignOutButton />
      </AuthGate>,
    );

    const badge = await screen.findByText("訪客 · 唯讀");
    // The pill only has to be noticed; the whole rule rides its title.
    expect(badge).toHaveAttribute("title", expect.stringMatching(/無法編輯/));
    expect(badge).toHaveAttribute("title", expect.stringMatching(/儲存可見的文件和附件/));
    expect(badge).toHaveAttribute("title", expect.stringMatching(/整案匯出/));
    expect(screen.getByRole("button", { name: "登出（訪客）" })).toBeInTheDocument();
  });

  it("wears no visitor badge for a signed-in account", async () => {
    mocked.getAuthStatus.mockResolvedValue({
      mode: "accounts",
      authenticated: true,
      actor: { id: "u1", name: "patrick", role: "member" },
    });

    render(
      <AuthGate>
        <VisitorBadge />
        <span>app</span>
      </AuthGate>,
    );

    await screen.findByText("app");
    expect(screen.queryByText("訪客 · 唯讀")).toBeNull();
  });
});
