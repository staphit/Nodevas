import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthGate, SignIn, SignOutButton, VisitorBadge } from "./SignIn";
import { AuthError, api } from "../api";
import { useApp } from "../store";

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
      visitorLogin: vi.fn(),
      logout: vi.fn(),
      getAuthStatus: vi.fn(),
    },
  };
});

const mocked = vi.mocked(api as unknown as {
  requestOtp: (email: string) => Promise<{ ok: boolean }>;
  login: (email: string, otp: string) => Promise<{ ok: boolean; actor: unknown }>;
  visitorLogin: (
    pin: string,
    passcode: string,
  ) => Promise<{ ok: boolean; actor: unknown }>;
  logout: () => Promise<{ ok: boolean }>;
  getAuthStatus: () => Promise<{
    mode: "local" | "accounts";
    authenticated: boolean;
    actor: unknown;
    visitor?: boolean;
  }>;
});

/** Types an address and asks for a passcode. */
async function requestPasscode(
  user: ReturnType<typeof userEvent.setup>,
  email = "ming@example.com",
) {
  await user.type(screen.getByLabelText("Email"), email);
  await user.click(screen.getByRole("button", { name: "寄送驗證碼" }));
}

/** Opens the visitor form and submits a credential with Enter. */
async function visitorSubmit(
  user: ReturnType<typeof userEvent.setup>,
  pin: string,
  passcode: string,
) {
  await user.click(screen.getByRole("button", { name: "訪客登入" }));
  await user.type(screen.getByLabelText("訪客 PIN"), pin);
  await user.type(screen.getByLabelText("通行碼"), `${passcode}{Enter}`);
}

