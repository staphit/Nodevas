import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, type InviteResult, type ServerAccounts } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useStepUp } from "../stepup/stepUpStore";
import { useApp } from "../store";
import { MembersEditor } from "./MembersEditor";
import { ServerMembersPanel, suggestedName } from "./ServerMembersPanel";

const LIST: ServerAccounts = {
  accounts: [
    { id: "u-ann", name: "ann", email: "ann@example.test", role: "admin", createdAt: "2026-10-24T00:00:00Z", twoFactor: true },
    { id: "u-bea", name: "bea", email: "bea@example.test", role: "member", createdAt: "2026-10-24T00:00:00Z", twoFactor: false },
  ],
  self: "u-ann",
  mailConfigured: true,
  signInUrl: "https://nodevas.example.com",
  seats: { active: 1, max: 0 },
};

const DAN = {
  id: "u-dan",
  name: "dan",
  email: "dan@example.test",
  role: "member" as const,
  createdAt: "2026-10-26T00:00:00Z",
  twoFactor: false,
};

function invited(status: InviteResult["email"]["status"]): InviteResult {
  return {
    account: DAN,
    email: { status, to: DAN.email },
    signInUrl: LIST.signInUrl,
    seats: LIST.seats,
  };
}

function open() {
  const notify = { onError: vi.fn(), onNotice: vi.fn() };
  render(
    <>
      <ServerMembersPanel notify={notify} />
      <ConfirmDialogHost />
    </>,
  );
  return { notify, user: userEvent.setup() };
}

beforeEach(() => {
  vi.restoreAllMocks();
  useStepUp.getState().reset();
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("language", "en");
});

