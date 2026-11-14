import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

import { api, forgetGlobalRuns, type Run, type RunnerLoad } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { makeView } from "../runs/workspaceFixtures";
import { useWorkspace } from "../runs/workspaceStore";
import { useApp } from "../store";
import { FOCUS_MS, GlobalAgentList } from "./GlobalAgentList";
import { GLOBAL_PAGE, useGlobalAgents } from "./globalAgentsStore";

// Who is signed in: alice, who owns runner r1, unless a test says.
let viewer: { id: string; name: string; role: string } | null = { id: "u1", name: "alice", role: "member" };
vi.mock("../components/SignIn", async (original) => ({
  ...(await original<typeof import("../components/SignIn")>()),
  useViewer: () => viewer,
}));

function makeGlobalRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "g1",
    project: "",
    nodeId: "",
    global: true,
    title: "Fix the flaky login test",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/fix-the-flaky-login-test-g1aaaaaa",
    prompt: "",
    status: "running",
    exitCode: 0,
    usage: { inputTokens: 12000, outputTokens: 345, costUsd: 0.042 },
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-14T08:00:00Z",
    startedAt: "2026-11-14T08:00:05Z",
    ...overrides,
  };
}

function makeLoad(overrides: Partial<RunnerLoad> = {}): RunnerLoad {
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
    today: { runs: 1, tokens: 12345, costUsd: 0.042 },
    ...overrides,
  };
}

function seed(runs: Run[], more = false) {
  useGlobalAgents.setState({ runs, loaded: true, more, runners: [makeLoad()] });
}

function show() {
  return render(
    <>
      <GlobalAgentList />
      <ConfirmDialogHost />
    </>,
  );
}

function rowOf(title: string): HTMLElement {
  return screen.getByText(title).closest("tr") as HTMLElement;
}

