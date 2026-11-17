import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, WorkspaceActionError, type Run } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
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
});
