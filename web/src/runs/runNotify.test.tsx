import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, forgetGlobalRuns, rememberGlobalRun, type Run, type RunMergeRequest } from "../api";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { resetRunNotify, runNotifyKind, runNotifyText, useRunToasts } from "./runNotify";
import { RunToasts } from "./RunToasts";
import { useRuns } from "./runStore";
import { useWorkspace } from "./workspaceStore";

const systemNotify = vi.hoisted(() => ({ show: vi.fn(() => true) }));
vi.mock("../agents/systemNotify", () => ({
  showSystemNotification: systemNotify.show,
  systemNotifyPermission: () => "unsupported",
  requestSystemNotifications: async () => "unsupported",
}));

const NOW = Date.parse("2026-10-24T08:12:00Z");

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run2",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha-run2",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    prUrl: "https://github.com/example/nodevas/pull/42",
    prNumber: 42,
    prState: "open",
    ciStatus: "pending",
    prHeadSha: "abc",
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    finishedAt: "2026-10-24T08:10:00Z",
    ...overrides,
  };
}

function merge(overrides: Partial<RunMergeRequest> = {}): RunMergeRequest {
  return {
    id: "m1",
    runId: "run2",
    headSha: "abc",
    status: "merged",
    requestedBy: "u1",
    createdAt: "2026-10-24T08:10:30Z",
    finishedAt: "2026-10-24T08:11:00Z",
    ...overrides,
  };
}

let hidden = false;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  hidden = false;
  vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
  systemNotify.show.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { nodes: [{ id: "alpha", title: "Design doc" }] } as never,
  });
  useRuns.getState().reset();
  resetRunNotify();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function budgetStatus(exceeded = "") {
  return {
    budget: { maxRuns: 5, maxCostUsd: 0, maxTokens: 0 },
    used: { runs: exceeded ? 5 : 1, costUsd: 0, tokens: 0 },
    exceeded,
  } as never;
}

async function notify(run: Run, exceeded = "") {
  vi.spyOn(api, "getRun").mockResolvedValue(run);
  vi.spyOn(api, "getRunBudget").mockResolvedValue(budgetStatus(exceeded));
  await useRunToasts.getState().handleRunNotify(run.id);
  // The budget is checked alongside; let it land.
  await Promise.resolve();
  await Promise.resolve();
}

describe("runNotifyKind", () => {
  it("reads the newest lifecycle step: merge, then red CI, then the run's end", () => {
    expect(runNotifyKind(makeRun(), NOW)).toEqual({ kind: "succeeded", key: "run2:succeeded" });
    expect(runNotifyKind(makeRun({ status: "failed", prUrl: undefined }), NOW)?.kind).toBe("failed");
    expect(runNotifyKind(makeRun({ ciStatus: "failure" }), NOW)).toEqual({
      kind: "ci-failed",
      key: "run2:ci-failed:abc",
    });
    expect(runNotifyKind(makeRun({ merge: merge() }), NOW)).toEqual({
      kind: "merged",
      key: "run2:merge:m1",
    });
    expect(runNotifyKind(makeRun({ merge: merge({ status: "failed" }) }), NOW)?.kind).toBe(
      "merge-failed",
    );
    expect(runNotifyKind(makeRun({ merge: merge({ status: "expired" }) }), NOW)?.kind).toBe(
      "merge-failed",
    );
    // A pending merge is not news yet; the run's own end still is.
    expect(runNotifyKind(makeRun({ merge: merge({ status: "pending" }) }), NOW)?.kind).toBe(
      "succeeded",
    );
    // Neither is an old one: a red CI after it is.
    expect(
      runNotifyKind(
        makeRun({ ciStatus: "failure", merge: merge({ status: "failed", finishedAt: "2026-10-23T00:00:00Z" }) }),
        NOW,
      )?.kind,
    ).toBe("ci-failed");
    expect(runNotifyKind(makeRun({ status: "running", finishedAt: undefined }), NOW)).toBeNull();
    expect(runNotifyKind(makeRun({ finishedAt: "2026-10-23T08:00:00Z" }), NOW)).toBeNull();
  });
});

