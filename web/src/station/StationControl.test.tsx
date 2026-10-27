import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AutopilotRun, type AutopilotView, type Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { controlRows, formatElapsed, pauseAllPlan } from "./control";
import { useControl } from "./controlStore";
import type { World } from "../game/world";
import { useRunners } from "../runners/runnerStore";
import { StationControl, StationControlReport, StationPauseButton } from "./StationControl";
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
    status: "offered",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-27T08:00:00Z",
    ...overrides,
  };
}

function makePilot(overrides: Partial<AutopilotRun> = {}, taskRun = "run2"): AutopilotView {
  return {
    run: {
      id: "ap1",
      project: "p",
      goalNodeId: "goal",
      status: "running",
      policy: { review: { mode: "agents" }, maxParallel: 1, maxRounds: 2, autoMerge: false },
      counts: { total: 4, success: 1, running: 1, waiting: 0, failed: 0, pending: 2, skipped: 0 },
      createdBy: "u1",
      createdByName: "alice",
      createdAt: "2026-10-27T07:00:00Z",
      updatedAt: "2026-10-27T08:00:00Z",
      ...overrides,
    },
    tasks: [
      {
        nodeId: "beta",
        title: "Beta",
        state: "running",
        stage: "review",
        try: 1,
        runId: "impl-run",
        pipelineId: "pipe1",
        stages: [],
        updatedAt: "2026-10-27T08:00:00Z",
      },
      {
        nodeId: "gamma",
        title: "Gamma",
        state: "queued",
        stage: "queued",
        try: 1,
        runId: taskRun,
        stages: [],
        updatedAt: "2026-10-27T08:00:00Z",
      },
    ],
  };
}

const offered = makeRun();
const reviewing = makeRun({
  id: "run3",
  nodeId: "beta",
  kind: "review",
  status: "running",
  pipelineId: "pipe1",
  agent: "codex",
  model: "gpt-5",
  runnerName: "laptop",
  startedAt: "2026-10-27T08:01:00Z",
  usage: { inputTokens: 12000, outputTokens: 345, costUsd: 0.042 },
});

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
  useRuns.getState().reset();
  useAutopilot.getState().reset();
  useStation.getState().reset();
  useControl.setState({ pausing: false, result: null, error: "" });
  vi.spyOn(api, "listProjectRuns").mockResolvedValue({ runs: [] });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
  // A runner is there: the list, not the call to add one.
  useRunners.setState({ loaded: true, loading: false, error: null, runners: [{ id: "r1" } as never] });
  useStation.getState().setLiveView("list");
});

afterEach(() => {
  vi.restoreAllMocks();
});

function mount(world?: World) {
  return render(
    <>
      <StationPauseButton />
      <StationControlReport />
      <StationControl world={world} />
      <ConfirmDialogHost />
    </>,
  );
}

/** The "In progress" rows, in order. */
async function rows() {
  const list = await screen.findByRole("list", { name: "In progress" });
  return within(list).getAllByRole("listitem");
}

/** Opens a row's ⋯ menu. */
function menuOf(row: HTMLElement) {
  fireEvent.click(within(row).getByRole("button", { name: /^Actions for / }));
  return screen.getByRole("menu");
}

