import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, GlobalRunError, type Run, type RunGroup, type RunnerLoad } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useCoordStore } from "../runs/coord/coordStore";
import { selectRunById, useRuns } from "../runs/runStore";
import { useApp } from "../store";
import {
  activeGlobalRuns,
  capacityErrorText,
  collapseGlobalChains,
  GLOBAL_COALESCE_MS,
  orderGlobalRuns,
  useGlobalAgents,
} from "./globalAgentsStore";

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
    status: "running",
    exitCode: 0,
    global: true,
    title: "Fix login",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-14T08:00:00Z",
    ...overrides,
  };
}

function load(overrides: Partial<RunnerLoad> = {}): RunnerLoad {
  return {
    runnerId: "r1",
    name: "desktop",
    online: true,
    maxConcurrent: 2,
    ceiling: 8,
    settable: "",
    globalRuns: "",
    busy: 1,
    queued: 0,
    globalActive: 1,
    today: { runs: 3, tokens: 12000, costUsd: 0.42 },
    ...overrides,
  };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useGlobalAgents.getState().reset();
  useRuns.getState().reset();
  useRuns.setState({ globalById: {}, stopRequested: {}, cancelErrors: {} });
  useRunners.setState({ loaded: false, runners: [] });
  useCoordStore.getState().reset();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("useGlobalAgents", () => {
  it("loads the viewer's global runs and runners, and holds the runs in the run store", async () => {
    const running = globalRun({ id: "g1", createdAt: "2026-11-14T08:00:00Z" });
    const done = globalRun({ id: "g0", status: "succeeded", createdAt: "2026-11-14T09:00:00Z" });
    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [done, running], more: true, runners: [load()] });
    await useGlobalAgents.getState().load();
    expect(list).toHaveBeenCalledWith({ limit: 50 });
    const state = useGlobalAgents.getState();
    // Active first, then the ended ones newest first.
    expect(state.runs.map((run) => run.id)).toEqual(["g1", "g0"]);
    expect(state.runners).toEqual([load()]);
    expect(state.more).toBe(true);
    expect(state.loaded).toBe(true);
    expect(useRuns.getState().globalById.g1).toEqual(running);
    expect(selectRunById(useRuns.getState(), "g0")).toEqual(done);
    // Global runs never land in a node's list.
    expect(useRuns.getState().runsByNode).toEqual({});
    expect(activeGlobalRuns(state.runs).map((run) => run.id)).toEqual(["g1"]);
  });

  it("says why a load failed and keeps what it held", async () => {
    vi.spyOn(api, "listGlobalRuns").mockRejectedValue(new Error("hub down"));
    await useGlobalAgents.getState().load();
    expect(useGlobalAgents.getState()).toMatchObject({ loaded: true, error: "hub down", runs: [] });
  });

  it("pages older ended runs in before the oldest held, and keeps them over a reload", async () => {
    const first = [globalRun({ id: "g3" }), globalRun({ id: "g2", status: "failed", createdAt: "2026-11-14T07:00:00Z" })];
    const older = [globalRun({ id: "g1", status: "succeeded", createdAt: "2026-11-13T07:00:00Z" })];
    const list = vi
      .spyOn(api, "listGlobalRuns")
      .mockResolvedValueOnce({ runs: first, more: true, runners: [] })
      .mockResolvedValueOnce({ runs: older, more: false, runners: [] })
      .mockResolvedValueOnce({ runs: first, more: true, runners: [] });
    await useGlobalAgents.getState().load();
    await useGlobalAgents.getState().loadMore();
    expect(list).toHaveBeenLastCalledWith({ limit: 50, before: "2026-11-14T07:00:00Z" });
    expect(useGlobalAgents.getState().runs.map((run) => run.id)).toEqual(["g3", "g2", "g1"]);
    expect(useGlobalAgents.getState().more).toBe(false);
    expect(useRuns.getState().globalById.g1).toBeTruthy();

    await useGlobalAgents.getState().load();
    expect(useGlobalAgents.getState().runs.map((run) => run.id)).toEqual(["g3", "g2", "g1"]);
    expect(useGlobalAgents.getState().more).toBe(false);
  });

  it("gathers global-runs-changed pushes into one reload while watched", async () => {
    vi.useFakeTimers();
    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [], more: false, runners: [] });
    useGlobalAgents.getState().onChanged("g1");
    expect(list).not.toHaveBeenCalled();

    useGlobalAgents.getState().setWatching(true);
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    useGlobalAgents.getState().onChanged("g1");
    useGlobalAgents.getState().onChanged("g2");
    useGlobalAgents.getState().onChanged("g3");
    vi.advanceTimersByTime(GLOBAL_COALESCE_MS - 1);
    expect(list).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  });

  it("reloads its runners' load when a run elsewhere moves (a node's run ended)", async () => {
    vi.useFakeTimers();
    const list = vi
      .spyOn(api, "listGlobalRuns")
      .mockResolvedValueOnce({ runs: [], more: false, runners: [load({ busy: 1, globalActive: 0 })] })
      .mockResolvedValue({ runs: [], more: false, runners: [load({ busy: 0, globalActive: 0 })] });
    // Nobody watches: nothing to keep current.
    useGlobalAgents.getState().onRunnerLoadChanged();
    vi.advanceTimersByTime(GLOBAL_COALESCE_MS);
    expect(list).not.toHaveBeenCalled();

    useGlobalAgents.getState().setWatching(true);
    await vi.waitFor(() => expect(useGlobalAgents.getState().runners[0]?.busy).toBe(1));
    useGlobalAgents.getState().onRunnerLoadChanged();
    useGlobalAgents.getState().onRunnerLoadChanged();
    vi.advanceTimersByTime(GLOBAL_COALESCE_MS);
    await vi.waitFor(() => expect(useGlobalAgents.getState().runners[0]?.busy).toBe(0));
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("leaves the load alone for a viewer with no runner of their own", async () => {
    vi.useFakeTimers();
    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [], more: false, runners: [] });
    useGlobalAgents.getState().setWatching(true);
    await vi.waitFor(() => expect(useGlobalAgents.getState().loaded).toBe(true));
    useGlobalAgents.getState().onRunnerLoadChanged();
    vi.advanceTimersByTime(GLOBAL_COALESCE_MS);
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("refreshes a held run on a push while nobody watches the list", async () => {
    useRuns.getState().putRun(globalRun());
    const get = vi.spyOn(api, "getRun").mockResolvedValue(globalRun({ status: "succeeded" }));
    const list = vi.spyOn(api, "listGlobalRuns");
    useGlobalAgents.getState().onChanged("g1");
    await vi.waitFor(() => expect(useRuns.getState().globalById.g1.status).toBe("succeeded"));
    expect(get).toHaveBeenCalledWith("g1");
    expect(list).not.toHaveBeenCalled();
    // One the store does not hold is not fetched.
    useGlobalAgents.getState().onChanged("other");
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("starts one, puts it first and focuses it", async () => {
    const created = globalRun({ id: "g5", status: "queued" });
    const create = vi.spyOn(api, "createGlobalRun").mockResolvedValue(created);
    const body = { runnerId: "r1", repoId: "repo1", agent: "claude", model: "opus", task: "Fix login", openPR: false };
    const run = await useGlobalAgents.getState().create(body);
    expect(create).toHaveBeenCalledWith(body);
    expect(run).toEqual(created);
    expect(useGlobalAgents.getState().runs[0].id).toBe("g5");
    expect(useGlobalAgents.getState().focus).toBe("g5");
    expect(useRuns.getState().globalById.g5).toEqual(created);
  });

  it("passes a refused start to the caller", async () => {
    vi.spyOn(api, "createGlobalRun").mockRejectedValue(new GlobalRunError(412, "RUNNER_OUTDATED", "outdated", "", 0, "old"));
    await expect(
      useGlobalAgents.getState().create({ runnerId: "r1", repoId: "x", agent: "claude", task: "t", openPR: false }),
    ).rejects.toMatchObject({ code: "RUNNER_OUTDATED" });
    expect(useGlobalAgents.getState().runs).toEqual([]);
  });

  it("stops one through the run store, and every active one at once", async () => {
    vi.spyOn(api, "listGlobalRuns").mockResolvedValue({
      runs: [globalRun({ id: "g1" }), globalRun({ id: "g2", status: "queued" })],
      more: false,
      runners: [],
    });
    await useGlobalAgents.getState().load();

    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue(globalRun({ id: "g1" }));
    expect(await useGlobalAgents.getState().cancel("g1")).toBe(true);
    expect(cancel).toHaveBeenCalledWith("g1");
    // Still running until the runner says it ended: the stop is on its way.
    expect(useRuns.getState().stopRequested.g1).toBe(true);

    vi.spyOn(api, "cancelAllGlobalRuns").mockResolvedValue({
      cancelled: 2,
      runs: [globalRun({ id: "g1" }), globalRun({ id: "g2", status: "cancelled" })],
    });
    expect(await useGlobalAgents.getState().cancelAll()).toBe(2);
    const runs = useGlobalAgents.getState().runs;
    expect(runs.find((run) => run.id === "g2")?.status).toBe("cancelled");
    expect(useRuns.getState().globalById.g2.status).toBe("cancelled");
    expect(useRuns.getState().stopRequested.g2).toBeUndefined();

    vi.spyOn(api, "cancelAllGlobalRuns").mockRejectedValue(new Error("nope"));
    expect(await useGlobalAgents.getState().cancelAll()).toBeNull();
    expect(useGlobalAgents.getState().cancelAllError).toBe("nope");
  });

  it("sets a runner's maxConcurrent and says whether it was saved", async () => {
    useGlobalAgents.setState({ runners: [load()] });
    const put = vi
      .spyOn(api, "setRunnerSettings")
      .mockResolvedValueOnce({ runner: { info: { maxConcurrent: 3 } } as never, persisted: true })
      .mockResolvedValueOnce({ runner: { info: { maxConcurrent: 4 } } as never, persisted: false });
    expect(await useGlobalAgents.getState().setMaxConcurrent("r1", 3)).toBe(true);
    expect(put).toHaveBeenCalledWith("r1", { maxConcurrent: 3 });
    expect(useGlobalAgents.getState().runners[0].maxConcurrent).toBe(3);
    expect(useGlobalAgents.getState().settingNotes.r1).toEqual({ kind: "saved", text: "desktop now runs 3 at once" });
    expect(useGlobalAgents.getState().saving.r1).toBeUndefined();

    await useGlobalAgents.getState().setMaxConcurrent("r1", 4);
    expect(useGlobalAgents.getState().settingNotes.r1).toEqual({
      kind: "notSaved",
      text: "Applied until desktop restarts: its settings file could not be saved",
    });
  });

  it("explains a refused capacity change", async () => {
    useGlobalAgents.setState({ runners: [load()] });
    vi.spyOn(api, "setRunnerSettings").mockRejectedValue(
      new GlobalRunError(409, "FAILED_PRECONDITION", "settings_off", "", 0, "off"),
    );
    expect(await useGlobalAgents.getState().setMaxConcurrent("r1", 3)).toBe(false);
    expect(useGlobalAgents.getState().settingNotes.r1).toEqual({
      kind: "error",
      text: "desktop's owner allows changing this only on that machine (remoteSettings)",
    });
    expect(useGlobalAgents.getState().runners[0].maxConcurrent).toBe(2);
  });

  it("words each settings refusal", () => {
    const err = (status: number, code: string, blocked = "", max = 0) =>
      new GlobalRunError(status, code, blocked, "", max, `server ${code}`);
    expect(capacityErrorText(err(400, "INVALID_ARGUMENT", "", 8), "desktop")).toBe(
      "At most 8 from here (maxConcurrentCeiling on desktop)",
    );
    expect(capacityErrorText(err(412, "RUNNER_OUTDATED"), "desktop")).toBe(
      "Update nodevas on desktop to change this here",
    );
    expect(capacityErrorText(err(503, "RUNNER_OFFLINE"), "desktop")).toBe("desktop is offline");
    expect(capacityErrorText(err(504, "TIMEOUT"), "desktop")).toBe("desktop did not answer in time");
    expect(capacityErrorText(err(403, "PERMISSION_DENIED"), "desktop")).toBe("server PERMISSION_DENIED");
    expect(capacityErrorText(new Error("boom"), "desktop")).toBe("boom");
  });

  it("orders unfinished first, then newest, one copy each", () => {
    const a = globalRun({ id: "a", status: "succeeded", createdAt: "2026-11-14T10:00:00Z" });
    const b = globalRun({ id: "b", status: "running", createdAt: "2026-11-14T08:00:00Z" });
    const c = globalRun({ id: "c", status: "failed", createdAt: "2026-11-14T09:00:00Z" });
    expect(orderGlobalRuns([a, c, b, a]).map((run) => run.id)).toEqual(["b", "a", "c"]);
  });

  it("collapses follow-up chains to the latest non-withdrawn turn", () => {
    const t1 = globalRun({ id: "g1", workRoot: "g1", continuedBy: "g2", status: "succeeded", createdAt: "2026-11-14T08:00:00Z", startedAt: "2026-11-14T08:00:05Z" });
    const t2 = globalRun({ id: "g2", continueOf: "g1", workRoot: "g1", status: "running", createdAt: "2026-11-14T08:10:00Z", startedAt: "2026-11-14T08:10:05Z" });
    const withdrawn = globalRun({ id: "g3", continueOf: "g2", workRoot: "g1", status: "cancelled", createdAt: "2026-11-14T08:15:00Z", startedAt: "" });
    const standalone = globalRun({ id: "g9", status: "succeeded", createdAt: "2026-11-14T07:00:00Z" });

    const { latest, turnCountById } = collapseGlobalChains([t1, t2, withdrawn, standalone]);
    expect(latest.map((r) => r.id)).toEqual(["g2", "g9"]);
    expect(turnCountById.g2).toBe(2);
    expect(turnCountById.g9).toBe(1);
  });

  it("loads, cancels, resumes, and follows up a group", async () => {
    const grp: RunGroup = {
      id: "grp1",
      runnerId: "r1",
      createdBy: "u1",
      title: "Upgrade deps",
      agent: "claude",
      model: "opus",
      noPR: true,
      repoCount: 2,
      heldUntil: "2026-11-14T10:00:00Z",
      createdAt: "2026-11-14T08:00:00Z",
    };
    const m1 = globalRun({ id: "m1", groupId: "grp1", repoId: "repo1", repoName: "api", status: "failed" });
    const m2 = globalRun({ id: "m2", groupId: "grp1", repoId: "repo2", repoName: "web", status: "queued" });

    vi.spyOn(api, "getGlobalGroup").mockResolvedValue({ group: grp, runs: [m1, m2] });
    await useGlobalAgents.getState().loadGroup("grp1");
    expect(useGlobalAgents.getState().groups.grp1).toEqual(grp);
    expect(useGlobalAgents.getState().groupRuns.grp1.map((r) => r.id)).toEqual(["m1", "m2"]);

    vi.spyOn(api, "resumeGlobalGroup").mockResolvedValue({ group: { ...grp, heldUntil: "" } });
    await useGlobalAgents.getState().resumeGroup("grp1");
    expect(useGlobalAgents.getState().groups.grp1.heldUntil).toBe("");

    const m1Turn2 = globalRun({ id: "m1b", groupId: "grp1", continueOf: "m1", workRoot: "m1", repoId: "repo1", repoName: "api", status: "queued" });
    const fu = vi.spyOn(api, "followUpGlobalGroup").mockResolvedValue({ started: [m1Turn2], skipped: [] });
    const fuRes = await useGlobalAgents.getState().followUpGroup("grp1", { message: "Fix tests", only: "failed" });
    expect(fu).toHaveBeenCalledWith("grp1", { message: "Fix tests", only: "failed" });
    expect(fuRes?.started?.map((r) => r.id)).toEqual(["m1b"]);
    expect(useRuns.getState().globalById.m1b).toEqual(m1Turn2);

    const cancel = vi.spyOn(api, "cancelGlobalGroup").mockResolvedValue({ cancelled: 1, runs: [{ ...m2, status: "cancelled" }] });
    expect(await useGlobalAgents.getState().cancelGroup("grp1")).toBe(1);
    expect(cancel).toHaveBeenCalledWith("grp1");
    expect(useRuns.getState().globalById.m2.status).toBe("cancelled");
  });

  it("refetches loaded groups and loaded contests on global-runs-changed without a new realtime event", async () => {
    vi.useFakeTimers();
    const grp: RunGroup = {
      id: "grp1",
      runnerId: "r1",
      createdBy: "u1",
      title: "Bump CI",
      agent: "claude",
      model: "opus",
      noPR: false,
      repoCount: 2,
      createdAt: "2026-11-14T08:00:00Z",
    };
    const m1 = globalRun({ id: "m1", groupId: "grp1", status: "running" });
    const c1Run = globalRun({ id: "c1a", contestId: "c1", contestLabel: "A", status: "running" });

    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({ runs: [m1, c1Run], more: false, runners: [] });
    const getGroup = vi.spyOn(api, "getGlobalGroup").mockResolvedValue({ group: grp, runs: [m1] });
    const getContest = vi.spyOn(api, "getContest").mockResolvedValue({
      contest: {
        id: "c1",
        title: "Compare",
        global: true,
        state: "running",
        entryCount: 2,
        createdBy: "u1",
        createdAt: "2026-11-14T08:00:00Z",
      },
      runs: [c1Run],
      compare: { entries: [], scores: null },
    });

    await useGlobalAgents.getState().loadGroup("grp1");
    await useCoordStore.getState().loadContest("c1");
    useGlobalAgents.getState().setWatching(true);
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));

    getGroup.mockClear();
    getContest.mockClear();
    list.mockClear();

    useGlobalAgents.getState().onChanged("m1");
    vi.advanceTimersByTime(GLOBAL_COALESCE_MS);
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(getGroup).toHaveBeenCalledWith("grp1"));
    await vi.waitFor(() => expect(getContest).toHaveBeenCalledWith("c1"));
  });
});
