import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Actor, RunBudgetStatus, Runner } from "../../api";
import type { Graph } from "../../types";
import { readPreferences } from "../../preferences";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";
import { useApp } from "../../store";
import { replayTour } from "./actions";
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
const refreshProjects = vi.fn(async () => {});
const toggleTheme = vi.fn();

const runner = (over: Partial<Runner>): Runner =>
  ({ id: "r", name: "mac", ownerId: "ann", ownerName: "ann", createdAt: "", lastSeenAt: "", online: true, ...over }) as Runner;

beforeEach(() => {
  auth.viewer = null;
  auth.signOut.mockReset();
  switchProject.mockClear();
  switchWorkspace.mockClear();
  refreshProjects.mockClear();
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
    refreshProjects,
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

  // The list is fetched on open and switch, not on every node create; the
  // open project's count is read off the graph on screen, as the explorer does.
  it("counts the open project's nodes off the graph on screen", async () => {
    const user = userEvent.setup();
    useApp.setState({
      graphProject: "beta",
      graph: {
        version: 1,
        type: "workflow",
        nodes: [{ id: "a" }, { id: "b" }],
      } as unknown as Graph,
    });
    render(<ProjectMenu />);
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    const beta = screen.getByRole("menuitemradio", { name: /Beta/ });
    expect(beta).toHaveTextContent("2");
    expect(beta).toHaveAttribute("title", "2 nodes");
    // Another project's count is still the server's.
    expect(screen.getByRole("menuitemradio", { name: /Alpha/ })).toHaveTextContent("3");
  });

  // Projects added on disk, and counts changed by another window, show up
  // without a project switch or a reload.
  it("refreshes the project list when it opens", async () => {
    const user = userEvent.setup();
    render(<ProjectMenu />);
    expect(refreshProjects).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    expect(refreshProjects).toHaveBeenCalledTimes(1);
    // Closing is not another read.
    await user.click(screen.getByRole("button", { name: "Project: Beta" }));
    expect(refreshProjects).toHaveBeenCalledTimes(1);
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
  it("holds the project menu, Board | Station | Settings and search, and nothing else it used to", () => {
    render(<TopBar />);
    expect(screen.getByRole("heading", { name: "Nodevas" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Project: Beta" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Board" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Station" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Search or command/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Settings" })).toHaveAttribute("aria-pressed", "false");
    // The theme is in Settings › General, not on the bar.
    expect(screen.queryByRole("button", { name: /light mode|dark mode/ })).toBeNull();
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
