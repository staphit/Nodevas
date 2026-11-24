import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Run } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { buildRunLines } from "./runLines";
import { ANNOUNCE_THROTTLE_MS, RunTranscript } from "./RunTranscript";

const baseRun: Run = {
  id: "r1",
  project: "",
  nodeId: "",
  global: true,
  runnerId: "runner1",
  runnerName: "local",
  agent: "claude",
  model: "opus",
  repoId: "repo1",
  repoName: "nodevas",
  base: "main",
  branch: "agent/r1",
  prompt: "",
  status: "running",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-11-20T08:00:00Z",
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("RunTranscript", () => {
  it("renders full text with code fences, expands merged tool rows, and marks failed tools", () => {
    const lines = buildRunLines([
      {
        run: baseRun,
        events: [
          { seq: 0, type: "user", text: "Fix the bug", at: "2026-11-20T08:00:00Z" },
          { seq: 1, type: "text", text: "Here is the patch:\n```ts\nconst x = 1;\n```", at: "2026-11-20T08:00:01Z" },
          { seq: 2, type: "tool_call", tool: "Bash", toolId: "c1", input: '{"command":"npm test"}', at: "2026-11-20T08:00:02Z" },
          { seq: 3, type: "tool_result", toolId: "c1", text: "1 test failed", isError: true, at: "2026-11-20T08:00:03Z" },
          { seq: 4, type: "thinking", text: "Let me check why", at: "2026-11-20T08:00:04Z" },
          { seq: 5, type: "error", text: "Process crashed", at: "2026-11-20T08:00:05Z" },
        ],
      },
    ]);

    const { container } = render(<RunTranscript lines={lines} />);
    expect(screen.getByRole("log")).toBeInTheDocument();
    expect(screen.getByText("Fix the bug")).toBeInTheDocument();
    expect(container.querySelector("pre.run-dlg-code code")).toHaveTextContent("const x = 1;");
    expect(screen.getByText(/Run npm test/)).toBeInTheDocument();
    expect(screen.getByLabelText("failed")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Process crashed");

    // Expand merged tool row to inspect input & output
    const details = container.querySelector("details.run-dlg-tool") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    fireEvent.click(details.querySelector("summary")!);
    expect(screen.getByText(/"command": "npm test"/)).toBeInTheDocument();
    expect(screen.getByText("1 test failed")).toBeInTheDocument();
  });

  it("hides ordinary runner logs behind 'Show runner notes' while always showing hub notes", () => {
    const lines = buildRunLines([
      {
        run: baseRun,
        events: [
          { seq: 1, type: "log", text: "debug runner boot", at: "2026-11-20T08:00:01Z" },
          { seq: 2, type: "log", text: "hub: switched to fallback", at: "2026-11-20T08:00:02Z" },
        ],
      },
    ]);
    render(<RunTranscript lines={lines} />);
    expect(screen.getByText(/switched to fallback/)).toBeInTheDocument();
    expect(screen.queryByText("debug runner boot")).toBeNull();

    fireEvent.click(screen.getByRole("checkbox", { name: "Show runner notes" }));
    expect(screen.getByText("debug runner boot")).toBeInTheDocument();
  });

  it("renders the last 200 lines and offers 'Show earlier' for the rest", () => {
    const events = Array.from({ length: 205 }, (_, i) => ({
      seq: i + 1,
      type: "text" as const,
      text: `msg-${i + 1}`,
      at: "2026-11-20T08:00:01Z",
    }));
    const lines = buildRunLines([{ run: baseRun, events }]);
    render(<RunTranscript lines={lines} />);
    expect(screen.queryByText("msg-1")).toBeNull();
    expect(screen.getByText("msg-205")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Show earlier" }));
    expect(screen.getByText("msg-1")).toBeInTheDocument();
  });

  // The window stops growing at 200 lines, and text can grow inside the last
  // row: following the end keys on what the end is, not on the line count.
  it("keeps following the end past 200 lines and while the last line grows", () => {
    const textEvents = (count: number, last = "") =>
      Array.from({ length: count }, (_, i) => ({
        seq: i + 1,
        type: "text" as const,
        text: i === count - 1 && last ? last : `msg-${i + 1}`,
        at: "2026-11-20T08:00:01Z",
      }));
    const { rerender } = render(<RunTranscript lines={buildRunLines([{ run: baseRun, events: textEvents(210) }])} />);
    const log = screen.getByRole("log");
    let height = 5000;
    Object.defineProperty(log, "scrollHeight", { get: () => height, configurable: true });
    Object.defineProperty(log, "clientHeight", { value: 200, configurable: true });

    // A new line while the window is full (still 200 rendered).
    log.scrollTop = 0;
    height = 5100;
    rerender(<RunTranscript lines={buildRunLines([{ run: baseRun, events: textEvents(211) }])} />);
    expect(log.scrollTop).toBe(5100);

    // The last line's text grows; no new line.
    log.scrollTop = 0;
    height = 5300;
    rerender(
      <RunTranscript lines={buildRunLines([{ run: baseRun, events: textEvents(211, "msg-211 and a much longer tail") }])} />,
    );
    expect(log.scrollTop).toBe(5300);
  });

  it("puts the colon after You in the catalog, full-width in Chinese", () => {
    const lines = buildRunLines([
      { run: baseRun, events: [{ seq: 0, type: "user", text: "Fix the bug", at: "2026-11-20T08:00:00Z" }] },
    ]);
    const { container, rerender } = render(<RunTranscript lines={lines} />);
    expect(container.querySelector(".run-dlg-user-label")?.textContent).toBe("You:");
    act(() => useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } }));
    rerender(<RunTranscript lines={lines} />);
    expect(container.querySelector(".run-dlg-user-label")?.textContent).toBe("你：");
  });

  // Simple speaks plain words: no runner notes toggle, and its notices are
  // the runDialog.simple.* sentences.
  it("offers no runner notes and plain notices in Simple", () => {
    const lines = buildRunLines([
      {
        run: baseRun,
        events: [
          { seq: 1, type: "log", text: "debug runner boot", at: "2026-11-20T08:00:01Z" },
          { seq: 2, type: "text", text: "Done", at: "2026-11-20T08:00:02Z" },
        ],
      },
    ]);
    const { container } = render(<RunTranscript lines={lines} simple />);
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(container.textContent).not.toContain("runner");
    expect(screen.queryByText("debug runner boot")).toBeNull();
  });

  it("follows the end while scrolled to bottom and offers 'Jump to latest' when scrolled up", () => {
    const lines1 = buildRunLines([
      { run: baseRun, events: [{ seq: 1, type: "text", text: "first", at: "2026-11-20T08:00:01Z" }] },
    ]);
    const { rerender } = render(<RunTranscript lines={lines1} />);
    const log = screen.getByRole("log");

    // Simulate scrollable container scrolled near the top (away from end)
    Object.defineProperty(log, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(log, "clientHeight", { value: 200, configurable: true });
    Object.defineProperty(log, "scrollTop", { value: 100, writable: true, configurable: true });
    fireEvent.scroll(log);

    const lines2 = buildRunLines([
      {
        run: baseRun,
        events: [
          { seq: 1, type: "text", text: "first", at: "2026-11-20T08:00:01Z" },
          { seq: 2, type: "text", text: "second", at: "2026-11-20T08:00:02Z" },
        ],
      },
    ]);
    rerender(<RunTranscript lines={lines2} />);

    const jump = screen.getByRole("button", { name: "Jump to latest" });
    expect(jump).toBeInTheDocument();
    expect(log.scrollTop).toBe(100);

    fireEvent.click(jump);
    expect(log.scrollTop).toBe(1000);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("announces only new agent text in a polite live region, cut to 200 chars and throttled to 5s", () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<RunTranscript lines={[]} />);
    const live = container.querySelector('[aria-live="polite"]') as HTMLElement;
    expect(live).toBeTruthy();
    expect(live.textContent).toBe("");

    // Tool call alone is NOT announced
    const toolOnly = buildRunLines([
      { run: baseRun, events: [{ seq: 1, type: "tool_call", tool: "Bash", toolId: "t1", input: '{"command":"ls"}', at: "2026-11-20T08:00:01Z" }] },
    ]);
    rerender(<RunTranscript lines={toolOnly} />);
    expect(live.textContent).toBe("");

    // First agent text (>200 chars) is announced immediately, cut to 200 chars
    const long1 = "A".repeat(250);
    const withText1 = buildRunLines([
      { run: baseRun, events: [{ seq: 2, type: "text", text: long1, at: "2026-11-20T08:00:02Z" }] },
    ]);
    rerender(<RunTranscript lines={withText1} />);
    expect(live.textContent).toBe("A".repeat(200));

    // Second agent text 1s later is throttled until 5s elapse
    act(() => vi.advanceTimersByTime(1000));
    const withText2 = buildRunLines([
      {
        run: baseRun,
        events: [
          { seq: 2, type: "text", text: long1, at: "2026-11-20T08:00:02Z" },
          { seq: 3, type: "text", text: "Second reply", at: "2026-11-20T08:00:03Z" },
        ],
      },
    ]);
    rerender(<RunTranscript lines={withText2} />);
    expect(live.textContent).toBe("A".repeat(200));

    act(() => vi.advanceTimersByTime(ANNOUNCE_THROTTLE_MS));
    expect(live.textContent).toBe("Second reply");
  });
});
