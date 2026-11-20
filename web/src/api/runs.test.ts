import { afterEach, describe, expect, it, vi } from "vitest";
import {
  api,
  AuthError,
  BusyError,
  ConflictError,
  type Run,
  type RunEvent,
  type RunnerAgent,
} from ".";
import { busyErrorFromBody, forgetGlobalRuns } from "./runs";

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

function nodeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run-1",
    project: "p",
    nodeId: "n1",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/n1-run1",
    prompt: "",
    status: "queued",
    exitCode: 0,
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

describe("BusyError parsing and runs API", () => {
  it("parses a 409 BUSY payload with holders, others, choices and continue", async () => {
    mockFetch(
      {
        code: "BUSY",
        message: "Someone is already on this",
        holders: [
          {
            runId: "r-hold",
            title: "Fix login",
            agent: "claude",
            model: "opus",
            mode: "interactive",
            status: "running",
            waiting: false,
            since: "2026-11-18T03:12:00Z",
            by: "Ann",
            nodeId: "node-0003",
            repoName: "api",
            mayStop: true,
            sameNode: true,
          },
        ],
        others: 1,
        choices: ["queue", "both"],
        continue: { runId: "r-hold", agent: "claude", model: "opus", openPR: true },
      },
      409,
    );

    const err = await api
      .createRun({ nodeId: "node-0003", runnerId: "r1", agent: "claude", model: "opus", repoId: "repo1" })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(BusyError);
    const busyErr = err as BusyError;
    expect(busyErr.status).toBe(409);
    expect(busyErr.code).toBe("BUSY");
    expect(busyErr.message).toBe("Someone is already on this");
    expect(busyErr.busy).toEqual({
      holders: [
        {
          runId: "r-hold",
          title: "Fix login",
          agent: "claude",
          model: "opus",
          mode: "interactive",
          status: "running",
          waiting: false,
          since: "2026-11-18T03:12:00Z",
          by: "Ann",
          nodeId: "node-0003",
          repoName: "api",
          mayStop: true,
          sameNode: true,
        },
      ],
      others: 1,
      choices: ["queue", "both"],
      continue: { runId: "r-hold", agent: "claude", model: "opus", openPR: true },
    });
  });

  it("falls back cleanly when optional BUSY fields are missing and ignores non-BUSY 409s", async () => {
    expect(busyErrorFromBody(409, { code: "FAILED_PRECONDITION" })).toBeNull();
    expect(busyErrorFromBody(400, { code: "BUSY" })).toBeNull();
    const minimal = busyErrorFromBody(409, { code: "BUSY" });
    expect(minimal).toBeInstanceOf(BusyError);
    expect(minimal?.message).toBe("BUSY");
    expect(minimal?.busy).toEqual({ holders: [], others: 0, choices: [] });

    mockFetch({ error: "turn running", code: "TURN_RUNNING" }, 409);
    await expect(
      api.createRun({ nodeId: "n1", runnerId: "r1", agent: "claude", model: "opus", repoId: "repo1" }),
    ).rejects.toBeInstanceOf(ConflictError);

    vi.restoreAllMocks();
    mockFetch({ error: "not yours", code: "NOT_YOURS_TO_STOP" }, 403);
    const forbidden = await api
      .createRun({
        nodeId: "n1",
        runnerId: "r1",
        agent: "claude",
        model: "opus",
        repoId: "repo1",
        onBusy: "takeOver",
        holderIds: ["r-other"],
      })
      .catch((e: unknown) => e);
    expect(forbidden).toBeInstanceOf(AuthError);
    expect((forbidden as AuthError).code).toBe("NOT_YOURS_TO_STOP");
  });

  it("sends onBusy and holderIds and unwraps a takeOver {run, stopped} reply", async () => {
    const fetch = mockFetch(
      { run: nodeRun({ id: "run-2", afterRun: "run-1", busyChoice: "takeOver" }), stopped: ["run-1"] },
      201,
    );
    const run = await api.createRun({
      nodeId: "n1",
      runnerId: "r1",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      onBusy: "takeOver",
      holderIds: ["run-1"],
    });
    expect(run.id).toBe("run-2");
    expect(run.afterRun).toBe("run-1");
    expect(run.busyChoice).toBe("takeOver");
    expect(JSON.parse(calls(fetch)[0].body ?? "")).toMatchObject({
      onBusy: "takeOver",
      holderIds: ["run-1"],
    });
  });

  it("calls node busy preflight, overlaps, and endRun with exact paths", async () => {
    const fetch = mockFetch({ holders: [], others: 0, choices: [] });
    await api.getRunBusy({ runnerId: "r1", repoId: "repo1", nodeId: "n 1" });
    await api.getRunBusy({ runnerId: "r1", repoId: "repo1" });
    expect(calls(fetch).map((c) => `${c.method} ${c.url}`)).toEqual([
      "GET /api/runs/busy?runnerId=r1&repoId=repo1&nodeId=n+1",
      "GET /api/runs/busy?runnerId=r1&repoId=repo1",
    ]);

    vi.restoreAllMocks();
    const ovFetch = mockFetch({
      overlaps: [{ hidden: true, since: "2026-11-18T00:00:00Z" }],
    });
    const ov = await api.getRunOverlaps("run-1");
    expect(ov.overlaps).toEqual([{ hidden: true, since: "2026-11-18T00:00:00Z" }]);
    expect(calls(ovFetch)[0]).toMatchObject({ url: "/api/runs/run-1/overlaps", method: "GET" });

    vi.restoreAllMocks();
    const endFetch = mockFetch(nodeRun({ status: "succeeded", endReason: "ended", viewOnly: false }), 202);
    const ended = await api.endRun("run-1");
    expect(ended.endReason).toBe("ended");
    expect(calls(endFetch)[0]).toMatchObject({ url: "/api/runs/run-1/end", method: "POST" });
  });

  it("supports the new wire fields on Run, RunEvent, and RunnerAgent", () => {
    const run = nodeRun({
      continueOf: "r0",
      workRoot: "r0",
      continuedBy: "r2",
      viewOnly: true,
      groupId: "grp1",
      resetAt: "2026-11-18T04:00:00Z",
      endReason: "quiet_waiting",
      pushRefused: true,
      afterRun: "r-prev",
      busyChoice: "queue",
      overlapCount: 2,
      contestId: "c1",
      contestLabel: "A",
    });
    const userEvent: RunEvent = { seq: 0, type: "user", at: "2026-11-18T00:00:00Z", text: "Hi" };
    const agent: RunnerAgent = {
      kind: "claude",
      installed: true,
      models: [],
      interactive: true,
      untrusted: false,
    };
    expect(run.endReason).toBe("quiet_waiting");
    expect(run.overlapCount).toBe(2);
    expect(userEvent.type).toBe("user");
    expect(agent.interactive).toBe(true);
    expect(agent.untrusted).toBe(false);
  });
});
