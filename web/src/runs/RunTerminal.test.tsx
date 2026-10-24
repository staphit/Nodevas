import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Run } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { FakeWebSocket } from "./fakeWebSocket";
import { RunModeTabs } from "./RunModeTabs";
import RunTerminal, { keySequence } from "./RunTerminal";

// xterm needs a real canvas and layout; a recording stand-in is enough to
// check what reaches it and what it sends back.
const xterm = vi.hoisted(() => {
  type Listener<T> = (value: T) => void;
  class FakeTerminal {
    static instances: FakeTerminal[] = [];
    options: Record<string, unknown>;
    cols = 80;
    rows = 24;
    modes = { applicationCursorKeysMode: false };
    write = vi.fn();
    reset = vi.fn();
    open = vi.fn();
    dispose = vi.fn();
    loadAddon = vi.fn();
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
    /** What xterm does when the user types. */
    type(data: string) {
      this.dataListener?.(data);
    }
    /** What the fit addon's resize does. */
    resize(cols: number, rows: number) {
      this.cols = cols;
      this.rows = rows;
      this.resizeListener?.({ cols, rows });
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

const run: Run = {
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
  status: "running",
  mode: "interactive",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

function term() {
  const instance = xterm.FakeTerminal.instances[xterm.FakeTerminal.instances.length - 1];
  if (!instance) throw new Error("no terminal");
  return instance;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  xterm.FakeTerminal.instances = [];
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("keySequence", () => {
  it("follows the cursor-key mode for arrows", () => {
    expect(keySequence("up", false)).toBe("\x1b[A");
    expect(keySequence("up", true)).toBe("\x1bOA");
    expect(keySequence("left", false)).toBe("\x1b[D");
    expect(keySequence("ctrlC", false)).toBe("\x03");
    expect(keySequence("esc", false)).toBe("\x1b");
    expect(keySequence("enter", false)).toBe("\r");
    expect(keySequence("shiftTab", false)).toBe("\x1b[Z");
  });
});

describe("RunTerminal", () => {
  it("writes output, resets before each replay and shows the status line", () => {
    render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    expect(socket.url).toContain("/api/runs/run1/terminal?project=p");
    expect(screen.getByText("Connecting…")).toBeInTheDocument();

    act(() => socket.serverOpen());
    expect(term().reset).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Run: Running")).toBeInTheDocument();

    act(() => socket.serverBinary("hello"));
    expect(term().write).toHaveBeenCalledTimes(1);
    expect(new TextDecoder().decode(term().write.mock.calls[0][0])).toBe("hello");
  });

  it("sends typing and the key row as binary frames", () => {
    render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    act(() => socket.serverOpen());

    act(() => term().type("ls\r"));
    fireEvent.click(screen.getByRole("button", { name: "Control-C" }));
    term().modes.applicationCursorKeysMode = true;
    fireEvent.click(screen.getByRole("button", { name: "Up arrow" }));
    expect(socket.input()).toEqual(["ls\r", "\x03", "\x1bOA"]);
    expect(term().options.disableStdin).toBe(false);
  });

  it("debounces a resize into one frame", () => {
    vi.useFakeTimers();
    render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    act(() => socket.serverOpen());
    socket.sent = [];
    act(() => {
      term().resize(100, 30);
      term().resize(120, 40);
    });
    expect(socket.controls()).toEqual([]);
    act(() => vi.advanceTimersByTime(200));
    expect(socket.controls()).toEqual([{ type: "resize", cols: 120, rows: 40 }]);
  });

  it("goes read-only when the hub says so", () => {
    render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    act(() => socket.serverOpen());
    act(() => socket.serverText({ type: "readonly" }));

    expect(screen.getByText("Read-only")).toBeInTheDocument();
    expect(term().options.disableStdin).toBe(true);
    expect(screen.getByRole("button", { name: "Escape" })).toBeDisabled();
    act(() => term().type("x"));
    expect(socket.input()).toEqual([]);
  });

  it("shows the session ended and stops reconnecting once the run is final", () => {
    vi.useFakeTimers();
    render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    act(() => socket.serverOpen());
    act(() => socket.serverText({ type: "status", status: "succeeded" }));
    act(() => socket.serverClose(1000));

    expect(screen.getByText("Session ended · Succeeded")).toBeInTheDocument();
    expect(term().options.disableStdin).toBe(true);
    act(() => vi.advanceTimersByTime(60_000));
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("reconnects while running and clears the screen for the replay", () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    render(<RunTerminal run={run} />);
    act(() => FakeWebSocket.last().serverOpen());
    act(() => FakeWebSocket.last().serverClose());
    expect(screen.getByText("Reconnecting…")).toBeInTheDocument();

    act(() => vi.advanceTimersByTime(750));
    expect(FakeWebSocket.instances).toHaveLength(2);
    act(() => FakeWebSocket.last().serverOpen());
    expect(term().reset).toHaveBeenCalledTimes(2);
  });

  it("stops retrying when the run turns final through the store", () => {
    vi.useFakeTimers();
    const { rerender } = render(<RunTerminal run={run} />);
    act(() => FakeWebSocket.last().serverOpen());
    act(() => FakeWebSocket.last().serverClose());
    rerender(<RunTerminal run={{ ...run, status: "cancelled" }} />);
    act(() => vi.advanceTimersByTime(60_000));
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(screen.getByText("Session ended · Cancelled")).toBeInTheDocument();
  });

  it("closes the socket and disposes the terminal on unmount", () => {
    const { unmount } = render(<RunTerminal run={run} />);
    const socket = FakeWebSocket.last();
    const instance = term();
    unmount();
    expect(socket.close).toHaveBeenCalled();
    expect(instance.dispose).toHaveBeenCalled();
  });
});

describe("RunModeTabs", () => {
  it("leaves a headless run's view as it was", () => {
    render(
      <RunModeTabs run={{ ...run, mode: undefined }}>
        <p>timeline</p>
      </RunModeTabs>,
    );
    expect(screen.getByText("timeline")).toBeVisible();
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(FakeWebSocket.instances).toHaveLength(0);
  });

  it("opens an interactive run on its terminal, with the activity a tab away", async () => {
    render(
      <RunModeTabs run={run}>
        <p>timeline</p>
      </RunModeTabs>,
    );
    expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(screen.getByTestId("run-terminal")).toBeInTheDocument());
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(screen.getByText("timeline")).not.toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(screen.getByText("timeline")).toBeVisible();
    // Still attached: switching tabs does not reconnect.
    fireEvent.click(screen.getByRole("tab", { name: "Terminal" }));
    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});