describe("ServerMembersPanel", () => {
  it("lists every account with its email, role and second factor", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue(LIST);
    open();

    const bea = (await screen.findByText("bea@example.test")).closest("li")!;
    expect(within(bea).getByText("2FA off")).toBeTruthy();
    expect((within(bea).getByLabelText("Server role for bea") as HTMLSelectElement).value).toBe("member");
    expect(within(bea).getByRole("button", { name: "Remove account bea" })).toBeTruthy();

    // Your own row offers neither a role change nor removal.
    const ann = screen.getByText("ann@example.test").closest("li")!;
    expect(within(ann).getByText("2FA on")).toBeTruthy();
    expect(within(ann).queryByRole("combobox")).toBeNull();
    expect(within(ann).queryByRole("button")).toBeNull();
  });

  it("invites by email, naming the account after the address", async () => {
    vi.spyOn(api, "listServerAccounts")
      .mockResolvedValueOnce(LIST)
      .mockResolvedValue({ ...LIST, accounts: [...LIST.accounts, DAN] });
    const invite = vi.spyOn(api, "inviteServerAccount").mockResolvedValue(invited("sent"));
    const { notify, user } = open();

    await user.type(await screen.findByLabelText("Email address to invite"), "dan@example.test");
    expect(screen.getByLabelText("Account name for the invited person").getAttribute("placeholder")).toBe("dan");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    await waitFor(() => expect(invite).toHaveBeenCalledWith("dan", "dan@example.test", "member"));
    expect(notify.onNotice).toHaveBeenCalledWith("Invitation sent to dan@example.test");
    expect(await screen.findByText("dan@example.test")).toBeTruthy();
    expect(screen.queryByText(/Tell them yourself/)).toBeNull();
  });

  it("says what to tell the person when the invitation could not be mailed", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue(LIST);
    vi.spyOn(api, "inviteServerAccount").mockResolvedValue(invited("failed"));
    const { user } = open();

    await user.type(await screen.findByLabelText("Email address to invite"), "dan@example.test");
    await user.type(screen.getByLabelText("Account name for the invited person"), "dan");
    await user.selectOptions(screen.getByLabelText("Server role for the invited person"), "member");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    expect(await screen.findByText(/The account dan was created, but the invitation email could not be sent/)).toBeTruthy();
    expect(screen.getByText(/Tell them yourself/)).toBeTruthy();
    expect(screen.getByText(/Open https:\/\/nodevas\.example\.com, enter dan@example\.test/)).toBeTruthy();
  });

  it("warns when the server cannot send mail at all", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue({ ...LIST, mailConfigured: false });
    open();
    expect(await screen.findByText(/This server cannot send mail/)).toBeTruthy();
  });

  it("asks for a step-up when the hub refuses a change", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue(LIST);
    const invite = vi
      .spyOn(api, "inviteServerAccount")
      .mockRejectedValue(new AuthError(403, "verify it's you first (step-up), then try again"));
    vi.spyOn(api, "getStepUp").mockResolvedValue({ required: true, active: false, method: "email" });
    const { notify, user } = open();

    await user.type(await screen.findByLabelText("Email address to invite"), "dan@example.test");
    await user.click(screen.getByRole("button", { name: "Invite" }));

    expect(await screen.findByRole("dialog", { name: "Verify it's you" })).toBeTruthy();
    expect(invite).toHaveBeenCalledTimes(1);
    expect(notify.onError).not.toHaveBeenCalledWith(expect.stringContaining("step-up"));
  });

  it("changes a role and removes an account after confirming", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue(LIST);
    const setRole = vi.spyOn(api, "setServerAccountRole").mockResolvedValue({
      ...LIST,
      accounts: [LIST.accounts[0], { ...LIST.accounts[1], role: "admin" }],
    });
    const remove = vi.spyOn(api, "removeServerAccount").mockResolvedValue({ ...LIST, accounts: [LIST.accounts[0]] });
    const { notify, user } = open();

    await user.selectOptions(await screen.findByLabelText("Server role for bea"), "admin");
    await waitFor(() => expect(setRole).toHaveBeenCalledWith("u-bea", "admin"));
    expect(notify.onNotice).toHaveBeenCalledWith("bea is now Administrator. They need to sign in again.");

    await user.click(screen.getByRole("button", { name: "Remove account bea" }));
    await user.click(await screen.findByRole("button", { name: "Remove account" }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith("u-bea"));
    await waitFor(() => expect(screen.queryByText("bea@example.test")).toBeNull());
  });

  it("does not remove without confirmation", async () => {
    vi.spyOn(api, "listServerAccounts").mockResolvedValue(LIST);
    const remove = vi.spyOn(api, "removeServerAccount");
    const { user } = open();

    await user.click(await screen.findByRole("button", { name: "Remove account bea" }));
    await user.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(remove).not.toHaveBeenCalled();
  });
});

describe("suggestedName", () => {
  it("keeps the characters an account name may hold", () => {
    expect(suggestedName("Dan.Lee+work@example.test")).toBe("Dan.Lee-work");
    expect(suggestedName("_x@example.test")).toBe("x");
    expect(suggestedName("")).toBe("");
  });
});

describe("MembersEditor invite link", () => {
  const access = {
    project: "main",
    restricted: false,
    role: "owner" as const,
    member: "" as const,
    admin: true,
    canWrite: true,
    canManage: true,
  };

  it("points an administrator at the server's members", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue(access);
    vi.spyOn(api, "listMembers").mockResolvedValue({ project: "main", restricted: false, members: [] });
    const onInvite = vi.fn();
    render(<MembersEditor notify={{ onError: vi.fn(), onNotice: vi.fn() }} onInvite={onInvite} />);

    await userEvent.setup().click(await screen.findByRole("button", { name: "Invite them to this server" }));
    expect(onInvite).toHaveBeenCalled();
  });

  it("offers nothing to someone who cannot invite", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue({ ...access, admin: false });
    vi.spyOn(api, "listMembers").mockResolvedValue({ project: "main", restricted: false, members: [] });
    render(<MembersEditor notify={{ onError: vi.fn(), onNotice: vi.fn() }} />);

    expect(await screen.findByText(/This project is open/)).toBeTruthy();
    expect(screen.queryByText("Person not found?")).toBeNull();
  });
});
