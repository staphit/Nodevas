import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { api, type AgentMessage, type Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { GameActionsProvider } from "../game/GameActions";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { makeInfo, makeView } from "../runs/workspaceFixtures";
import { useWorkspace } from "../runs/workspaceStore";
import { useApp } from "../store";
import { useGlobalAgents } from "./globalAgentsStore";
import { forgetKeptResults } from "./keptResults";
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
  useRunners.setState({ runners: [], loaded: false });
  useGlobalAgents.setState({ runs: [], loaded: true });
  useWorkspace.getState().reset();
  forgetKeptResults();
});

afterEach(() => {
  vi.restoreAllMocks();
});

function mount(edition: Edition = "team") {
  return render(
    <EditionProvider edition={edition}>
      <GameActionsProvider>
        <StationWaiting />
        <ConfirmDialogHost />
      </GameActionsProvider>
    </EditionProvider>,
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
    expect(items.map((item) => item.querySelector(".station-tile-title")?.textContent)).toEqual([
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

  it("leaves out an ask the hub says the viewer may not answer, admin or not", () => {
    useAgents.setState({
      viewer: { id: "root", name: "root", role: "admin" },
      asks: [
        {
          id: "ask-p2",
          project: "p",
          sessionId: "s1",
          sessionName: "laptop-claude",
          actorId: "u1",
          kind: "permission",
          tool: "Bash",
          question: "Allow Bash?",
          status: "pending",
          createdAt: 1,
          canAnswer: false,
        },
      ],
    });
    mount();
    expect(screen.queryByText("laptop-claude wants to run Bash")).toBeNull();
  });

  it("lists permission asks and opens answer dialog", async () => {
    useAgents.setState({
      asks: [
        {
          id: "ask-p1",
          project: "p",
          sessionId: "s1",
          sessionName: "laptop-claude",
          actorId: "u1",
          kind: "permission",
          tool: "Bash",
          toolInput: "npm test",
          runId: "run-1",
          question: "Run command?",
          status: "pending",
          createdAt: 1,
        },
      ],
    });
    mount();

    expect(screen.getByText("laptop-claude wants to run Bash")).toBeInTheDocument();
    expect(screen.getByText("npm test")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Answer" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByRole("button", { name: /Allow for this run/ })).toBeInTheDocument();
  });

  // Single-user edition: a finished result on this computer waits for the
  // person to look at it and apply it, in plain words.
  describe("results to apply", () => {
    const result = (overrides: Partial<Run> = {}) =>
      makeRun({ runnerId: "local1", runnerName: "this machine", commit: "c0ffee", workspace: "", ...overrides });

    it("shows a card with both plain buttons in the single-user edition", () => {
      useRunners.setState({ runners: [localRunner()], loaded: true });
      useStation.setState({ runs: [result()], project: "p", loaded: true });
      useGlobalAgents.setState({ runs: [result({ id: "g1", nodeId: "", global: true, title: "Bakery page" })] });
      mount("single");
      const card = screen.getByText("Ready for you: Fix the login").closest(".station-waiting-item") as HTMLElement;
      expect(within(card).getByText("Claude opus finished. Take a look, then apply it to your folder.")).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Take a look" })).toBeInTheDocument();
      expect(within(card).getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
      const global = screen.getByText("Ready for you: Bakery page").closest(".station-waiting-item") as HTMLElement;
      expect(within(global).getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
    });

    // Applying removes the result from what waits; the card stays where the
    // person pressed it, saying it was applied, with Open my folder.
    it.each([
      ["a project result", false],
      ["a global result", true],
    ])("keeps %s in place once applied, with Open my folder, until dismissed", async (_what, global) => {
      useRunners.setState({ runners: [localRunner()], loaded: true });
      const ready = global ? result({ id: "g1", nodeId: "", global: true, title: "Bakery page" }) : result();
      const applied = { ...ready, workspace: "merged_local" as const };
      if (global) useGlobalAgents.setState({ runs: [ready] });
      else useStation.setState({ runs: [ready], project: "p", loaded: true });
      vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView({ info: makeInfo({ baseCheckout: "repo" }) }));
      vi.spyOn(api, "getRun").mockResolvedValue(applied);
      vi.spyOn(api, "mergeRunLocal").mockImplementation(async () => {
        // The lists follow the hub too.
        if (global) useGlobalAgents.setState({ runs: [applied] });
        else useStation.setState({ runs: [applied] });
        return applied;
      });
      const open = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "D:\\code\\my-app" });
      const { container, unmount } = mount("single");
      const title = global ? "Ready for you: Bakery page" : "Ready for you: Fix the login";
      const card = screen.getByText(title).closest(".station-waiting-item") as HTMLElement;
      fireEvent.click(within(card).getByRole("button", { name: "Apply to my folder" }));
      fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Apply" }));

      const done = await screen.findByText("Applied to your folder");
      const kept = done.closest(".station-waiting-item") as HTMLElement;
      expect(kept).toBeInTheDocument();
      expect(kept).toHaveClass("is-applied");
      // Nothing waits any more: no count, no call for attention.
      expect(container.querySelector(".station-count")).toBeNull();
      expect(container.querySelector(".station-waiting")).not.toHaveClass("is-attention");
      fireEvent.click(within(kept).getByRole("button", { name: "Open my folder" }));
      await waitFor(() => expect(open).toHaveBeenCalledWith("D:\\code\\my-app"));

      // Still there after a look at Station's other tab.
      unmount();
      mount("single");
      const again = screen.getByText("Applied to your folder").closest(".station-waiting-item") as HTMLElement;
      fireEvent.click(within(again).getByRole("button", { name: "Got it" }));
      expect(screen.queryByText("Applied to your folder")).toBeNull();
      expect(screen.getByText(/Nothing waits on you/)).toBeInTheDocument();
    });

    it("forgets an applied result once Station is left", async () => {
      useRunners.setState({ runners: [localRunner()], loaded: true });
      useStation.setState({ runs: [result()], project: "p", loaded: true });
      const { unmount } = mount("single");
      expect(screen.getByText("Ready for you: Fix the login")).toBeInTheDocument();
      act(() => useStation.setState({ runs: [result({ workspace: "merged_local" })] }));
      expect(screen.getByText("Applied: Fix the login")).toBeInTheDocument();
      unmount();
      forgetKeptResults();
      mount("single");
      expect(screen.queryByText("Applied: Fix the login")).toBeNull();
      expect(screen.getByText(/Nothing waits on you/)).toBeInTheDocument();
    });

    it("shows no card on a team hub", () => {
      useRunners.setState({ runners: [localRunner()], loaded: true });
      useStation.setState({ runs: [result()], project: "p", loaded: true });
      mount("team");
      expect(screen.queryByText(/Ready for you/)).toBeNull();
      expect(screen.queryByRole("button", { name: "Apply to my folder" })).toBeNull();
    });
  });
});
