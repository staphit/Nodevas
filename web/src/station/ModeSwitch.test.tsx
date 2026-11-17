import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AgentAsk, type Run, type RunBudgetStatus } from "../api";
import { useAgents } from "../agents/agentStore";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useAutopilot, type WaitingTask } from "../autopilot/autopilotStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { ModeSwitch } from "./ModeSwitch";
import { useStation } from "./stationStore";

const ask = (over: Partial<AgentAsk>): AgentAsk =>
  ({ id: "a", project: "p", sessionId: "s", sessionName: "s", actorId: "ann", question: "?", status: "pending", createdAt: 1, ...over }) as AgentAsk;

// A run finished a minute ago with a green PR: one "ready to merge".
const mergeable = (over: Partial<Run> = {}): Run =>
  ({
    id: "r1",
    nodeId: "n1",
    status: "succeeded",
    prUrl: "https://example.test/pr/1",
    prState: "open",
    ciStatus: "success",
    createdAt: new Date(Date.now() - 120_000).toISOString(),
    finishedAt: new Date(Date.now() - 60_000).toISOString(),
    ...over,
  }) as Run;

beforeEach(() => {
  useApp.setState({
    activeProject: "p",
    preferences: { ...readPreferences(), language: "en", projectMode: "info", agentWorkstation: false },
  });
  useAgents.setState({ asks: [], held: [], viewer: { id: "ann", name: "ann", role: "member" } });
  useStation.setState({ runs: [], project: "", loaded: false });
  useBudget.setState({ status: null });
  useAutopilot.setState({ waiting: [] });
  useRuns.setState({ activeRuns: [], globalById: {} });
  useRunners.setState({ runners: [], loaded: false });
  useGlobalAgents.setState({ runs: [], loaded: true });
});

afterEach(() => vi.restoreAllMocks());

describe("ModeSwitch", () => {
  it("switches between Board and Station, and Station turns the workstation on", () => {
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Board" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Station" }));
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("station");
    expect(prefs.agentWorkstation).toBe(true);
  });

  // Settings is entered from the account menu; there both halves are the way
  // back, so neither is pressed.
  it("switches between Board, Station and Settings, one pressed at a time", () => {
    render(<ModeSwitch />);
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(useApp.getState().preferences.projectMode).toBe("settings");
    expect(screen.getByRole("button", { name: "Settings" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Board" }).getAttribute("aria-pressed")).toBe("false");
    expect(screen.getByRole("button", { name: "Station" }).getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "Board" }));
    expect(useApp.getState().preferences.projectMode).toBe("info");
  });

  it("counts only the questions this viewer may answer", () => {
    useAgents.setState({
      asks: [ask({}), ask({ id: "b", actorId: "carol" }), ask({ id: "c", status: "approved" })],
    });
    render(<ModeSwitch />);
    const station = screen.getByRole("button", { name: "Station, 1 waiting on you" });
    expect(station.querySelector(".mode-switch-badge")?.textContent).toBe("1");
  });

  it("counts by the hub's canAnswer when the listing carries it", () => {
    useAgents.setState({
      viewer: { id: "ann", name: "ann", role: "admin" },
      asks: [
        ask({ id: "a", actorId: "carol", kind: "permission", canAnswer: false }),
        ask({ id: "b", actorId: "carol", canAnswer: true }),
        ask({ id: "c", actorId: "carol", kind: "permission" }),
      ],
    });
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Station, 1 waiting on you" })).toBeInTheDocument();
  });

  it("adds the runs and the budget Station's waiting strip lists", () => {
    useStation.setState({ runs: [mergeable()], project: "p", loaded: true });
    useBudget.setState({ status: { exceeded: "cost" } as unknown as RunBudgetStatus });
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Station, 2 waiting on you" })).toBeTruthy();
  });

  // Runs Station loaded for another project are not this board's to count.
  it("ignores runs loaded for another project", () => {
    useStation.setState({ runs: [mergeable()], project: "other", loaded: true });
    render(<ModeSwitch />);
    const station = screen.getByRole("button", { name: "Station" });
    expect(station.querySelector(".mode-switch-badge")).toBeNull();
  });

  // The badge counts what the "Waiting on you" card lists, reviews and offers
  // included.
  it("counts autopilot reviews and offers no runner took yet", () => {
    useAutopilot.setState({
      waiting: [{ autopilotId: "ap", goalNodeId: "g", task: { nodeId: "t", title: "T" } } as unknown as WaitingTask],
    });
    useRuns.setState({
      activeRuns: [
        { id: "o1", nodeId: "n2", status: "offered" } as Run,
        { id: "o2", nodeId: "n3", status: "running" } as Run,
      ],
    });
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Station, 2 waiting on you" })).toBeTruthy();
  });

  // Single-user edition: a result on this computer waits to be applied, from
  // the project's runs and the global agents alike, each counted once.
  it("counts the results waiting to be applied, in the single-user edition only", () => {
    const result = (over: Partial<Run>) =>
      ({
        id: "x",
        nodeId: "n1",
        runnerId: "local1",
        status: "succeeded",
        commit: "abc",
        workspace: "",
        createdAt: new Date(Date.now() - 120_000).toISOString(),
        finishedAt: new Date(Date.now() - 60_000).toISOString(),
        ...over,
      }) as Run;
    const global = result({ id: "g1", nodeId: "", global: true });
    useRunners.setState({ runners: [localRunner()], loaded: true });
    useStation.setState({ runs: [result({ id: "p1" })], project: "p", loaded: true });
    useGlobalAgents.setState({ runs: [global] });
    useRuns.setState({ globalById: { g1: global } });
    const { unmount } = render(
      <EditionProvider edition="single">
        <ModeSwitch />
      </EditionProvider>,
    );
    expect(screen.getByRole("button", { name: "Station, 2 waiting on you" })).toBeTruthy();
    unmount();
    render(<ModeSwitch />);
    expect(screen.getByRole("button", { name: "Station" }).querySelector(".mode-switch-badge")).toBeNull();
  });

  // Reopened on the board, Station never mounted: the global runs are loaded
  // once for the badge, so a result that finished meanwhile is counted.
  it("loads the global runs once in the single-user edition, for a result waiting since the last visit", async () => {
    const finished = {
      id: "g1",
      nodeId: "",
      global: true,
      runnerId: "local1",
      status: "succeeded",
      commit: "abc",
      workspace: "",
      createdAt: new Date(Date.now() - 120_000).toISOString(),
      finishedAt: new Date(Date.now() - 60_000).toISOString(),
    } as Run;
    useRunners.setState({ runners: [localRunner()], loaded: true });
    useGlobalAgents.setState({ runs: [], loaded: false });
    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [finished], runners: [], more: false });
    render(
      <EditionProvider edition="single">
        <ModeSwitch />
      </EditionProvider>,
    );
    expect(await screen.findByRole("button", { name: "Station, 1 waiting on you" })).toBeTruthy();
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("loads no global runs on a team hub", () => {
    useGlobalAgents.setState({ runs: [], loaded: false });
    const list = vi.spyOn(api, "listGlobalRuns");
    render(<ModeSwitch />);
    expect(list).not.toHaveBeenCalled();
  });
});