beforeEach(() => {
  viewer = { id: "u1", name: "alice", role: "member" };
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  forgetGlobalRuns();
  useGlobalAgents.getState().reset();
  useRuns.getState().reset();
  useRuns.setState({ globalById: {}, cancelling: {}, cancelErrors: {}, stopRequested: {} });
  useWorkspace.getState().reset();
  useWorkspace.setState({ open: null });
  useRunners.setState({ runners: [], loaded: false });
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView({ runId: "g1" }));
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("GlobalAgentList", () => {
  it("lists the agents at work, then the recent ones, with their branch, runner, usage, PR and chips", () => {
    const working = makeGlobalRun();
    const talking = makeGlobalRun({ id: "g3", title: "Talk it through", mode: "interactive", usage: undefined });
    const merged = makeGlobalRun({
      id: "g2",
      title: "Bump the linter",
      status: "succeeded",
      branch: "agent/bump-the-linter-g2aaaaaa",
      startedAt: "2026-11-14T08:00:00Z",
      finishedAt: "2026-11-14T08:04:05Z",
      commit: "c0ffee1",
      prUrl: "https://github.com/o/r/pull/9",
      prNumber: 9,
      prState: "open",
      ciStatus: "success",
    });
    const kept = makeGlobalRun({
      id: "g4",
      title: "Try a refactor",
      status: "succeeded",
      noPR: true,
      commit: "abc1234",
      finishedAt: "2026-11-14T08:10:00Z",
    });
    const local = makeGlobalRun({
      id: "g5",
      title: "Rename a helper",
      status: "succeeded",
      noPR: true,
      workspace: "merged_local",
      finishedAt: "2026-11-14T08:10:00Z",
    });
    seed([working, talking, merged, kept, local]);
    show();

    expect(screen.getByRole("heading", { name: "Working (2)" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Recent" })).toBeInTheDocument();

    const row = rowOf("Fix the flaky login test");
    expect(within(row).getByText("Running")).toBeInTheDocument();
    expect(within(row).getByText("Claude opus")).toBeInTheDocument();
    expect(within(row).getByText("nodevas · agent/fix-the-flaky-login-test-g1aaaaaa ← main")).toBeInTheDocument();
    expect(within(row).getByText("desktop")).toBeInTheDocument();
    expect(within(row).getByText("12.3K tokens · $0.042")).toBeInTheDocument();
    // Headless and at work: it cannot ask anything.
    expect(within(row).getByText("Cannot ask")).toHaveAttribute(
      "title",
      "Runs in the background: it cannot ask you anything. Start one Interactive to talk with it.",
    );
    expect(within(row).getByRole("button", { name: "Copy the branch name" })).toBeInTheDocument();

    const interactive = rowOf("Talk it through");
    expect(within(interactive).getByText("Interactive")).toBeInTheDocument();
    expect(within(interactive).queryByText("Cannot ask")).not.toBeInTheDocument();
    expect(within(interactive).getByText("—")).toBeInTheDocument();

    const prRow = rowOf("Bump the linter");
    expect(within(prRow).getByText("Succeeded")).toBeInTheDocument();
    expect(within(prRow).getByText("4m 05s")).toBeInTheDocument();
    expect(within(prRow).getByRole("link", { name: /PR #9/ })).toHaveAttribute("href", "https://github.com/o/r/pull/9");
    expect(within(prRow).getByRole("img", { name: "CI passed" })).toBeInTheDocument();
    // An open PR merges through its workspace: no local merge or discard here.
    expect(within(prRow).queryByRole("button", { name: "Merge locally" })).not.toBeInTheDocument();
    expect(within(prRow).queryByRole("button", { name: "Discard" })).not.toBeInTheDocument();
    expect(within(prRow).queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();

    expect(within(rowOf("Try a refactor")).getByText("Branch kept on the runner")).toBeInTheDocument();
    expect(within(rowOf("Rename a helper")).getByText("Merged locally")).toBeInTheDocument();
    expect(within(rowOf("Rename a helper")).queryByText("Branch kept on the runner")).not.toBeInTheDocument();
  });

  it("shows the run store's fresher copy of a row", () => {
    const run = makeGlobalRun();
    seed([run]);
    useRuns.setState({ globalById: { g1: { ...run, status: "failed", finishedAt: "2026-11-14T08:01:00Z" } } });
    show();
    expect(within(rowOf("Fix the flaky login test")).getByText("Failed")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Working (0)" })).toBeInTheDocument();
  });

  it("opens a run's workspace", () => {
    seed([makeGlobalRun()]);
    show();
    fireEvent.click(within(rowOf("Fix the flaky login test")).getByRole("button", { name: "Workspace" }));
    expect(useWorkspace.getState().open?.runId).toBe("g1");
  });

  it("asks before stopping one, and stops it on yes", async () => {
    const run = makeGlobalRun();
    seed([run]);
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue({ ...run });
    show();

    fireEvent.click(within(rowOf("Fix the flaky login test")).getByRole("button", { name: "Stop" }));
    let confirm = await screen.findByRole("alertdialog", { name: "Stop Claude opus?" });
    expect(confirm).toHaveTextContent(
      "It stops on desktop; what it committed stays on agent/fix-the-flaky-login-test-g1aaaaaa.",
    );
    fireEvent.click(within(confirm).getByRole("button", { name: "Keep it" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(cancel).not.toHaveBeenCalled();

    fireEvent.click(within(rowOf("Fix the flaky login test")).getByRole("button", { name: "Stop" }));
    confirm = await screen.findByRole("alertdialog", { name: "Stop Claude opus?" });
    fireEvent.click(within(confirm).getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledWith("g1"));
    // Still running until its runner says it ended.
    expect(await within(rowOf("Fix the flaky login test")).findByRole("button", { name: "Stopping…" })).toBeDisabled();
  });

  it("says why a stop was refused", async () => {
    seed([makeGlobalRun()]);
    vi.spyOn(api, "cancelRun").mockRejectedValue(new Error("runner went away"));
    show();
    fireEvent.click(within(rowOf("Fix the flaky login test")).getByRole("button", { name: "Stop" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Stop" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("runner went away");
  });

  it("offers Stop all from two agents at work, and stops them all", async () => {
    const one = makeGlobalRun();
    const two = makeGlobalRun({ id: "g2", title: "Second task", status: "queued", startedAt: undefined });
    seed([one, two]);
    const cancelAll = vi.spyOn(api, "cancelAllGlobalRuns").mockResolvedValue({
      cancelled: 2,
      runs: [
        { ...one, status: "cancelled", finishedAt: "2026-11-14T08:05:00Z" },
        { ...two, status: "cancelled", finishedAt: "2026-11-14T08:05:00Z" },
      ],
    });
    show();

    fireEvent.click(screen.getByRole("button", { name: "Stop all" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Stop 2 global agents?" });
    expect(confirm).toHaveTextContent("Each stops on its runner; what each committed stays on its branch.");
    fireEvent.click(within(confirm).getByRole("button", { name: "Stop all" }));
    await waitFor(() => expect(cancelAll).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole("heading", { name: "Working (0)" })).toBeInTheDocument();
    expect(screen.getAllByText("Cancelled")).toHaveLength(2);
  });

  it("has no Stop all for a single agent at work", () => {
    seed([makeGlobalRun(), makeGlobalRun({ id: "g2", title: "Done", status: "succeeded" })]);
    show();
    expect(screen.queryByRole("button", { name: "Stop all" })).not.toBeInTheDocument();
  });

  it("merges an ended run's branch locally after the workspace's own question", async () => {
    const run = makeGlobalRun({
      id: "g2",
      title: "Try a refactor",
      status: "succeeded",
      noPR: true,
      commit: "c0ffee1234567",
      finishedAt: "2026-11-14T08:10:00Z",
    });
    seed([run]);
    const merge = vi.spyOn(api, "mergeRunLocal").mockResolvedValue({ ...run, workspace: "merged_local" });
    show();

    fireEvent.click(within(rowOf("Try a refactor")).getByRole("button", { name: "Merge locally" }));
    const confirm = await screen.findByRole("alertdialog", {
      name: "Merge agent/fix-the-flaky-login-test-g1aaaaaa into main?",
    });
    fireEvent.click(within(confirm).getByRole("button", { name: "Merge locally" }));
    await waitFor(() => expect(merge).toHaveBeenCalledWith("g2", "c0ffee1234567"));
    // The answer is the row's now: merged, nothing left to merge or discard.
    expect(await within(rowOf("Try a refactor")).findByText("Merged locally")).toBeInTheDocument();
    expect(within(rowOf("Try a refactor")).queryByRole("button", { name: "Discard" })).not.toBeInTheDocument();
  });

  it("discards an ended run after the workspace's own question", async () => {
    const run = makeGlobalRun({ id: "g2", title: "Try a refactor", status: "failed", noPR: true });
    seed([run]);
    const discard = vi.spyOn(api, "discardRun").mockResolvedValue({ ...run, workspace: "discarded" });
    show();

    // Nothing was committed: there is nothing to merge.
    expect(within(rowOf("Try a refactor")).queryByRole("button", { name: "Merge locally" })).not.toBeInTheDocument();
    fireEvent.click(within(rowOf("Try a refactor")).getByRole("button", { name: "Discard" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Discard this run's work?" });
    expect(discard).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(discard).toHaveBeenCalledWith("g2"));
  });

  it("offers no local merge or discard on someone else's runner", () => {
    viewer = { id: "u9", name: "admin", role: "admin" };
    useGlobalAgents.setState({ runners: [] });
    useRunners.setState({
      runners: [
        {
          id: "r1",
          name: "desktop",
          ownerId: "u1",
          ownerName: "alice",
          createdAt: "",
          lastSeenAt: "",
          online: true,
        },
      ],
    });
    useGlobalAgents.setState({
      runs: [makeGlobalRun({ status: "succeeded", commit: "abc", finishedAt: "2026-11-14T08:10:00Z" })],
      loaded: true,
    });
    show();
    const row = rowOf("Fix the flaky login test");
    expect(within(row).getByRole("button", { name: "Workspace" })).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Merge locally" })).not.toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Discard" })).not.toBeInTheDocument();
  });

  it("loads older ended runs from the oldest one held", async () => {
    const ended = makeGlobalRun({
      id: "g2",
      title: "Newer",
      status: "succeeded",
      createdAt: "2026-11-14T07:00:00Z",
      finishedAt: "2026-11-14T07:10:00Z",
    });
    seed([makeGlobalRun(), ended], true);
    const list = vi.spyOn(api, "listGlobalRuns").mockResolvedValue({
      runs: [
        makeGlobalRun({
          id: "g0",
          title: "Older",
          status: "failed",
          createdAt: "2026-11-13T07:00:00Z",
          finishedAt: "2026-11-13T07:10:00Z",
        }),
      ],
      more: false,
      runners: [],
    });
    show();

    fireEvent.click(screen.getByRole("button", { name: "Load older" }));
    await waitFor(() =>
      expect(list).toHaveBeenCalledWith({ limit: GLOBAL_PAGE, before: "2026-11-14T07:00:00Z" }),
    );
    expect(await screen.findByText("Older")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load older" })).not.toBeInTheDocument();
  });

  it("says when there is none yet, while loading, and when the list failed", () => {
    const { unmount } = show();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    unmount();

    useGlobalAgents.setState({ loaded: true, runs: [] });
    const second = show();
    expect(screen.getByText("No global agents yet.")).toBeInTheDocument();
    second.unmount();

    useGlobalAgents.setState({ loaded: true, runs: [], error: "hub down" });
    show();
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load global agents: hub down");
  });

  it("highlights the row opened from elsewhere, brings it into view, then lets go", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
    const scroll = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scroll;
    onTestFinished(() => {
      Element.prototype.scrollIntoView = original;
    });
    seed([makeGlobalRun(), makeGlobalRun({ id: "g2", title: "Other" })]);
    useGlobalAgents.setState({ focus: "g2" });
    show();

    expect(rowOf("Other")).toHaveClass("is-focus");
    expect(rowOf("Fix the flaky login test")).not.toHaveClass("is-focus");
    expect(scroll).toHaveBeenCalledWith({ block: "nearest" });

    act(() => {
      vi.advanceTimersByTime(FOCUS_MS);
    });
    expect(useGlobalAgents.getState().focus).toBeNull();
    expect(rowOf("Other")).not.toHaveClass("is-focus");
  });
});
