import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, BusyError, GlobalRunError, type Run } from ".";
import {
  DEFAULT_GLOBAL_SLUG,
  globalRunError,
  globalRunsApi,
  globalTitle,
  MAX_GLOBAL_TITLE_CHARS,
  slugFrom,
  validSlug,
} from "./globalRuns";
import { setProjectOverride } from "./http";
import { REDETECT_WAIT_MS, REPO_INIT_WAIT_MS } from "./runners";
import { forgetGlobalRuns, isGlobalRun, rememberGlobalRun, rememberGlobalRuns, runPath } from "./runs";
import { terminalSocketURL } from "./terminal";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** Answers every request with `body` and records what was asked. */
function mockFetch(body: unknown = {}, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async () => json(body, status));
}

function calls(fetch: ReturnType<typeof mockFetch>) {
  return fetch.mock.calls.map(([url, init]) => ({
    url: String(url),
    method: (init?.method ?? "GET").toUpperCase(),
    body: typeof init?.body === "string" ? init.body : undefined,
    project: new Headers(init?.headers).get("X-Nodevas-Project"),
  }));
}

function globalRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/fix-login-g1000000",
    prompt: "",
    status: "queued",
    exitCode: 0,
    global: true,
    title: "Fix login",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-14T08:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  forgetGlobalRuns();
});

afterEach(() => {
  vi.restoreAllMocks();
  setProjectOverride("");
  forgetGlobalRuns();
});