describe("StationControl", () => {
  it("lists the active runs and autopilots one line each, and a count", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing, offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    mount();

    await waitFor(async () => expect(await rows()).toHaveLength(3));
    const [first, second, pilot] = await rows();
    // Not started yet first, then running, then the autopilot.
    expect(within(first).getByText("Fix the login")).toBeInTheDocument();
    expect(within(first).getByText(/^Implement · Claude opus · desktop · /)).toBeInTheDocument();
    expect(within(first).getByText("Awaiting acceptance")).toBeInTheDocument();
    expect(within(second).getByText("Add a test")).toBeInTheDocument();
    // The review run is a step of the autopilot's task (by its pipeline).
    expect(
      await within(second).findByText(/^Review · Codex gpt-5 · laptop · .* · 12\.3K tokens · \$0\.042 · Ship login$/),
    ).toBeInTheDocument();
    expect(within(pilot).getByText("Ship login")).toBeInTheDocument();
    expect(within(pilot).getByText("1 of 4 tasks done")).toBeInTheDocument();
    expect(screen.getByTitle("2 active runs")).toHaveTextContent("2");

    // Each row keeps its actions behind one ⋯.
    expect(within(second).getAllByRole("button")).toHaveLength(1);
    const menu = menuOf(second);
    expect(within(menu).getByRole("menuitem", { name: "Open run" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "View changes" })).toBeDisabled();
    expect(within(menu).getByRole("menuitem", { name: "Open goal “Ship login”" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "Pause autopilot" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "Cancel run" })).toBeInTheDocument();
    // Escape closes it and hands focus back.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(within(second).getByRole("button", { name: /^Actions for / })).toHaveFocus();

    expect(within(menuOf(first)).queryByRole("menuitem", { name: "Pause autopilot" })).not.toBeInTheDocument();
    // An outside click closes it too.
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("opens the goal from a row and steers its autopilot", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    const action = vi.spyOn(api, "autopilotAction").mockResolvedValue(undefined as never);
    vi.spyOn(api, "getAutopilot").mockResolvedValue(makePilot({ status: "paused" }));
    mount();

    const [pilot] = await rows();
    fireEvent.click(within(menuOf(pilot)).getByRole("menuitem", { name: "Open goal “Ship login”" }));
    expect(useStation.getState().goal).toBe("goal");
    fireEvent.click(within(menuOf(pilot)).getByRole("menuitem", { name: "Pause autopilot" }));
    await waitFor(() => expect(action).toHaveBeenCalledWith("ap1", "pause"));
  });

  it("asks the Runs tab's question before cancelling a run", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue(makeRun({ status: "cancelled" }));
    mount();

    const [row] = await rows();
    fireEvent.click(within(menuOf(row)).getByRole("menuitem", { name: "Cancel run" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Cancel this run?")).toBeInTheDocument();
    expect(
      within(dialog).getByText("The run is withdrawn before anyone at the runner accepts it."),
    ).toBeInTheDocument();
    expect(cancel).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel run" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledWith("run1"));
  });

  it("counts what Pause everything will do and sends cancelRunning as ticked", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing, offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    const pauseAll = vi.spyOn(api, "pauseAll").mockResolvedValue({
      paused: [{ kind: "autopilot", id: "ap1" }],
      cancelled: [
        { kind: "run", id: "run1" },
        { kind: "run", id: "run3" },
      ],
      skipped: [{ kind: "run", reason: "not_permitted" }],
    });
    mount();

    await screen.findByText("1 of 4 tasks done");
    await waitFor(() => expect(screen.getByTitle("2 active runs")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Pause everything" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Pause everything?" });
    expect(within(dialog).getByText("Pause 1 running autopilots")).toBeInTheDocument();
    expect(within(dialog).getByText("Cancel 1 runs that have not started")).toBeInTheDocument();
    expect(within(dialog).getByText("Leave 1 running runs running")).toBeInTheDocument();
    const box = within(dialog).getByRole("checkbox", { name: "Also cancel the 1 runs already running" });
    expect(box).not.toBeChecked();
    fireEvent.click(box);
    expect(within(dialog).getByText("Cancel 1 running runs too")).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Pause everything" }));
    await waitFor(() => expect(pauseAll).toHaveBeenCalledWith({ cancelRunning: true }));
    expect(
      await screen.findByText("Paused 1 autopilots and cancelled 2 runs; 1 left alone."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("keeps running runs by default", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    const pauseAll = vi
      .spyOn(api, "pauseAll")
      .mockResolvedValue({ paused: [], cancelled: [], skipped: [{ kind: "run", id: "run3", reason: "running" }] });
    mount();

    await waitFor(() => expect(screen.getByTitle("1 active runs")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Pause everything" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Pause everything?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Pause everything" }));
    await waitFor(() => expect(pauseAll).toHaveBeenCalledWith({ cancelRunning: false }));
  });

  it("turns into Resume all once only paused autopilots are left, and asks first", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot({ status: "paused" })]);
    const action = vi.spyOn(api, "autopilotAction").mockResolvedValue(undefined as never);
    vi.spyOn(api, "getAutopilot").mockResolvedValue(makePilot());
    mount();

    fireEvent.click(await screen.findByRole("button", { name: "Resume all" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Resume all?" });
    expect(action).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Resume all" }));
    await waitFor(() => expect(action).toHaveBeenCalledWith("ap1", "resume"));
    // Running again: the emergency stop is back.
    expect(await screen.findByRole("button", { name: "Pause everything" })).toBeEnabled();
  });

  it("follows runs-changed", async () => {
    const list = vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    mount();

    await waitFor(() => expect(screen.getByTitle("1 active runs")).toBeInTheDocument());
    list.mockResolvedValue({ runs: [offered, reviewing] });
    useRuns.getState().onRunsChanged("beta");
    await waitFor(() => expect(screen.getByTitle("2 active runs")).toBeInTheDocument(), { timeout: 3000 });
    expect(screen.getByText("Add a test")).toBeInTheDocument();
  });

  it("folds the tasks waiting on prerequisites into one line", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    const world = {
      dungeon: {
        monsters: [
          { nodeId: "beta", title: "Add a test", state: "locked", blockers: ["alpha"] },
          { nodeId: "alpha", title: "Fix the login", state: "attackable", blockers: [] },
        ],
      },
    } as unknown as World;
    mount(world);

    const expand = await screen.findByRole("button", { name: "Expand" });
    expect(screen.getByText("1 tasks waiting on prerequisites")).toBeInTheDocument();
    expect(screen.queryByText("Waiting on Fix the login")).not.toBeInTheDocument();
    fireEvent.click(expand);
    expect(screen.getByRole("button", { name: "Collapse" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Waiting on Fix the login")).toBeInTheDocument();
  });

  it("offers one view menu: the list, the war room, the boss map", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    mount();
    await rows();

    fireEvent.click(screen.getByRole("button", { name: "View: List" }));
    const menu = screen.getByRole("menu", { name: "View: List" });
    expect(within(menu).getByRole("menuitemradio", { name: "List" })).toHaveAttribute("aria-checked", "true");
    expect(within(menu).getByRole("menuitemradio", { name: "War room" })).toHaveAttribute("aria-checked", "false");
    expect(within(menu).queryByRole("menuitemcheckbox")).not.toBeInTheDocument();
    fireEvent.click(within(menu).getByRole("menuitemradio", { name: "Boss map" }));
    expect(useApp.getState().preferences.stageSkin).toBe("dungeon");
    expect(useStation.getState().liveView).toBe("game");
    expect(screen.queryByRole("list", { name: "In progress" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "View: Boss map" }));
    const again = screen.getByRole("menu");
    expect(within(again).getByRole("menuitemcheckbox", { name: "Show as text" })).toHaveAttribute(
      "aria-checked",
      "false",
    );
    fireEvent.click(within(again).getByRole("menuitemradio", { name: "List" }));
    expect(useStation.getState().liveView).toBe("list");
    expect(await screen.findByRole("list", { name: "In progress" })).toBeInTheDocument();
  });

  it("asks for a runner instead of empty views when there is none", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    useRunners.setState({ loaded: false, runners: [], error: null });
    mount();

    fireEvent.click(await screen.findByRole("button", { name: "Add a runner" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
  });
});

describe("control helpers", () => {
  it("leaves someone else's autopilot out of the plan, and counts it", () => {
    const viewer = { id: "u2", name: "bob", role: "member" as const };
    const views = {
      ap1: makePilot(),
      ap2: makePilot({ id: "ap2", createdBy: "u2" }, "run9"),
      ap3: makePilot({ id: "ap3", createdBy: "u2", status: "paused" }, "run8"),
    };
    const plan = pauseAllPlan(viewer, [offered, reviewing], views);
    expect(plan.pause.map((view) => view.run.id)).toEqual(["ap2"]);
    expect(plan.othersAutopilots).toBe(1);
    expect(plan.pending.map((run) => run.id)).toEqual(["run1"]);
    expect(plan.running.map((run) => run.id)).toEqual(["run3"]);
    // An administrator may pause both.
    expect(pauseAllPlan({ ...viewer, role: "admin" }, [], views).pause).toHaveLength(2);
  });

  it("lists recently ended runs apart, newest first, an hour back", () => {
    const now = Date.parse("2026-10-27T09:00:00Z");
    const recent = [
      makeRun({ id: "old", status: "succeeded", finishedAt: "2026-10-27T07:30:00Z" }),
      makeRun({ id: "done", status: "failed", finishedAt: "2026-10-27T08:30:00Z" }),
      makeRun({ id: "later", status: "cancelled", finishedAt: "2026-10-27T08:50:00Z" }),
      offered,
    ];
    const { live, ended } = controlRows([offered], recent, { ap1: makePilot({}, "done") }, now);
    expect(live.map((row) => row.run.id)).toEqual(["run1"]);
    expect(ended.map((row) => row.run.id)).toEqual(["later", "done"]);
    expect(ended[1].autopilot?.run.id).toBe("ap1");
  });

  it("formats elapsed time", () => {
    expect(formatElapsed(42_000)).toBe("42s");
    expect(formatElapsed(245_000)).toBe("4m 05s");
    expect(formatElapsed(3_780_000)).toBe("1h 03m");
  });
});
