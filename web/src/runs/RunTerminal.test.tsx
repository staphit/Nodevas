import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Run, TerminalConnection } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { FakeWebSocket } from "./fakeWebSocket";
import { RunModeTabs } from "./RunModeTabs";
import RunTerminal from "./RunTerminal";
import {
  hasScreenMenuPrompt,
  keySequence,
  MENU_LINES_ABOVE,
  MENU_LINES_BELOW,
  readCursorLines,
  terminalTheme,
} from "./terminalKeys";

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
    // The terminal connects in an effect, which React may run after the
    // commit that put it in the document: wait for the socket itself.
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    expect(screen.getByText("timeline")).not.toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: "Activity" }));
    expect(screen.getByText("timeline")).toBeVisible();
    // Still attached: switching tabs does not reconnect.
    fireEvent.click(screen.getByRole("tab", { name: "Terminal" }));
    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});

describe("terminalKeys and story palette", () => {
  it("keeps terminalKeys free of any @xterm import", () => {
    const source = readFileSync(resolve(process.cwd(), "src/runs/terminalKeys.ts"), "utf8");
    expect(source).not.toMatch(/@xterm/);
  });

  it("reads --story-* palette tokens when mounted inside .px-story-talk", () => {
    const storyBox = document.createElement("div");
    storyBox.className = "px-story-talk";
    storyBox.style.setProperty("--story-night", "#150e08");
    storyBox.style.setProperty("--story-ink", "#efe6d0");
    storyBox.style.setProperty("--story-soft", "#b3a48c");
    storyBox.style.setProperty("--story-accent", "#d9b86c");
    const host = document.createElement("div");
    storyBox.appendChild(host);
    document.body.appendChild(storyBox);

    try {
      const theme = terminalTheme(host);
      expect(theme.background).toBe("#150e08");
      expect(theme.foreground).toBe("#efe6d0");
      expect(theme.cursor).toBe("#d9b86c");
      expect(theme.brightBlack).toBe("#b3a48c");
    } finally {
      storyBox.remove();
    }
  });

  it("exposes the live connection via onConnection so RunComposer shares one socket", () => {
    const seen: Array<TerminalConnection | null> = [];
    const { unmount } = render(<RunTerminal run={run} onConnection={(c) => seen.push(c)} />);
    expect(seen.length).toBeGreaterThanOrEqual(1);
    expect(seen[0]).not.toBeNull();
    const socket = FakeWebSocket.last();
    act(() => socket.serverOpen());
    expect(seen[0]?.sendInput("hi")).toBe(true);
    expect(socket.input()).toEqual(["hi"]);
    unmount();
    expect(seen[seen.length - 1]).toBeNull();
  });

  it("detects verified CLI trust/menu prompts without false positives on idle main prompts", () => {
    // Verified prompts from CLI smoke report §6.3
    expect(hasScreenMenuPrompt(["Quick safety check", "❯ 1. Yes, I trust this folder", "Enter to confirm · Esc to cancel"])).toBe(true);
    expect(hasScreenMenuPrompt(["Do you trust the contents of this directory?", "› 1. Yes, continue", "Press enter to continue"])).toBe(true);
    expect(hasScreenMenuPrompt(["Update Available", "A new release is available", "Skip   Confirm"])).toBe(true);
    expect(hasScreenMenuPrompt(["Do you trust the contents of this project?", "> Yes, I trust this folder", "↑/↓ Navigate · enter Confirm"])).toBe(true);

    // Idle main prompts must NOT trigger false positives
    expect(hasScreenMenuPrompt(["❯ ", "shift+tab to cycle"])).toBe(false);
    expect(hasScreenMenuPrompt(["› Ask Codex to do anything"])).toBe(false);
    expect(hasScreenMenuPrompt(["Ask anything...", "tab agents  ctrl+p commands"])).toBe(false);
    expect(hasScreenMenuPrompt(["> ", "? for shortcuts"])).toBe(false);
  });

  // The agent's own prose ("you can allow or reject…", "approve or deny…",
  // a quoted "> yes, do it") further up the screen must not lock text
  // submit: only the cursor line and a few lines around it count (§3.1).
  it("reads only the lines around the cursor, so the agent's prose never locks the composer", () => {
    const prose = [
      "I changed the permission check: callers can now allow or reject a request,",
      "and reviewers approve or deny it in the panel.",
      "As you said in the issue:",
      "> yes, keep the old flag for now",
    ];
    const idle = ["", "", "", "", "", "", "", "", "", "", "╭────────╮", "│ >      │", "╰────────╯", "? for shortcuts"];
    const screen = [...prose, ...idle];
    // Cursor in the input box at the bottom: the prose is out of reach.
    expect(hasScreenMenuPrompt(screen, screen.length - 3)).toBe(false);
    // Without a cursor the last line stands for it: still out of reach.
    expect(hasScreenMenuPrompt(screen)).toBe(false);
    // A quoted "> yes," inside prose is not a focused option.
    expect(hasScreenMenuPrompt(["she wrote > yes, ship it", "❯ "], 1)).toBe(false);

    // A real menu at the cursor still locks it, prose above or not.
    const menu = [...prose, ...idle.slice(0, 6), "Allow this command?", "❯ Allow once", "  Reject", "Esc to cancel"];
    expect(hasScreenMenuPrompt(menu, menu.length - 2)).toBe(true);
    expect(hasScreenMenuPrompt(["│ > Yes, I trust this folder", "│   No, exit"], 0)).toBe(true);
  });

  // xterm's cursorY counts from the top of the viewport; with scrollback the
  // viewport starts at baseY, so the cursor's buffer line is baseY + cursorY.
  it("reads the lines around baseY + cursorY, not around the buffer's last line", () => {
    const text = Array.from({ length: 40 }, (_, index) => `line ${index}`);
    const buffer = (baseY?: number, cursorY?: number) => ({
      buffer: {
        active: {
          length: text.length,
          baseY,
          cursorY,
          getLine: (index: number) => ({ translateToString: () => text[index] }),
        },
      },
    });

    // 30 lines of scrollback, cursor on the viewport's 2nd row: buffer line 31.
    const scrolled = readCursorLines(buffer(30, 1));
    expect(scrolled.lines).toEqual(text.slice(31 - MENU_LINES_ABOVE, 31 + MENU_LINES_BELOW + 1));
    expect(scrolled.lines[scrolled.cursorRow!]).toBe("line 31");

    // No scrollback: cursorY alone, clamped at the top.
    const top = readCursorLines(buffer(0, 2));
    expect(top.lines).toEqual(text.slice(0, 2 + MENU_LINES_BELOW + 1));
    expect(top.lines[top.cursorRow!]).toBe("line 2");

    // No cursor reported: the last line stands for it.
    const last = readCursorLines(buffer());
    expect(last.lines[last.cursorRow!]).toBe("line 39");
    expect(last.lines).toEqual(text.slice(39 - MENU_LINES_ABOVE));

    // An empty or missing buffer reads nothing.
    expect(readCursorLines({ buffer: { active: { length: 0, getLine: () => undefined } } })).toEqual({ lines: [] });
    expect(readCursorLines({})).toEqual({ lines: [] });
  });

  it("finds a menu at the cursor deep in scrollback through the live terminal", () => {
    const onScreenAsksChange = vi.fn();
    render(<RunTerminal run={run} onScreenAsksChange={onScreenAsksChange} />);
    const screenText = [
      ...Array.from({ length: 30 }, (_, index) => `old output ${index}`),
      "Allow this command?",
      "❯ Allow once",
      "  Reject",
      "Esc to cancel",
      ...Array.from({ length: 20 }, () => ""),
    ];
    // The menu sits at baseY + cursorY (31); the buffer's last line is
    // 20 blank lines further down, out of its reach.
    Object.assign(term(), {
      buffer: {
        active: {
          length: screenText.length,
          baseY: 30,
          cursorY: 1,
          getLine: (index: number) => ({ translateToString: () => screenText[index] }),
        },
      },
    });
    act(() => FakeWebSocket.last().serverOpen());
    act(() => FakeWebSocket.last().serverBinary("menu"));
    expect(onScreenAsksChange).toHaveBeenLastCalledWith(true);
  });
});
