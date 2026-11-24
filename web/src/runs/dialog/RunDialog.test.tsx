import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  BusyError,
  forgetGlobalRuns,
  rememberGlobalRuns,
  type Actor,
  type Run,
  type Runner,
} from "../../api";
import { readPreferences } from "../../preferences";
import { EditionProvider } from "../../runners/edition";
import { useRunners } from "../../runners/runnerStore";
import { useApp } from "../../store";
import { FakeWebSocket } from "../fakeWebSocket";
import { useRuns } from "../runStore";
import { resetLiveTerminals } from "./liveTerminals";
import { RunDialog } from "./RunDialog";

const xterm = vi.hoisted(() => {
  type Listener<T> = (value: T) => void;
  class FakeTerminal {
    static instances: FakeTerminal[] = [];
    options: Record<string, unknown>;
    cols = 80;
    rows = 24;
    modes = { applicationCursorKeysMode: false, bracketedPasteMode: true };
    buffer = {
      active: {
        length: 0,
        getLine: () => null,
      },
    };
    write = vi.fn();
    reset = vi.fn();
    open = vi.fn();
    dispose = vi.fn();
    loadAddon = vi.fn();
    focus = vi.fn();
    dataListener: Listener<string> | null = null;
    binaryListener: Listener<string> | null = null;
    resizeListener: Listener<{ cols: number; rows: number }> | null = null;
    constructor(options: Record<string, unknown>) {
      this.options = { ...options };
      FakeTerminal.instances.push(this);
    }
    onData(listener: Listener<string>) {
      this.dataListener = listener;
      return { dispose: () => (this.dataListener = null) };
    }
    onBinary(listener: Listener<string>) {
      this.binaryListener = listener;
      return { dispose: () => (this.binaryListener = null) };
    }
    onResize(listener: Listener<{ cols: number; rows: number }>) {
      this.resizeListener = listener;
      return { dispose: () => (this.resizeListener = null) };
    }
  }
  return { FakeTerminal };
});

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../../components/SignIn", () => ({ useViewer: () => viewer.current }));

vi.mock("@xterm/xterm", () => ({ Terminal: xterm.FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit = vi.fn();
  },
}));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
    global: true,
    title: "Add a button",
    runnerId: "runner1",
    runnerName: "my-mac",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/add-a-button-g1",
    prompt: "",
    status: "running",
    mode: "interactive",
    exitCode: 0,
    createdBy: "local",
    createdByName: "local",
    createdAt: "2026-11-20T08:00:00Z",
    ...overrides,
  };
}

const localRunner: Runner = {
  id: "runner1",
  name: "my-mac",
  ownerId: "local",
  ownerName: "local",
  builtIn: true,
  createdAt: "2026-11-01T00:00:00Z",
  lastSeenAt: "2026-11-20T08:00:00Z",
  online: true,
};

/** A runner that can run an agent's own screen, and whose Claude can. */
const interactiveRunner: Runner = {
  ...localRunner,
  info: {
    version: "2.1.0",
    hostname: "my-mac",
    os: "darwin",
    arch: "arm64",
    capabilities: ["headless", "interactive", "global-runs", "global-runs-on", "continue"],
    agents: [{ kind: "claude", installed: true, interactive: true, models: [] }],
    repos: [{ id: "repo1", name: "nodevas" }],
  },
};

/** The thread of whatever chain the asked run is in, as the store holds it. */
function threadFromStore(id: string) {
  const all = useRuns.getState().globalById;
  const run = all[id];
  const root = run?.workRoot || run?.id || id;
  const runs = Object.values(all).filter((item) => item.id === root || item.workRoot === root);
  return { runs };
}

