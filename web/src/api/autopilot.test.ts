import { afterEach, describe, expect, it, vi } from "vitest";
import {
  autopilotApi,
  normalizeAutopilotView,
  planApi,
  reviewHasAgents,
  reviewHasHuman,
  type AutopilotView,
} from "./autopilot";

afterEach(() => {
  vi.restoreAllMocks();
});

function mockFetch(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
  );
}

function sent(
  fetch: ReturnType<typeof mockFetch>,
  call = 0,
): { url: string; method: string; body: unknown } {
  const [url, init] = fetch.mock.calls[call] as [string, RequestInit | undefined];
  return {
    url: String(url),
    method: init?.method ?? "GET",
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  };
}

const VIEW = {
  run: {
    id: "ap1",
    project: "main",
    goalNodeId: "goal",
    status: "running",
    policy: {
      review: { mode: "agents", reviewers: null },
      maxParallel: 2,
      maxRounds: 1,
      autoMerge: false,
    },
    createdBy: "u1",
    createdByName: "Ann",
    createdAt: "2026-10-24T08:00:00Z",
    updatedAt: "2026-10-24T08:01:00Z",
  },
  tasks: [
    {
      nodeId: "t1",
      title: "One",
      state: "running",
      stage: "implement",
      stages: null,
      votes: null,
    },
  ],
} as unknown as AutopilotView;

describe("autopilot API", () => {
  it("starts a run with the goal and the policy", async () => {
    const fetch = mockFetch(VIEW.run);
    const run = await autopilotApi.createAutopilot({
      goalNodeId: "goal",
      policy: {
        review: { mode: "human" },
        maxParallel: 3,
        maxRounds: 2,
        autoMerge: true,
      },
    });
    const req = sent(fetch);
    expect(req.method).toBe("POST");
    expect(req.url).toBe("/api/autopilot");
    expect(req.body).toEqual({
      goalNodeId: "goal",
      policy: {
        review: { mode: "human" },
        maxParallel: 3,
        maxRounds: 2,
        autoMerge: true,
      },
    });
    // Counts the server left out read as zeros.
    expect(run.counts.total).toBe(0);
  });

  it("answers null for a goal that never had a run", async () => {
    mockFetch({ error: "not found" }, 404);
    expect(await autopilotApi.getAutopilotForGoal("goal")).toBeNull();
  });

  it("asks for a goal's run by node id and fills nil slices", async () => {
    const fetch = mockFetch(VIEW);
    const view = await autopilotApi.getAutopilotForGoal("a goal/1");
    expect(sent(fetch).url).toBe("/api/autopilot?goal=a%20goal%2F1");
    expect(view?.tasks?.[0].stages).toEqual([]);
    expect(view?.tasks?.[0].votes).toEqual([]);
    expect(view?.run.policy.review.reviewers).toEqual([]);
  });

  it("sends run and task actions to their routes", async () => {
    const fetch = mockFetch({});
    await autopilotApi.autopilotAction("ap1", "pause");
    await autopilotApi.autopilotAction("ap1", "resume");
    await autopilotApi.autopilotAction("ap1", "cancel");
    await autopilotApi.autopilotTaskAction("ap1", "t/1", "retry");
    await autopilotApi.autopilotTaskAction("ap1", "t1", "skip");
    expect(
      fetch.mock.calls.map((_, i) => `${sent(fetch, i).method} ${sent(fetch, i).url}`),
    ).toEqual([
      "POST /api/autopilot/ap1/pause",
      "POST /api/autopilot/ap1/resume",
      "POST /api/autopilot/ap1/cancel",
      "POST /api/autopilot/ap1/tasks/t%2F1/retry",
      "POST /api/autopilot/ap1/tasks/t1/skip",
    ]);
  });

  it("posts a person's review to the pipeline", async () => {
    const fetch = mockFetch({});
    await autopilotApi.reviewPipeline("p1", {
      verdict: "changes",
      comment: "rename it",
    });
    expect(sent(fetch)).toEqual({
      url: "/api/runs/pipelines/p1/review",
      method: "POST",
      body: { verdict: "changes", comment: "rename it" },
    });
  });

  it("dispatches a breakdown as a plan run", async () => {
    const fetch = mockFetch({ id: "r1" });
    await planApi.createPlanRun({
      nodeId: "goal",
      runnerId: "rn",
      agent: "claude",
      model: "opus",
      repoId: "repo",
    });
    expect(sent(fetch)).toMatchObject({
      url: "/api/runs",
      method: "POST",
      body: {
        nodeId: "goal",
        runnerId: "rn",
        agent: "claude",
        model: "opus",
        repoId: "repo",
        kind: "plan",
      },
    });
  });

  it("knows which review modes use agents and a person", () => {
    expect(reviewHasAgents("agents")).toBe(true);
    expect(reviewHasAgents("agents+human")).toBe(true);
    expect(reviewHasAgents("human")).toBe(false);
    expect(reviewHasHuman("human")).toBe(true);
    expect(reviewHasHuman("agents+human")).toBe(true);
    expect(reviewHasHuman("none")).toBe(false);
  });

  it("normalizes a view without tasks", () => {
    const view = normalizeAutopilotView({ run: VIEW.run, tasks: null });
    expect(view.tasks).toEqual([]);
    expect(view.run.policy.maxParallel).toBe(2);
  });
});
