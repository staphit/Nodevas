import { describe, expect, it } from "vitest";
import type { Run } from "../api";
import { waitingRuns } from "./waiting";

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

  it("lists a PR ready to merge once per node, and not one already merging", () => {
    const ready = { prUrl: "https://x/pull/1", prNumber: 1, prState: "open", ciStatus: "success" } as const;
    const newer = run({ id: "new", ...ready, finishedAt: min(5) });
    const older = run({ id: "old", ...ready, finishedAt: min(25) });
    const merging = run({
      id: "m", nodeId: "n2", ...ready,
      merge: { id: "x", runId: "m", headSha: "", status: "pending", requestedBy: "u", createdAt: min(1) },
    });
    const red = run({ id: "red", nodeId: "n3", ...ready, ciStatus: "failure" });
    expect(waitingRuns([older, newer, merging, red], NOW)).toEqual([{ kind: "merge", run: newer }]);
  });

  it("forgets what is older than the window, and plain failures", () => {
    const stale = run({ status: "failed", failureKind: "quota", finishedAt: min(60 * 7) });
    const plain = run({ id: "p", status: "failed", failureKind: "task" });
    expect(waitingRuns([stale, plain], NOW)).toEqual([]);
  });
});
