import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor, RunBudgetStatus, Runner } from "../../api";
import type { Graph } from "../../types";
import { readPreferences } from "../../preferences";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";
import { openSettings } from "../../settings/openSettings";
import { useApp } from "../../store";
import { replayTour } from "./actions";
import { AccountMenu } from "./AccountMenu";
import { ProjectMenu } from "./ProjectMenu";
import { RunnerPill } from "./RunnerPill";
import { SettingsButton, ThemeButton } from "./SettingsButton";
import { TopBar } from "./TopBar";

const auth = vi.hoisted(() => ({ viewer: null as Actor | null, signOut: vi.fn() }));
vi.mock("../SignIn", () => ({
  useViewer: () => auth.viewer,
  useSignOut: () => auth.signOut,
}));

const switchProject = vi.fn(async () => {});
const switchWorkspace = vi.fn(async () => {});
const toggleTheme = vi.fn();

const runner = (over: Partial<Runner>): Runner =>
  ({ id: "r", name: "mac", ownerId: "ann", ownerName: "ann", createdAt: "", lastSeenAt: "", online: true, ...over }) as Runner;

beforeEach(() => {
  auth.viewer = null;
  auth.signOut.mockReset();
  switchProject.mockClear();
  switchWorkspace.mockClear();
  toggleTheme.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en", projectMode: "info", agentWorkstation: false },
    activeProject: "beta",
    projects: [
      { name: "alpha", label: "Alpha", depth: 0, path: "alpha", nodes: 3 },
      { name: "folder", label: "Folder", depth: 0, path: "folder", nodes: 0, isFolder: true },
      { name: "beta", label: "Beta", depth: 1, path: "folder/beta", nodes: 7 },
    ],
    workspace: "/w/one",
    workspaces: [
      { path: "/w/one", label: "One", active: true, projects: 2 },
      { path: "/w/two", label: "Two", active: false, projects: 1 },
    ],
    graph: { version: 1, type: "workflow", nodes: [] } as unknown as Graph,
    theme: "dark",
    switchProject,
    switchWorkspace,
    toggleTheme,
  });
  // Nothing here reaches the hub: the lists are already loaded.
  useRunners.setState({ loaded: true, runners: [], refresh: vi.fn(async () => {}) });
  useBudget.setState({ status: null, load: vi.fn(async () => {}) });
});

