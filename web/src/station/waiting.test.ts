import { describe, expect, it } from "vitest";
import type { AgentAsk, AgentMessage, BuildingRun, Run } from "../api";
import { waitingKey, waitingOnYou, waitingRuns } from "./waiting";

const NOW = Date.parse("2026-10-26T14:00:00Z");
const min = (n: number) => new Date(NOW - n * 60_000).toISOString();

function run(over: Partial<Run>): Run {
  return {
    id: "r1", project: "p", nodeId: "n1", runnerId: "x", runnerName: "mac", agent: "claude", model: "sonnet",
    repoId: "r", repoName: "repo", base: "main", branch: "b", prompt: "", status: "succeeded", exitCode: 0,
    createdBy: "u", createdByName: "u", createdAt: min(30), finishedAt: min(20), ...over,
  } as Run;
}

describe("waitingRuns", () => {
  it("lists a usage-limit failure with the run that took over", () => {
    const limited = run({ id: "a", status: "failed", failureKind: "rate_limit" });
    const fallback = run({ id: "b", agent: "codex", retryOf: "a", status: "running", finishedAt: undefined });
    expect(waitingRuns([limited, fallback], NOW)).toEqual([{ kind: "limit", run: limited, tookOver: fallback }]);
  });

  it("lists a PR ready to merge once per node, not one already merging, and a red one as CI", () => {
    const ready = { prUrl: "https://x/pull/1", prNumber: 1, prState: "open", ciStatus: "success" } as const;
    const newer = run({ id: "new", ...ready, finishedAt: min(5) });
    const older = run({ id: "old", ...ready, finishedAt: min(25) });
    const merging = run({
      id: "m", nodeId: "n2", ...ready,
      merge: { id: "x", runId: "m", headSha: "", status: "pending", requestedBy: "u", createdAt: min(1) },
    });
    const red = run({ id: "red", nodeId: "n3", ...ready, ciStatus: "failure" });
    expect(waitingRuns([older, newer, merging, red], NOW)).toEqual([
      { kind: "merge", run: newer },
      { kind: "ci", run: red },
    ]);
  });

  it("lists a node's PR as its newest run has it", () => {
    const pr = { prUrl: "https://x/pull/1", prNumber: 1, prState: "open" } as const;
    const fixed = run({ id: "fixed", ...pr, ciStatus: "success", finishedAt: min(2) });
    const red = run({ id: "red", ...pr, ciStatus: "failure", finishedAt: min(10) });
    expect(waitingRuns([red, fixed], NOW)).toEqual([{ kind: "merge", run: fixed }]);
  });

  // The newest run decides: an older green PR waits while a newer run's CI
  // is pending, it failed, or it is still running.
  it("does not list an older run's green PR while the newest is pending or failed", () => {
    const pr = { prUrl: "https://x/pull/1", prNumber: 1, prState: "open" } as const;
    const green = run({ id: "green", ...pr, ciStatus: "success", finishedAt: min(20) });
    const pending = run({ id: "pending", ...pr, ciStatus: "pending", finishedAt: min(2) });
    expect(waitingRuns([green, pending], NOW)).toEqual([]);
    const failed = run({ id: "failed", status: "failed", failureKind: "task", finishedAt: min(2) });
    expect(waitingRuns([green, failed], NOW)).toEqual([]);
    const running = run({ id: "running", status: "running", createdAt: min(5), finishedAt: undefined });
    expect(waitingRuns([green, running], NOW)).toEqual([]);
    // Another node's PR is its own.
    const other = run({ id: "other", nodeId: "n2", ...pr, ciStatus: "success" });
    expect(waitingRuns([green, pending, other], NOW)).toEqual([{ kind: "merge", run: other }]);
  });

  it("forgets what is older than the window, and plain failures", () => {
    const stale = run({ status: "failed", failureKind: "quota", finishedAt: min(60 * 7) });
    const plain = run({ id: "p", status: "failed", failureKind: "task" });
    expect(waitingRuns([stale, plain], NOW)).toEqual([]);
  });
});

describe("waitingOnYou", () => {
  const ask = (id: string, actorId: string, status: AgentAsk["status"] = "pending"): AgentAsk => ({
    id, project: "p", sessionId: "s", sessionName: "laptop-claude", actorId, question: "May I?", status, createdAt: 1,
  });
  const held = { id: "h1", from: "a", to: "b", body: "Rebase first." } as AgentMessage;
  const review = {
    autopilotId: "ap1",
    goalNodeId: "goal",
    task: { nodeId: "t1", title: "Task", state: "waiting_human" },
  } as never;

  it("puts everything waiting on the viewer in one list, most blocking first", () => {
    const merge = run({ id: "m", prUrl: "https://x/pull/2", prNumber: 2, prState: "open", ciStatus: "success" });
    const limited = run({ id: "l", nodeId: "n9", status: "failed", failureKind: "quota" });
    const offered = run({ id: "o", nodeId: "n4", status: "offered", finishedAt: undefined });
    const items = waitingOnYou({
      runs: [merge, limited],
      active: [offered, run({ id: "busy", status: "running", finishedAt: undefined })],
      asks: [ask("a1", "me"), ask("a2", "someone-else"), ask("a3", "me", "approved")],
      held: [held],
      reviews: [review],
      budgetExceeded: "cost",
      canAnswer: (item) => item.actorId === "me",
      now: NOW,
    });
    expect(items.map(waitingKey)).toEqual([
      "budget",
      "ask:a1",
      "held:h1",
      "review:ap1:t1",
      "merge:m",
      "offer:o",
      "limit:l",
    ]);
  });

  it("is empty when nothing waits", () => {
    expect(
      waitingOnYou({ runs: [], active: [], asks: [], held: [], reviews: [], canAnswer: () => true, now: NOW }),
    ).toEqual([]);
  });
});

describe("waiting on you, for another project's run summaries", () => {
  const summary = (over: Partial<BuildingRun>): BuildingRun => ({
    id: "r1", nodeId: "n1", title: "Login", agent: "claude", model: "opus", kind: "", status: "succeeded",
    runnerId: "x", runnerName: "mac", createdAt: min(30), finishedAt: min(20), ...over,
  });

  it("counts a building run the way it counts a full run", () => {
    const ready = summary({ id: "pr", prUrl: "https://x/pull/2", prNumber: 2, prState: "open", ciStatus: "success" });
    const merging = summary({
      id: "m", nodeId: "n2", prUrl: "https://x/pull/3", prState: "open", ciStatus: "success", merge: { status: "pending" },
    });
    const offered = summary({ id: "o", nodeId: "n3", status: "offered", finishedAt: undefined });
    const limited = summary({ id: "l", nodeId: "n4", status: "failed", failureKind: "quota" });
    const runs = [ready, merging, offered, limited];
    const items = waitingOnYou<BuildingRun>({
      runs,
      active: runs.filter((r) => r.status === "offered"),
      asks: [],
      held: [],
      reviews: [],
      canAnswer: () => true,
      now: NOW,
    });
    expect(items.map((item) => waitingKey(item))).toEqual(["merge:pr", "offer:o", "limit:l"]);
    expect(waitingRuns(runs, NOW).map((item) => item.kind)).toEqual(["merge", "limit"]);
  });
});
