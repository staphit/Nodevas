import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { AgentAsk, Run, RunBudgetStatus } from "../api";
import { useAgents } from "../agents/agentStore";
import { readPreferences } from "../preferences";
import { useAutopilot, type WaitingTask } from "../autopilot/autopilotStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
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
  useRuns.setState({ activeRuns: [] });
});

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
  it("has no Settings half, and presses neither half in Settings", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, projectMode: "settings" } });
    render(<ModeSwitch />);
    expect(screen.queryByRole("button", { name: "Settings" })).toBeNull();
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
});
