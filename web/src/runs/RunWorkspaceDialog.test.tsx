import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, forgetGlobalRuns, rememberGlobalRun, WorkspaceActionError, type Run, type RunWorkspaceView } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { openRunWorkspace } from "./openWorkspace";
import { RunWorkspaceDialog } from "./RunWorkspaceDialog";
import { RunWorkspaceHost } from "./RunWorkspaceHost";
import { useRuns } from "./runStore";
import { useWorkspace } from "./workspaceStore";
import { makeAction, makeInfo, makeView, makeWorkspaceRun, notOwnerView } from "./workspaceFixtures";

/** The panel as the app draws it: the open run's, on the store's tab. */
function Panel() {
  const open = useWorkspace((s) => s.open);
  const close = useWorkspace((s) => s.close);
  return (
    <>
      {open && <RunWorkspaceDialog runId={open.runId} tab={open.tab} onClose={close} />}
      <ConfirmDialogHost />
    </>
  );
}

function seed(run: Run, view: RunWorkspaceView = makeView()) {
  useRuns.setState({ runsByNode: { [run.nodeId]: [run] } });
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(view);
  vi.spyOn(api, "getRunChanges").mockResolvedValue({
    changes: {
      commit: run.commit ?? "",
      base: run.base,
      files: [{ path: "src/a.ts", status: "modified", additions: 5, deletions: 1 }],
      additions: 5,
      deletions: 1,
      patch: "diff --git a/src/a.ts b/src/a.ts\n--- a/src/a.ts\n+++ b/src/a.ts\n@@ -1 +1 @@\n-old\n+new\n",
    },
  });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({
    events: [{ seq: 1, type: "log", at: "2026-11-12T08:00:01Z", text: "worktree created" }],
    more: false,
  });
  vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
  vi.spyOn(api, "workspaceTree").mockResolvedValue({
    path: "",
    entries: [{ name: "README.md", type: "file", size: 10 }],
    hidden: 0,
    truncated: false,
  });
}

