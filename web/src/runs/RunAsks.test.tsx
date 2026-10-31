import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunAsk, type RunEvent } from "../api";
import { useAgents } from "../agents/agentStore";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { NodeRuns } from "./NodeRuns";
import { interleaveAsks } from "./runAsks";
import { useRuns } from "./runStore";

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
    status: "running",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

const events: RunEvent[] = [
  { seq: 1, type: "log", at: "2026-10-24T08:00:01.000Z", text: "worktree created" },
  { seq: 2, type: "text", at: "2026-10-24T08:00:03.000Z", text: "Tests pass." },
];

const ms = (iso: string) => Date.parse(iso);

function ask(overrides: Partial<RunAsk>): RunAsk {
  return {
    id: "a1",
    kind: "permission",
    status: "pending",
    tool: "Bash",
    createdAt: ms("2026-10-24T08:00:02Z"),
    canAnswer: false,
    ...overrides,
  };
}

function setLanguage(language: "en" | "zh-TW") {
  useApp.setState({ preferences: { ...readPreferences(), language } });
}

async function timelineWith(asks: RunAsk[]) {
  vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun()] });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events, more: false });
  const getAsks = vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks });
  render(<NodeRuns id="alpha" />);
  const timeline = await screen.findByRole("list", { name: /Run activity|執行過程/ });
  await within(timeline).findByText("worktree created");
  return { timeline, getAsks };
}

beforeEach(() => {
  setLanguage("en");
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  useAgents.setState({ open: false });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("interleaveAsks", () => {
  it("puts each ask before the first event that came after it", () => {
    const early = ask({ id: "early", createdAt: ms("2026-10-24T08:00:00Z") });
    const middle = ask({ id: "middle" });
    const late = ask({ id: "late", createdAt: ms("2026-10-24T08:00:09Z") });
    const items = interleaveAsks(events, [late, early, middle]);
    expect(items.map((item) => (item.kind === "ask" ? item.ask.id : item.event.seq))).toEqual([
      "early",
      1,
      "middle",
      2,
      "late",
    ]);
  });

  it("keeps the events alone when the run asked nothing", () => {
    const items = interleaveAsks(events, undefined);
    expect(items.map((item) => item.kind)).toEqual(["event", "event"]);
  });
});

describe("a run's asks in its timeline", () => {
  it("shows what the run waits on, in order, and opens the agent panel to answer it", async () => {
    const { timeline, getAsks } = await timelineWith([
      ask({ preview: "go test ./...", canAnswer: true }),
    ]);
    expect(getAsks).toHaveBeenCalledWith("run1");

    const waiting = await within(timeline).findByRole("button", { name: /Waiting on you: Bash · go test \.\/\.\.\./ });
    const items = Array.from(timeline.querySelectorAll(":scope > li"));
    expect(items.map((item) => item.textContent)).toEqual([
      "worktree created",
      "Waiting on you: Bash · go test ./...",
      "Tests pass.",
    ]);
    expect(waiting.closest("li")).toHaveClass("run-ev-ask", "is-pending");

    fireEvent.click(waiting);
    expect(useAgents.getState().open).toBe(true);
  });

  it("says how each ask ended, and who else waits sees no button", async () => {
    const { timeline } = await timelineWith([
      ask({ id: "a1", status: "approved", decision: "approve_run", preview: "go test", decidedAt: 1 }),
      ask({ id: "a2", tool: "Edit", status: "denied", decision: "deny", preview: "a.go", note: "not that file" }),
      ask({ id: "a3", kind: "question", tool: undefined, question: "Which port?", status: "answered", decision: "answer" }),
      ask({ id: "a4", status: "expired" }),
      ask({ id: "a5", status: "approved", decision: "approve" }),
      ask({ id: "a6", kind: "approval", tool: undefined, question: "Drop it?", status: "approved", decision: "approve" }),
      ask({ id: "a7" }),
    ]);
    for (const text of [
      "Bash · go test — Allowed for this run",
      "Edit · a.go — Denied: not that file",
      "Which port? — Answered",
      "Bash — Expired",
      "Bash — Allowed",
      "Drop it? — Approved",
      "Waiting for an answer: Bash",
    ]) {
      expect(await within(timeline).findByText(text)).toBeInTheDocument();
    }
    expect(within(timeline).queryByRole("button", { name: /Waiting/ })).toBeNull();
  });

  it("speaks Traditional Chinese with full-width punctuation", async () => {
    setLanguage("zh-TW");
    const { timeline } = await timelineWith([
      ask({ id: "a1", preview: "go test", canAnswer: true }),
      ask({ id: "a2", tool: "Edit", status: "denied", decision: "deny", note: "不要動這個檔案" }),
      ask({ id: "a3", status: "approved", decision: "approve_run" }),
    ]);
    expect(await within(timeline).findByRole("button", { name: /等你回覆：Bash · go test/ })).toBeInTheDocument();
    expect(within(timeline).getByText("Edit——已拒絕：不要動這個檔案")).toBeInTheDocument();
    expect(within(timeline).getByText("Bash——本次執行一律允許")).toBeInTheDocument();
  });
});

describe("run-events pushes", () => {
  it("refetch a run's asks for an ask, and its events otherwise", async () => {
    const getEvents = vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
    const getAsks = vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [ask({})] });
    const unwatch = useRuns.getState().watchRun("run1");
    await vi.waitFor(() => expect(getAsks).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(getEvents).toHaveBeenCalledTimes(1));

    useRuns.getState().onRunEvents("run1", "ask");
    await vi.waitFor(() => expect(getAsks).toHaveBeenCalledTimes(2));
    expect(getEvents).toHaveBeenCalledTimes(1);
    expect(useRuns.getState().asksByRun.run1).toHaveLength(1);

    useRuns.getState().onRunEvents("run1");
    await vi.waitFor(() => expect(getEvents).toHaveBeenCalledTimes(2));
    expect(getAsks).toHaveBeenCalledTimes(2);

    // A run nobody shows is not followed.
    useRuns.getState().onRunEvents("run2", "ask");
    expect(getAsks).toHaveBeenCalledTimes(2);
    unwatch();
  });
});
