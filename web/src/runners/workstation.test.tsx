import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type Runner } from "../api";
import { useAgents } from "../agents/agentStore";
import { localRunner, restartedRunner, startingRunner } from "../local/localFixtures";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useRuns } from "../runs/runStore";
import SettingsView from "../settings/SettingsView";
import { setProjectMode } from "../station/projectMode";
import { useApp } from "../store";
import { EditionProvider } from "./edition";
import { useRunners } from "./runnerStore";
import { useWorkstationAutoEnable, useWorkstationVisible } from "./workstation";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listRunners: vi.fn(),
      getRunBudget: vi.fn(),
      getRouting: vi.fn(() => Promise.resolve({ rules: [] })),
    },
  };
});

const listRunners = vi.mocked(api.listRunners);

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function Probe() {
  useWorkstationAutoEnable();
  return <p>{useWorkstationVisible() ? "shown" : "hidden"}</p>;
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ runners: [] });
  useRuns.setState({ latestByNode: {} });
  useAgents.setState({ sessions: [], asks: [], held: [] });
  listRunners.mockResolvedValue({ runners: [] });
});

afterEach(() => vi.clearAllMocks());

describe("agent workstation visibility", () => {
  it("stays hidden on a board with no runners, runs or agents", async () => {
    render(<Probe />);
    await waitFor(() => expect(listRunners).toHaveBeenCalledTimes(1));
    expect(screen.getByText("hidden")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(false);
  });

  it("turns itself on for good when this person has a runner", async () => {
    listRunners.mockResolvedValue({
      runners: [{ id: "r1", name: "pc" } as never],
    });
    render(<Probe />);

    expect(await screen.findByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("turns itself on when the project has a run", async () => {
    render(<Probe />);
    act(() => {
      useRuns.setState({ latestByNode: { a: { id: "run1", nodeId: "a" } as Run } });
    });

    expect(await screen.findByText("shown")).toBeInTheDocument();
    // Remembered: a project without runs does not take it away again.
    act(() => {
      useRuns.setState({ latestByNode: {} });
    });
    expect(screen.getByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("treats a hub that cannot list runners as having none", async () => {
    listRunners.mockRejectedValue(new Error("403"));
    render(<Probe />);
    await waitFor(() => expect(listRunners).toHaveBeenCalled());
    expect(screen.getByText("hidden")).toBeInTheDocument();
  });

  it("is switched on in the Agents header, which is all Agents shows while off", async () => {
    const user = userEvent.setup();
    vi.mocked(api.getRunBudget).mockResolvedValue({
      budget: { dailyRuns: 5, dailyCostUsd: 0, dailyTokens: 0 },
      used: { runs: 1, costUsd: 0, tokens: 0 },
      exceeded: "",
    });
    // "Adjust budget" with the workstation hidden lands on its switch.
    useApp.getState().updateUIPreference("settingsSection", "budget");
    render(<SettingsView />);

    const nav = screen.getByRole("navigation", { name: "Settings" });
    const agents = screen.getByRole("region", { name: "Agents" });
    expect(within(nav).getByRole("button", { name: /^Agents/ })).toHaveAttribute("aria-current", "page");
    expect(within(agents).queryByRole("button", { name: "Add runner" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Advanced" })).not.toBeInTheDocument();
    // Search does not offer what is hidden.
    await user.type(within(nav).getByRole("searchbox", { name: "Search settings" }), "budget");
    expect(within(nav).getByText("Nothing found")).toBeInTheDocument();
    await user.clear(within(nav).getByRole("searchbox", { name: "Search settings" }));

    const toggle = within(agents).getByRole("switch", { name: "Let agents work here" });
    expect(toggle).not.toBeChecked();
    await user.click(toggle);

    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
    expect(toggle).toBeChecked();
    expect(within(agents).getByRole("button", { name: "Add runner" })).toBeInTheDocument();
    // The part that was asked for opens once it is offered: Advanced, and
    // its budget row in it.
    expect(await screen.findByText("1 / 5")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Daily budget" })).toBeInTheDocument();
  });

  it("cannot be hidden while it is in use", () => {
    useAgents.setState({ sessions: [{ id: "claude:1" } as never] });
    useApp.getState().updateUIPreference("settingsSection", "workstation");
    render(<SettingsView />);

    expect(screen.getByRole("switch", { name: "Let agents work here" })).toBeDisabled();
    expect(screen.getByText(/In use: this workspace has runners/)).toBeInTheDocument();
  });
});

// A single-user hub starts a runner of its own at every start. It is part
// of the app, not something the person added: on a brand-new board it
// switched the whole workstation on before they had done anything.
describe("agent workstation visibility on a single-user hub", () => {
  /** This computer's runner with no repo added yet. */
  const bare = (): Runner => {
    const runner = localRunner();
    return { ...runner, info: { ...runner.info!, repos: [] } };
  };
  const laptop = { id: "r9", name: "laptop", online: false } as Runner;

  function SingleProbe() {
    return (
      <EditionProvider edition="single">
        <Probe />
      </EditionProvider>
    );
  }

  /** The hub's answer to the probe and, once something loads it, the store. */
  async function hubLists(runners: Runner[]) {
    listRunners.mockResolvedValue({ runners });
    render(<SingleProbe />);
    await waitFor(() => expect(listRunners).toHaveBeenCalledTimes(1));
    act(() => useRunners.setState({ runners }));
  }

  it.each([
    ["before it has connected", () => startingRunner()],
    ["after a restart, before it reconnects", () => ({ ...restartedRunner(), info: bare().info })],
    ["connected, with no repo yet", bare],
  ])("stays hidden with only the hub's own runner, %s", async (_when, runner) => {
    await hubLists([runner()]);
    expect(screen.getByText("hidden")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(false);
  });

  it("turns itself on once this computer has a repo", async () => {
    listRunners.mockResolvedValue({ runners: [localRunner()] });
    render(<SingleProbe />);
    expect(await screen.findByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("turns itself on when a repo is added to a list already loaded", async () => {
    await hubLists([bare()]);
    expect(screen.getByText("hidden")).toBeInTheDocument();
    act(() => useRunners.setState({ runners: [localRunner()] }));
    expect(screen.getByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  // serve --repo on a first start: the repo is in the runner's settings, but
  // the hub learns of it only from the runner's hello, which can come after
  // the page asked. The probe runs once, so it has to keep following the list.
  it("turns itself on when the hub's own runner brings its repos once it connects", async () => {
    // Nothing has loaded the list: the workstation, and with it the pill, is hidden.
    useRunners.setState({ loaded: false, loading: false, runners: [] });
    listRunners.mockResolvedValue({ runners: [startingRunner()] });
    render(<SingleProbe />);
    await waitFor(() => expect(useRunners.getState().loaded).toBe(true));
    await waitFor(() => expect(useRunners.getState().loading).toBe(false));
    expect(screen.getByText("hidden")).toBeInTheDocument();

    // It connects: the hub pushes runners-changed (App refreshes a loaded list).
    listRunners.mockResolvedValue({ runners: [localRunner()] });
    await act(() => useRunners.getState().refreshIfLoaded());
    expect(screen.getByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("turns itself on for a runner connected by hand beside it", async () => {
    listRunners.mockResolvedValue({ runners: [startingRunner(), laptop] });
    render(<SingleProbe />);
    expect(await screen.findByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("turns itself on for a run or a connected agent", async () => {
    await hubLists([startingRunner()]);
    act(() => useRuns.setState({ latestByNode: { a: { id: "run1", nodeId: "a" } as Run } }));
    expect(screen.getByText("shown")).toBeInTheDocument();

    act(() => {
      useApp.getState().updateUIPreference("agentWorkstation", false);
      useRuns.setState({ latestByNode: {} });
    });
    expect(screen.getByText("hidden")).toBeInTheDocument();
    act(() => useAgents.setState({ sessions: [{ id: "claude:1" } as never] }));
    expect(screen.getByText("shown")).toBeInTheDocument();
  });

  it("turns on when the person enters Station", async () => {
    await hubLists([startingRunner()]);
    act(() => setProjectMode("station"));
    expect(screen.getByText("shown")).toBeInTheDocument();
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("is the person's to switch on in Settings, which then shows This computer", async () => {
    const user = userEvent.setup();
    useRunners.setState({ loaded: true, loading: false, runners: [bare()] });
    useApp.getState().updateUIPreference("settingsSection", "runners");
    render(
      <EditionProvider edition="single">
        <SettingsView />
      </EditionProvider>,
    );

    const agents = screen.getByRole("region", { name: "Agents" });
    const toggle = within(agents).getByRole("switch", { name: "Let agents work here" });
    expect(toggle).not.toBeChecked();
    expect(toggle).toBeEnabled();
    expect(within(agents).getByText(/Everything for letting agents work on this computer/)).toBeInTheDocument();
    expect(agents.textContent).not.toMatch(/runner/i);
    expect(within(agents).queryByRole("heading", { name: "Agents found" })).not.toBeInTheDocument();

    await user.click(toggle);
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
    expect(within(agents).getByRole("heading", { name: "Agents found" })).toBeInTheDocument();
    // Its own runner is no reason to keep it on: it can be switched off again.
    expect(toggle).toBeEnabled();
  });

  it("says why it stays on once this computer has a repo, without a word about runners", () => {
    useRunners.setState({ loaded: true, loading: false, runners: [localRunner()] });
    useApp.getState().updateUIPreference("settingsSection", "workstation");
    render(
      <EditionProvider edition="single">
        <SettingsView />
      </EditionProvider>,
    );

    expect(screen.getByRole("switch", { name: "Let agents work here" })).toBeDisabled();
    expect(
      screen.getByText("In use: agents already have work or a project folder here, so this stays on."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/In use: this workspace has runners/)).not.toBeInTheDocument();
  });
});
