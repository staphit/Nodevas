import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, BusyError, WorkspaceActionError, type Run } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { useCoordStore } from "./coord/coordStore";
import { ResultActions } from "./ResultActions";
import { useRuns } from "./runStore";
import { makeAction, makeInfo, makeView, makeWorkspaceRun } from "./workspaceFixtures";
import { useWorkspace } from "./workspaceStore";

/** A finished run on this computer's own runner (localRunner: local1, whose
 * repo1 is D:\code\my-app). */
const result = (overrides: Partial<Run> = {}) =>
  makeWorkspaceRun({ runnerId: "local1", runnerName: "this machine", repoName: "my-app", ...overrides });

function show(run: Run, edition: Edition = "single") {
  return render(
    <EditionProvider edition={edition}>
      <ResultActions run={run} />
      <ConfirmDialogHost />
    </EditionProvider>,
  );
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ runners: [localRunner()], loaded: true });
  useRuns.getState().reset();
  useWorkspace.getState().reset();
  useCoordStore.getState().reset();
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView({ info: makeInfo({ baseCheckout: "repo" }) }));
  vi.spyOn(api, "getRun").mockImplementation(async (id) => result({ id }));
});

afterEach(() => vi.restoreAllMocks());

describe("ResultActions", () => {
  // The dev server renders under StrictMode, which unmounts and mounts once
  // more: the answers must still show after that.
  it("still answers under StrictMode", async () => {
    vi.spyOn(api, "previewRun").mockResolvedValue({ web: false, folder: "D:\\work\\run7" });
    vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "D:\\work\\run7" });
    vi.mocked(api.getRunWorkspace).mockResolvedValue(makeView({ merge: { mode: "", blocked: "in_use" } }));
    render(
      <StrictMode>
        <EditionProvider edition="single">
          <ResultActions run={result()} />
          <ConfirmDialogHost />
        </EditionProvider>
      </StrictMode>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Take a look" }));
    expect(await screen.findByText(/not a web page, so its folder was opened instead/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Take a look" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Still working on it.");
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeEnabled();
  });

  // Only an apply is this component's to show: a discard pressed beside it
  // is neither "Applying…" nor an apply refused.
  it("shows nothing of a discard pressed beside it", () => {
    useWorkspace.setState({ acting: { run7: "discard" } });
    const { unmount } = show(result());
    expect(screen.queryByText("Applying…")).toBeNull();
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
    unmount();
    useWorkspace.setState({
      acting: {},
      actionErrors: { run7: "in use" },
      actionCodes: { run7: "in_use" },
      actionKinds: { run7: "discard" },
    });
    show(result());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("offers two plain buttons, and loads nothing until one is pressed", () => {
    show(result());
    expect(screen.getByRole("button", { name: "Take a look" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeInTheDocument();
    expect(api.getRunWorkspace).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toMatch(/merge|branch|commit|runner/i);
  });

  it("loads the workspace, asks, then applies at the run's commit", async () => {
    const merge = vi.spyOn(api, "mergeRunLocal").mockResolvedValue(result({ workspace: "merged_local" }));
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(api.getRunWorkspace).toHaveBeenCalledWith("run7");
    expect(within(dialog).getByText(/will be put into my-app/)).toBeInTheDocument();
    expect(merge).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(merge).toHaveBeenCalledWith("run7", "c0ffee1234567"));
  });

  it("applies nothing when the person says no", async () => {
    const merge = vi.spyOn(api, "mergeRunLocal");
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(merge).not.toHaveBeenCalled();
  });

  it("says a refusal in plain words, with the details folded away", async () => {
    vi.spyOn(api, "mergeRunLocal").mockRejectedValue(
      new WorkspaceActionError(409, "FAILED_PRECONDITION", "dirty_base", "base checkout has uncommitted changes"),
    );
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Apply" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Your folder has edits of your own that are not saved yet.");
    expect(alert.textContent).not.toMatch(/merge|branch/i);
    fireEvent.click(within(alert).getByRole("button", { name: "Show details" }));
    expect(alert.textContent).toMatch(/uncommitted|checkout/i);
  });

  it("says the runner's own refusal plainly too", () => {
    show(result({ workspaceAction: makeAction({ status: "failed", code: "conflict", reason: "CONFLICT (content)" }) }));
    expect(screen.getByRole("alert")).toHaveTextContent(/changed the same place in different ways/);
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeEnabled();
  });

  it("does not ask when the workspace already says it cannot be applied", async () => {
    vi.mocked(api.getRunWorkspace).mockResolvedValue(makeView({ merge: { mode: "", blocked: "in_use" } }));
    const merge = vi.spyOn(api, "mergeRunLocal");
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Still working on it.");
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(merge).not.toHaveBeenCalled();
  });

  it("shows Applying… while the apply is under way", () => {
    show(result({ workspaceAction: makeAction() }));
    expect(screen.getByRole("status")).toHaveTextContent("Applying…");
    expect(screen.queryByRole("button", { name: "Apply to my folder" })).toBeNull();
  });

  it("once applied, opens the person's folder", async () => {
    const open = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "D:\\code\\my-app" });
    show(result({ workspace: "merged_local" }));
    expect(screen.getByText("Applied to your folder")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Take a look" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open my folder" }));
    await waitFor(() => expect(open).toHaveBeenCalledWith("D:\\code\\my-app"));
  });

  it("says when the folder could not be opened", async () => {
    vi.spyOn(api, "openFolder").mockRejectedValue(new Error("not here"));
    show(result({ workspace: "merged_local" }));
    fireEvent.click(screen.getByRole("button", { name: "Open my folder" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not open the folder: not here");
  });

  it("says the folder shows another version after an apply it cannot see", async () => {
    vi.mocked(api.getRunWorkspace).mockResolvedValue(makeView({ info: makeInfo({ baseCheckout: "none" }) }));
    vi.spyOn(api, "mergeRunLocal").mockResolvedValue(result({ workspace: "merged_local" }));
    const { rerender } = show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(api.mergeRunLocal).toHaveBeenCalled());
    rerender(
      <EditionProvider edition="single">
        <ResultActions run={result({ workspace: "merged_local" })} />
        <ConfirmDialogHost />
      </EditionProvider>,
    );
    expect(await screen.findByText(/your folder is showing a different version/)).toBeInTheDocument();
  });

  it("opens the page, and says what it did with anything else", async () => {
    const preview = vi.spyOn(api, "previewRun").mockResolvedValue({
      web: true,
      url: "http://localhost:5799/p/0123456789abcdef0123456789abcdef/index.html",
    });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Take a look" }));
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    const link = await screen.findByRole("link", { name: "Open the page" });
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(link).toHaveAttribute("target", "_blank");

    preview.mockResolvedValue({ web: false, folder: "/w/run7" });
    vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "/w/run7" });
    fireEvent.click(screen.getByRole("button", { name: "Take a look" }));
    expect(await screen.findByText(/not a web page, so its folder was opened instead/)).toBeInTheDocument();
  });

  it("follows the run store's fresher copy of the run", () => {
    const stale = result();
    useRuns.setState({ runsByNode: { alpha: [result({ workspace: "merged_local" })] } });
    show(stale);
    expect(screen.getByText("Applied to your folder")).toBeInTheDocument();
  });

  it("renders nothing on a team hub, for another runner's run, or a run with a PR", () => {
    const { container, unmount } = show(result(), "team");
    expect(container.querySelector(".result-actions")).toBeNull();
    unmount();
    const other = show(result({ runnerId: "r9" }));
    expect(other.container.querySelector(".result-actions")).toBeNull();
    other.unmount();
    const pr = show(result({ prUrl: "https://github.com/o/r/pull/3", prState: "open" }));
    expect(pr.container.querySelector(".result-actions")).toBeNull();
  });

  it("is spoken in Traditional Chinese", () => {
    act(() => useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } }));
    show(result());
    expect(screen.getByRole("button", { name: "打開看看" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "套用到我的資料夾" })).toBeInTheDocument();
  });

  it("acts on the latest turn only and says continued-below on earlier turns", () => {
    const earlier = result({ id: "g1", global: true, continuedBy: "g2" });
    const { unmount } = show(earlier);
    expect(screen.getByText("Continued below")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply to my folder" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Take a look" })).toBeNull();
    unmount();

    act(() => useApp.setState({ preferences: { ...readPreferences(), language: "en", experience: "simple" } }));
    const simpleView = show(earlier);
    expect(screen.getByText("It went on below")).toBeInTheDocument();
    simpleView.unmount();

    act(() => useApp.setState({ preferences: { ...readPreferences(), language: "en", experience: "full" } }));
    show(result({ id: "g2", global: true, continueOf: "g1", workRoot: "g1" }));
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeEnabled();
  });

  it("shows TURN_RUNNING message when merge is refused while a turn is running", async () => {
    vi.spyOn(api, "mergeRunLocal").mockRejectedValue(
      new WorkspaceActionError(409, "TURN_RUNNING", "turn_running", "a turn of this work is still running"),
    );
    show(result());
    fireEvent.click(screen.getByRole("button", { name: "Apply to my folder" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Apply" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A turn of this work is still running; merge after it ends",
    );
  });

  // "Start new work with this" never starts on its own: the whole task (not
  // just its first line, the title) opens in a box first; the start uses the
  // first start's mode rule, and a busy folder asks what to do.
  it("opens the whole task on a PR-ended run, then starts it interactive, asking when the folder is busy", async () => {
    const interactiveRunner = localRunner();
    interactiveRunner.info = {
      ...interactiveRunner.info!,
      agents: (interactiveRunner.info!.agents ?? []).map((agent) => ({ ...agent, interactive: true })),
    };
    useRunners.setState({ runners: [interactiveRunner], loaded: true });
    const ended = result({
      id: "g1",
      global: true,
      agent: "claude",
      title: "Upgrade React",
      prUrl: "https://github.com/o/r/pull/12",
      prState: "merged",
      noPR: false,
    });
    const task = "Upgrade React\n\nAlso move the router to v7 and keep the old tests.";
    vi.spyOn(api, "getRunEvents").mockResolvedValue({
      events: [{ seq: 0, type: "user", text: task, at: "2026-11-20T08:00:00Z" }],
      more: false,
    });
    const created = result({ id: "g2", global: true, status: "queued", prUrl: undefined, prState: undefined });
    const holder = { runId: "h1", title: "Other work", agent: "codex", model: "gpt-5", status: "running", since: "2026-11-20T08:00:00Z", mayStop: true };
    const create = vi
      .spyOn(api, "createGlobalRun")
      .mockRejectedValueOnce(new BusyError("busy", { holders: [holder], others: 0, choices: ["queue", "both"] }))
      .mockResolvedValueOnce(created);
    show(ended);

    expect(screen.getByText("Its pull request was merged or closed; start new work instead")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply to my folder" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Start new work with this" }));

    const box = await screen.findByRole("textbox", { name: "What the new work should do" });
    expect(box).toHaveValue(task);
    expect(create).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith(
        expect.objectContaining({
          runnerId: "local1",
          repoId: ended.repoId,
          agent: ended.agent,
          task,
          mode: "interactive",
          openPR: true,
        }),
      ),
    );

    // BUSY: the choice, not a raw message.
    fireEvent.click(await screen.findByRole("radio", { name: /Start mine after it ends/ }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(create).toHaveBeenLastCalledWith(
        expect.objectContaining({ task, onBusy: "queue", holderIds: ["h1"] }),
      ),
    );
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "What the new work should do" })).toBeNull());
  });

  it("hands the whole task to the caller's composer when it has one", async () => {
    const ended = result({
      id: "g1",
      global: true,
      title: "Upgrade React",
      prUrl: "https://github.com/o/r/pull/13",
      prState: "closed",
      noPR: false,
    });
    useRuns.setState({
      eventsByRun: { g1: [{ seq: 0, type: "user", text: "Upgrade React\nand the router", at: "2026-11-20T08:00:00Z" }] },
    });
    const create = vi.spyOn(api, "createGlobalRun");
    const onStartNewWork = vi.fn();
    render(
      <EditionProvider edition="single">
        <ResultActions run={ended} onStartNewWork={onStartNewWork} />
      </EditionProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Start new work with this" }));
    await waitFor(() => expect(onStartNewWork).toHaveBeenCalledWith("Upgrade React\nand the router"));
    expect(create).not.toHaveBeenCalled();
  });

  it("locks ResultActions on an undecided contest entry and unlocks on a decided winner", () => {
    const entry = result({ id: "e1", global: true, contestId: "c1", contestLabel: "A" });
    useCoordStore.setState({
      contests: {
        c1: {
          contest: {
            id: "c1",
            title: "Compare",
            global: true,
            state: "ready",
            entryCount: 2,
            createdBy: "u1",
            createdAt: "2026-11-20T08:00:00Z",
          },
          runs: [entry],
          compare: { entries: [], scores: null },
        },
      },
    });
    const { rerender } = show(entry);
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("Ready to compare");

    act(() => {
      useCoordStore.setState({
        contests: {
          c1: {
            contest: {
              id: "c1",
              title: "Compare",
              global: true,
              state: "decided",
              entryCount: 2,
              winnerRunId: "e1",
              createdBy: "u1",
              createdAt: "2026-11-20T08:00:00Z",
            },
            runs: [entry],
            compare: { entries: [], scores: null },
          },
        },
      });
    });
    rerender(
      <EditionProvider edition="single">
        <ResultActions run={entry} />
        <ConfirmDialogHost />
      </EditionProvider>,
    );
    expect(screen.getByRole("button", { name: "Apply to my folder" })).toBeEnabled();
  });
});
