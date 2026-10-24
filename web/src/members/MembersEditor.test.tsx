import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError, type ProjectAccess, type ProjectMembers } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { MembersEditor } from "./MembersEditor";
import { notifyAccessChanged, ProjectAccessBanner } from "./ProjectAccessBanner";

const OPEN: ProjectMembers = { project: "main", restricted: false, members: [] };
const RESTRICTED: ProjectMembers = {
  project: "main",
  restricted: true,
  members: [
    { userId: "u-bea", name: "bea", role: "owner", createdAt: "2026-10-24T00:00:00Z" },
    { userId: "u-cal", name: "cal", role: "viewer", createdAt: "2026-10-24T00:00:00Z" },
  ],
};

function access(over: Partial<ProjectAccess> = {}): ProjectAccess {
  return {
    project: "main",
    restricted: false,
    role: "editor",
    member: "",
    admin: false,
    canWrite: true,
    canManage: true,
    ...over,
  };
}

function open() {
  const notify = { onError: vi.fn(), onNotice: vi.fn() };
  render(
    <>
      <MembersEditor notify={notify} />
      <ConfirmDialogHost />
    </>,
  );
  return { notify, user: userEvent.setup() };
}

beforeEach(() => {
  vi.restoreAllMocks();
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("language", "en");
});

describe("MembersEditor", () => {
  it("restricts an open project after confirming, adding the person by name", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue(access());
    vi.spyOn(api, "listMembers").mockResolvedValue(OPEN);
    const setMember = vi.spyOn(api, "setMember").mockResolvedValue(RESTRICTED);
    const { notify, user } = open();

    expect(await screen.findByText(/This project is open/)).toBeTruthy();
    await user.type(screen.getByLabelText("Account to add"), "cal");
    await user.selectOptions(screen.getByLabelText("Role for the new member"), "viewer");
    await user.click(screen.getByRole("button", { name: "Add" }));
    await user.click(await screen.findByRole("button", { name: "Restrict and add" }));

    await waitFor(() => expect(setMember).toHaveBeenCalledWith("main", "cal", "viewer"));
    expect(await screen.findByText("bea")).toBeTruthy();
    expect(notify.onNotice).toHaveBeenCalledWith("Added cal");
  });

  it("lets an owner change a role and remove a member", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue(access({ restricted: true, role: "owner", member: "owner" }));
    vi.spyOn(api, "listMembers").mockResolvedValue(RESTRICTED);
    const setRole = vi.spyOn(api, "setMemberRole").mockResolvedValue(RESTRICTED);
    const remove = vi.spyOn(api, "removeMember").mockResolvedValue({ ...RESTRICTED, members: [RESTRICTED.members[0]] });
    const { user } = open();

    await user.selectOptions(await screen.findByLabelText("Role for cal"), "editor");
    await waitFor(() => expect(setRole).toHaveBeenCalledWith("main", "u-cal", "editor"));
    await user.click(screen.getByRole("button", { name: "Remove cal" }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith("main", "u-cal"));
    await waitFor(() => expect(screen.queryByText("cal")).toBeNull());
  });

  it("shows a viewer the list without controls", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue(
      access({ restricted: true, role: "viewer", member: "viewer", canWrite: false, canManage: false }),
    );
    vi.spyOn(api, "listMembers").mockResolvedValue(RESTRICTED);
    open();

    expect(await screen.findByText("Your role: Viewer")).toBeTruthy();
    expect(screen.queryByLabelText("Account to add")).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove cal" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Open to everyone" })).toBeNull();
  });

  it("reports the server's refusal", async () => {
    vi.spyOn(api, "projectAccess").mockResolvedValue(access({ restricted: true, role: "owner" }));
    vi.spyOn(api, "listMembers").mockResolvedValue(RESTRICTED);
    vi.spyOn(api, "setMemberRole").mockRejectedValue(new RequestError(409, "a project with members needs at least one owner"));
    const { notify, user } = open();

    await user.selectOptions(await screen.findByLabelText("Role for bea"), "editor");
    await waitFor(() =>
      expect(notify.onError).toHaveBeenCalledWith("a project with members needs at least one owner"),
    );
  });
});

describe("ProjectAccessBanner", () => {
  it("says view only to a viewer and nothing to an editor", async () => {
    const spy = vi.spyOn(api, "projectAccess").mockResolvedValue(access({ canWrite: false, role: "viewer" }));
    render(<ProjectAccessBanner />);
    expect(await screen.findByText(/View only/)).toBeTruthy();

    spy.mockResolvedValue(access());
    notifyAccessChanged();
    await waitFor(() => expect(screen.queryByText(/View only/)).toBeNull());
  });

  it("tells a non-member they are not a member", async () => {
    vi.spyOn(api, "projectAccess").mockRejectedValue(new RequestError(404, "no such project"));
    render(<ProjectAccessBanner />);
    expect(await screen.findByText(/not a member of this project/)).toBeTruthy();
  });
});
