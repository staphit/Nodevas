import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useMemo } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AutopilotRun, type AutopilotView, type Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { buildWorld } from "../game/buildWorld";
import { GameActionsProvider } from "../game/GameActions";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { useSettingsRequest } from "../settings/openSettings";
import { useApp } from "../store";
import { controlRows, formatElapsed, pauseAllPlan } from "./control";
import { useControl } from "./controlStore";
import { StationControlReport, StationPauseButton } from "./StationControl";
import { useStation } from "./stationStore";
import { StationWarRoom } from "./StationWarRoom";

// Who is signed in: nobody (a server without accounts) unless a test says.
let viewer: { id: string; name: string; role: string } | null = null;
vi.mock("../components/SignIn", async (original) => ({
  ...(await original<typeof import("../components/SignIn")>()),
  useViewer: () => viewer,
}));

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
        { id: "goal", title: "Ship login", kind: "goal" },
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
  // A runner is there: the war room, not the call to add one.
  useRunners.setState({
    loaded: true,
    loading: false,
    error: null,
    runners: [{ id: "r1", name: "desktop", online: true, info: { agents: [] } } as never],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  viewer = null;
});

/** Station's war room section over the stores, as StationView mounts it
 * (useWorld, minus its loaders). */
function Harness() {
  const graph = useApp((s) => s.graph);
  const runs = useRuns((s) => s.activeRuns);
  const runners = useRunners((s) => s.runners);
  const world = useMemo(
    () =>
      buildWorld({
        graph,
        runs,
        runners,
        sessions: [],
        asks: [],
        held: [],
        messages: [],
        now: Date.now(),
        activeProject: "p",
        projectLabel: "p",
        remote: null,
        waitingItems: [],
      }),
    [graph, runs, runners],
  );
  return <StationWarRoom world={world} />;
}

function mount() {
  return render(
    <GameActionsProvider>
      <StationPauseButton />
      <StationControlReport />
      <Harness />
      <ConfirmDialogHost />
    </GameActionsProvider>,
  );
}

// The room is a lazy chunk. Its first import is transformed on demand, which
// under a full parallel run took longer than a findBy's one second; loading it
// up front leaves the tests to time only what they check.
beforeAll(async () => {
  await import("../game/warroom/WarRoom");
}, 30_000);

/** A character in the war room, by the node it works on. */
function desk(node: string) {
  return screen.findByRole("button", { name: new RegExp(`, on “${node}”`) });
}

/** Opens a character's or a tile's menu. */
function menuOf(trigger: HTMLElement) {
  fireEvent.click(trigger);
  return screen.getByRole("menu");
}