describe("SignIn", () => {
  beforeEach(() => {
    useApp.setState({
      preferences: { ...useApp.getState().preferences, language: "zh-TW" },
    });
    mocked.requestOtp.mockResolvedValue({ ok: true });
    mocked.login.mockResolvedValue({ ok: true, actor: { name: "阿明" } });
    mocked.visitorLogin.mockResolvedValue({
      ok: true,
      actor: { id: "visitor", name: "訪客", role: "visitor" },
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
    localStorage.clear();
    sessionStorage.clear();
  });

  it("starts with an email field and no PIN", () => {
    render(<SignIn onSignedIn={vi.fn()} />);

    const field = screen.getByLabelText("Email");
    expect(field).toHaveAttribute("type", "email");
    expect(field).toHaveAttribute("autocomplete", "email");
    expect(field).toHaveAttribute("inputmode", "email");
    expect(field).toHaveFocus();
    expect(screen.queryByText(/PIN/)).toBeNull();
    // The passcode only exists after a send, so its field waits for one.
    expect(screen.queryByLabelText("驗證碼")).toBeNull();
    expect(screen.getByRole("button", { name: "寄送驗證碼" })).toBeDisabled();
  });

  it("switches the sign-in screen language and persists the choice", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await user.selectOptions(screen.getByRole("combobox", { name: "語系" }), "en");

    expect(screen.getByText(/Enter your email/)).toBeInTheDocument();
    expect(useApp.getState().preferences.language).toBe("en");

    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-TW");
    expect(screen.getByText(/請輸入你的 email/)).toBeInTheDocument();
  });

  it("requests a passcode for the trimmed email and focuses the passcode field", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "  ming@example.com ");

    expect(mocked.requestOtp).toHaveBeenCalledWith("ming@example.com");
    const field = screen.getByLabelText("驗證碼");
    expect(field).toHaveAttribute("autocomplete", "one-time-code");
    // Focus follows the send, or the keyboard user has to hunt for the field
    // the code they were just sent belongs in.
    expect(field).toHaveFocus();
  });

  /**
   * The server answers 202 for an unregistered address precisely so the form
   * cannot be used to enumerate accounts. Any difference in wording here would
   * undo that, so the two renders are compared character for character.
   */
  it("says the same thing for a registered and an unknown email", async () => {
    const user = userEvent.setup();
    const first = render(<SignIn onSignedIn={vi.fn()} />);
    await requestPasscode(user, "ming@example.com");
    const knownWording = screen.getByRole("status").textContent;
    first.unmount();

    render(<SignIn onSignedIn={vi.fn()} />);
    await requestPasscode(user, "nobody@example.com");
    const unknownWording = screen.getByRole("status").textContent;

    expect(unknownWording).toBe(knownWording);
    expect(knownWording).toBe("若此 email 已註冊，驗證碼已寄出");
  });

  it("signs in with the email and the passcode, whitespace dropped", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} />);

    await requestPasscode(user, "ming@example.com");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2 m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));

    // Displayed uppercase for legibility, but the server normalises case and
    // gets what was typed, minus the space a pasted code often carries.
    expect(mocked.login).toHaveBeenCalledWith("ming@example.com", "a7k2m9p4");
    expect(onSignedIn).toHaveBeenCalledWith({ name: "阿明" });
  });

  it("sends with Enter in the email field and signs in with Enter in the passcode field", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} />);

    await user.type(screen.getByLabelText("Email"), "ming@example.com{Enter}");
    expect(mocked.requestOtp).toHaveBeenCalledWith("ming@example.com");

    await user.keyboard("a7k2m9p4{Enter}");

    expect(mocked.login).toHaveBeenCalledWith("ming@example.com", "a7k2m9p4");
    expect(onSignedIn).toHaveBeenCalledTimes(1);
  });

  it("goes back to change the email", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "mign@example.com");
    expect(screen.getByLabelText("Email")).toHaveAttribute("readonly");

    await user.click(screen.getByRole("button", { name: "更換 email" }));
    const field = screen.getByLabelText("Email");
    expect(field).not.toHaveAttribute("readonly");
    expect(screen.queryByLabelText("驗證碼")).toBeNull();

    await user.clear(field);
    // A corrected address is a different account, so the first address's
    // cooldown must not block it.
    await requestPasscode(user, "ming@example.com");
    expect(mocked.requestOtp).toHaveBeenCalledTimes(2);
    expect(mocked.requestOtp).toHaveBeenLastCalledWith("ming@example.com");
  });

  it("shows a rejected passcode as an alert and keeps the email", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    mocked.login.mockRejectedValue(new AuthError(401, "驗證碼不正確或已過期"));
    render(<SignIn onSignedIn={onSignedIn} />);

    await requestPasscode(user, "ming@example.com");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("驗證碼不正確或已過期");
    expect(screen.getByLabelText("Email")).toHaveValue("ming@example.com");
    expect(onSignedIn).not.toHaveBeenCalled();
  });

  // A raw English throttle string is not something this UI's reader can act on.
  it("rewrites an unhelpful 429 into readable advice", async () => {
    const user = userEvent.setup();
    mocked.requestOtp.mockRejectedValue(new AuthError(429, "rate limited"));
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "ming@example.com");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "嘗試次數過多，請稍後再試",
    );
    // Nothing was sent, so there is no passcode to type.
    expect(screen.queryByLabelText("驗證碼")).toBeNull();
  });

  it("explains a server with no mail transport", async () => {
    const user = userEvent.setup();
    mocked.requestOtp.mockRejectedValue(new AuthError(503, "mailer not configured"));
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "ming@example.com");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "此伺服器尚未設定郵件服務",
    );
  });

  it("holds a resend for the cooldown, then asks for another passcode", async () => {
    let now = 1000000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} />);

    await requestPasscode(user, "ming@example.com");
    expect(screen.getByRole("button", { name: /秒後可重新寄送/ })).toBeDisabled();

    now += 35000;
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "重新寄送" }));

    expect(mocked.requestOtp).toHaveBeenCalledTimes(2);
    expect(mocked.requestOtp).toHaveBeenLastCalledWith("ming@example.com");
    // The old code is dead the moment a new one is issued, so leaving it in the
    // field would invite the user to submit something guaranteed to fail.
    expect(screen.getByLabelText("驗證碼")).toHaveValue("");
  });

  // The server revokes the account's other sessions on a successful sign-in;
  // the user has to be told before pressing the button, not after.
  it("warns that signing in signs out other devices", () => {
    render(<SignIn onSignedIn={vi.fn()} />);
    expect(screen.getByText(/其他裝置.*登出/)).toBeInTheDocument();
  });

  it("offers no visitor entry when visitor access is off", () => {
    render(<SignIn onSignedIn={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "訪客登入" })).toBeNull();
  });

  /**
   * The visitor credential is a shared PIN and a fixed passcode that is never
   * mailed, so it has its own form and never touches the email flow.
   */
  it("signs a visitor in with the visitor PIN and passcode", async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    render(<SignIn onSignedIn={onSignedIn} visitor />);

    await user.click(screen.getByRole("button", { name: "訪客登入" }));
    expect(screen.getByLabelText("訪客 PIN")).toHaveFocus();
    await user.type(screen.getByLabelText("訪客 PIN"), "777");
    await user.type(screen.getByLabelText("通行碼"), "LOOKONLY");
    await user.click(screen.getByRole("button", { name: "登入" }));

    expect(mocked.visitorLogin).toHaveBeenCalledWith("777", "LOOKONLY");
    expect(mocked.requestOtp).not.toHaveBeenCalled();
    expect(mocked.login).not.toHaveBeenCalled();
    expect(onSignedIn).toHaveBeenCalledWith({
      id: "visitor",
      name: "訪客",
      role: "visitor",
    });
  });

  it("shows a rejected visitor credential and links back to the email form", async () => {
    const user = userEvent.setup();
    mocked.visitorLogin.mockRejectedValue(
      new AuthError(401, "訪客 PIN 或通行碼不正確"),
    );
    render(<SignIn onSignedIn={vi.fn()} visitor />);

    await visitorSubmit(user, "777", "WRONG");

    expect(await screen.findByRole("alert")).toHaveTextContent("訪客 PIN 或通行碼不正確");

    await user.click(screen.getByRole("button", { name: "改用 email 登入" }));
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
    expect(screen.queryByLabelText("訪客 PIN")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("rewrites a visitor 429 into readable advice", async () => {
    const user = userEvent.setup();
    mocked.visitorLogin.mockRejectedValue(new AuthError(429, "rate limited"));
    render(<SignIn onSignedIn={vi.fn()} visitor />);

    await visitorSubmit(user, "777", "LOOKONLY");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "嘗試次數過多，請稍後再試",
    );
  });

  /**
   * Nothing typed here may survive the tab. Both stores are checked wholesale
   * rather than by key: a future refactor could persist it under any name.
   */
  it("leaves no trace of credentials in web storage", async () => {
    const user = userEvent.setup();
    render(<SignIn onSignedIn={vi.fn()} visitor />);

    await requestPasscode(user, "ming@example.com");
    await user.type(screen.getByLabelText("驗證碼"), "a7k2m9p4");
    await user.click(screen.getByRole("button", { name: "登入" }));
    await visitorSubmit(user, "777", "LOOKONLY");

    expect(mocked.login).toHaveBeenCalled();
    expect(mocked.visitorLogin).toHaveBeenCalled();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(window.location.search).toBe("");
  });
});