beforeEach(() => {
  viewer.current = null;
  useApp.setState({ preferences: { ...readPreferences(), language: "en", runMode: "auto" } });
  useRuns.getState().reset();
  useRuns.setState({ watched: {}, globalById: {} });
  useRunners.setState({ runners: [localRunner], loaded: true });
  xterm.FakeTerminal.instances = [];
  FakeWebSocket.reset();
  resetLiveTerminals();
  forgetGlobalRuns();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
  vi.spyOn(api, "getGlobalRunThread").mockImplementation(async (id) => {
    const run = useRuns.getState().globalById[id];
    return { runs: run ? [run] : [] };
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  resetLiveTerminals();
});

describe("RunDialog", () => {
  it("lazily mounts RunTerminal for interactive runs and renders Simple frame when simple=true", async () => {
    const run = makeRun();
    rememberGlobalRuns([run]);
    useRuns.getState().putRun(run);

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" simple />
      </EditionProvider>,
    );

    expect(
      screen.getByText("This is Claude's own screen. It may speak English. Type below, or use the buttons."),
    ).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("run-terminal")).toBeInTheDocument());
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    expect(screen.getByRole("button", { name: "Done, keep what it made" })).toBeInTheDocument();
  });

  it("shows read-only composer on viewOnly and hides End for non-owners", async () => {
    const run = makeRun({ viewOnly: true, createdBy: "u2", createdByName: "bob" });
    rememberGlobalRuns([run]);
    useRuns.getState().putRun(run);
    useRunners.setState({ runners: [{ ...localRunner, ownerId: "u1", ownerName: "alice", builtIn: false }] });
    // Bob dispatched it but does not own the runner: on a view-only run only
    // the runner's owner may End it (§7.1).
    viewer.current = { id: "u2", name: "bob", role: "member" };

    const { unmount } = render(
      <EditionProvider edition="team">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );

    await waitFor(() => expect(screen.getByTestId("run-terminal")).toBeInTheDocument());
    expect(screen.getByRole("textbox")).toBeDisabled();
    expect(screen.getByRole("textbox")).toHaveAttribute(
      "placeholder",
      "The runner's owner did not allow typing from here",
    );
    expect(screen.queryByRole("button", { name: "Done, keep the work" })).toBeNull();
    unmount();
    resetLiveTerminals();

    // The runner's owner does get End on the same run.
    viewer.current = { id: "u1", name: "alice", role: "admin" };
    render(
      <EditionProvider edition="team">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    expect(await screen.findByRole("button", { name: "Done, keep the work" })).toBeInTheDocument();
  });

  it("shows SimpleRunSummary in Simple mode and screenGone summary in full mode after 10 min expiry", async () => {
    const expired = makeRun({
      status: "succeeded",
      mode: "interactive",
      commit: "abc123",
      filesChanged: 2,
      summary: "All buttons resized.",
      finishedAt: new Date(Date.now() - 15 * 60 * 1000).toISOString(),
    });
    rememberGlobalRuns([expired]);
    useRuns.getState().putRun(expired);

    const { rerender } = render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" simple />
      </EditionProvider>,
    );
    expect(screen.queryByTestId("run-terminal")).toBeNull();
    expect(screen.getByLabelText("What happened")).toBeInTheDocument();

    rerender(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    expect(screen.getByText(/The screen is kept 10 minutes after the end/)).toBeInTheDocument();
    expect(screen.getByText("All buttons resized.")).toBeInTheDocument();
  });

  it("loads a multi-turn chain and renders You lines for each turn", async () => {
    const t1 = makeRun({ id: "g1", mode: "", status: "succeeded", continuedBy: "g2", createdAt: "2026-11-20T08:00:00Z" });
    const t2 = makeRun({
      id: "g2",
      mode: "",
      status: "succeeded",
      continueOf: "g1",
      workRoot: "g1",
      createdAt: "2026-11-20T08:05:00Z",
    });
    rememberGlobalRuns([t1, t2]);
    useRuns.getState().putRuns([t1, t2]);
    vi.spyOn(api, "getGlobalRunThread").mockResolvedValue({ runs: [t1, t2] });
    vi.spyOn(api, "getRunEvents").mockImplementation(async (id) => {
      if (id === "g1") {
        return {
          events: [
            { seq: 0, type: "user", text: "First prompt", at: "2026-11-20T08:00:00Z" },
            { seq: 1, type: "text", text: "First answer", at: "2026-11-20T08:01:00Z" },
          ],
          more: false,
        };
      }
      return {
        events: [
          { seq: 0, type: "user", text: "Second prompt", at: "2026-11-20T08:05:00Z" },
          { seq: 1, type: "text", text: "Second answer", at: "2026-11-20T08:06:00Z" },
        ],
        more: false,
      };
    });

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g2" variant="story" />
      </EditionProvider>,
    );

    await waitFor(() => expect(screen.getByText("First prompt")).toBeInTheDocument());
    expect(screen.getByText("Second prompt")).toBeInTheDocument();
    expect(screen.getByText("Second answer")).toBeInTheDocument();
    // Background run never mounts xterm
    expect(FakeWebSocket.instances).toHaveLength(0);
  });

  it("words quiet and quiet_waiting endings apart in both full and Simple modes", async () => {
    const quietRun = makeRun({ id: "gq1", mode: "", status: "succeeded", endReason: "quiet" });
    const waitingRun = makeRun({ id: "gq2", mode: "", status: "succeeded", endReason: "quiet_waiting" });
    rememberGlobalRuns([quietRun, waitingRun]);
    useRuns.getState().putRuns([quietRun, waitingRun]);

    const { rerender } = render(
      <EditionProvider edition="single">
        <RunDialog runId="gq1" variant="plain" />
      </EditionProvider>,
    );
    expect(screen.getByText("Ended after 20 quiet minutes; the work is kept")).toBeInTheDocument();

    rerender(
      <EditionProvider edition="single">
        <RunDialog runId="gq2" variant="plain" />
      </EditionProvider>,
    );
    expect(screen.getByText("It stopped while it was waiting for your answer")).toBeInTheDocument();

    rerender(
      <EditionProvider edition="single">
        <RunDialog runId="gq1" variant="plain" simple />
      </EditionProvider>,
    );
    expect(screen.getByText("It stopped after 20 quiet minutes; what it made is kept")).toBeInTheDocument();
  });

  it("supports Open large: story CSS vars, the screen handed to the modal, and focus that stays put on new events", async () => {
    const run = makeRun();
    rememberGlobalRuns([run]);
    useRuns.getState().putRun(run);

    const { container } = render(
      <EditionProvider edition="single">
        <div
          className="px-story-talk has-run"
          style={{ ["--story-night" as string]: "#150e08", ["--story-ink" as string]: "#efe6d0" }}
        >
          <RunDialog runId="g1" variant="story" />
        </div>
      </EditionProvider>,
    );

    expect(container.querySelector(".run-dialog.run-dialog-story:not(.is-expanded)")).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId("run-terminal")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Open large" }));
    const modal = await screen.findByRole("dialog");
    expect(modal.querySelector(".run-dialog.is-expanded")).toBeTruthy();
    const styled = modal.querySelector(".run-dialog-portal-theme") as HTMLElement | null;
    expect(styled?.style.getPropertyValue("--story-night")).toBe("#150e08");

    // The modal takes the live screen over; the inline dialog says where it is
    // and its composer cannot send (it has no socket).
    await waitFor(() => expect(within(modal).getByTestId("run-terminal")).toBeInTheDocument());
    const inline = container.querySelector(".run-dialog:not(.is-expanded)") as HTMLElement;
    await waitFor(() => expect(within(inline).queryByTestId("run-terminal")).toBeNull());
    expect(within(inline).getByRole("textbox")).toBeDisabled();
    expect(within(inline).getByRole("textbox")).toHaveAttribute(
      "placeholder",
      "Its screen is open in another view; type there",
    );

    // Typing in the modal's composer: new events re-render both dialogs, and
    // the focus must stay in the composer (not jump to Close, where Enter
    // would close the modal).
    const composer = within(modal).getByRole("textbox");
    expect(composer).toBeEnabled();
    composer.focus();
    fireEvent.change(composer, { target: { value: "make it blue" } });
    act(() => {
      useRuns.getState().appendEvents("g1", [{ seq: 1, type: "text", text: "Working on it", at: "2026-11-20T08:01:00Z" }]);
    });
    act(() => {
      useRuns.getState().appendEvents("g1", [{ seq: 2, type: "text", text: "Still going", at: "2026-11-20T08:02:00Z" }]);
    });
    expect(document.activeElement).toBe(composer);
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    act(() => fireEvent.click(screen.getByRole("button", { name: "Close" })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("gives a second mount of a live screen a disabled composer and a way to the one that shows it", async () => {
    const run = makeRun();
    rememberGlobalRuns([run]);
    useRuns.getState().putRun(run);

    const { container } = render(
      <EditionProvider edition="single">
        <div data-testid="first">
          <RunDialog runId="g1" variant="plain" />
        </div>
        <div data-testid="second">
          <RunDialog runId="g1" variant="plain" />
        </div>
      </EditionProvider>,
    );
    const first = screen.getByTestId("first");
    const second = screen.getByTestId("second");
    await waitFor(() => expect(within(first).getByTestId("run-terminal")).toBeInTheDocument());
    expect(within(first).getByRole("textbox")).toBeEnabled();

    expect(within(second).queryByTestId("run-terminal")).toBeNull();
    expect(within(second).getByRole("textbox")).toBeDisabled();
    expect(within(second).queryByRole("button", { name: "Send" })).toBeNull();
    expect(second.querySelector(".run-dlg-jump-owner")).toBeTruthy();
    // Stop stays in reach from either mount.
    expect(within(second).getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(container.querySelectorAll(".run-dlg-composer")).toHaveLength(2);
  });

  // §8.3: a follow-up uses the first start's rule (interactive by default),
  // read for the prior run's runner and agent.
  it("sends a follow-up on an ended run in interactive mode when the rule says so", async () => {
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const prior = makeRun({ mode: "", status: "succeeded", finishedAt: "2026-11-20T08:10:00Z" });
    rememberGlobalRuns([prior]);
    useRuns.getState().putRun(prior);
    const next = makeRun({ id: "g2", mode: "interactive", status: "queued", continueOf: "g1", workRoot: "g1" });
    const followUp = vi.spyOn(api, "followUpGlobalRun").mockResolvedValue(next);

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    const box = screen.getByRole("textbox");
    fireEvent.change(box, { target: { value: "Make the button bigger" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(followUp).toHaveBeenCalledWith("g1", { message: "Make the button bigger", mode: "interactive" }),
    );
  });

  // The hub asks the agent for plain words when the reply comes from Simple mode.
  it("marks a follow-up sent from Simple mode", async () => {
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const prior = makeRun({ mode: "", status: "succeeded", finishedAt: "2026-11-20T08:10:00Z" });
    rememberGlobalRuns([prior]);
    useRuns.getState().putRun(prior);
    const next = makeRun({ id: "g2", mode: "interactive", status: "queued", continueOf: "g1", workRoot: "g1" });
    const followUp = vi.spyOn(api, "followUpGlobalRun").mockResolvedValue(next);

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" simple />
      </EditionProvider>,
    );
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Make the button bigger" } });
    fireEvent.click(screen.getByRole("button", { name: /Send|送出/ }));
    await waitFor(() => expect(followUp).toHaveBeenCalled());
    expect(followUp.mock.calls[0][1]).toMatchObject({ message: "Make the button bigger", simple: true });
  });

  it("keeps a follow-up in the background when the saved preference or the agent says so", async () => {
    useRunners.setState({
      runners: [
        {
          ...interactiveRunner,
          info: { ...interactiveRunner.info!, agents: [{ kind: "claude", installed: true, interactive: true, untrusted: true, models: [] }] },
        },
      ],
      loaded: true,
    });
    // A saved "interactive" never overrides a hard reason (an untrusted folder).
    useApp.setState({ preferences: { ...readPreferences(), language: "en", runMode: "interactive" } });
    const prior = makeRun({ mode: "", status: "succeeded" });
    rememberGlobalRuns([prior]);
    useRuns.getState().putRun(prior);
    const followUp = vi
      .spyOn(api, "followUpGlobalRun")
      .mockResolvedValue(makeRun({ id: "g2", mode: "", status: "queued", continueOf: "g1", workRoot: "g1" }));

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "One more thing" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(followUp).toHaveBeenCalledWith("g1", { message: "One more thing" }));
  });

  // A reply queued behind a running turn: always background, and the view
  // stays on the running turn (its Stop in reach) until it ends.
  it("stays on the running turn after a queued reply, then moves to the reply when the turn ends", async () => {
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const prior = makeRun({ mode: "", status: "running" });
    rememberGlobalRuns([prior]);
    useRuns.getState().putRun(prior);
    vi.mocked(api.getGlobalRunThread).mockImplementation(async (id) => threadFromStore(id));
    const queued = makeRun({ id: "g2", mode: "", status: "queued", continueOf: "g1", workRoot: "g1", createdAt: "2026-11-20T08:05:00Z" });
    const followUp = vi.spyOn(api, "followUpGlobalRun").mockImplementation(async () => {
      rememberGlobalRuns([queued]);
      return queued;
    });

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "It will read this when it finishes");
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Also fix the footer" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(followUp).toHaveBeenCalledWith("g1", { message: "Also fix the footer" }));

    // Still the running turn: its Stop and its "reads it when done" box.
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "It will read this when it finishes");

    // The turn ends: the view moves to the reply's turn.
    act(() => {
      useRuns.getState().putRun({ ...prior, status: "succeeded", continuedBy: "g2", finishedAt: "2026-11-20T08:06:00Z" });
    });
    await waitFor(() =>
      expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "Waiting for the runner to start it"),
    );
  });

  it("goes back to the latest turn still there after a queued reply is taken back", async () => {
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const prior = makeRun({ mode: "", status: "running" });
    const queued = makeRun({
      id: "g2",
      mode: "",
      status: "queued",
      continueOf: "g1",
      workRoot: "g1",
      createdAt: "2026-11-20T08:05:00Z",
    });
    rememberGlobalRuns([prior, queued]);
    useRuns.getState().putRuns([prior, queued]);
    vi.mocked(api.getGlobalRunThread).mockImplementation(async (id) => threadFromStore(id));
    vi.spyOn(api, "getRunEvents").mockImplementation(async (id) =>
      id === "g2"
        ? { events: [{ seq: 0, type: "user", text: "Also fix the footer", at: "2026-11-20T08:05:00Z" }], more: false }
        : { events: [], more: false },
    );
    const cancel = vi.spyOn(api, "cancelRun").mockImplementation(async (id) => ({ ...queued, id, status: "cancelled" }));

    // A list row shows the latest turn: the queued reply.
    render(
      <EditionProvider edition="single">
        <RunDialog runId="g2" variant="plain" />
      </EditionProvider>,
    );
    // The pending line is redrawn once its "You" event loads: wait for that first.
    await waitFor(() => expect(api.getRunEvents).toHaveBeenCalledWith("g2", 0));
    await waitFor(() => {
      fireEvent.click(screen.getByRole("button", { name: "Take back" }));
      expect(cancel).toHaveBeenCalledWith("g2");
    });
    await waitFor(() =>
      expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "It will read this when it finishes"),
    );
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
  });

  // "Start new work with this": a busy folder asks what to do, like any start.
  it("routes a busy new-work start to the busy choice and starts it with the choice", async () => {
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const ended = makeRun({
      mode: "",
      status: "succeeded",
      prUrl: "https://github.com/o/r/pull/3",
      prState: "merged",
    });
    rememberGlobalRuns([ended]);
    useRuns.getState().putRun(ended);
    const holder = {
      runId: "h1",
      title: "Other work",
      agent: "codex",
      model: "gpt-5",
      status: "running",
      since: "2026-11-20T08:00:00Z",
      mayStop: true,
    };
    const created = makeRun({ id: "g3", mode: "interactive", status: "queued" });
    const create = vi
      .spyOn(api, "createGlobalRun")
      .mockRejectedValueOnce(new BusyError("busy", { holders: [holder], others: 0, choices: ["queue", "both"] }))
      .mockResolvedValueOnce(created);

    render(
      <EditionProvider edition="single">
        <RunDialog runId="g1" variant="plain" />
      </EditionProvider>,
    );
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Redo it on main" } });
    fireEvent.click(screen.getByRole("button", { name: "Start new work with this" }));
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith(expect.objectContaining({ task: "Redo it on main", mode: "interactive" })),
    );
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("textbox")).toHaveValue("Redo it on main");

    fireEvent.click(await screen.findByRole("radio", { name: /Start mine after it ends/ }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(create).toHaveBeenLastCalledWith(
        expect.objectContaining({ task: "Redo it on main", onBusy: "queue", holderIds: ["h1"] }),
      ),
    );
  });
});