describe("global runs API", () => {
  it("creates with the exact body and remembers the run as global", async () => {
    const fetch = mockFetch(globalRun(), 201);
    const run = await globalRunsApi.createGlobalRun({
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      model: "opus",
      task: "Fix login\nThe button does nothing.",
      openPR: false,
    });
    expect(run.id).toBe("g1");
    const [call] = calls(fetch);
    expect(call).toMatchObject({ url: "/api/global-runs", method: "POST" });
    expect(JSON.parse(call.body ?? "")).toEqual({
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      model: "opus",
      task: "Fix login\nThe button does nothing.",
      openPR: false,
    });
    expect(isGlobalRun("g1")).toBe(true);
  });

  it("sends onBusy and holderIds and unwraps a takeOver {run, stopped} reply", async () => {
    const fetch = mockFetch({ run: globalRun({ id: "g-new", afterRun: "g-old", busyChoice: "takeOver" }), stopped: ["g-old"] }, 201);
    const run = await globalRunsApi.createGlobalRun({
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      task: "Take over",
      openPR: false,
      onBusy: "takeOver",
      holderIds: ["g-old"],
    });
    expect(run.id).toBe("g-new");
    expect(run.afterRun).toBe("g-old");
    expect(isGlobalRun("g-new")).toBe(true);
    expect(JSON.parse(calls(fetch)[0].body ?? "")).toMatchObject({
      onBusy: "takeOver",
      holderIds: ["g-old"],
    });
  });

  it("throws BusyError on 409 BUSY from createGlobalRun and createGlobalGroup", async () => {
    mockFetch(
      {
        code: "BUSY",
        message: "Someone is already on this",
        holders: [
          {
            runId: "g-hold",
            title: "Upgrade deps",
            agent: "claude",
            model: "opus",
            status: "running",
            since: "2026-11-18T03:00:00Z",
            mayStop: true,
          },
        ],
        others: 0,
        choices: ["queue", "continue", "takeOver", "both"],
        continue: { runId: "g-hold", agent: "claude", model: "opus", openPR: false },
      },
      409,
    );
    const err = await globalRunsApi
      .createGlobalRun({ runnerId: "r1", repoId: "repo1", agent: "claude", task: "x", openPR: false })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(BusyError);
    expect((err as BusyError).busy.choices).toEqual(["queue", "continue", "takeOver", "both"]);
    expect((err as BusyError).busy.continue).toEqual({
      runId: "g-hold",
      agent: "claude",
      model: "opus",
      openPR: false,
    });

    vi.restoreAllMocks();
    mockFetch(
      {
        code: "BUSY",
        error: "busy repos",
        repos: [{ repoId: "repo1", holders: [], others: 1 }],
        choices: ["both", "skip"],
      },
      409,
    );
    const groupErr = await globalRunsApi
      .createGlobalGroup({
        runnerId: "r1",
        repoIds: ["repo1", "repo2"],
        agent: "claude",
        task: "Upgrade",
        openPR: true,
      })
      .catch((e: unknown) => e);
    expect(groupErr).toBeInstanceOf(BusyError);
    expect((groupErr as BusyError).busy.repos).toEqual([{ repoId: "repo1", holders: [], others: 1 }]);
  });

  it("sends follow-up, fetches thread, and reads latestId on 409 CONTINUED", async () => {
    const fetch = mockFetch(globalRun({ id: "g2", continueOf: "g1", workRoot: "g1" }), 201);
    const next = await api.followUpGlobalRun("g1", { message: "Make the button bigger", mode: "interactive" });
    expect(next.continueOf).toBe("g1");
    expect(isGlobalRun("g2")).toBe(true);
    expect(calls(fetch)[0]).toMatchObject({ url: "/api/global-runs/g1/follow-up", method: "POST" });
    expect(JSON.parse(calls(fetch)[0].body ?? "")).toEqual({
      message: "Make the button bigger",
      mode: "interactive",
    });

    vi.restoreAllMocks();
    const threadFetch = mockFetch({ runs: [globalRun({ id: "g1" }), globalRun({ id: "g2", continueOf: "g1" })] });
    const thread = await api.getGlobalRunThread("g1");
    expect(thread.runs).toHaveLength(2);
    expect(calls(threadFetch)[0]).toMatchObject({ url: "/api/global-runs/g1/thread", method: "GET" });
    expect(isGlobalRun("g1") && isGlobalRun("g2")).toBe(true);

    vi.restoreAllMocks();
    mockFetch({ error: "already continued", code: "CONTINUED", latestId: "g3" }, 409);
    await expect(api.followUpGlobalRun("g1", { message: "late" })).rejects.toMatchObject({
      status: 409,
      code: "CONTINUED",
      latestId: "g3",
    });
  });

  it("queries global busy preflight with comma-joined repoIds", async () => {
    const fetch = mockFetch({ repos: [{ repoId: "a", holders: [], others: 0, choices: [] }] });
    const res = await api.getGlobalRunBusy("r1", ["a", "b"]);
    expect(res.repos).toHaveLength(1);
    expect(calls(fetch)[0]).toMatchObject({
      url: "/api/global-runs/busy?runnerId=r1&repoIds=a%2Cb",
      method: "GET",
    });
  });

  it("calls all global group endpoints and remembers returned runs", async () => {
    const group = {
      id: "grp1",
      runnerId: "r1",
      createdBy: "u1",
      title: "Bump deps",
      agent: "claude",
      model: "opus",
      noPR: false,
      repoCount: 2,
      createdAt: "2026-11-18T00:00:00Z",
    };

    const createFetch = mockFetch({ group, runs: [globalRun({ id: "gm1", groupId: "grp1" })] }, 201);
    const created = await api.createGlobalGroup({
      runnerId: "r1",
      repoIds: ["a", "b"],
      agent: "claude",
      model: "opus",
      task: "Bump deps",
      openPR: true,
      onBusy: "skip",
    });
    expect(created.group.id).toBe("grp1");
    expect(isGlobalRun("gm1")).toBe(true);
    expect(calls(createFetch)[0]).toMatchObject({ url: "/api/global-runs/groups", method: "POST" });
    expect(JSON.parse(calls(createFetch)[0].body ?? "")).toMatchObject({
      repoIds: ["a", "b"],
      openPR: true,
      onBusy: "skip",
    });

    vi.restoreAllMocks();
    const getFetch = mockFetch({ group, runs: [globalRun({ id: "gm2", groupId: "grp1" })] });
    await api.getGlobalGroup("grp1");
    expect(calls(getFetch)[0]).toMatchObject({ url: "/api/global-runs/groups/grp1", method: "GET" });
    expect(isGlobalRun("gm2")).toBe(true);

    vi.restoreAllMocks();
    const cancelFetch = mockFetch({ cancelled: 2, runs: [globalRun({ id: "gm1", status: "cancelled" })] });
    const cancelled = await api.cancelGlobalGroup("grp1");
    expect(cancelled.cancelled).toBe(2);
    expect(calls(cancelFetch)[0]).toMatchObject({
      url: "/api/global-runs/groups/grp1/cancel",
      method: "POST",
      body: "{}",
    });

    vi.restoreAllMocks();
    const resumeFetch = mockFetch({ group });
    const resumed = await api.resumeGlobalGroup("grp1");
    expect(resumed.group.id).toBe("grp1");
    expect(calls(resumeFetch)[0]).toMatchObject({
      url: "/api/global-runs/groups/grp1/resume",
      method: "POST",
      body: "{}",
    });

    vi.restoreAllMocks();
    const followFetch = mockFetch({
      started: [globalRun({ id: "gm3", continueOf: "gm1" })],
      skipped: [{ runId: "gm2", code: "WORK_GONE" }],
    });
    const followed = await api.followUpGlobalGroup("grp1", { message: "Fix lint", only: "failed" });
    expect(followed.skipped).toEqual([{ runId: "gm2", code: "WORK_GONE" }]);
    expect(isGlobalRun("gm3")).toBe(true);
    expect(calls(followFetch)[0]).toMatchObject({ url: "/api/global-runs/groups/grp1/follow-up", method: "POST" });
    expect(JSON.parse(calls(followFetch)[0].body ?? "")).toEqual({ message: "Fix lint", only: "failed" });

    vi.restoreAllMocks();
    mockFetch({ error: "over budget", code: "BUDGET_EXCEEDED", exceeded: "runs", room: 3 }, 429);
    await expect(
      api.createGlobalGroup({ runnerId: "r1", repoIds: ["a", "b"], agent: "claude", task: "x", openPR: false }),
    ).rejects.toMatchObject({ status: 429, exceeded: "runs", room: 3 });
  });

  it("lists with limit and before, and remembers every global run", async () => {
    const fetch = mockFetch({ runs: [globalRun(), globalRun({ id: "g2" })], more: true, runners: [] });
    const res = await globalRunsApi.listGlobalRuns({ limit: 50, before: "2026-11-14T07:00:00Z" });
    expect(res.more).toBe(true);
    expect(calls(fetch)[0].url).toBe("/api/global-runs?limit=50&before=2026-11-14T07%3A00%3A00Z");
    expect(isGlobalRun("g1") && isGlobalRun("g2")).toBe(true);

    await globalRunsApi.listGlobalRuns();
    expect(calls(fetch)[1].url).toBe("/api/global-runs");
  });

  it("cancels them all with an empty body", async () => {
    const fetch = mockFetch({ cancelled: 2, runs: [globalRun({ status: "cancelled" })] });
    const res = await globalRunsApi.cancelAllGlobalRuns();
    expect(res.cancelled).toBe(2);
    expect(calls(fetch)[0]).toMatchObject({ url: "/api/global-runs/cancel-all", method: "POST", body: "{}" });
  });

  it("puts a runner's maxConcurrent", async () => {
    const fetch = mockFetch({ runner: { id: "r 1" }, persisted: true });
    const res = await api.setRunnerSettings("r 1", { maxConcurrent: 3 });
    expect(res.persisted).toBe(true);
    const [call] = calls(fetch);
    expect(call).toMatchObject({ url: "/api/runners/r%201/settings", method: "PUT" });
    expect(JSON.parse(call.body ?? "")).toEqual({ maxConcurrent: 3 });
  });

  it("keeps a refusal's code, field, gate, bound and budget limit", async () => {
    mockFetch({ error: "task is empty", code: "INVALID_ARGUMENT", field: "task" }, 400);
    const invalid = await globalRunsApi
      .createGlobalRun({ runnerId: "r1", repoId: "x", agent: "claude", task: "", openPR: false })
      .catch((error: unknown) => error);
    expect(invalid).toBeInstanceOf(GlobalRunError);
    expect(invalid).toMatchObject({ status: 400, code: "INVALID_ARGUMENT", field: "task", message: "task is empty" });

    vi.restoreAllMocks();
    mockFetch({ error: "off", code: "GLOBAL_RUNS_OFF", blocked: "off" }, 409);
    await expect(
      globalRunsApi.createGlobalRun({ runnerId: "r1", repoId: "x", agent: "claude", task: "t", openPR: false }),
    ).rejects.toMatchObject({ status: 409, code: "GLOBAL_RUNS_OFF", blocked: "off" });

    vi.restoreAllMocks();
    mockFetch({ error: "too many", code: "INVALID_ARGUMENT", max: 8 }, 400);
    await expect(api.setRunnerSettings("r1", { maxConcurrent: 9 })).rejects.toMatchObject({ status: 400, max: 8 });

    vi.restoreAllMocks();
    mockFetch({ error: "over", code: "BUDGET_EXCEEDED", exceeded: "cost" }, 429);
    await expect(
      globalRunsApi.createGlobalRun({ runnerId: "r1", repoId: "x", agent: "claude", task: "t", openPR: false }),
    ).rejects.toMatchObject({ status: 429, exceeded: "cost" });
  });

  it("reads a body that is not JSON as the status alone", async () => {
    const error = await globalRunError(new Response("nope", { status: 418 }));
    expect(error).toMatchObject({ status: 418, code: "", blocked: "", field: "", max: 0, message: "418" });
  });
});