describe("run-notify toasts", () => {
  it.each([
    [makeRun(), "Run succeeded: Design doc", "PR #42"],
    [makeRun({ status: "failed", prUrl: undefined, prNumber: undefined, error: "exit status 1" }), "Run failed: Design doc", "exit status 1"],
    [makeRun({ ciStatus: "failure" }), "CI failed on PR #42", "Design doc"],
    [makeRun({ prState: "merged", merge: merge() }), "Merged PR #42", "Design doc"],
    [
      makeRun({ merge: merge({ status: "failed", reason: "review required" }) }),
      "Merge failed: review required",
      "Design doc",
    ],
    [
      makeRun({ merge: merge({ status: "expired" }) }),
      "Merge failed: The approval expired before the runner merged it.",
      "Design doc",
    ],
  ])("toasts %#", async (run, title, body) => {
    render(<RunToasts />);
    await notify(run);
    const toast = await screen.findByText(title);
    expect(within(toast.closest(".run-toast") as HTMLElement).getByText(body)).toBeInTheDocument();
  });

  it("toasts each (run, kind) once", async () => {
    render(<RunToasts />);
    await notify(makeRun());
    await notify(makeRun());
    expect(screen.getAllByText("Run succeeded: Design doc")).toHaveLength(1);

    // A new kind for the same run is news.
    await notify(makeRun({ ciStatus: "failure" }));
    expect(screen.getByText("CI failed on PR #42")).toBeInTheDocument();
    await notify(makeRun({ ciStatus: "failure" }));
    expect(screen.getAllByText("CI failed on PR #42")).toHaveLength(1);
    expect(useRunToasts.getState().toasts).toHaveLength(2);
  });

  it("falls back to the node id and stays quiet when the run cannot be read", async () => {
    useApp.setState({ graph: { nodes: [] } as never });
    render(<RunToasts />);
    await notify(makeRun());
    expect(screen.getByText("Run succeeded: alpha")).toBeInTheDocument();

    vi.spyOn(api, "getRun").mockRejectedValue(new Error("404"));
    await useRunToasts.getState().handleRunNotify("gone");
    expect(useRunToasts.getState().toasts).toHaveLength(1);
  });

  it("opens the node's Runs tab on that run when clicked", async () => {
    const openTab = vi.fn().mockResolvedValue(undefined);
    useApp.setState({ openTab } as never);
    render(<RunToasts />);
    await notify(makeRun({ ciStatus: "failure" }));

    fireEvent.click(screen.getByRole("button", { name: /CI failed on PR #42/ }));
    expect(openTab).toHaveBeenCalledWith("alpha");
    expect(useRuns.getState().showRunsRequest?.nodeId).toBe("alpha");
    expect(useRuns.getState().selectedRun.alpha).toBe("run2");
    expect(screen.queryByText("CI failed on PR #42")).not.toBeInTheDocument();
  });

  it("dismisses a toast", async () => {
    render(<RunToasts />);
    await notify(makeRun());
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("Run succeeded: Design doc")).not.toBeInTheDocument();
  });

  it("raises a system notification only while the page is hidden", async () => {
    await notify(makeRun());
    expect(systemNotify.show).not.toHaveBeenCalled();

    hidden = true;
    await notify(makeRun({ merge: merge() }));
    expect(systemNotify.show).toHaveBeenCalledTimes(1);
    expect(systemNotify.show).toHaveBeenCalledWith(
      "Merged PR #42",
      "Design doc",
      "nodevas-run-run2:merge:m1",
      expect.any(Function),
    );
  });
});

describe("usage limits and the daily budget", () => {
  it("says a run failed on its agent's usage limit rather than plain failed", async () => {
    const limited = makeRun({ status: "failed", prUrl: undefined, failureKind: "quota", error: "usage limit reached" });
    expect(runNotifyKind(limited, NOW)).toEqual({ kind: "limit", key: "run2:limit" });
    expect(runNotifyKind({ ...limited, failureKind: "rate_limit" }, NOW)?.kind).toBe("limit");
    expect(runNotifyKind({ ...limited, failureKind: "task" }, NOW)?.kind).toBe("failed");

    render(<RunToasts />);
    await notify(limited);
    const toast = await screen.findByText("claude reached its usage limit: Design doc");
    expect(toast.closest(".run-toast")?.className).toContain("is-limit");
    expect(screen.getByText("usage limit reached")).toBeTruthy();
  });

  it("tells once per UTC day that the budget is used up, and opens it", async () => {
    render(<RunToasts />);
    const opened = vi.fn();
    window.addEventListener("nodevas-open-budget", opened);
    await notify(makeRun(), "runs");
    await notify(makeRun({ id: "run3" }), "runs");
    expect(await screen.findAllByText("Daily budget used up")).toHaveLength(1);

    fireEvent.click(screen.getByText("Daily budget used up"));
    expect(opened).toHaveBeenCalledTimes(1);
    window.removeEventListener("nodevas-open-budget", opened);

    // A new UTC day is news again.
    vi.setSystemTime(NOW + 24 * 60 * 60 * 1000);
    await notify(makeRun({ id: "run4", finishedAt: new Date(NOW + 24 * 60 * 60 * 1000 - 60_000).toISOString() }), "runs");
    expect(await screen.findAllByText("Daily budget used up")).toHaveLength(1);
  });

  it("says nothing about a budget that is not used up", async () => {
    render(<RunToasts />);
    await notify(makeRun());
    expect(screen.queryByText("Daily budget used up")).toBeNull();
  });
});

describe("workspace actions", () => {
  const action = (status: string, overrides = {}) => ({
    id: "wa1",
    runId: "run2",
    action: "merge_local",
    status,
    requestedBy: "alice",
    createdAt: "2026-10-24T08:11:00Z",
    finishedAt: "2026-10-24T08:11:30Z",
    ...overrides,
  });

  it("tells once when the runner merged locally, newest step first", () => {
    const run = makeRun({ merge: merge(), workspaceAction: action("done") });
    expect(runNotifyKind(run, NOW)).toEqual({ kind: "workspace-merged", key: "run2:workspace:wa1" });
    expect(runNotifyKind(makeRun({ workspaceAction: action("pending") }), NOW)?.kind).not.toMatch(/^workspace/);
    expect(runNotifyKind(makeRun({ workspaceAction: action("done", { action: "discard" }) }), NOW)?.kind).toBe(
      "workspace-discarded",
    );
    expect(runNotifyKind(makeRun({ workspaceAction: action("expired") }), NOW)?.kind).toBe("workspace-failed");
  });

  it("toasts a refused local merge in the reader's words and opens the workspace", async () => {
    const run = makeRun({ workspaceAction: action("failed", { code: "dirty_base" }) });
    vi.spyOn(api, "getRun").mockResolvedValue(run);
    vi.spyOn(api, "getRunBudget").mockResolvedValue({
      budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
      used: { runs: 0, costUsd: 0, tokens: 0 },
      exceeded: "",
    });
    const openWorkspace = vi.fn();
    useWorkspace.setState({ openWorkspace });
    render(<RunToasts />);
    await useRunToasts.getState().handleRunNotify("run2");
    const toast = await screen.findByText("Local merge for Claude opus failed");
    expect(toast.closest("[role]") ?? toast.parentElement).toHaveTextContent(
      "Your checkout of the base has uncommitted changes or an unfinished merge or rebase",
    );
    // The answer reaches the run lists at once.
    expect(useRuns.getState().runsByNode.alpha[0].workspaceAction?.status).toBe("failed");
    fireEvent.click(toast);
    expect(openWorkspace).toHaveBeenCalledWith("run2", undefined);
  });
});

describe("global agents", () => {
  const globalRun = (overrides: Partial<Run> = {}) =>
    makeRun({
      id: "g1",
      project: "",
      nodeId: "",
      global: true,
      title: "Fix the login button",
      prUrl: undefined,
      prNumber: undefined,
      prState: undefined,
      ciStatus: undefined,
      ...overrides,
    });

  afterEach(() => {
    forgetGlobalRuns();
    useRuns.setState({ globalById: {} });
  });

  it("fetches a global-run-notify's run from its own route and toasts its task", async () => {
    const run = globalRun();
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      const body = url.startsWith("/api/global-runs/g1")
        ? run
        : { budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 }, used: { runs: 0, costUsd: 0, tokens: 0 }, exceeded: "" };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    const openWorkspace = vi.fn();
    useWorkspace.setState({ openWorkspace });
    render(<RunToasts />);
    // As App does for global-run-notify: the id is known global first.
    rememberGlobalRun("g1");
    await useRunToasts.getState().handleRunNotify("g1");
    expect(fetch.mock.calls.map(([url]) => String(url))).toContain("/api/global-runs/g1");
    const toast = await screen.findByText("Global agent Claude opus finished: Fix the login button");
    // Held for its workspace, outside any node's list.
    expect(useRuns.getState().globalById.g1).toEqual(run);
    expect(useRuns.getState().runsByNode).toEqual({});
    fireEvent.click(toast);
    expect(openWorkspace).toHaveBeenCalledWith("g1", undefined);
  });

  it("says a global agent failed with its error", async () => {
    await notify(globalRun({ status: "failed", error: "tests failed" }));
    const [toast] = useRunToasts.getState().toasts;
    expect(toast).toMatchObject({
      runId: "g1",
      kind: "failed",
      global: true,
      title: "Global agent Claude opus failed: Fix the login button",
      body: "tests failed",
    });
  });

  it("names the task in a global run's merge and workspace toasts", async () => {
    await notify(globalRun({ prUrl: "https://github.com/o/r/pull/7", prNumber: 7, prState: "merged", merge: merge({ runId: "g1" }) }));
    expect(useRunToasts.getState().toasts[0]).toMatchObject({ kind: "merged", body: "Fix the login button" });
  });
});

// Single-user edition: a result on this computer's own runner is told in
// plain words, as something to look at and apply.
describe("results on this computer", () => {
  const local = (overrides: Partial<Run> = {}) =>
    makeRun({
      runnerId: "local1",
      runnerName: "this machine",
      commit: "c0ffee",
      prUrl: undefined,
      prNumber: undefined,
      prState: undefined,
      ciStatus: undefined,
      ...overrides,
    });

  beforeEach(() => useRunners.setState({ runners: [localRunner()], loaded: true }));
  afterEach(() => useRunners.setState({ runners: [], loaded: false }));

  it("says a finished result is ready to look at and apply", () => {
    expect(runNotifyText(local(), "succeeded")).toEqual({
      title: "Claude opus finished: Design doc",
      body: "Take a look, then apply it to your folder.",
    });
    expect(
      runNotifyText(local({ id: "g1", global: true, nodeId: "", title: "Bakery page" }), "succeeded"),
    ).toEqual({ title: "Claude opus finished: Bakery page", body: "Take a look, then apply it to your folder." });
  });

  it("says an apply landed in the folder", () => {
    const run = local({
      workspace: "merged_local",
      workspaceAction: {
        id: "wa1",
        runId: "run2",
        action: "merge_local",
        status: "done",
        requestedBy: "alice",
        createdAt: "2026-10-24T08:11:00Z",
        finishedAt: "2026-10-24T08:11:30Z",
      },
    });
    expect(runNotifyKind(run, NOW)?.kind).toBe("workspace-merged");
    expect(runNotifyText(run, "workspace-merged").title).toBe("Applied to your folder: Design doc");
  });

  it("offers Open my folder on the toast after an apply", async () => {
    const run = local({
      workspace: "merged_local",
      workspaceAction: {
        id: "wa1",
        runId: "run2",
        action: "merge_local",
        status: "done",
        requestedBy: "alice",
        createdAt: "2026-10-24T08:11:00Z",
        finishedAt: "2026-10-24T08:11:30Z",
      },
    });
    const openFolder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "D:\\code\\my-app" });
    const openWorkspace = vi.fn();
    useWorkspace.setState({ openWorkspace });
    render(<RunToasts />);
    await notify(run);
    const toast = screen.getByText("Applied to your folder: Design doc").closest(".run-toast") as HTMLElement;
    fireEvent.click(within(toast).getByRole("button", { name: "Open my folder" }));
    expect(openFolder).toHaveBeenCalledWith("D:\\code\\my-app");
    expect(openWorkspace).not.toHaveBeenCalled();
    expect(screen.queryByText("Applied to your folder: Design doc")).toBeNull();

    // Another runner's merge has no folder here to open.
    resetRunNotify();
    render(<RunToasts />);
    await notify({ ...run, runnerId: "r1" });
    expect(screen.queryByRole("button", { name: "Open my folder" })).toBeNull();
  });

  it("says a refused apply in plain words that say nothing was changed", () => {
    const refused = (status: "failed" | "expired", code?: string) =>
      local({
        workspaceAction: {
          id: "wa2",
          runId: "run2",
          action: "merge_local",
          status,
          code,
          reason: "base checkout has uncommitted changes",
          requestedBy: "alice",
          createdAt: "2026-10-24T08:11:00Z",
          finishedAt: "2026-10-24T08:11:30Z",
        },
      });
    const dirty = runNotifyText(refused("failed", "dirty_base"), "workspace-failed");
    expect(dirty).toEqual({
      title: "Not applied: Design doc",
      body: "Your folder has edits of your own that are not saved yet. Nothing was changed, so your edits are safe.",
    });
    const conflict = runNotifyText(refused("failed", "conflict"), "workspace-failed");
    expect(conflict.body).toMatch(/Nothing was changed/);
    for (const text of [dirty, conflict]) {
      expect(`${text.title} ${text.body}`).not.toMatch(/merge|branch|rebase|checkout|commit/i);
    }
    // In Chinese too.
    useApp.setState({ preferences: { ...useApp.getState().preferences, language: "zh-TW" } });
    try {
      const zh = runNotifyText(refused("failed", "dirty_base"), "workspace-failed");
      expect(`${zh.title} ${zh.body}`).not.toMatch(/合併|分支|rebase|提交/);
    } finally {
      useApp.setState({ preferences: { ...useApp.getState().preferences, language: "en" } });
    }
    // Another runner keeps the precise words.
    expect(runNotifyText({ ...refused("failed", "dirty_base"), runnerId: "r1" }, "workspace-failed").title).toMatch(
      /Local merge/,
    );
  });

  it("keeps the old words for a run with a PR, nothing committed, or another runner", () => {
    expect(runNotifyText(local({ prUrl: "https://github.com/o/r/pull/4", prNumber: 4 }), "succeeded").title).toBe(
      "Run succeeded: Design doc",
    );
    expect(runNotifyText(local({ commit: undefined }), "succeeded").body).not.toMatch(/apply/);
    expect(runNotifyText(local({ runnerId: "r1" }), "succeeded").body).not.toMatch(/apply/);
    expect(runNotifyText(local({ runnerId: "r1" }), "workspace-merged").title).not.toMatch(/Applied/);
  });
});
