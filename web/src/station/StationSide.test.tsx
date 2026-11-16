import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { api, type AgentMessage, type Runner, type UsageReport } from "../api";
import type { World } from "../game/world";
import { localRunner, startingRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useWorkspace } from "../runs/workspaceStore";
import { useApp } from "../store";
import { useUsage } from "../usage/usageStore";
import { StationSide } from "./StationSide";

const world = {
  building: {
    actors: [
      {
        id: "a1",
        agent: "claude",
        model: "opus",
        pose: "working",
        area: "work",
        runnerName: "mac-mini",
        nodeId: "api",
        runId: "",
        nodeTitle: "Auth API",
      },
    ],
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
      { id: "m1", from: "a", to: "b", body: "hi", createdAt: 1, senderAgent: "claude" } as AgentMessage,
      { id: "m2", from: "b", to: "a", body: "yo", createdAt: 2, senderAgent: "codex" } as AgentMessage,
    ],
    seenAt: 0,
    seenProject: "p",
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
    expect(within(roster).getByText("Work area · mac-mini")).toBeInTheDocument();
  });

  // Simple: no token counts, and the built-in runner is this computer.
  it("leaves token counts out of the roster in Simple and says this computer", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
    const here = {
      building: {
        actors: [
          {
            ...(world.building.actors[0] as object),
            runnerName: "this machine",
            energy: { used: 12000, cap: 0 },
          },
        ],
      },
    } as unknown as World;
    render(
      <EditionProvider edition="single">
        <StationSide world={here} />
      </EditionProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /1 working · 1 in all/ }));
    const roster = screen.getByRole("list", { name: "Agents" });
    expect(roster.textContent).not.toMatch(/tokens|this machine/);
    expect(within(roster).getByText("Work area · This computer")).toBeInTheDocument();
  });

  it("keeps token counts in the roster in full mode", () => {
    const here = {
      building: { actors: [{ ...(world.building.actors[0] as object), energy: { used: 12000, cap: 0 } }] },
    } as unknown as World;
    render(
      <EditionProvider edition="single">
        <StationSide world={here} />
      </EditionProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /1 working · 1 in all/ }));
    expect(screen.getByRole("list", { name: "Agents" }).textContent).toMatch(/tokens/);
  });

  it("counts a global agent in the roster, and opens its workspace (it has no node)", () => {
    const openWorkspace = vi.fn();
    useWorkspace.setState({ openWorkspace });
    const withGlobal = {
      building: {
        actors: [
          ...world.building.actors,
          {
            id: "run:g1",
            agent: "codex",
            model: "gpt-5",
            pose: "working",
            area: "work",
            runnerName: "mac-mini",
            runId: "g1",
            nodeTitle: "Bump the deps",
            global: true,
          },
        ],
      },
    } as unknown as World;
    render(<StationSide world={withGlobal} />);
    fireEvent.click(screen.getByRole("button", { name: /2 working · 2 in all/ }));
    const roster = screen.getByRole("list", { name: "Agents" });
    const line = within(roster).getByRole("button", { name: /Codex gpt-5.*Bump the deps/ });
    expect(line).toBeEnabled();
    fireEvent.click(line);
    expect(openWorkspace).toHaveBeenCalledWith("g1", undefined);
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

  it("names the hub's own runner in the reader's language", () => {
    useApp.setState({ preferences: { ...useApp.getState().preferences, language: "zh-TW" } });
    useRunners.setState({ runners: [{ id: "r0", name: "this machine", online: true } as never] });
    render(<StationSide world={world} />);
    const card = screen.getByRole("region", { name: "Runner" });
    expect(within(card).getByText("本機")).toBeInTheDocument();
    expect(within(card).queryByText("this machine")).not.toBeInTheDocument();
  });

  // Single-user: the card read "Runners / this machine / Offline / Manage"
  // for a reader who never added a runner.
  it("is This computer in the single-user edition, saying how it is doing", () => {
    const single = (runners: Runner[]) => {
      useRunners.setState({ runners });
      const view = render(
        <EditionProvider edition="single">
          <StationSide world={world} />
        </EditionProvider>,
      );
      const card = screen.getByRole("region", { name: "This computer" });
      return { card, text: card.textContent ?? "", unmount: view.unmount };
    };

    const ready = single([localRunner()]);
    expect(within(ready.card).getByText("Ready")).toBeInTheDocument();
    expect(ready.card.querySelector(".station-dot.status-running")).not.toBeNull();
    expect(ready.text).not.toMatch(/runner|this machine|Online|Offline/i);
    fireEvent.click(within(ready.card).getByRole("button", { name: "Settings" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    ready.unmount();

    const starting = single([startingRunner()]);
    expect(within(starting.card).getByText("Starting…")).toBeInTheDocument();
    expect(starting.text).not.toMatch(/runner|this machine|Offline/i);
    starting.unmount();

    const stopped = single([{ ...localRunner(), online: false }]);
    expect(within(stopped.card).getByText("Not running")).toBeInTheDocument();
    expect(stopped.card.querySelector(".station-dot.is-off")).not.toBeNull();
    expect(stopped.text).not.toMatch(/runner|this machine|Offline/i);
    stopped.unmount();

    // A runner connected by hand beside it keeps its own line.
    const both = single([localRunner(), { id: "r9", name: "laptop", online: false } as Runner]);
    expect(within(both.card).getByText("Ready")).toBeInTheDocument();
    expect(within(both.card).getByText("laptop")).toBeInTheDocument();
    expect(within(both.card).getByText("Offline")).toBeInTheDocument();
  });

  it("says how an agent gets on the roster, without a runner to add, in the single-user edition", () => {
    const nobody = { building: { actors: [] } } as unknown as World;
    render(
      <EditionProvider edition="single">
        <StationSide world={nobody} />
      </EditionProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /0 working · 0 in all/ }));
    expect(screen.getByText("No agents yet. Send a task to an agent and it shows up here.")).toBeInTheDocument();
  });

  it("keeps Agent chat folded to what is unread until opened, and reads it when open", () => {
    render(<StationSide world={world} />);
    const toggle = screen.getByRole("button", { name: /^Agent chat/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("2 unread · 0 online");
    expect(screen.queryByRole("region", { name: "Agent chat" })).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const panel = screen.getByRole("region", { name: "Agent chat" });
    // Its own controls live inside the card.
    expect(within(panel).getByRole("checkbox", { name: /offline/i })).toBeInTheDocument();
    expect(within(panel).getByRole("combobox")).toBeInTheDocument();
    // Open, what it shows is read.
    expect(toggle).toHaveTextContent("0 unread · 0 online");
  });

  it("unfolds when something asks for the agent panel", () => {
    render(<StationSide world={world} />);
    expect(screen.queryByRole("region", { name: "Agent chat" })).not.toBeInTheDocument();
    act(() => useAgents.getState().setOpen(true));
    expect(screen.getByRole("region", { name: "Agent chat" })).toBeInTheDocument();
    // Consumed once: the next Ctrl+Shift+A asks again rather than clearing
    // it, and no floating panel waits on the board.
    expect(useAgents.getState().open).toBe(false);
    act(() => useAgents.getState().toggleOpen());
    expect(useAgents.getState().open).toBe(false);
    expect(screen.getByRole("region", { name: "Agent chat" })).toBeInTheDocument();
  });
});

// At 1280x800 the side column is pinned to Station's height and scrolls on
// its own once the open agent chat no longer fits, right beside Station's own
// scrollbar: two bars 30px apart. A column taller than Station now lets go of
// its pin and scrolls with the rest, so there is one scrollbar.
describe("StationSide in a Station too short for it", () => {
  let observed: Array<() => void> = [];
  beforeEach(() => {
    observed = [];
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private callback: () => void) {
          observed.push(() => this.callback());
        }
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function sized(station: number, side: number) {
    const host = document.createElement("div");
    host.className = "station";
    Object.defineProperty(host, "clientHeight", { configurable: true, get: () => station });
    document.body.appendChild(host);
    const view = render(<StationSide world={world} />, { container: host });
    const column = host.querySelector(".station-side") as HTMLElement;
    let content = side;
    Object.defineProperty(column, "scrollHeight", { configurable: true, get: () => content });
    return {
      column,
      resize(next: number) {
        content = next;
        act(() => observed.forEach((fire) => fire()));
      },
      unmount() {
        view.unmount();
        host.remove();
      },
    };
  }

  it("scrolls with Station instead of on its own once it is taller", () => {
    const station = sized(700, 600);
    station.resize(600);
    expect(station.column).not.toHaveClass("is-overflowing");
    station.resize(820);
    expect(station.column).toHaveClass("is-overflowing");
    station.resize(640);
    expect(station.column).not.toHaveClass("is-overflowing");
    station.unmount();
  });
});
