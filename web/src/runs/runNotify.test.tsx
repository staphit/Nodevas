import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunMergeRequest } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { resetRunNotify, runNotifyKind, useRunToasts } from "./runNotify";
import { RunToasts } from "./RunToasts";
import { useRuns } from "./runStore";

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
