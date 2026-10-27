import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor, RunBudgetStatus, Runner } from "../../api";
import type { Graph } from "../../types";
import { readPreferences } from "../../preferences";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";
import { useApp } from "../../store";
import { AccountMenu } from "./AccountMenu";
import { ProjectMenu } from "./ProjectMenu";
import { RunnerPill } from "./RunnerPill";
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
    expect(within(menu).getByRole("menuitemradio", { name: /Beta/ })).toHaveAttribute("aria-checked", "true");
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
    expect(screen.getByRole("menuitem", { name: "Layout: Workflow" })).toBeDisabled();

    await user.click(screen.getByRole("menuitem", { name: "Project settings" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("settings");
    expect(prefs.settingsSection).toBe("project");
  });

  it("switches workspace", async () => {
    const user = userEvent.setup();
    render(<ProjectMenu />);
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    expect(screen.getByRole("menuitemradio", { name: "One" })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("menuitemradio", { name: "Two" }));
    expect(switchWorkspace).toHaveBeenCalledWith("/w/two");
  });
});

describe("AccountMenu", () => {
  it("enters Settings, flips the theme and replays the tour", async () => {
    const user = userEvent.setup();
    const tour = vi.fn();
    window.addEventListener("nodevas-tour-start", tour);
    render(<AccountMenu />);
    const trigger = screen.getByRole("button", { name: "Account and settings" });

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "Settings" }));
    expect(useApp.getState().preferences.projectMode).toBe("settings");

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "Switch to light mode" }));
    expect(toggleTheme).toHaveBeenCalledTimes(1);

    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "Replay tour" }));
    expect(tour).toHaveBeenCalledTimes(1);
    window.removeEventListener("nodevas-tour-start", tour);
  });

  it("opens the palette on its help view", async () => {
    const user = userEvent.setup();
    const opened = vi.fn();
    const listener = (event: Event) => opened((event as CustomEvent).detail);
    window.addEventListener("nodevas-command-palette", listener);
    render(<AccountMenu />);
    await user.click(screen.getByRole("button", { name: "Account and settings" }));
    await user.click(screen.getByRole("menuitem", { name: "Help and shortcuts" }));
    expect(opened).toHaveBeenCalledWith({ view: "help" });
    window.removeEventListener("nodevas-command-palette", listener);
  });

  // A loopback server has no session to end.
  it("offers sign-out only with an account", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<AccountMenu />);
    await user.click(screen.getByRole("button", { name: "Account and settings" }));
    expect(screen.queryByRole("menuitem", { name: /Sign out/ })).toBeNull();
    unmount();

    auth.viewer = { id: "ann", name: "Ann", role: "member" } as Actor;
    render(<AccountMenu />);
    const trigger = screen.getByRole("button", { name: "Account and settings" });
    expect(trigger.textContent).toBe("A");
    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "Sign out (Ann)" }));
    expect(auth.signOut).toHaveBeenCalledTimes(1);
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
  it("holds the project menu, Board | Station, search and the account menu, and nothing else it used to", () => {
    render(<TopBar />);
    expect(screen.getByRole("heading", { name: "Nodevas" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Project: Beta" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Board" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Station" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Search or command/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Account and settings" })).toBeTruthy();
    // Relocated: the explorer toggle, the project <select>, the theme and help icons.
    expect(screen.queryByRole("button", { name: /project explorer/ })).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByRole("button", { name: /light mode|Replay tour/ })).toBeNull();
  });
});
