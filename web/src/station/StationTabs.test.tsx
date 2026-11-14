import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { openGlobalAgents } from "./openGlobalAgents";
import { STATION_TAB_KEY, useStation } from "./stationStore";
import { StationTabs } from "./StationTabs";
import StationView from "./StationView";

// StationView's heavy parts are not what these tests are about.
vi.mock("./StationWarRoom", () => ({ StationWarRoom: () => <div data-testid="war-room" /> }));
vi.mock("./StationWaiting", () => ({ StationWaiting: () => <div data-testid="waiting" /> }));
vi.mock("./StationSide", () => ({ StationSide: () => <div data-testid="side" /> }));
vi.mock("./GlobalAgentsPanel", () => ({ GlobalAgentsPanel: () => <div data-testid="global-panel" /> }));
vi.mock("./StationControl", () => ({
  StationPauseButton: () => <button type="button">Pause everything</button>,
  StationControlReport: () => <div data-testid="control-report" />,
}));
vi.mock("../game/useWorld", () => ({
  useWorld: () => ({ now: 0, building: { actors: [] } }),
  mergeRuns: () => [],
}));

function globalRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/task-g1000000",
    prompt: "",
    status: "running",
    exitCode: 0,
    global: true,
    title: "Fix login",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-14T08:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  localStorage.removeItem(STATION_TAB_KEY);
  useApp.setState({ preferences: { ...readPreferences(), language: "en" }, activeProject: "p" });
  useStation.getState().reset();
  useStation.setState({ tab: "project" });
  useGlobalAgents.getState().reset();
  vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [], more: false, runners: [] });
  vi.spyOn(api, "listProjectRuns").mockResolvedValue({ runs: [] });
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
  vi.spyOn(api, "getBuilding").mockResolvedValue({ projects: [], truncated: false, total: 0 });
});

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.removeItem(STATION_TAB_KEY);
});

describe("StationTabs", () => {
  it("shows two tabs, the selected one in the tab order", () => {
    render(<StationTabs />);
    expect(screen.getByRole("tablist", { name: "Workstation views" })).toBeInTheDocument();
    const project = screen.getByRole("tab", { name: "This project" });
    const global = screen.getByRole("tab", { name: "Global agents" });
    expect(project).toHaveAttribute("aria-selected", "true");
    expect(project).toHaveAttribute("tabIndex", "0");
    expect(global).toHaveAttribute("aria-selected", "false");
    expect(global).toHaveAttribute("tabIndex", "-1");
    expect(global).toHaveAttribute("aria-controls", "station-tabpanel");
  });

  it("switches by click and by arrow keys, Home and End, and remembers the tab", () => {
    render(<StationTabs />);
    fireEvent.click(screen.getByRole("tab", { name: "Global agents" }));
    expect(useStation.getState().tab).toBe("global");
    expect(localStorage.getItem(STATION_TAB_KEY)).toBe("global");

    fireEvent.keyDown(screen.getByRole("tab", { name: "Global agents" }), { key: "ArrowRight" });
    expect(useStation.getState().tab).toBe("project");
    expect(screen.getByRole("tab", { name: "This project" })).toHaveFocus();

    fireEvent.keyDown(screen.getByRole("tab", { name: "This project" }), { key: "ArrowLeft" });
    expect(useStation.getState().tab).toBe("global");
    fireEvent.keyDown(screen.getByRole("tab", { name: "Global agents" }), { key: "Home" });
    expect(useStation.getState().tab).toBe("project");
    fireEvent.keyDown(screen.getByRole("tab", { name: "This project" }), { key: "End" });
    expect(useStation.getState().tab).toBe("global");
    expect(localStorage.getItem(STATION_TAB_KEY)).toBe("global");
  });

  it("counts the global agents at work", () => {
    useGlobalAgents.setState({
      runs: [globalRun({ id: "a" }), globalRun({ id: "b", status: "queued" }), globalRun({ id: "c", status: "succeeded" })],
    });
    render(<StationTabs />);
    expect(screen.getByRole("tab", { name: "Global agents (2)" })).toBeInTheDocument();
  });

  it("reads the count in Traditional Chinese with full-width brackets", () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    useGlobalAgents.setState({ runs: [globalRun()] });
    render(<StationTabs />);
    expect(screen.getByRole("tab", { name: "此專案" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "全域 agent（1）" })).toBeInTheDocument();
  });
});

describe("Station's tabs in StationView", () => {
  it("shows the project's content or the global agents, and watches both", async () => {
    render(<StationView drawer={null} />);
    expect(useGlobalAgents.getState().watching).toBe(true);
    expect(api.listGlobalRuns).toHaveBeenCalled();
    const panel = screen.getByRole("tabpanel");
    expect(panel).toHaveAttribute("id", "station-tabpanel");
    expect(panel).toHaveAttribute("aria-labelledby", "station-tab-project");
    expect(screen.getByTestId("war-room")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause everything" })).toBeInTheDocument();
    expect(screen.queryByTestId("global-panel")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Global agents" }));
    expect(screen.getByTestId("global-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("war-room")).not.toBeInTheDocument();
    // The emergency stop is the project's.
    expect(screen.queryByRole("button", { name: "Pause everything" })).not.toBeInTheDocument();
    expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", "station-tab-global");
    // The side column stays on both.
    expect(screen.getByTestId("side")).toBeInTheDocument();
  });

  it("stops watching the global agents when Station closes", () => {
    const { unmount } = render(<StationView drawer={null} />);
    unmount();
    expect(useGlobalAgents.getState().watching).toBe(false);
  });

  it("opens on the remembered tab", () => {
    useStation.setState({ tab: "global" });
    render(<StationView drawer={null} />);
    expect(screen.getByTestId("global-panel")).toBeInTheDocument();
  });
});

describe("openGlobalAgents", () => {
  it("opens Station on the global tab with the run highlighted", () => {
    act(() => openGlobalAgents("g7"));
    expect(useApp.getState().preferences.projectMode).toBe("station");
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g7");
  });

  it("leaves the highlight alone without a run", () => {
    useGlobalAgents.setState({ focus: null });
    act(() => openGlobalAgents());
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBeNull();
  });

  it("goes back to the project's tab when a goal is opened", () => {
    useStation.setState({ tab: "global" });
    useStation.getState().focusGoal("goal", "p");
    expect(useStation.getState().tab).toBe("project");
  });
});