describe("ProjectMenu", () => {
  it("names the project, lists the projects with the current one checked, and switches", async () => {
    const user = userEvent.setup();
    render(<ProjectMenu />);
    const trigger = screen.getByRole("button", { name: "Project: Beta" });
    await user.click(trigger);

    const menu = screen.getByRole("menu");
    // The projects are one group, named by their heading.
    const projects = within(menu).getByRole("group", { name: "Projects" });
    expect(within(projects).getByRole("menuitemradio", { name: /Beta/ })).toHaveAttribute("aria-checked", "true");
    // A folder is where projects live, not one to open.
    expect(within(menu).queryByRole("menuitemradio", { name: /Folder/ })).toBeNull();
    await user.click(within(menu).getByRole("menuitemradio", { name: /Alpha/ }));
    expect(switchProject).toHaveBeenCalledWith("alpha");
  });

  // The hub has no way to change a project's layout once made: said, not offered.
  it("shows the layout read-only and opens project settings", async () => {
    const user = userEvent.setup();
    render(<ProjectMenu />);
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    expect(screen.getByRole("menuitem", { name: "Layout: Workflow" })).toHaveAttribute("aria-disabled", "true");

    await user.click(screen.getByRole("menuitem", { name: "Project settings" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("settings");
    expect(prefs.settingsSection).toBe("project");
  });

  it("switches workspace", async () => {
    const user = userEvent.setup();
    render(<ProjectMenu />);
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    const workspaces = screen.getByRole("group", { name: "Workspace" });
    expect(within(workspaces).getByRole("menuitemradio", { name: "One" })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("menuitemradio", { name: "Two" }));
    expect(switchWorkspace).toHaveBeenCalledWith("/w/two");
  });
});

describe("SettingsButton", () => {
  it("opens Settings in one click, shows pressed, and a second click goes back", async () => {
    const user = userEvent.setup();
    useApp.setState({ preferences: { ...useApp.getState().preferences, projectMode: "station" } });
    render(<SettingsButton />);
    const button = screen.getByRole("button", { name: "Settings" });
    expect(button).toHaveAttribute("aria-pressed", "false");
    // A label in words, not a bare gear.
    expect(button.textContent).toBe("Settings");

    await user.click(button);
    expect(useApp.getState().preferences.projectMode).toBe("settings");
    expect(button).toHaveAttribute("aria-pressed", "true");

    // Back to where the viewer was, not always the board.
    await user.click(button);
    expect(useApp.getState().preferences.projectMode).toBe("station");
    expect(button).toHaveAttribute("aria-pressed", "false");
  });

  it("is pressed however Settings was entered, and leaves to the board by default", async () => {
    const user = userEvent.setup();
    render(<SettingsButton />);
    act(() => openSettings("account"));
    const button = screen.getByRole("button", { name: "Settings" });
    expect(button).toHaveAttribute("aria-pressed", "true");
    await user.click(button);
    expect(useApp.getState().preferences.projectMode).toBe("info");
  });
});

describe("ThemeButton", () => {
  it("names the theme it switches to and flips it", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<ThemeButton />);
    const button = screen.getByRole("button", { name: "Switch to light mode" });
    expect(button).toHaveAttribute("title", "Switch to light mode");
    await user.click(button);
    expect(toggleTheme).toHaveBeenCalledTimes(1);
    unmount();

    useApp.setState({ theme: "light" });
    render(<ThemeButton />);
    expect(screen.getByRole("button", { name: "Switch to dark mode" })).toBeTruthy();
  });
});

describe("AccountMenu", () => {
  // A loopback server has no session, so no account to show.
  it("is not drawn without an account", () => {
    const { container } = render(<AccountMenu />);
    expect(container).toBeEmptyDOMElement();
  });

  it("holds only account things: your account and sign-out", async () => {
    const user = userEvent.setup();
    auth.viewer = { id: "ann", name: "Ann", role: "member" } as Actor;
    render(<AccountMenu />);
    const trigger = screen.getByRole("button", { name: "Account" });
    expect(trigger.textContent).toBe("A");
    expect(trigger).toHaveAttribute("title", "Ann: account");

    await user.click(trigger);
    const items = within(screen.getByRole("menu")).getAllByRole("menuitem");
    expect(items.map((item) => item.textContent)).toEqual(["Your account", "Sign out (Ann)"]);

    await user.click(screen.getByRole("menuitem", { name: "Your account" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("settings");
    expect(prefs.settingsSection).toBe("account");

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "Sign out (Ann)" }));
    expect(auth.signOut).toHaveBeenCalledTimes(1);
  });
});

describe("actions", () => {
  it("replays the tour on the board, wherever it was asked from", () => {
    const tour = vi.fn();
    window.addEventListener("nodevas-tour-start", tour);
    useApp.setState({ preferences: { ...useApp.getState().preferences, projectMode: "settings" } });
    replayTour();
    expect(useApp.getState().preferences.projectMode).toBe("info");
    expect(tour).toHaveBeenCalledTimes(1);
    window.removeEventListener("nodevas-tour-start", tour);
  });
});

describe("RunnerPill", () => {
  it("is not drawn while the workstation is hidden", () => {
    const { container } = render(<RunnerPill />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says how many runners are online and what today cost, and opens Station", async () => {
    const user = userEvent.setup();
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: true } });
    useRunners.setState({ runners: [runner({ id: "a" }), runner({ id: "b", online: false })] });
    useBudget.setState({ status: { used: { runs: 2, costUsd: 1.2, tokens: 10 }, exceeded: "" } as unknown as RunBudgetStatus });
    render(<RunnerPill />);

    const pill = screen.getByRole("button", { name: "1 runner online, $1.20 spent today" });
    expect(pill.textContent).toBe("1 runner · $1.20");
    expect(pill.querySelector(".runner-dot.online")).not.toBeNull();
    await user.click(pill);
    expect(useApp.getState().preferences.projectMode).toBe("station");
  });

  it("goes grey with nobody online", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: true } });
    useRunners.setState({ runners: [runner({ online: false })] });
    render(<RunnerPill />);
    const pill = screen.getByRole("button", { name: "0 runners online" });
    expect(pill.querySelector(".runner-dot.online")).toBeNull();
  });
});

describe("TopBar", () => {
  it("holds the project menu, Board | Station, search, Settings and the theme, and nothing else it used to", () => {
    render(<TopBar />);
    expect(screen.getByRole("heading", { name: "Nodevas" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Project: Beta" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Board" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Station" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Search or command/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Settings" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: "Switch to light mode" })).toBeTruthy();
    // Standalone: no account, so no avatar.
    expect(screen.queryByRole("button", { name: "Account" })).toBeNull();
    // Relocated: the explorer toggle, the project <select>, the help and tour icons.
    expect(screen.queryByRole("button", { name: /project explorer/ })).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByRole("button", { name: /Replay tour|Help/ })).toBeNull();
  });

  it("draws the account avatar after Settings on a server with a session", () => {
    auth.viewer = { id: "ann", name: "Ann", role: "member" } as Actor;
    render(<TopBar />);
    const settings = screen.getByRole("button", { name: "Settings" });
    const account = screen.getByRole("button", { name: "Account" });
    expect(settings.compareDocumentPosition(account) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
