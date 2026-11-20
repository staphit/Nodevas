import { afterEach, describe, expect, it, vi } from "vitest";
import { api, BusyError, type Contest, type Run } from ".";
import { forgetGlobalRuns, isGlobalRun } from "./runs";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockFetch(body: unknown = {}, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async () => json(body, status));
}

function calls(fetch: ReturnType<typeof mockFetch>) {
  return fetch.mock.calls.map(([url, init]) => ({
    url: String(url),
    method: (init?.method ?? "GET").toUpperCase(),
    body: typeof init?.body === "string" ? init.body : undefined,
  }));
}

function sampleContest(overrides: Partial<Contest> = {}): Contest {
  return {
    id: "ct-1",
    title: "Fix login",
    nodeId: "n1",
    project: "p",
    global: false,
    state: "running",
    entryCount: 2,
    createdBy: "u1",
    createdAt: "2026-11-18T00:00:00Z",
    ...overrides,
  };
}

function sampleRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "r-a",
    project: "p",
    nodeId: "n1",
    runnerId: "rn1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "app",
    base: "main",
    branch: "agent/n1-ra",
    prompt: "",
    status: "queued",
    exitCode: 0,
    contestId: "ct-1",
    contestLabel: "A",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-18T00:00:00Z",
    ...overrides,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  forgetGlobalRuns();
});

describe("contests API", () => {
  it("creates a node contest and parses 409 BUSY into BusyError", async () => {
    const fetch = mockFetch(
      { contest: sampleContest(), runs: [sampleRun({ id: "r-a" }), sampleRun({ id: "r-b", contestLabel: "B" })] },
      201,
    );
    const res = await api.createContest({
      nodeId: "n1",
      repoId: "repo1",
      entries: [
        { runnerId: "rn1", agent: "claude", model: "opus" },
        { runnerId: "rn1", agent: "codex", model: "gpt-5" },
      ],
      reviewer: { runnerId: "rn1", agent: "claude", model: "sonnet" },
      onBusy: "both",
    });
    expect(res.contest.id).toBe("ct-1");
    expect(res.runs).toHaveLength(2);
    expect(calls(fetch)[0]).toMatchObject({ url: "/api/runs/contests", method: "POST" });
    expect(JSON.parse(calls(fetch)[0].body ?? "")).toEqual({
      nodeId: "n1",
      repoId: "repo1",
      entries: [
        { runnerId: "rn1", agent: "claude", model: "opus" },
        { runnerId: "rn1", agent: "codex", model: "gpt-5" },
      ],
      reviewer: { runnerId: "rn1", agent: "claude", model: "sonnet" },
      onBusy: "both",
    });

    vi.restoreAllMocks();
    mockFetch({ code: "BUSY", message: "busy", holders: [], others: 1, choices: ["queue", "both"] }, 409);
    await expect(
      api.createContest({
        nodeId: "n1",
        repoId: "repo1",
        entries: [
          { runnerId: "rn1", agent: "claude", model: "opus" },
          { runnerId: "rn1", agent: "codex", model: "gpt-5" },
        ],
      }),
    ).rejects.toBeInstanceOf(BusyError);
  });

  it("creates a global contest and remembers its global runs", async () => {
    const fetch = mockFetch(
      {
        contest: sampleContest({ id: "ct-g1", global: true, nodeId: "", project: "" }),
        runs: [
          sampleRun({ id: "gr-a", global: true, project: "", nodeId: "" }),
          sampleRun({ id: "gr-b", global: true, project: "", nodeId: "", contestLabel: "B" }),
        ],
      },
      201,
    );
    const res = await api.createGlobalContest({
      runnerId: "rn1",
      repoId: "repo1",
      task: "Improve error handling",
      entries: [
        { agent: "claude", model: "opus" },
        { agent: "codex", model: "gpt-5" },
      ],
    });
    expect(res.contest.global).toBe(true);
    expect(isGlobalRun("gr-a") && isGlobalRun("gr-b")).toBe(true);
    expect(calls(fetch)[0]).toMatchObject({ url: "/api/global-runs/contests", method: "POST" });
    expect(JSON.parse(calls(fetch)[0].body ?? "")).toMatchObject({
      runnerId: "rn1",
      repoId: "repo1",
      task: "Improve error handling",
    });
  });

  it("fetches, judges, picks, and cancels a contest", async () => {
    const getFetch = mockFetch({
      contest: sampleContest({ state: "ready" }),
      runs: [sampleRun({ id: "gr-1", global: true })],
      compare: {
        entries: [
          {
            label: "A",
            runId: "gr-1",
            agent: "claude",
            model: "opus",
            status: "succeeded",
            files: [],
            additions: 12,
            deletions: 3,
            summary: "Clean fix",
          },
        ],
        scores: { scores: [{ entry: "A", score: 9, why: "Solid tests" }], pick: "A" },
      },
    });
    const detail = await api.getContest("ct 1");
    expect(detail.compare.scores?.pick).toBe("A");
    expect(isGlobalRun("gr-1")).toBe(true);
    expect(calls(getFetch)[0]).toMatchObject({ url: "/api/contests/ct%201", method: "GET" });

    vi.restoreAllMocks();
    const judgeFetch = mockFetch({ contest: sampleContest({ state: "judging", judgeRunId: "r-j" }) }, 202);
    const judged = await api.judgeContest("ct-1", { runnerId: "rn1", agent: "claude", model: "opus" });
    expect(judged.contest.state).toBe("judging");
    expect(calls(judgeFetch)[0]).toMatchObject({ url: "/api/contests/ct-1/judge", method: "POST" });
    expect(JSON.parse(calls(judgeFetch)[0].body ?? "")).toEqual({
      runnerId: "rn1",
      agent: "claude",
      model: "opus",
    });

    vi.restoreAllMocks();
    const pickFetch = mockFetch(
      {
        contest: sampleContest({ state: "decided", winnerRunId: "r-a" }),
        actions: [
          { runId: "r-a", action: "open_pr", status: "pending" },
          { runId: "r-b", action: "discard", status: "done" },
        ],
      },
      202,
    );
    const picked = await api.pickContest("ct-1", { runId: "r-a", apply: "pr", discardOthers: true });
    expect(picked.contest.winnerRunId).toBe("r-a");
    expect(picked.actions).toHaveLength(2);
    expect(calls(pickFetch)[0]).toMatchObject({ url: "/api/contests/ct-1/pick", method: "POST" });
    expect(JSON.parse(calls(pickFetch)[0].body ?? "")).toEqual({
      runId: "r-a",
      apply: "pr",
      discardOthers: true,
    });

    vi.restoreAllMocks();
    const cancelFetch = mockFetch({ contest: sampleContest({ state: "cancelled" }), runs: [] });
    const cancelled = await api.cancelContest("ct-1");
    expect(cancelled.contest.state).toBe("cancelled");
    expect(calls(cancelFetch)[0]).toMatchObject({
      url: "/api/contests/ct-1/cancel",
      method: "POST",
      body: "{}",
    });
  });
});