describe("AuthGate", () => {
  beforeEach(() => {
    useApp.setState({
      preferences: { ...useApp.getState().preferences, language: "zh-TW" },
    });
  });

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
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
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
    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
  });

  it("offers the visitor entry when the server enables it", async () => {
    const user = userEvent.setup();
    mocked.getAuthStatus.mockResolvedValue({
      mode: "accounts",
      authenticated: false,
      actor: null,
      visitor: true,
    });
    mocked.visitorLogin.mockResolvedValue({
      ok: true,
      actor: { id: "visitor", name: "訪客", role: "visitor" },
    });

    render(
      <AuthGate>
        <VisitorBadge />
      </AuthGate>,
    );

    await screen.findByRole("button", { name: "訪客登入" });
    await visitorSubmit(user, "777", "LOOKONLY");

    expect(mocked.visitorLogin).toHaveBeenCalledWith("777", "LOOKONLY");
    expect(await screen.findByText("訪客 · 唯讀")).toBeInTheDocument();
  });

  // An older server sends no visitor field at all; that must read as "off".
  it("hides the visitor entry when status omits it", async () => {
    mocked.getAuthStatus.mockResolvedValue({
      mode: "accounts",
      authenticated: false,
      actor: null,
    });

    render(
      <AuthGate>
        <p>app</p>
      </AuthGate>,
    );

    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "訪客登入" })).toBeNull();
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