describe("runPath", () => {
  it("routes a remembered global run's calls under /api/global-runs", async () => {
    expect(runPath("p1")).toBe("/api/runs/p1");
    rememberGlobalRun("g1");
    expect(runPath("g1")).toBe("/api/global-runs/g1");

    const fetch = mockFetch({ events: [], more: false });
    await api.getRunEvents("g1", 3);
    await api.getRunAsks("g1");
    await api.getRunChanges("g1");
    await api.cancelRun("g1");
    await api.mergeRun("g1", "abc");
    await api.getRunWorkspace("g1");
    await api.workspaceStatus("g1");
    await api.workspaceTree("g1", "src");
    await api.workspaceFile("g1", "README.md");
    await api.mergeRunLocal("g1", "abc");
    await api.discardRun("g1");
    await api.endRun("g1");
    await api.getRunOverlaps("g1");
    expect(calls(fetch).map((call) => `${call.method} ${call.url}`)).toEqual([
      "GET /api/global-runs/g1/events?after=3&limit=500",
      "GET /api/global-runs/g1/asks",
      "GET /api/global-runs/g1/changes",
      "POST /api/global-runs/g1/cancel",
      "POST /api/global-runs/g1/merge",
      "GET /api/global-runs/g1/workspace",
      "GET /api/global-runs/g1/workspace/git/status",
      "GET /api/global-runs/g1/workspace/tree?path=src",
      "GET /api/global-runs/g1/workspace/file?path=README.md",
      "POST /api/global-runs/g1/workspace/merge",
      "POST /api/global-runs/g1/workspace/discard",
      "POST /api/global-runs/g1/end",
      "GET /api/global-runs/g1/overlaps",
    ]);
  });

  it("remembers a run fetched by id that turns out global", async () => {
    mockFetch(globalRun({ id: "g9" }));
    rememberGlobalRun("g9");
    const run = await api.getRun("g9");
    expect(run.global).toBe(true);
    rememberGlobalRuns([{ id: "p2", global: false }, null]);
    expect(isGlobalRun("p2")).toBe(false);
  });

  it("gives a global run's terminal its own socket, without a project", () => {
    setProjectOverride("other");
    rememberGlobalRun("g1");
    expect(terminalSocketURL("g1", "p")).toBe(`ws://${location.host}/api/global-runs/g1/terminal`);
    expect(terminalSocketURL("p1", "p")).toBe(`ws://${location.host}/api/runs/p1/terminal?project=p`);
  });
});

