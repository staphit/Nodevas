import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { api, type AgentMessage, type Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { GameActionsProvider } from "../game/GameActions";
import { readPreferences } from "../preferences";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { StationWaiting } from "./StationWaiting";
import { useStation } from "./stationStore";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run1",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha-run1",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: new Date(Date.now() - 20 * 60_000).toISOString(),
    finishedAt: new Date(Date.now() - 10 * 60_000).toISOString(),
    ...overrides,
  };
}

beforeEach(() => {
  useApp.setState({
    activeProject: "p",
    preferences: { ...readPreferences(), language: "en" },
    graph: {
      nodes: [
        { id: "alpha", title: "Fix the login" },
        { id: "beta", title: "Add a test" },
        { id: "goal", title: "Ship login" },
      ],
      edges: [],
    } as never,
  });
  useAgents.setState({ asks: [], held: [], viewer: null });
  useAutopilot.getState().reset();
  useStation.getState().reset();
  useRuns.getState().reset();
  useBudget.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

function mount() {
  return render(
    <GameActionsProvider>
      <StationWaiting />
      <ConfirmDialogHost />
    </GameActionsProvider>,
  );
}

describe("StationWaiting", () => {
  it("says nothing waits, in a card that does not call for attention", () => {
    const { container } = mount();
    expect(screen.getByRole("heading", { name: "Waiting on you" })).toBeInTheDocument();
    expect(screen.getByText(/Nothing waits on you/)).toBeInTheDocument();
    expect(container.querySelector(".station-waiting")).not.toHaveClass("is-attention");
  });

  it("lists every kind of wait with its one primary action", () => {
    useBudget.setState({
      status: { budget: { dailyRuns: 0, dailyCostUsd: 5, dailyTokens: 0 }, used: { runs: 3, costUsd: 5.2, tokens: 0 }, exceeded: "cost" },
    });
    useAgents.setState({
      asks: [
        {
          id: "ask1",
          project: "p",
          sessionId: "s1",
          sessionName: "laptop-claude",
          actorId: "u1",
          question: "May I drop the table?",
          nodeId: "beta",
          status: "pending",
          createdAt: 1,
        },
      ],
      held: [{ id: "h1", from: "a/codex", to: "b/claude", body: "Rebase first." } as AgentMessage],
    });
    useAutopilot.setState({
      waiting: [
        {
          autopilotId: "ap1",
          goalNodeId: "goal",
          task: { nodeId: "beta", title: "Add a test", state: "waiting_human" } as never,
        },
      ],
    });
    useStation.setState({
      runs: [
        makeRun({ id: "m", prUrl: "https://x/pull/7", prNumber: 7, prState: "open", ciStatus: "success" }),
        makeRun({ id: "c", nodeId: "beta", prUrl: "https://x/pull/8", prNumber: 8, prState: "open", ciStatus: "failure" }),
      ],
    });
    useRuns.setState({ activeRuns: [makeRun({ id: "o", nodeId: "goal", status: "offered", finishedAt: undefined })] });
    const { container } = mount();

    expect(container.querySelector(".station-waiting")).toHaveClass("is-attention");
    const items = within(screen.getByRole("list")).getAllByRole("listitem");
    expect(items.map((item) => item.firstChild?.firstChild?.textContent)).toEqual([
      "Today's budget is used up (Estimated cost): new runs are refused until 00:00 UTC.",
      "laptop-claude asks: May I drop the table?",
      "Held message: a/codex → b/claude",
      "Review “Add a test”",
      "PR #7 is ready to merge: Fix the login",
      "CI failed on PR #8: Add a test",
      "Waiting for desktop to accept: Ship login",
    ]);
    expect(container.querySelector(".station-count")).toHaveTextContent("7");
    expect(within(items[1]).getByRole("button", { name: "Answer" })).toBeInTheDocument();
    expect(within(items[5]).getAllByRole("button").map((b) => b.textContent)).toEqual(["Open run", "Open PR"]);
    expect(within(items[6]).getAllByRole("button").map((b) => b.textContent)).toEqual(["Open run", "Withdraw"]);
  });

  it("answers a question in place and opens the budget's settings", async () => {
    useAgents.setState({
      asks: [
        {
          id: "ask1",
          project: "p",
          sessionId: "s1",
          sessionName: "laptop-claude",
          actorId: "u1",
          question: "May I drop the table?",
          status: "pending",
          createdAt: 1,
        },
      ],
    });
    useBudget.setState({
      status: { budget: { dailyRuns: 1, dailyCostUsd: 0, dailyTokens: 0 }, used: { runs: 1, costUsd: 0, tokens: 0 }, exceeded: "runs" },
    });
    mount();

    fireEvent.click(screen.getByRole("button", { name: "Answer" }));
    const dialog = screen.getByRole("dialog", { name: "Answer question" });
    expect(within(dialog).getAllByText("May I drop the table?").length).toBeGreaterThan(0);
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Adjust budget" }));
    expect(useApp.getState().preferences.settingsSection).toBe("budget");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
  });

  it("withdraws an offer only after the Runs tab's question", async () => {
    useRuns.setState({ activeRuns: [makeRun({ id: "o", status: "offered", finishedAt: undefined })] });
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue(makeRun({ id: "o", status: "cancelled" }));
    mount();

    fireEvent.click(screen.getByRole("button", { name: "Withdraw" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(cancel).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel run" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledWith("o"));
  });
});
