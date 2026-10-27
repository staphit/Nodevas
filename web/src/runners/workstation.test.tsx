import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run } from "../api";
import { useAgents } from "../agents/agentStore";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useRuns } from "../runs/runStore";
import SettingsView from "../settings/SettingsView";
import { useApp } from "../store";
import { useRunners } from "./runnerStore";
import { useWorkstationAutoEnable, useWorkstationVisible } from "./workstation";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return { ...actual, api: { ...actual.api, listRunners: vi.fn(), getRunBudget: vi.fn() } };
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

  it("is switched on from its Settings part, which is all the Agents group shows while off", async () => {
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
    expect(screen.getByRole("region", { name: "Agent workstation" })).toBeInTheDocument();
    expect(within(nav).getByRole("button", { name: "Agent workstation" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).queryByRole("button", { name: "Runners" })).not.toBeInTheDocument();
    expect(within(nav).queryByRole("button", { name: "Daily budget" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Enable agent workstation" }));

    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
    for (const part of ["Agent workstation", "Runners", "Usage", "Daily budget", "Routing rules"]) {
      expect(within(nav).getByRole("button", { name: part })).toBeInTheDocument();
    }
    // The part that was asked for opens once it is offered.
    expect(await screen.findByText("1 / 5")).toBeInTheDocument();
  });

  it("cannot be hidden while it is in use", () => {
    useAgents.setState({ sessions: [{ id: "claude:1" } as never] });
    useApp.getState().updateUIPreference("settingsSection", "workstation");
    render(<SettingsView />);

    expect(screen.getByRole("button", { name: "Hide agent workstation" })).toBeDisabled();
  });
});
