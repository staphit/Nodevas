import { describe, expect, it } from "vitest";
import type { AgentAsk, AgentMessage, Run } from "../api";
import { buildActivity, MAX_LANES } from "./activity";

const NOW = Date.parse("2026-10-26T14:00:00Z");
const min = (n: number) => new Date(NOW - n * 60_000).toISOString();

function run(over: Partial<Run>): Run {
  return {
    id: "r1", project: "p", nodeId: "n1", runnerId: "x", runnerName: "mac", agent: "claude", model: "sonnet",
    repoId: "r", repoName: "repo", base: "main", branch: "b", prompt: "", status: "succeeded", exitCode: 0,
    createdBy: "u", createdByName: "u", createdAt: min(50), startedAt: min(50), finishedAt: min(20), ...over,
  } as Run;
}

const titles: Record<string, string> = { n1: "Search", n2: "Auth API" };
const titleOf = (id: string) => titles[id] ?? "";

describe("buildActivity", () => {
  it("puts each agent's runs on its lane, placed within the hour", () => {
    const lanes = buildActivity({
      runs: [
        run({}),
        run({ id: "r2", nodeId: "n2", startedAt: min(10), finishedAt: undefined, status: "running" }),
        run({ id: "r3", agent: "codex", kind: "review", startedAt: min(90), finishedAt: min(30) }),
        // Ended before the window: not shown.
        run({ id: "old", agent: "gemini", startedAt: min(200), finishedAt: min(120) }),
      ],
      asks: [],
      messages: [],
      now: NOW,
      titleOf,
    });
    expect(lanes.map((l) => l.key)).toEqual(["claude", "codex"]);
    const [claude, codex] = lanes;
    expect(claude.bars.map((b) => [b.label, +b.start.toFixed(3), +b.end.toFixed(3), b.open])).toEqual([
      ["Search", 0.167, 0.667, false],
      ["Auth API", 0.833, 1, true],
    ]);
    // Clamped to the window's start; a review is told apart.
    expect(codex.bars[0]).toMatchObject({ kind: "review", start: 0, end: 0.5 });
  });

  it("marks usage limits, questions and agents' messages, not a person's", () => {
    const lanes = buildActivity({
      runs: [run({ status: "failed", failureKind: "quota", error: "usage limit reached", finishedAt: min(30) })],
      asks: [{ id: "a", agent: "claude", sessionName: "s", question: "Drop it?", createdAt: NOW - 5 * 60_000 } as AgentAsk],
      messages: [
        { id: "m", senderAgent: "codex", to: "claude", body: "schema ready", createdAt: NOW - 15 * 60_000 } as AgentMessage,
        { id: "h", senderAgent: "human", to: "codex", body: "thanks", createdAt: NOW - 1 * 60_000 } as AgentMessage,
      ],
      now: NOW,
      titleOf,
    });
    const claude = lanes.find((l) => l.key === "claude")!;
    expect(claude.marks.map((m) => m.kind)).toEqual(["limit", "ask"]);
    expect(claude.marks[0].text).toContain("usage limit reached");
    expect(lanes.find((l) => l.key === "codex")!.marks).toEqual([
      expect.objectContaining({ kind: "message", text: "→ claude: schema ready" }),
    ]);
    expect(lanes.find((l) => l.key === "human")).toBeUndefined();
  });

  it("keeps the most recently busy lanes when there are too many", () => {
    const runs = Array.from({ length: MAX_LANES + 3 }, (_, i) =>
      run({ id: `r${i}`, agent: `agent${i}`, finishedAt: min(50 - i) }),
    );
    const lanes = buildActivity({ runs, asks: [], messages: [], now: NOW, titleOf });
    expect(lanes).toHaveLength(MAX_LANES);
    expect(lanes[0].key).toBe(`agent${MAX_LANES + 2}`);
  });
});
