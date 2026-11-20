import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, forgetGlobalRuns, rememberGlobalRuns, type Run, type Runner } from "../../api";
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

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
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

    // Team hub without viewer owning the runner (viewer is null in test unless provided, so simulate team edition via props or store)
    render(
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

  it("supports Open large modal passing story CSS vars and marking expanded state", async () => {
    const run = makeRun({ mode: "", status: "succeeded" });
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

    fireEvent.click(screen.getByRole("button", { name: "Open large" }));
    const modal = await screen.findByRole("dialog");
    expect(modal.querySelector(".run-dialog.is-expanded")).toBeTruthy();
    const styled = modal.querySelector(".run-dialog-portal-theme") as HTMLElement | null;
    expect(styled?.style.getPropertyValue("--story-night")).toBe("#150e08");

    act(() => fireEvent.click(screen.getByRole("button", { name: "Close" })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});
