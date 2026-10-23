import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, ConflictError, type Run, type RunMergeRequest } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { RunDiffDialog } from "./DiffViewer";
import { MergeControl, mergeGate, pendingMergeLeft, shortSha } from "./MergeApproval";
import { findRun, useRuns } from "./runStore";

const HEAD = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678";

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
    commit: "c0ffee1",
    filesChanged: 2,
    additions: 2,
    deletions: 1,
    prUrl: "https://github.com/example/nodevas/pull/42",
    prNumber: 42,
    prState: "open",
    ciStatus: "success",
    prHeadSha: HEAD,
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
    headSha: HEAD,
    status: "pending",
    requestedBy: "alice",
    // Just approved: a pending merge older than 15 minutes is stale.
    createdAt: new Date(Date.now() - 60_000).toISOString(),
    ...overrides,
  };
}

/** The control as the run view uses it: fed from the store, so a push (or
 * the merge response) re-renders it. */
function Live({ runId }: { runId: string }) {
  const run = useRuns((s) => findRun(s, runId));
  return run ? <MergeControl run={run} /> : null;
}

function seed(run: Run) {
  useRuns.setState({ runsByNode: { [run.nodeId]: [run] } });
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRuns.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("mergeGate", () => {
  it("is ready only for an open PR with green CI and a known head", () => {
    expect(mergeGate(makeRun())).toEqual({ kind: "ready" });
    expect(mergeGate(makeRun({ ciStatus: "pending" }))).toEqual({ kind: "blocked", reason: "ci-pending" });
    expect(mergeGate(makeRun({ ciStatus: "failure" }))).toEqual({ kind: "blocked", reason: "ci-failure" });
    expect(mergeGate(makeRun({ ciStatus: "" }))).toEqual({ kind: "blocked", reason: "ci-none" });
    expect(mergeGate(makeRun({ prHeadSha: undefined }))).toEqual({ kind: "blocked", reason: "no-head" });
    expect(mergeGate(makeRun({ merge: merge() }))).toEqual({ kind: "pending" });
    expect(mergeGate(makeRun({ prState: "merged" }))).toEqual({ kind: "none" });
    expect(mergeGate(makeRun({ prState: "closed" }))).toEqual({ kind: "none" });
    expect(mergeGate(makeRun({ prUrl: undefined }))).toEqual({ kind: "none" });
    expect(mergeGate(makeRun({ prUrl: "javascript:alert(1)" }))).toEqual({ kind: "none" });
    // A failed merge can be approved again.
    expect(mergeGate(makeRun({ merge: merge({ status: "failed", reason: "x" }) }))).toEqual({
      kind: "ready",
    });
    expect(shortSha(HEAD)).toBe("a1b2c3d");
  });

  it("lets a pending merge older than the hub's 15 minutes be approved again", () => {
    const createdAt = "2026-10-24T08:00:00Z";
    const at = Date.parse(createdAt);
    const run = makeRun({ merge: merge({ createdAt }) });
    expect(pendingMergeLeft(run, at + 60_000)).toBe(14 * 60_000);
    expect(mergeGate(run, at + 14 * 60_000)).toEqual({ kind: "pending" });
    expect(pendingMergeLeft(run, at + 16 * 60_000)).toBe(0);
    expect(mergeGate(run, at + 16 * 60_000)).toEqual({ kind: "ready" });
  });
});

describe("MergeControl", () => {
  it("offers Merge PR when the PR is open and green", () => {
    render(<MergeControl run={makeRun()} />);
    const button = screen.getByRole("button", { name: "Merge PR #42" });
    expect(button).toBeEnabled();
  });

  it.each([
    ["pending", "CI is still running."],
    ["failure", "CI is failing."],
    ["", "No CI checks reported; the runner only merges a PR whose checks pass."],
  ])("disables Merge with a reason while CI is %j", (ciStatus, reason) => {
    render(<MergeControl run={makeRun({ ciStatus })} />);
    const button = screen.getByRole("button", { name: "Merge PR #42" });
    expect(button).toBeDisabled();
    expect(screen.getByText(reason)).toBeVisible();
    expect(button).toHaveAccessibleDescription(reason);
  });

  it("disables Merge until the runner reports the PR head", () => {
    render(<MergeControl run={makeRun({ prHeadSha: "" })} />);
    expect(screen.getByRole("button", { name: "Merge PR #42" })).toBeDisabled();
    expect(screen.getByText(/head commit/)).toBeVisible();
  });

  it("shows nothing without an open PR", () => {
    const { container, rerender } = render(<MergeControl run={makeRun({ prUrl: undefined })} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<MergeControl run={makeRun({ prState: "merged" })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("names PR, branch, base and head in the confirmation and posts that head", async () => {
    const run = makeRun();
    seed(run);
    let resolve!: (value: Run) => void;
    const post = vi
      .spyOn(api, "mergeRun")
      .mockImplementation(() => new Promise<Run>((done) => (resolve = done)));

    render(<Live runId="run2" />);
    fireEvent.click(screen.getByRole("button", { name: "Merge PR #42" }));

    const dialog = screen.getByRole("alertdialog", { name: "Merge PR #42?" });
    expect(within(dialog).getByText("PR #42")).toBeInTheDocument();
    expect(within(dialog).getByText("nodevas")).toBeInTheDocument();
    expect(within(dialog).getByText("agent/alpha-run2 → main")).toBeInTheDocument();
    expect(within(dialog).getByText("a1b2c3d")).toBeInTheDocument();
    expect(within(dialog).getByText(/squash by default/)).toBeInTheDocument();
    // Cancel has the focus: Enter alone must not merge anything.
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();

    fireEvent.click(within(dialog).getByRole("button", { name: "Merge PR #42" }));
    expect(post).toHaveBeenCalledWith("run2", HEAD);
    expect(within(dialog).getByRole("button", { name: "Approving…" })).toBeDisabled();

    await act(async () => resolve({ ...run, merge: merge() }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Waiting for the runner to merge…");
    expect(screen.queryByRole("button", { name: "Merge PR #42" })).not.toBeInTheDocument();
  });

  it("posts the head the approver saw, even if a push lands while confirming", async () => {
    const run = makeRun();
    seed(run);
    const post = vi.spyOn(api, "mergeRun").mockResolvedValue({ ...run, merge: merge() });
    render(<Live runId="run2" />);
    fireEvent.click(screen.getByRole("button", { name: "Merge PR #42" }));
    act(() => seed({ ...run, prHeadSha: "ffffffffffffffff" }));
    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByText("a1b2c3d")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Merge PR #42" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("run2", HEAD));
  });

  it("cancels without posting", () => {
    const post = vi.spyOn(api, "mergeRun");
    render(<MergeControl run={makeRun()} />);
    fireEvent.click(screen.getByRole("button", { name: "Merge PR #42" }));
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("renders a merged, failed and expired merge", () => {
    const { rerender } = render(
      <MergeControl
        run={makeRun({
          prState: "merged",
          merge: merge({ status: "merged", finishedAt: "2026-10-24T08:21:00Z" }),
        })}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Merged PR #42");
    expect(screen.queryByRole("button", { name: /Merge PR/ })).not.toBeInTheDocument();

    rerender(
      <MergeControl
        run={makeRun({ merge: merge({ status: "failed", reason: "branch protection: review required" }) })}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Merge failed: branch protection: review required",
    );
    // Still open and green: it may be approved again.
    expect(screen.getByRole("button", { name: "Merge PR #42" })).toBeEnabled();

    rerender(<MergeControl run={makeRun({ merge: merge({ status: "expired" }) })} />);
    expect(screen.getByRole("status")).toHaveTextContent(
      "The approval expired before the runner merged it.",
    );
    expect(screen.getByRole("button", { name: "Merge PR #42" })).toBeEnabled();
  });

  it("says a stale pending approval expired and offers it again", () => {
    render(
      <MergeControl
        run={makeRun({
          merge: merge({ createdAt: new Date(Date.now() - 20 * 60_000).toISOString() }),
        })}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("approve again");
    expect(screen.getByRole("button", { name: "Merge PR #42" })).toBeEnabled();
  });

  it("turns a pending approval stale while on screen", () => {
    vi.useFakeTimers();
    try {
      render(<MergeControl run={makeRun({ merge: merge() })} />);
      expect(screen.getByRole("status")).toHaveTextContent("Waiting for the runner to merge…");
      act(() => {
        vi.advanceTimersByTime(15 * 60_000);
      });
      expect(screen.getByRole("status")).toHaveTextContent("approve again");
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the hub's 409 reason and refreshes the run", async () => {
    const run = makeRun();
    seed(run);
    vi.spyOn(api, "mergeRun").mockRejectedValue(
      new ConflictError("", "", "the PR changed since you looked; reload", "FAILED_PRECONDITION"),
    );
    const refresh = vi.spyOn(api, "getRun").mockResolvedValue({ ...run, prHeadSha: "f00" });
    render(<Live runId="run2" />);
    fireEvent.click(screen.getByRole("button", { name: "Merge PR #42" }));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Merge PR #42" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not merged: the PR changed since you looked; reload",
    );
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(refresh).toHaveBeenCalledWith("run2"));
  });

  it("says when the caller may not approve merges", async () => {
    vi.spyOn(api, "mergeRun").mockRejectedValue(
      new AuthError(403, "agent sessions may not approve merges"),
    );
    render(<MergeControl run={makeRun()} />);
    fireEvent.click(screen.getByRole("button", { name: "Merge PR #42" }));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Merge PR #42" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not allowed: agent sessions may not approve merges",
    );
  });
});

describe("merge from the diff viewer", () => {
  it("sits at the diff's foot, and Escape closes only the confirmation", async () => {
    vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: null });
    const onClose = vi.fn();
    render(<RunDiffDialog run={makeRun()} onClose={onClose} />);
    const diff = screen.getByRole("dialog", { name: "Changes" });
    fireEvent.click(within(diff).getByRole("button", { name: "Merge PR #42" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