function show(run: Run, tab?: "files" | "changes" | "terminal") {
  render(<Panel />);
  act(() => openRunWorkspace(run.id, tab));
}

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { nodes: [{ id: "alpha", title: "Login form" }] } as never,
  });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  useWorkspace.getState().reset();
  useWorkspace.setState({ open: null });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RunWorkspaceDialog", () => {
  it("names the agent, node, runner, branch and base, status, PR and CI", async () => {
    const run = makeWorkspaceRun({
      prUrl: "https://github.com/o/r/pull/9",
      prNumber: 9,
      prState: "open",
      ciStatus: "success",
      prHeadSha: "abc",
    });
    seed(run, makeView({ merge: { mode: "pr", blocked: "" } }));
    show(run);
    const dialog = await screen.findByRole("dialog", { name: "Agent workspace: Claude opus" });
    expect(within(dialog).getByRole("button", { name: "Login form" })).toBeInTheDocument();
    expect(within(dialog).getByText("desktop")).toBeInTheDocument();
    expect(within(dialog).getByRole("img", { name: "Online" })).toBeInTheDocument();
    expect(within(dialog).getByText("agent/alpha-run7abcd ← main")).toBeInTheDocument();
    expect(within(dialog).getByText("Succeeded")).toBeInTheDocument();
    const chips = dialog.querySelector(".run-ws-chips") as HTMLElement;
    expect(within(chips).getByRole("link", { name: /PR #9/ })).toHaveAttribute("href", "https://github.com/o/r/pull/9");
    expect(within(chips).getByRole("img", { name: "CI passed" })).toBeInTheDocument();
    // The runner's word on the worktree.
    expect(
      await within(dialog).findByText(
        "Worktree on desktop · 3 uncommitted files · main is not checked out; a temporary worktree is used",
      ),
    ).toBeInTheDocument();
  });

  it("switches tabs by click and by arrow keys; a headless run shows its log", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    show(run, "changes");
    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((tab) => tab.textContent)).toEqual(["Files", "Changes", "Log"]);
    expect(screen.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
    // Changes: the run's diff against its base, from /changes.
    expect(await screen.findByText("src/a.ts", { selector: ".run-diff-file-path" })).toBeInTheDocument();
    expect(api.getRunChanges).toHaveBeenCalledWith("run7");

    fireEvent.click(screen.getByRole("tab", { name: "Log" }));
    expect(screen.getByRole("tab", { name: "Log" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("worktree created")).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole("tab", { name: "Log" }), { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Files" })).toHaveFocus();
    expect(await screen.findByRole("button", { name: "README.md" })).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole("tab", { name: "Files" }), { key: "ArrowLeft" });
    expect(screen.getByRole("tab", { name: "Log" })).toHaveAttribute("aria-selected", "true");
  });

  it("names the terminal tab for an interactive run", async () => {
    const run = makeWorkspaceRun({ mode: "interactive", status: "running" });
    seed(run, makeView({ merge: { mode: "", blocked: "running" }, discard: { allowed: false, blocked: "running" } }));
    show(run, "files");
    expect((await screen.findAllByRole("tab")).map((tab) => tab.textContent)).toEqual([
      "Files",
      "Changes",
      "Terminal",
    ]);
  });

  it("shows a non-owner no Merge or Discard, and Files says why", async () => {
    const run = makeWorkspaceRun();
    seed(run, notOwnerView());
    show(run, "files");
    expect(await screen.findByText("Only the runner's owner can browse its files")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Merge into main locally/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Discard" })).not.toBeInTheDocument();
    expect(api.workspaceTree).not.toHaveBeenCalled();
  });

  it("uses the PR's merge control when the run has an open PR", async () => {
    const run = makeWorkspaceRun({
      prUrl: "https://github.com/o/r/pull/9",
      prNumber: 9,
      prState: "open",
      ciStatus: "success",
      prHeadSha: "abc",
    });
    seed(run, makeView({ merge: { mode: "pr", blocked: "" } }));
    show(run);
    expect(await screen.findByRole("button", { name: "Merge PR #9" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: /locally/ })).not.toBeInTheDocument();
  });

  it("disables a blocked action and says why in the reader's words", async () => {
    const run = makeWorkspaceRun();
    seed(run, makeView({ merge: { mode: "local", blocked: "actions_off" }, discard: { allowed: false, blocked: "pending" } }));
    show(run);
    const merge = await screen.findByRole("button", { name: "Merge into main locally" });
    expect(merge).toBeDisabled();
    expect(merge).toHaveAttribute("title", "The runner's owner turned workspace actions off (workspaceActions)");
    const discard = screen.getByRole("button", { name: "Discard" });
    expect(discard).toBeDisabled();
    expect(discard).toHaveAttribute("title", "Another action on this run is waiting for the runner");
  });

  it("closes on Escape", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    show(run);
    await screen.findByRole("dialog");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(useWorkspace.getState().open).toBeNull();
  });
});

describe("workspace actions", () => {
  it("merges locally only once confirmed, posting the run's commit", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    const merged = vi.spyOn(api, "mergeRunLocal").mockResolvedValue({
      ...run,
      workspaceAction: makeAction({ status: "pending" }),
    });
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Merge into main locally" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Merge agent/alpha-run7abcd into main?" });
    expect(confirm).toHaveTextContent("main is not checked out, so your runner merges in a temporary worktree");
    expect(confirm).toHaveTextContent("Then the run's worktree and branch agent/alpha-run7abcd are removed.");

    // Escape goes to the confirmation, not the panel under it.
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(screen.getByRole("dialog", { name: /Agent workspace/ })).toBeInTheDocument();
    expect(merged).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Merge into main locally" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Merge locally" }));
    await waitFor(() => expect(merged).toHaveBeenCalledWith("run7", "c0ffee1234567"));
    // 202: the answer is pending, and the panel says so.
    expect(await screen.findByText("Waiting for the runner to merge…")).toBeInTheDocument();
  });

  it("names where the merge happens when base is checked out and clean", async () => {
    const run = makeWorkspaceRun();
    seed(run, makeView({ info: { ...makeView().info!, baseCheckout: "repo", baseClean: true } }));
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Merge into main locally" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent(
      "Your runner merges with --no-ff in your checkout of main, which is clean. Nothing is pushed.",
    );
  });

  it("never calls a checkout clean that last looked unclean", async () => {
    const run = makeWorkspaceRun();
    seed(run, makeView({ info: { ...makeView().info!, baseCheckout: "repo", baseClean: false } }));
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Merge into main locally" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("which last looked unclean: it checks again and merges nothing unless the checkout is clean");
    expect(confirm).not.toHaveTextContent("which is clean");
  });

  it("shows a runner's refusal as its translated code and its own detail", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    vi.spyOn(api, "mergeRunLocal").mockResolvedValue({
      ...run,
      workspaceAction: makeAction({
        status: "failed",
        code: "conflict",
        reason: "CONFLICT (content): src/a.ts",
        finishedAt: new Date().toISOString(),
      }),
    });
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Merge into main locally" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Merge locally" }));
    const alert = await screen.findByText(/Local merge failed: The merge has conflicts; nothing was changed/);
    expect(alert.closest('[role="alert"]')).toHaveTextContent("CONFLICT (content): src/a.ts");
  });

  it("shows a hub refusal (409) in the reader's words", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    vi.spyOn(api, "mergeRunLocal").mockRejectedValue(
      new WorkspaceActionError(409, "FAILED_PRECONDITION", "head_moved", "the branch moved"),
    );
    vi.spyOn(api, "getRun").mockResolvedValue(run);
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Merge into main locally" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Merge locally" }));
    expect(await screen.findByText("The branch changed since you looked; reload")).toBeInTheDocument();
  });

  it("lists what a discard removes and posts it once confirmed", async () => {
    const run = makeWorkspaceRun({
      prUrl: "https://github.com/o/r/pull/9",
      prNumber: 9,
      prState: "open",
      ciStatus: "failure",
      prHeadSha: "abc",
    });
    seed(
      run,
      makeView({ merge: { mode: "pr", blocked: "" }, info: makeInfo({ watchedPr: "https://github.com/o/r/pull/9" }) }),
    );
    const discarded = vi.spyOn(api, "discardRun").mockResolvedValue({
      ...run,
      workspace: "discarded",
      workspaceAction: makeAction({ action: "discard", status: "done", finishedAt: new Date().toISOString() }),
    });
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Discard" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Discard this run's work?" });
    expect(confirm).toHaveTextContent("Removes the worktree, with 3 uncommitted files.");
    expect(confirm).toHaveTextContent("Deletes branch agent/alpha-run7abcd.");
    expect(confirm).toHaveTextContent("Closes PR #9 and deletes its branch on GitHub.");
    expect(confirm).toHaveTextContent("This cannot be undone.");

    fireEvent.click(within(confirm).getByRole("button", { name: "Keep" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(discarded).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(discarded).toHaveBeenCalledWith("run7"));
    // The run's row follows at once, before any push.
    await waitFor(() => expect(useRuns.getState().runsByNode.alpha[0].workspace).toBe("discarded"));
    expect(await screen.findByText("Discarded", { selector: ".run-ws-status-line" })).toBeInTheDocument();
  });

  // The confirm promises only what the runner will really remove.
  it.each([
    {
      name: "no answer from the runner",
      run: {},
      info: null,
      has: ["may hold uncommitted files (the runner could not check)", "Deletes branch agent/alpha-run7abcd."],
      lacks: ["0 uncommitted"],
    },
    {
      name: "an open PR, no answer from the runner",
      run: { prUrl: "https://github.com/o/r/pull/9", prNumber: 9, prState: "open" },
      info: null,
      has: ["If your runner watches PR #9, closes it and deletes its branch on GitHub (the runner could not be asked)."],
      lacks: ["Closes PR"],
    },
    {
      name: "a status the runner could not read",
      run: {},
      info: makeInfo({ uncommitted: -1 }),
      has: ["may hold uncommitted files"],
      lacks: ["-1", "0 uncommitted"],
    },
    {
      name: "a review run",
      run: { kind: "review", prUrl: "https://github.com/o/r/pull/9", prNumber: 9, prState: "open" },
      info: makeInfo({ ownBranch: false, watchedPr: "https://github.com/o/r/pull/9" }),
      has: ["Removes the worktree, with 3 uncommitted files."],
      lacks: ["Deletes branch", "Closes PR"],
    },
    {
      name: "a PR the runner does not watch",
      run: { prUrl: "https://github.com/o/r/pull/9", prNumber: 9, prState: "open" },
      info: makeInfo(),
      has: ["Deletes branch agent/alpha-run7abcd."],
      lacks: ["Closes PR"],
    },
    {
      name: "a branch moved past the run's commit",
      run: {},
      info: makeInfo({ branchHead: "feed5678" }),
      has: ["Branch agent/alpha-run7abcd has moved past the run's commit"],
      lacks: [],
    },
  ])("discard confirm: $name", async ({ run: overrides, info, has, lacks }) => {
    const run = makeWorkspaceRun(overrides as Partial<Run>);
    seed(run, makeView({ merge: { mode: "", blocked: "kind" }, info, infoError: info ? "" : "timeout" }));
    show(run);
    fireEvent.click(await screen.findByRole("button", { name: "Discard" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Discard this run's work?" });
    for (const text of has) expect(confirm).toHaveTextContent(text);
    for (const text of lacks) expect(confirm).not.toHaveTextContent(text);
  });
});

describe("RunWorkspaceHost", () => {
  it("draws the panel openRunWorkspace asks for", async () => {
    const run = makeWorkspaceRun();
    seed(run);
    render(<RunWorkspaceHost />);
    act(() => openRunWorkspace(run.id));
    expect(await screen.findByRole("dialog", { name: "Agent workspace: Claude opus" })).toBeInTheDocument();
    // A finished run opens on what it changed.
    expect(screen.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("a global agent's workspace", () => {
  const globalRun = (overrides: Partial<Run> = {}) =>
    makeWorkspaceRun({
      id: "g1",
      project: "",
      nodeId: "",
      global: true,
      title: "Fix the login button",
      branch: "agent/fix-the-login-button-g1000000",
      noPR: true,
      ...overrides,
    });

  /** Answers the global routes with the run and its view; records the URLs. */
  function serve(run: Run, view: RunWorkspaceView = makeView({ runId: run.id })) {
    return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      const body = url.endsWith("/workspace")
        ? view
        : url.includes("/changes")
          ? { changes: null }
          : url.includes("/events")
            ? { events: [], more: false }
            : url.includes("/asks")
              ? { asks: [] }
              : run;
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
  }

  afterEach(() => {
    forgetGlobalRuns();
    useRuns.setState({ globalById: {} });
  });

  it("names its task and says it is global, with no node to open", async () => {
    const run = globalRun();
    rememberGlobalRun(run.id);
    useRuns.getState().putRun(run);
    const fetch = serve(run);
    render(<Panel />);
    act(() => openRunWorkspace(run.id, "changes"));
    const dialog = await screen.findByRole("dialog", { name: "Agent workspace: Claude opus" });
    expect(within(dialog).getByText("Task")).toBeInTheDocument();
    expect(within(dialog).getByText("Fix the login button")).toBeInTheDocument();
    expect(within(dialog).getByText("Global agent")).toBeInTheDocument();
    expect(within(dialog).queryByText("Node")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(fetch.mock.calls.map(([url]) => String(url))).toContain("/api/global-runs/g1/workspace"),
    );
    expect(fetch.mock.calls.map(([url]) => String(url))).toContain("/api/global-runs/g1/changes");
    expect(fetch.mock.calls.some(([url]) => String(url).startsWith("/api/runs/"))).toBe(false);
  });

  it("says a headless one at work cannot ask anything", async () => {
    const run = globalRun({ status: "running", finishedAt: undefined, commit: undefined });
    rememberGlobalRun(run.id);
    useRuns.getState().putRun(run);
    serve(run, makeView({ runId: run.id, merge: { mode: "", blocked: "running" }, discard: { allowed: false, blocked: "running" } }));
    render(<Panel />);
    act(() => openRunWorkspace(run.id, "changes"));
    expect(
      await screen.findByText(
        "Runs in the background: it cannot ask you anything. Start one Interactive to talk with it.",
      ),
    ).toBeInTheDocument();
  });

  it("fetches a global run no list holds from its own route", async () => {
    // Its own id: an earlier test's late answers cannot hold it already.
    const run = globalRun({ id: "g3" });
    rememberGlobalRun(run.id);
    const fetch = serve(run, makeView({ runId: run.id }));
    render(<Panel />);
    act(() => openRunWorkspace(run.id, "changes"));
    expect(await screen.findByText("Fix the login button")).toBeInTheDocument();
    expect(fetch.mock.calls.map(([url]) => String(url))).toContain("/api/global-runs/g3");
    expect(useRuns.getState().globalById.g3).toEqual(run);
  });
});
