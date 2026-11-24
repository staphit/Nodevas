import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

import { api, forgetGlobalRuns, rememberGlobalRuns, WorkspaceActionError, type Run, type RunnerLoad } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useCoordStore } from "../runs/coord/coordStore";
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
  useCoordStore.getState().reset();
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView({ runId: "g1" }));
  vi.spyOn(api, "getGlobalRunThread").mockResolvedValue({ runs: [] });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
  vi.spyOn(api, "getRunOverlaps").mockResolvedValue({ overlaps: [] });
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
    // The old noAsk chip is gone; replyNote takes its place in the list.
    expect(within(row).queryByText("Cannot ask")).not.toBeInTheDocument();
    expect(
      screen.getByText("Background runs cannot ask you anything; reply here when it finishes to continue."),
    ).toBeInTheDocument();
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

  // An agent that was signed out: the row says how to sign in on this
  // computer, or, on a team hub, to the runner's owner what to run there
  // and to anyone else whom to ask.
  it("says a signed-out agent needs signing in, to whoever can", () => {
    const failed = (overrides: Partial<Run> = {}) =>
      makeGlobalRun({
        status: "failed",
        error: "Not logged in · Please run /login",
        failureKind: "agent_unavailable",
        failureCode: "signed_out",
        finishedAt: "2026-11-14T08:00:09Z",
        ...overrides,
      });
    useRunners.setState({ runners: [{ ...localRunner(), id: "r1", name: "desktop", ownerId: "u1", builtIn: false }], loaded: true });
    seed([failed()]);
    const { unmount } = show();
    expect(rowOf("Fix the flaky login test")).toHaveTextContent(
      "Claude isn't signed in on desktop. Open a terminal there, run claude and sign in. Nodevas notices within a minute; then start this task again here.",
    );
    unmount();

    // Bob sees the run but neither owns r1 nor has its load.
    viewer = { id: "u2", name: "bob", role: "member" };
    useGlobalAgents.setState({ runners: [] });
    const again = show();
    expect(rowOf("Fix the flaky login test")).toHaveTextContent("Only the person that computer belongs to can sign it in");
    again.unmount();

    viewer = null;
    useRunners.setState({ runners: [localRunner()], loaded: true });
    seed([failed({ runnerId: "local1", runnerName: "this machine" })]);
    render(
      <EditionProvider edition="single">
        <GlobalAgentList />
      </EditionProvider>,
    );
    expect(rowOf("Fix the flaky login test")).toHaveTextContent(
      "Claude is on this computer but isn't signed in yet. Open the Terminal app on this computer, type claude",
    );
  });

  // Single-user edition: a finished result on this computer is offered in
  // plain words; the technical merge stays in its workspace panel.
  describe("plain result buttons", () => {
    const done = () =>
      makeGlobalRun({
        id: "g2",
        title: "Bakery page",
        runnerId: "local1",
        runnerName: "this machine",
        status: "succeeded",
        noPR: true,
        commit: "c0ffee1234567",
        finishedAt: "2026-11-14T08:10:00Z",
      });

    // "Start new work with this" on a merged PR opens the row and fills its
    // composer with the whole task; nothing starts until the person sends.
    it("opens the row's composer with the whole task for Start new work", async () => {
      viewer = null;
      useRunners.setState({ runners: [localRunner()], loaded: true });
      const merged = makeGlobalRun({
        id: "g3",
        title: "Bakery page",
        runnerId: "local1",
        runnerName: "this machine",
        status: "succeeded",
        noPR: false,
        prUrl: "https://github.com/o/r/pull/9",
        prState: "merged",
        finishedAt: "2026-11-14T08:10:00Z",
      });
      rememberGlobalRuns([merged]);
      useRuns.getState().putRun(merged);
      seed([merged]);
      const task = "Bakery page\n\nWith a menu and opening hours.";
      vi.mocked(api.getRunEvents).mockResolvedValue({
        events: [{ seq: 0, type: "user", text: task, at: "2026-11-14T08:00:00Z" }],
        more: false,
      });
      const create = vi.spyOn(api, "createGlobalRun");
      render(
        <EditionProvider edition="single">
          <GlobalAgentList />
          <ConfirmDialogHost />
        </EditionProvider>,
      );
      fireEvent.click(within(rowOf("Bakery page")).getByRole("button", { name: "Start new work with this" }));
      await waitFor(() =>
        expect(document.querySelector(".global-agent-dialog-row textarea.run-dlg-textarea")).toHaveValue(task),
      );
      expect(create).not.toHaveBeenCalled();
    });

    it("shows Take a look and Apply to my folder in place of Merge locally", () => {
      viewer = null;
      useRunners.setState({ runners: [localRunner()], loaded: true });
      seed([done()]);
      render(
        <EditionProvider edition="single">
          <GlobalAgentList />
          <ConfirmDialogHost />
        </EditionProvider>,
      );
      const row = rowOf("Bakery page");
      expect(within(row).getByRole("button", { name: "Take a look" })).toBeInTheDocument();
      expect(within(row).getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
      expect(within(row).queryByRole("button", { name: "Merge locally" })).not.toBeInTheDocument();
      expect(within(row).getByRole("button", { name: "Discard" })).toBeInTheDocument();
    });

    // A discard pressed beside the result buttons is not an apply: no
    // "Applying…" while it runs, and its refusal is the row's own words.
    it("says a refused discard as a discard, not as an apply", async () => {
      viewer = null;
      useRunners.setState({ runners: [localRunner()], loaded: true });
      seed([done()]);
      let refuse!: (error: unknown) => void;
      vi.spyOn(api, "discardRun").mockReturnValue(
        new Promise((_, reject) => {
          refuse = reject;
        }),
      );
      vi.spyOn(api, "getRun").mockResolvedValue(done());
      render(
        <EditionProvider edition="single">
          <GlobalAgentList />
          <ConfirmDialogHost />
        </EditionProvider>,
      );
      fireEvent.click(within(rowOf("Bakery page")).getByRole("button", { name: "Discard" }));
      fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Discard" }));
      await waitFor(() => expect(api.discardRun).toHaveBeenCalled());
      expect(within(rowOf("Bakery page")).queryByText("Applying…")).not.toBeInTheDocument();
      expect(within(rowOf("Bakery page")).getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();

      await act(async () => refuse(new WorkspaceActionError(409, "FAILED_PRECONDITION", "in_use", "the worktree is in use")));
      const alerts = within(rowOf("Bakery page")).getAllByRole("alert");
      expect(alerts).toHaveLength(1);
      expect(alerts[0]).toHaveClass("global-agent-error");
      expect(rowOf("Bakery page").textContent).not.toMatch(/could not be applied|Still working on it/);
    });

    // Simple: the row speaks of results, never of branches or pull requests.
    it("shows no branch or pull request words in Simple", async () => {
      viewer = null;
      useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
      useRunners.setState({ runners: [localRunner()], loaded: true });
      const running = makeGlobalRun({ id: "g3", title: "Menu page", runnerId: "local1", noPR: true });
      seed([done(), running, { ...done(), id: "g4", title: "Old page", workspace: "discarded", status: "failed" }]);
      const { container } = render(
        <EditionProvider edition="single">
          <GlobalAgentList />
          <ConfirmDialogHost />
        </EditionProvider>,
      );
      expect(screen.queryByRole("columnheader", { name: "Branch" })).not.toBeInTheDocument();
      expect(screen.queryByRole("columnheader", { name: "Pull request" })).not.toBeInTheDocument();
      expect(container.textContent).not.toMatch(/agent\/fix-the-flaky|Branch kept|branch|pull request/i);
      expect(within(rowOf("Bakery page")).getByRole("button", { name: "See what it did" })).toBeInTheDocument();
      fireEvent.click(within(rowOf("Menu page")).getByRole("button", { name: "Stop" }));
      const confirm = await screen.findByRole("alertdialog");
      expect(confirm.textContent).not.toMatch(/branch|commit|runner/i);
    });

    // Simple: an applied result says so in the person's words, usage is
    // what it cost (no token counts), and a result that is not ready to look
    // at is still applied in the person's words.
    it("says applied, cost only, and apply in Simple", () => {
      viewer = null;
      useApp.setState({ preferences: { ...useApp.getState().preferences, experience: "simple" } });
      useRunners.setState({ runners: [localRunner()], loaded: true });
      seed([
        { ...done(), id: "g5", title: "Applied page", workspace: "merged_local" },
        { ...done(), id: "g6", title: "Half page", status: "failed" },
      ]);
      render(
        <EditionProvider edition="single">
          <GlobalAgentList />
          <ConfirmDialogHost />
        </EditionProvider>,
      );
      const applied = rowOf("Applied page");
      expect(within(applied).getAllByText("Applied to your folder").length).toBeGreaterThan(0);
      expect(applied.textContent).not.toMatch(/Merged locally|tokens/);
      expect(applied.textContent).toMatch(/\$0\.04/);
      const half = rowOf("Half page");
      expect(within(half).getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
      expect(within(half).queryByRole("button", { name: "Merge locally" })).not.toBeInTheDocument();
    });

    it("keeps the precise words in full mode", () => {
      viewer = null;
      useRunners.setState({ runners: [localRunner()], loaded: true });
      seed([{ ...done(), id: "g5", title: "Applied page", workspace: "merged_local" }]);
      render(
        <EditionProvider edition="single">
          <GlobalAgentList />
        </EditionProvider>,
      );
      expect(rowOf("Applied page").textContent).toMatch(/Merged locally/);
      expect(rowOf("Applied page").textContent).toMatch(/tokens/);
    });

    it("leaves a team hub's row as it was", () => {
      seed([{ ...done(), runnerId: "r1" }]);
      show();
      const row = rowOf("Bakery page");
      expect(within(row).getByRole("button", { name: "Merge locally" })).toBeInTheDocument();
      expect(within(row).queryByRole("button", { name: "Apply to my folder" })).not.toBeInTheDocument();
    });
  });

  describe("chains, single expansion, overlap, waiting-behind, and contests", () => {
    it("shows only the latest turn for a follow-up chain and expands one row at a time", () => {
      const turn1 = makeGlobalRun({
        id: "g1",
        workRoot: "g1",
        continuedBy: "g2",
        title: "First draft",
        status: "succeeded",
        createdAt: "2026-11-14T08:00:00Z",
        startedAt: "2026-11-14T08:00:05Z",
        finishedAt: "2026-11-14T08:05:00Z",
      });
      const turn2 = makeGlobalRun({
        id: "g2",
        continueOf: "g1",
        workRoot: "g1",
        title: "Make the button bigger",
        status: "succeeded",
        createdAt: "2026-11-14T08:10:00Z",
        startedAt: "2026-11-14T08:10:05Z",
        finishedAt: "2026-11-14T08:15:00Z",
      });
      const other = makeGlobalRun({
        id: "g3",
        title: "Update README",
        status: "succeeded",
        createdAt: "2026-11-14T08:20:00Z",
        finishedAt: "2026-11-14T08:25:00Z",
      });
      useRuns.getState().putRuns([turn1, turn2, other]);
      seed([turn1, turn2, other]);
      show();

      expect(screen.queryByText("First draft")).not.toBeInTheDocument();
      expect(screen.getByText("Make the button bigger")).toBeInTheDocument();
      expect(screen.getByText("Turn 2")).toBeInTheDocument();

      fireEvent.click(within(rowOf("Make the button bigger")).getByRole("button", { name: "Make the button bigger" }));
      expect(screen.getAllByRole("region", { name: /session/i })).toHaveLength(1);
      expect(within(rowOf("Make the button bigger")).getByRole("button", { name: "Make the button bigger" })).toHaveAttribute(
        "aria-expanded",
        "true",
      );

      fireEvent.click(within(rowOf("Update README")).getByRole("button", { name: "Update README" }));
      expect(screen.getAllByRole("region", { name: /session/i })).toHaveLength(1);
      expect(within(rowOf("Make the button bigger")).getByRole("button", { name: "Make the button bigger" })).toHaveAttribute(
        "aria-expanded",
        "false",
      );
      expect(within(rowOf("Update README")).getByRole("button", { name: "Update README" })).toHaveAttribute(
        "aria-expanded",
        "true",
      );
    });

    it("shows the overlap badge and the waiting-behind line", () => {
      const overlapping = makeGlobalRun({
        id: "g1",
        title: "Touch auth",
        overlapCount: 2,
      });
      const waiting = makeGlobalRun({
        id: "g2",
        title: "Queued task",
        status: "queued",
        afterRun: "g1",
        startedAt: undefined,
      });
      seed([overlapping, waiting]);
      show();

      expect(within(rowOf("Touch auth")).getByText("2 overlap")).toBeInTheDocument();
      expect(within(rowOf("Queued task")).getByText("Waiting for an earlier run to end")).toBeInTheDocument();
    });

    it("renders a contest row with entry pips and expands to ContestView", async () => {
      const a = makeGlobalRun({
        id: "ca",
        contestId: "c1",
        contestLabel: "A",
        title: "Solve caching",
        status: "succeeded",
        commit: "aaa1111",
        noPR: true,
        finishedAt: "2026-11-14T08:10:00Z",
      });
      const b = makeGlobalRun({
        id: "cb",
        contestId: "c1",
        contestLabel: "B",
        title: "Solve caching",
        agent: "codex",
        model: "o3",
        status: "running",
        noPR: true,
      });
      vi.spyOn(api, "getContest").mockResolvedValue({
        contest: {
          id: "c1",
          title: "Solve caching",
          global: true,
          state: "running",
          entryCount: 2,
          createdBy: "u1",
          createdAt: "2026-11-14T08:00:00Z",
        },
        runs: [a, b],
        compare: {
          entries: [
            { label: "A", runId: "ca", agent: "claude", model: "opus", status: "succeeded", files: [], additions: 10, deletions: 2 },
            { label: "B", runId: "cb", agent: "codex", model: "o3", status: "running", files: [], additions: 0, deletions: 0 },
          ],
          scores: null,
        },
      });
      seed([a, b]);
      show();

      const contestRow = screen.getByTestId("global-contest-c1");
      expect(contestRow).toHaveTextContent("Solve caching");
      expect(contestRow).toHaveTextContent("2 agents on the same task");
      expect(within(contestRow).getByLabelText(/Entry A/i)).toBeInTheDocument();
      expect(within(contestRow).getByLabelText(/Entry B/i)).toBeInTheDocument();

      fireEvent.click(within(contestRow).getByRole("button", { name: /Solve caching/i }));
      await waitFor(() => expect(screen.getByTestId("contest-col-A")).toBeInTheDocument());
    });
  });
});
