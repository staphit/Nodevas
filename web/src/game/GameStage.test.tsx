import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { useSessions } from "../runs/sessionStore";
import { useStation } from "../station/stationStore";
import { useApp } from "../store";
import { ARM_MS } from "./dungeon/Dungeon";
import GameStage from "./GameStage";
import { RUN_WORKING, seedScene, type SceneSpies } from "./testScene";
import { makeRun, NOW, runId } from "./testFixtures";

function stubMatchMedia(reduced: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: reduced && query.includes("prefers-reduced-motion"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
}

let spies: SceneSpies;

beforeEach(() => {
  useApp.getState().updateUIPreference("language", "en");
  useApp.getState().updateUIPreference("agentWorkstation", true);
  spies = seedScene();
  stubMatchMedia(false);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function stage(skin: "warroom" | "dungeon") {
  return render(<GameStage skin={skin} />);
}

/** Opens a sprite's menu and picks an item. */
function pick(sprite: HTMLElement, item: RegExp | string) {
  fireEvent.click(sprite);
  fireEvent.click(screen.getByRole("menuitem", { name: item }));
}

describe("war room", () => {
  it("draws a room per runner and the terminal room, a character per run and loose session", () => {
    stage("warroom");
    expect(screen.getByRole("region", { name: "Room mac-mini" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Room build-box" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Room Terminals" })).toBeInTheDocument();

    const claude = screen.getByRole("button", { name: /^Claude opus in mac-mini: working, on “Auth API”/ });
    expect(claude).toHaveAttribute("data-pose", "working");
    expect(claude.getAttribute("aria-label")).toMatch(/Says: “Schema is ready\.”/);
    expect(claude.getAttribute("aria-label")).toMatch(/50k of 1\.0M tokens/);
    expect(screen.getByRole("button", { name: /Codex gpt-5 in mac-mini: waiting to be accepted, on “Login form”/ })).toHaveClass(
      "queue",
    );
    const asking = screen.getByRole("button", { name: /Claude in Terminals: waiting for your answer, on “Deploy”/ });
    expect(asking.getAttribute("aria-label")).toMatch(/Asks: “May I drop the table\?”.*1 held message/);
    // The office's energy bar is the day's budget.
    expect(screen.getByRole("meter", { name: "Tokens today" })).toHaveAttribute("aria-valuenow", "250000");
  });

  it("opens the run in the drawer from a character's menu", () => {
    stage("warroom");
    pick(screen.getByRole("button", { name: /Claude opus in mac-mini/ }), "Open run");
    expect(spies.showRuns).toHaveBeenCalledWith("api", RUN_WORKING);
    expect(spies.openTab).toHaveBeenCalledWith("api");
  });

  it("opens the node, the run's diff and the session's diff", async () => {
    stage("warroom");
    pick(screen.getByRole("button", { name: /Claude opus in mac-mini/ }), "Open “Auth API”");
    expect(spies.openTab).toHaveBeenCalledWith("api");

    pick(screen.getByRole("button", { name: /Claude opus in mac-mini/ }), "View changes");
    expect(spies.loadChanges).toHaveBeenCalledWith(RUN_WORKING, "abc123");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // The external session's diff comes from its node's session changes.
    spies.loadSessions.mockImplementation(async () => {
      useSessions.setState({
        byNode: {
          deploy: [
            {
              sessionId: "s1",
              nodeId: "deploy",
              agent: "claude",
              sessionName: "laptop-claude",
              files: ["a.go"],
              updatedAt: "2026-10-26T11:59:00Z",
              snapshot: { commit: "", base: "", files: [{ path: "a.go", status: "modified", additions: 1, deletions: 0 }], additions: 1, deletions: 0, patch: "" },
            } as never,
          ],
        },
      });
    });
    pick(screen.getByRole("button", { name: /Claude in Terminals/ }), "View session changes");
    expect(spies.loadSessions).toHaveBeenCalledWith("deploy");
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("answers an ask and decides a held message in place", async () => {
    stage("warroom");
    pick(screen.getByRole("button", { name: /Claude in Terminals/ }), "Answer question");
    const ask = screen.getByRole("dialog", { name: "Answer question" });
    expect(within(ask).getByText("May I drop the table?")).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    pick(screen.getByRole("button", { name: /Claude in Terminals/ }), "Review held message");
    const held = screen.getByRole("dialog", { name: "Review held message" });
    expect(within(held).getByText("Rebase first.")).toBeInTheDocument();
    fireEvent.click(within(held).getByRole("button", { name: "Approve and deliver" }));
    expect(spies.decideHeld).toHaveBeenCalledWith("held1", "approve");
  });

  it("follows a PR link only over http(s), without an opener", () => {
    const open = vi.fn();
    vi.stubGlobal("open", open);
    useRuns.setState({
      activeRuns: [
        ...useRuns.getState().activeRuns,
        makeRun({ id: runId("b9"), nodeId: "docs", status: "failed", prUrl: "https://github.com/x/y/pull/9", prState: "open", ciStatus: "failure" }),
      ],
    });
    stage("warroom");
    pick(screen.getByRole("button", { name: /in mac-mini: failed, on “API docs”/ }), "Open pull request");
    expect(open).toHaveBeenCalledWith("https://github.com/x/y/pull/9", "_blank", "noopener,noreferrer");
  });

  it("changes a character's pose when the store changes", () => {
    stage("warroom");
    const claude = () => screen.getByRole("button", { name: /Claude opus in mac-mini/ });
    expect(claude()).toHaveAttribute("data-pose", "working");
    act(() => {
      useRuns.setState({
        activeRuns: useRuns.getState().activeRuns.map((run) =>
          run.id === RUN_WORKING ? { ...run, status: "failed" } : run,
        ),
      });
    });
    expect(claude()).toHaveAttribute("data-pose", "failed");
  });

  it("shows the same world as a list, with the same actions", () => {
    stage("warroom");
    fireEvent.click(screen.getByRole("button", { name: "List view" }));
    const item = screen.getByText(/^Claude opus in mac-mini: working, on “Auth API”/).closest("li")!;
    fireEvent.click(within(item).getByRole("button", { name: "Open run" }));
    expect(spies.showRuns).toHaveBeenCalledWith("api", RUN_WORKING);
  });

  it("explains how to bring the workstation back when it is hidden", () => {
    useApp.getState().updateUIPreference("agentWorkstation", false);
    useRunners.setState({ runners: [] });
    useRuns.setState({ latestByNode: {} });
    useAgents.setState({ sessions: [], asks: [], held: [] });
    stage("warroom");
    expect(screen.getByText(/The war room shows your agent workstation/)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: /^Room / })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Enable agent workstation" }));
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });

  it("holds still for reduced motion", () => {
    stubMatchMedia(true);
    const { container } = stage("warroom");
    expect(container.querySelector(".game-stage")).toHaveAttribute("data-motion", "reduced");
  });

  it("switches skins from its own header", () => {
    stage("warroom");
    fireEvent.click(screen.getByRole("button", { name: "Boss map" }));
    expect(useApp.getState().preferences.stageSkin).toBe("dungeon");
    expect(useStation.getState().liveView).toBe("game");
  });

  it("leaves the switch to Station when it holds it, and draws the list it is told to", () => {
    const { rerender } = render(<GameStage skin="warroom" controls={false} />);
    expect(screen.queryByRole("group", { name: "Board view" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "List view" })).not.toBeInTheDocument();
    expect(screen.queryByText(/^Claude opus in mac-mini: working/)).not.toBeInTheDocument();
    rerender(<GameStage skin="warroom" controls={false} listView />);
    expect(screen.getByText(/^Claude opus in mac-mini: working, on “Auth API”/)).toBeInTheDocument();
  });
});

describe("boss map", () => {
  it("draws the boss, monsters by state, the door and the party", () => {
    stage("dungeon");
    expect(screen.getByRole("meter", { name: "Boss HP" })).toHaveAttribute("aria-valuenow", "4");
    expect(screen.getByRole("meter", { name: "Boss HP" })).toHaveAttribute("aria-valuemax", "5");
    expect(screen.getByRole("button", { name: /^Monster “DB schema”: defeated/ })).toHaveAttribute("data-state", "defeated");
    expect(screen.getByRole("button", { name: /^Monster “Auth API”: attackable.*An agent is fighting it/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Monster “Login form”: locked\. Locked by Auth API/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Door \(AND\) to “Deploy”: shut by Auth API, API docs/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Autopilot party for “Deploy”: 1 of 3 done\. Advancing\. At Auth API/ })).toBeInTheDocument();
    expect(screen.getByText("XP 1")).toBeInTheDocument();
  });

  it("sends each sprite to the real thing", () => {
    stage("dungeon");
    pick(screen.getByRole("button", { name: /^Monster “Login form”/ }), "Open blocker “Auth API”");
    expect(spies.openTab).toHaveBeenLastCalledWith("api");

    // Attacking never dispatches on a click: it opens the dispatch dialog.
    pick(screen.getByRole("button", { name: /^Monster “Auth API”/ }), "Dispatch an agent…");
    expect(spies.openDispatch).toHaveBeenCalledWith("api");

    fireEvent.click(screen.getByRole("button", { name: /^Door \(AND\)/ }));
    expect(spies.openTab).toHaveBeenLastCalledWith("api");

    // A party opens its goal in Station, where Autopilot lives.
    fireEvent.click(screen.getByRole("button", { name: /^Autopilot party/ }));
    expect(useStation.getState().goal).toBe("deploy");
    expect(useApp.getState().preferences.projectMode).toBe("station");

    pick(screen.getByRole("button", { name: /^Boss main/ }), "Open “Auth API”");
    expect(spies.openTab).toHaveBeenLastCalledWith("api");
  });

  it("offers no dispatch without the workstation, and lists by state", () => {
    useApp.getState().updateUIPreference("agentWorkstation", false);
    useRunners.setState({ runners: [] });
    useRuns.setState({ latestByNode: {}, activeRuns: [] });
    useAgents.setState({ sessions: [], asks: [], held: [] });
    useApp.setState({ statuses: { ...useApp.getState().statuses, api: "ready" } });
    stage("dungeon");
    fireEvent.click(screen.getByRole("button", { name: /^Monster “Auth API”/ }));
    expect(screen.queryByRole("menuitem", { name: "Dispatch an agent…" })).not.toBeInTheDocument();
    // Without agents the map still moves: a node can be started by hand.
    fireEvent.click(screen.getByRole("menuitem", { name: "Mark as started" }));
    expect(spies.setLifecycleStatus).toHaveBeenCalledWith("api", "started");

    fireEvent.click(screen.getByRole("button", { name: "List view" }));
    const locked = screen.getByRole("region", { name: "Locked" });
    expect(within(locked).getByText(/Monster “Login form”: locked\. Locked by Auth API/)).toBeInTheDocument();
    fireEvent.click(within(locked).getAllByRole("button", { name: "Open blocker “Auth API”" })[0]);
    expect(spies.openTab).toHaveBeenLastCalledWith("api");
  });

  it("shows +XP when a PR merges while the map is open, and says so", () => {
    vi.useFakeTimers({ now: NOW });
    stage("dungeon");
    expect(screen.queryByText("+XP")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(ARM_MS + 10);
    });
    act(() => {
      useRuns.setState({
        latestByNode: {
          ...useRuns.getState().latestByNode,
          docs: makeRun({ id: runId("c1"), nodeId: "docs", status: "succeeded", prUrl: "https://x/y/pull/1", prState: "merged" }),
        },
      });
    });
    expect(screen.getByText("+XP")).toBeInTheDocument();
    expect(screen.getByText(/Pull request merged: API docs/)).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(3_000);
    });
    expect(screen.queryByText("+XP")).not.toBeInTheDocument();
  });
});
