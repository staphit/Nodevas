import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { api, type AgentMessage, type UsageReport } from "../api";
import type { World } from "../game/world";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useApp } from "../store";
import { useUsage } from "../usage/usageStore";
import { StationSide } from "./StationSide";

const world = {
  office: {
    actors: [
      {
        id: "a1",
        agent: "claude",
        model: "opus",
        pose: "working",
        roomId: "room1",
        nodeId: "api",
        runId: "",
        nodeTitle: "Auth API",
      },
    ],
    rooms: [{ id: "room1", name: "mac-mini" }],
  },
} as unknown as World;

function day(key: string, costUsd: number) {
  return { key, costUsd } as UsageReport["byDay"][number];
}

beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    /* no storage */
  }
  useApp.setState({ activeProject: "p", preferences: { ...readPreferences(), language: "en" } });
  useUsage.setState({ loaded: false, report: null, error: null });
  vi.spyOn(api, "getUsage").mockResolvedValue({ byDay: [day("2026-10-26", 0.5), day("2026-10-27", 1.2)] } as UsageReport);
  useBudget.setState({
    status: { budget: { dailyRuns: 0, dailyCostUsd: 5, dailyTokens: 0 }, used: { runs: 2, costUsd: 1.2, tokens: 0 }, exceeded: "" },
  });
  useRunners.setState({
    loaded: true,
    loading: false,
    error: null,
    runners: [{ id: "r1", name: "mac-mini", online: true } as never, { id: "r2", name: "build-box", online: false } as never],
  });
  useAgents.setState({
    open: false,
    sessions: [],
    asks: [],
    held: [],
    messages: [
      { id: "m1", from: "a", to: "b", body: "hi", createdAt: 1 } as AgentMessage,
      { id: "m2", from: "b", to: "a", body: "yo", createdAt: 2 } as AgentMessage,
    ],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("StationSide", () => {
  it("shows today's spend against the daily budget, and links to Usage", async () => {
    render(<StationSide world={world} />);
    expect(await screen.findByText("$1.20")).toBeInTheDocument();
    const bar = screen.getByRole("progressbar", { name: "$1.20 of the $5.00 daily budget" });
    expect(bar).toHaveAttribute("max", "5");
    fireEvent.click(screen.getByRole("button", { name: "Details" }));
    expect(useApp.getState().preferences.settingsSection).toBe("usage");
  });

  it("says when there is no daily budget", async () => {
    useBudget.setState({
      status: { budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 }, used: { runs: 0, costUsd: 0, tokens: 0 }, exceeded: "" },
    });
    render(<StationSide world={world} />);
    expect(await screen.findByText("No daily budget")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("folds who is working into one line", () => {
    render(<StationSide world={world} />);
    const fold = screen.getByRole("button", { name: /1 working · 1 in all/ });
    expect(fold).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("list", { name: "Agents" })).not.toBeInTheDocument();
    fireEvent.click(fold);
    const roster = screen.getByRole("list", { name: "Agents" });
    expect(within(roster).getByRole("button", { name: /Claude opus/ })).toBeInTheDocument();
  });

  it("lists the runners, and manages them in Settings", () => {
    render(<StationSide world={world} />);
    const card = screen.getByRole("region", { name: "Runners" });
    expect(within(card).getByText("mac-mini")).toBeInTheDocument();
    expect(within(card).getByText("Online")).toBeInTheDocument();
    expect(within(card).getByText("Offline")).toBeInTheDocument();
    fireEvent.click(within(card).getByRole("button", { name: "Manage" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
  });

  it("keeps the agent messages folded to a count until opened", () => {
    render(<StationSide world={world} />);
    const toggle = screen.getByRole("button", { name: /Agent messages/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("2 messages · 0 online");
    expect(screen.queryByRole("region", { name: "Agent Network" })).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const panel = screen.getByRole("region", { name: "Agent Network" });
    // Its own controls live inside the card.
    expect(within(panel).getByRole("checkbox", { name: /offline/i })).toBeInTheDocument();
    expect(within(panel).getByRole("combobox")).toBeInTheDocument();
  });

  it("unfolds when something asks for the agent panel", () => {
    render(<StationSide world={world} />);
    expect(screen.queryByRole("region", { name: "Agent Network" })).not.toBeInTheDocument();
    act(() => useAgents.getState().setOpen(true));
    expect(screen.getByRole("region", { name: "Agent Network" })).toBeInTheDocument();
    // Consumed once: the next Ctrl+Shift+A asks again rather than clearing
    // it, and no floating panel waits on the board.
    expect(useAgents.getState().open).toBe(false);
    act(() => useAgents.getState().toggleOpen());
    expect(useAgents.getState().open).toBe(false);
    expect(screen.getByRole("region", { name: "Agent Network" })).toBeInTheDocument();
  });
});
