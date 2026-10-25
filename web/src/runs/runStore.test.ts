import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunChanges, type RunEvent } from "../api";
import { foldLatest, mergeEvents, useRuns } from "./runStore";

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
    branch: "",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

const ev = (seq: number, text = `e${seq}`): RunEvent => ({
  seq,
  type: "log",
  at: "2026-10-24T08:00:00.000Z",
  text,
});

beforeEach(() => {
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("mergeEvents", () => {
  it("drops seqs already held and keeps seq order", () => {
    const merged = mergeEvents([ev(1), ev(2)], [ev(2, "dup"), ev(4), ev(3), ev(4, "dup")]);
    expect(merged.map((event) => event.seq)).toEqual([1, 2, 3, 4]);
    expect(merged[1].text).toBe("e2");
    expect(merged[3].text).toBe("e4");
  });

  it("returns the same array when nothing is new", () => {
    const held = [ev(1)];
    expect(mergeEvents(held, [ev(1)])).toBe(held);
    expect(mergeEvents(held, [])).toBe(held);
  });
});

describe("useRuns", () => {
  it("loads events after the last seq, page by page, without duplicates", async () => {
    const get = vi
      .spyOn(api, "getRunEvents")
      .mockResolvedValueOnce({ events: [ev(1), ev(2)], more: true })
      .mockResolvedValueOnce({ events: [ev(2), ev(3)], more: false })
      .mockResolvedValueOnce({ events: [ev(3), ev(4)], more: false });

    await useRuns.getState().loadEvents("run1");
    expect(get).toHaveBeenNthCalledWith(1, "run1", 0);
    expect(get).toHaveBeenNthCalledWith(2, "run1", 2);
    expect(useRuns.getState().eventsByRun.run1.map((event) => event.seq)).toEqual([1, 2, 3]);

    await useRuns.getState().loadEvents("run1");
    expect(get).toHaveBeenNthCalledWith(3, "run1", 3);
    expect(useRuns.getState().eventsByRun.run1.map((event) => event.seq)).toEqual([1, 2, 3, 4]);
  });

  it("follows run-events only for watched runs", async () => {
    const get = vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [ev(1)], more: false });

    useRuns.getState().onRunEvents("run1");
    expect(get).not.toHaveBeenCalled();

    const unwatch = useRuns.getState().watchRun("run1");
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(useRuns.getState().eventsByRun.run1).toHaveLength(1));

    get.mockResolvedValue({ events: [ev(2)], more: false });
    useRuns.getState().onRunEvents("run1");
    await vi.waitFor(() => expect(useRuns.getState().eventsByRun.run1).toHaveLength(2));
    expect(get).toHaveBeenLastCalledWith("run1", 1);

    unwatch();
    useRuns.getState().onRunEvents("run1");
    expect(get).toHaveBeenCalledTimes(2);
    expect(useRuns.getState().watched.run1).toBeUndefined();
  });

  it("coalesces a push that lands mid-fetch into one more round", async () => {
    let release!: () => void;
    const get = vi
      .spyOn(api, "getRunEvents")
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            release = () => resolve({ events: [ev(1)], more: false });
          }),
      )
      .mockResolvedValue({ events: [ev(2)], more: false });

    useRuns.setState({ watched: { run1: 1 } });
    const first = useRuns.getState().loadEvents("run1");
    useRuns.getState().onRunEvents("run1");
    useRuns.getState().onRunEvents("run1");
    release();
    await first;

    expect(get).toHaveBeenCalledTimes(2);
    expect(get).toHaveBeenLastCalledWith("run1", 1);
    expect(useRuns.getState().eventsByRun.run1.map((event) => event.seq)).toEqual([1, 2]);
  });

  it("refreshes a node's runs on runs-changed only once loaded", async () => {
    const list = vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun()] });

    useRuns.getState().onRunsChanged("alpha");
    expect(list).not.toHaveBeenCalled();

    await useRuns.getState().loadRuns("alpha");
    expect(useRuns.getState().runsByNode.alpha).toHaveLength(1);

    list.mockResolvedValue({ runs: [makeRun({ id: "run2" }), makeRun()] });
    useRuns.getState().onRunsChanged("alpha");
    await vi.waitFor(() => expect(useRuns.getState().runsByNode.alpha).toHaveLength(2));
    expect(list).toHaveBeenCalledWith("alpha");
    useRuns.getState().onRunsChanged("beta");
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("keeps a list error per node", async () => {
    vi.spyOn(api, "listRuns").mockRejectedValue(new Error("boom"));
    await useRuns.getState().loadRuns("alpha");
    expect(useRuns.getState().nodeErrors.alpha).toBe("boom");
    expect(useRuns.getState().loadingNodes.alpha).toBeUndefined();
  });

  it("puts a created run on top of its node's list", async () => {
    useRuns.setState({ runsByNode: { alpha: [makeRun({ id: "old" })] } });
    vi.spyOn(api, "createRun").mockResolvedValue(makeRun({ id: "new", status: "queued" }));

    const run = await useRuns.getState().createRun({
      nodeId: "alpha",
      runnerId: "r1",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
    });
    expect(run.id).toBe("new");
    expect(useRuns.getState().runsByNode.alpha.map((item) => item.id)).toEqual(["new", "old"]);
  });

  it("replaces a cancelled run in place and reports a failed cancel", async () => {
    useRuns.setState({ runsByNode: { alpha: [makeRun()] } });
    const cancel = vi
      .spyOn(api, "cancelRun")
      .mockResolvedValueOnce(makeRun({ status: "cancelled" }))
      .mockRejectedValueOnce(new Error("already finished"));

    expect(await useRuns.getState().cancelRun("run1")).toBe(true);
    expect(cancel).toHaveBeenCalledWith("run1");
    expect(useRuns.getState().runsByNode.alpha[0].status).toBe("cancelled");
    expect(useRuns.getState().cancelling.run1).toBeUndefined();

    expect(await useRuns.getState().cancelRun("run1")).toBe(false);
    expect(useRuns.getState().cancelErrors.run1).toBe("already finished");
  });

  it("drops a response that arrives after a project switch", async () => {
    let release!: () => void;
    vi.spyOn(api, "listRuns").mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve({ runs: [makeRun()] });
        }),
    );
    const pending = useRuns.getState().loadRuns("alpha");
    useRuns.getState().reset();
    release();
    await pending;
    expect(useRuns.getState().runsByNode.alpha).toBeUndefined();
  });

  it("asks the detail view for a node's runs and selects the run", () => {
    useRuns.getState().showRuns("alpha", "run1");
    expect(useRuns.getState().showRunsRequest?.nodeId).toBe("alpha");
    expect(useRuns.getState().selectedRun.alpha).toBe("run1");
    useRuns.getState().clearShowRunsRequest();
    expect(useRuns.getState().showRunsRequest).toBeNull();
  });
});