describe("StationWarRoom", () => {
  it("draws the room straight away, a character per active run, and a count", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing, offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    mount();

    expect(screen.getByRole("region", { name: "War room" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^View: / })).not.toBeInTheDocument();
    await desk("Fix the login");
    await desk("Add a test");
    await waitFor(() => expect(screen.getByTitle("2 active runs")).toHaveTextContent("2"));
    // The same room in words, for a screen reader.
    const text = screen.getByTestId("war-room-text");
    expect(within(text).getByText(/^Codex gpt-5 in desktop: reviewing, on “Add a test”\./)).toBeInTheDocument();
  });

  it("opens a run's actions from its character", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing, offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    mount();

    const reviewer = await desk("Add a test");
    await screen.findByText("1 of 4 tasks done");
    const menu = menuOf(reviewer);
    // The first item takes focus once the panel is placed.
    expect(within(menu).getByRole("menuitem", { name: "Open run" })).toHaveFocus();
    expect(within(menu).getByRole("menuitem", { name: "View changes" })).toBeDisabled();
    expect(within(menu).getByRole("menuitem", { name: "Open “Add a test”" })).toBeInTheDocument();
    // The review run is a step of the autopilot's task (by its pipeline).
    expect(within(menu).getByRole("menuitem", { name: "Open goal “Ship login”" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "Pause autopilot" })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: "Cancel run" })).toBeInTheDocument();
    // Escape closes it and hands focus back.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(reviewer).toHaveFocus();

    fireEvent.click(within(menuOf(reviewer)).getByRole("menuitem", { name: "Open goal “Ship login”" }));
    expect(useStation.getState().goal).toBe("goal");

    expect(
      within(menuOf(await desk("Fix the login"))).queryByRole("menuitem", { name: "Pause autopilot" }),
    ).not.toBeInTheDocument();
    // An outside click closes it too.
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("shows a live autopilot as a tile, and steers it", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    const action = vi.spyOn(api, "autopilotAction").mockResolvedValue(undefined as never);
    vi.spyOn(api, "getAutopilot").mockResolvedValue(makePilot({ status: "paused" }));
    mount();

    const tiles = await screen.findByRole("list", { name: "Autopilots" });
    const [pilot] = within(tiles).getAllByRole("listitem");
    expect(within(pilot).getByText("Ship login")).toBeInTheDocument();
    expect(within(pilot).getByText("1 of 4 tasks done")).toBeInTheDocument();
    // The node's type label, as on its board card.
    expect(within(pilot).getByText("Goal")).toBeInTheDocument();
    const actions = within(pilot).getByRole("button", { name: "Actions for Ship login" });
    fireEvent.click(within(menuOf(actions)).getByRole("menuitem", { name: "Open goal “Ship login”" }));
    expect(useStation.getState().goal).toBe("goal");
    fireEvent.click(within(menuOf(actions)).getByRole("menuitem", { name: "Pause autopilot" }));
    await waitFor(() => expect(action).toHaveBeenCalledWith("ap1", "pause"));
  });

  it("says whose someone else's autopilot is, and offers no steering", async () => {
    viewer = { id: "u2", name: "bob", role: "member" };
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([makePilot()]);
    mount();

    const tiles = await screen.findByRole("list", { name: "Autopilots" });
    const [pilot] = within(tiles).getAllByRole("listitem");
    expect(within(pilot).getByText("1 of 4 tasks done · Started by alice")).toBeInTheDocument();
    const menu = menuOf(within(pilot).getByRole("button", { name: "Actions for Ship login" }));
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Open goal “Ship login”"]);
  });

  it("asks the Runs tab's question before cancelling a run", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [offered] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue(makeRun({ status: "cancelled" }));
    mount();

    fireEvent.click(within(menuOf(await desk("Fix the login"))).getByRole("menuitem", { name: "Cancel run" }));
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

  // The dialog claims its Escape, so the drawer beneath does not close too.
  it("closes Pause everything on Escape without passing the key on", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [reviewing] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    mount();

    await waitFor(() => expect(screen.getByTitle("1 active runs")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Pause everything" }));
    await screen.findByRole("alertdialog", { name: "Pause everything?" });
    const beneath = vi.fn();
    window.addEventListener("keydown", beneath);
    try {
      fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    } finally {
      window.removeEventListener("keydown", beneath);
    }
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(beneath).not.toHaveBeenCalled();
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
    expect(await desk("Add a test")).toBeInTheDocument();
  });

  it("folds the tasks waiting on prerequisites into one line of tiles", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    const openTab = vi.fn().mockResolvedValue(undefined);
    useApp.setState({
      graph: {
        nodes: [
          { id: "alpha", title: "Fix the login" },
          { id: "beta", title: "Add a test", requires: "alpha" },
        ],
        edges: [{ from: "alpha", to: "beta" }],
      } as never,
      statuses: {},
      openTab,
    } as never);
    mount();

    const fold = await screen.findByRole("button", { name: /1 tasks waiting on prerequisites/ });
    expect(fold).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Waiting on Fix the login")).not.toBeInTheDocument();
    fireEvent.click(fold);
    expect(fold).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Waiting on Fix the login")).toBeInTheDocument();
    // Its title opens the node.
    fireEvent.click(screen.getByRole("button", { name: "Add a test" }));
    expect(openTab).toHaveBeenCalledWith("beta");
  });

  it("folds the runs that ended in the last hour, each a tile with its menu", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    useStation.setState({ runs: [makeRun({ id: "gone", status: "failed", finishedAt: new Date().toISOString() })] });
    mount();

    const fold = await screen.findByRole("button", { name: /Recently ended \(1\)/ });
    fireEvent.click(fold);
    const tiles = screen.getByRole("list", { name: "Recently ended (1)" });
    expect(within(tiles).getByText("Fix the login")).toBeInTheDocument();
    const menu = menuOf(within(tiles).getByRole("button", { name: "Actions for Fix the login" }));
    expect(within(menu).getByRole("menuitem", { name: "Open run" })).toBeInTheDocument();
    expect(within(menu).queryByRole("menuitem", { name: "Cancel run" })).not.toBeInTheDocument();
  });

  it("asks for a runner instead of an empty room when there is none", async () => {
    vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] });
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([]);
    useRunners.setState({ loaded: false, runners: [], error: null });
    mount();

    fireEvent.click(await screen.findByRole("button", { name: "Add a runner" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
    // Straight to the add form, not just the runners list.
    expect(useSettingsRequest.getState().add).toBe(true);
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