describe("slugs and titles", () => {
  it("validates a slug like the hub", () => {
    expect(validSlug("fix-login")).toBe(true);
    expect(validSlug("a1")).toBe(true);
    expect(validSlug("")).toBe(false);
    expect(validSlug("Fix")).toBe(false);
    expect(validSlug("fix--login")).toBe(false);
    expect(validSlug("-fix")).toBe(false);
    expect(validSlug("fix-")).toBe(false);
    expect(validSlug("a".repeat(41))).toBe(false);
    expect(validSlug("a".repeat(40))).toBe(true);
  });

  it("derives a slug from a title by the runner's rules", () => {
    expect(slugFrom("Fix the Login button!")).toBe("fix-the-login-button");
    expect(slugFrom("  --Hello__World 2--")).toBe("hello-world-2");
    expect(slugFrom("修正登入")).toBe(DEFAULT_GLOBAL_SLUG);
    expect(slugFrom("修正 login 按鈕")).toBe("login");
    // Cut at a dash within 40.
    const long = slugFrom("alpha beta gamma delta epsilon zeta eta theta");
    expect(long).toBe("alpha-beta-gamma-delta-epsilon-zeta-eta");
    expect(long.length).toBeLessThanOrEqual(40);
    expect(validSlug(long)).toBe(true);
    // A word that runs past 40 with no dash is cut there.
    expect(slugFrom("x".repeat(50))).toBe("x".repeat(40));
  });

  it("takes the first non-empty line as the title, at most 120 characters", () => {
    expect(globalTitle("\r\n  \n  Fix login  \r\nmore")).toBe("Fix login");
    expect(globalTitle("   ")).toBe("");
    expect([...globalTitle("界".repeat(130))]).toHaveLength(MAX_GLOBAL_TITLE_CHARS);
  });
});

describe("runner requests that take long", () => {
  /** A fetch that answers only once its signal aborts. */
  function hangingFetch() {
    return vi.spyOn(globalThis, "fetch").mockImplementation(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
  }

  afterEach(() => {
    vi.useRealTimers();
  });

  it("waits for a folder to become a Git project as long as the hub does, not 30 s", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const settled = vi.fn();
    const add = api.addRepo("r1", "C:/code/app", "plan-1").catch((error: unknown) => {
      settled(error);
    });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(REPO_INIT_WAIT_MS);
    await add;
    expect(settled).toHaveBeenCalledTimes(1);
  });

  it("waits for a fresh look for the agents, and keeps 30 s for the rest", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const redetect = vi.fn();
    const plain = vi.fn();
    const look = api.setRunnerSettings("r1", { redetect: true }).catch(redetect);
    const add = api.addRepo("r1", "C:/code/app").catch(plain);
    await vi.advanceTimersByTimeAsync(31_000);
    await add;
    expect(plain).toHaveBeenCalledTimes(1);
    expect(redetect).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(REDETECT_WAIT_MS);
    await look;
    expect(redetect).toHaveBeenCalledTimes(1);
  });
});