describe("run changes", () => {
  const changes = (commit: string): RunChanges => ({
    commit,
    base: "main",
    files: [{ path: "a.go", status: "modified", additions: 1, deletions: 0 }],
    additions: 1,
    deletions: 0,
    patch: "",
  });

  it("fetches a run's changes once per commit, sharing an in-flight request", async () => {
    const get = vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: changes("c1") });
    await Promise.all([
      useRuns.getState().loadChanges("run1", "c1"),
      useRuns.getState().loadChanges("run1", "c1"),
    ]);
    await useRuns.getState().loadChanges("run1", "c1");
    expect(get).toHaveBeenCalledTimes(1);
    expect(useRuns.getState().changesByRun.run1.changes?.commit).toBe("c1");
    expect(useRuns.getState().changesLoading.run1).toBeUndefined();

    // A new commit on the run makes the held copy stale.
    get.mockResolvedValueOnce({ changes: changes("c2") });
    await useRuns.getState().loadChanges("run1", "c2");
    expect(get).toHaveBeenCalledTimes(2);
    expect(useRuns.getState().changesByRun.run1.commit).toBe("c2");
  });

  it("holds a null answer and reports a failure", async () => {
    const get = vi
      .spyOn(api, "getRunChanges")
      .mockResolvedValueOnce({ changes: null })
      .mockRejectedValueOnce(new Error("boom"));
    await useRuns.getState().loadChanges("run1", undefined);
    expect(useRuns.getState().changesByRun.run1).toEqual({ commit: "", changes: null });
    await useRuns.getState().loadChanges("run1", undefined);
    expect(get).toHaveBeenCalledTimes(1);

    await useRuns.getState().loadChanges("run2", "c9");
    expect(useRuns.getState().changesErrors.run2).toBe("boom");
    expect(useRuns.getState().changesByRun.run2).toBeUndefined();
  });
});

describe("latest run per node", () => {
  it("asks for the project's runs once and keeps the newest per node", async () => {
    const list = vi.spyOn(api, "listProjectRuns").mockResolvedValue({
      runs: [
        makeRun({ id: "b2", nodeId: "beta", createdAt: "2026-10-24T09:00:00Z" }),
        makeRun({ id: "a1", nodeId: "alpha", createdAt: "2026-10-24T08:00:00Z" }),
        makeRun({ id: "b1", nodeId: "beta", createdAt: "2026-10-24T07:00:00Z" }),
      ],
    });
    await Promise.all([useRuns.getState().loadProjectRuns(), useRuns.getState().loadProjectRuns()]);
    await useRuns.getState().loadProjectRuns();
    expect(list).toHaveBeenCalledTimes(1);
    const latest = useRuns.getState().latestByNode;
    expect(latest.alpha.id).toBe("a1");
    expect(latest.beta.id).toBe("b2");
  });

  it("follows runs-changed for unopened nodes only once the board asked", async () => {
    const listRuns = vi
      .spyOn(api, "listRuns")
      .mockResolvedValue({ runs: [makeRun({ id: "g1", nodeId: "gamma", prState: "merged" })] });
    useRuns.getState().onRunsChanged("gamma");
    expect(listRuns).not.toHaveBeenCalled();

    vi.spyOn(api, "listProjectRuns").mockResolvedValue({ runs: [] });
    await useRuns.getState().loadProjectRuns();
    useRuns.getState().onRunsChanged("gamma");
    await vi.waitFor(() => expect(useRuns.getState().latestByNode.gamma?.id).toBe("g1"));
    expect(listRuns).toHaveBeenCalledWith("gamma");
  });

  it("lets a node's own list replace what the project list said", async () => {
    useRuns.setState({ latestByNode: { alpha: makeRun({ id: "stale", status: "running" }) } });
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [] });
    await useRuns.getState().loadRuns("alpha");
    expect(useRuns.getState().latestByNode.alpha).toBeUndefined();
  });
});

describe("foldLatest", () => {
  it("replaces the same run and ignores older ones", () => {
    const first = makeRun({ id: "x", createdAt: "2026-10-24T08:00:00Z" });
    const held = foldLatest({}, [first]);
    expect(foldLatest(held, [makeRun({ id: "old", createdAt: "2026-10-23T08:00:00Z" })])).toBe(held);
    const updated = foldLatest(held, [{ ...first, prState: "open" }]);
    expect(updated.alpha.prState).toBe("open");
  });
});
