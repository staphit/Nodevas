import { describe, expect, it } from "vitest";
import type { Run, RunAsk, RunEvent } from "../../api";
import { buildRunLines, MAX_EVENT_TEXT } from "./runLines";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
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
    branch: "agent/task-r1",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-20T08:00:00.000Z",
    ...overrides,
  };
}

describe("buildRunLines", () => {
  it("merges tool_call and tool_result by toolId into one tool line", () => {
    const events: RunEvent[] = [
      { seq: 1, type: "tool_call", tool: "Bash", toolId: "t1", input: '{"command":"npm test"}', at: "2026-11-20T08:00:01.000Z" },
      { seq: 2, type: "text", text: "Running tests…", at: "2026-11-20T08:00:02.000Z" },
      { seq: 3, type: "tool_result", toolId: "t1", text: "PASS", isError: false, at: "2026-11-20T08:00:03.000Z" },
    ];
    const lines = buildRunLines([{ run: makeRun(), events }]);
    expect(lines).toHaveLength(2);
    expect(lines[0].kind).toBe("tool");
    if (lines[0].kind === "tool") {
      expect(lines[0].summary.state).toBe("ok");
      expect(lines[0].result?.text).toBe("PASS");
    }
    expect(lines[1].kind).toBe("text");
  });

  it("interleaves settled asks by time and pins pending asks to the bottom", () => {
    const events: RunEvent[] = [
      { seq: 1, type: "text", text: "first", at: "2026-11-20T08:00:01.000Z" },
      { seq: 2, type: "text", text: "second", at: "2026-11-20T08:00:10.000Z" },
    ];
    const asks: RunAsk[] = [
      {
        id: "ask-pending",
        kind: "question",
        status: "pending",
        question: "Which color?",
        // Even with an earlier hub clock timestamp, pending asks pin to bottom!
        createdAt: Date.parse("2026-11-20T08:00:00.500Z"),
        canAnswer: true,
      },
      {
        id: "ask-settled",
        kind: "permission",
        status: "approved",
        decision: "approve",
        tool: "Bash",
        createdAt: Date.parse("2026-11-20T08:00:05.000Z"),
        canAnswer: false,
      },
    ];
    const lines = buildRunLines([{ run: makeRun({ global: false, project: "p", nodeId: "n1" }), events, asks }]);
    expect(lines.map((l) => (l.kind === "ask" ? `ask:${l.ask.id}` : `${l.kind}:${l.kind === "text" ? l.text : ""}`))).toEqual([
      "text:first",
      "ask:ask-settled",
      "text:second",
      "ask:ask-pending",
    ]);
  });

  it("builds multi-turn chains with turn markers, user lines, and pending queued follow-up withdrawable line", () => {
    const t1 = makeRun({ id: "r1", status: "running", createdAt: "2026-11-20T08:00:00.000Z" });
    const t2 = makeRun({
      id: "r2",
      continueOf: "r1",
      workRoot: "r1",
      status: "queued",
      title: "Make the button bigger",
      createdAt: "2026-11-20T08:01:00.000Z",
    });
    const lines = buildRunLines([
      {
        run: t1,
        events: [
          { seq: 0, type: "user", text: "Add a button", at: "2026-11-20T08:00:00.000Z" },
          { seq: 1, type: "text", text: "Working on it", at: "2026-11-20T08:00:05.000Z" },
        ],
      },
      {
        run: t2,
        events: [{ seq: 0, type: "user", text: "Make the button bigger", at: "2026-11-20T08:01:00.000Z" }],
      },
    ]);
    expect(lines.filter((l) => l.kind === "turn")).toHaveLength(2);
    const users = lines.filter((l) => l.kind === "user");
    expect(users).toHaveLength(2);
    if (users[1].kind === "user") {
      expect(users[1].pending).toBe(true);
      expect(users[1].withdrawRunId).toBe("r2");
    }
  });

  it("hides usage, hides ordinary logs, shows hub notes, and suppresses End's -1 exit line", () => {
    const run = makeRun({ status: "succeeded", endReason: "ended" });
    const events: RunEvent[] = [
      { seq: 1, type: "usage", at: "2026-11-20T08:00:01.000Z", usage: { inputTokens: 10, outputTokens: 5 } },
      { seq: 2, type: "log", text: "session started abc", at: "2026-11-20T08:00:02.000Z" },
      { seq: 3, type: "log", text: "hub: fallback taken", at: "2026-11-20T08:00:03.000Z" },
      { seq: 4, type: "log", text: "exited with code -1", at: "2026-11-20T08:00:04.000Z" },
      { seq: 5, type: "log", text: "exited with code 2", at: "2026-11-20T08:00:05.000Z" },
    ];
    const lines = buildRunLines([{ run, events }]);
    expect(lines.some((l) => l.kind === "log" && l.text.includes("code -1"))).toBe(false);
    const logs = lines.filter((l) => l.kind === "log");
    expect(logs).toHaveLength(3);
    if (logs[0].kind === "log") expect(logs[0].hidden).toBe(true);
    if (logs[1].kind === "log") {
      expect(logs[1].hubNote).toBe(true);
      expect(logs[1].hidden).toBe(false);
      expect(logs[1].text).toBe("fallback taken");
    }
    if (logs[2].kind === "log") {
      expect(logs[2].exitNote).toBe(true);
      expect(logs[2].hidden).toBe(false);
    }
  });

  it("marks text truncated at MaxEventText and adds error/notice rows for run failures and pushRefused", () => {
    const huge = "a".repeat(MAX_EVENT_TEXT);
    const run = makeRun({ status: "failed", error: "boom", pushRefused: true, endReason: "quiet_waiting" });
    const lines = buildRunLines([
      { run, events: [{ seq: 1, type: "text", text: huge, at: "2026-11-20T08:00:01.000Z" }] },
    ]);
    const textLine = lines.find((l) => l.kind === "text");
    expect(textLine?.kind === "text" && textLine.truncated).toBe(true);
    expect(lines.some((l) => l.kind === "error" && l.text === "boom")).toBe(true);
    expect(lines.some((l) => l.kind === "notice" && l.notice === "branch_moved")).toBe(true);
    expect(lines.some((l) => l.kind === "notice" && l.notice === "quiet_waiting")).toBe(true);
  });
});
