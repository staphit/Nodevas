import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, WorkspaceActionError, WORKSPACE_GATE_CODES, WORKSPACE_RUNNER_CODES } from "../api";
import { hasMessage } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { applyRefusalKey, applyRuns, APPLY_WINDOW_MS, lookAt, resultState } from "./resultState";
import { makeAction, makeInfo, makeView, makeWorkspaceRun } from "./workspaceFixtures";
import { useWorkspace } from "./workspaceStore";

const LOCAL = "local1";
const NOW = Date.parse("2026-11-12T09:00:00Z");

/** A finished run on this computer's own runner, ready to be applied. */
const ready = (overrides: Parameters<typeof makeWorkspaceRun>[0] = {}) =>
  makeWorkspaceRun({ runnerId: LOCAL, workspace: "", ...overrides });

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useWorkspace.getState().reset();
});

afterEach(() => vi.restoreAllMocks());

describe("resultState", () => {
  it("is ready for a succeeded run with a commit, no PR and nothing pending, on the built-in runner", () => {
    expect(resultState(ready(), LOCAL)).toBe("ready");
    expect(resultState(ready({ kind: "test" }), LOCAL)).toBe("ready");
    // A failed earlier apply leaves it ready to try again.
    expect(resultState(ready({ workspaceAction: makeAction({ status: "failed", code: "dirty_base" }) }), LOCAL)).toBe(
      "ready",
    );
  });

  it("is none for an open PR, a pending action, a failed run, another runner, or a team hub", () => {
    expect(resultState(ready({ prUrl: "https://github.com/o/r/pull/1", prState: "open" }), LOCAL)).toBe("none");
    expect(resultState(ready({ workspaceAction: makeAction({ action: "discard" }) }), LOCAL)).toBe("none");
    expect(resultState(ready({ merge: { id: "m", runId: "run7", headSha: "x", status: "pending", requestedBy: "a", createdAt: "" } }), LOCAL)).toBe("none");
    expect(resultState(ready({ status: "failed" }), LOCAL)).toBe("none");
    expect(resultState(ready({ commit: undefined }), LOCAL)).toBe("none");
    expect(resultState(ready({ kind: "review" }), LOCAL)).toBe("none");
    expect(resultState(ready({ workspace: "discarded" }), LOCAL)).toBe("none");
    expect(resultState(ready({ runnerId: "r9" }), LOCAL)).toBe("none");
    // No built-in runner: a team hub, or the list not loaded.
    expect(resultState(ready(), undefined)).toBe("none");
  });

  it("is applied once merged into the folder, and applying while that is under way", () => {
    expect(resultState(ready({ workspace: "merged_local" }), LOCAL)).toBe("applied");
    expect(resultState(ready({ workspaceAction: makeAction() }), LOCAL)).toBe("applying");
  });

  it("returns continued for earlier turns, pr_ended for ended PRs, and contest_locked for undecided contest entries", () => {
    expect(resultState(ready({ continuedBy: "run8" }), LOCAL)).toBe("continued");
    expect(resultState(ready({ prUrl: "https://github.com/o/r/pull/2", prState: "merged" }), LOCAL)).toBe("pr_ended");
    expect(resultState(ready({ prUrl: "https://github.com/o/r/pull/2", prState: "closed" }), LOCAL)).toBe("pr_ended");
    expect(resultState(ready({ status: "declined", declineCode: "pr_ended", commit: undefined }), LOCAL)).toBe("pr_ended");

    const entry = ready({ id: "entryA", contestId: "c1", contestLabel: "A" });
    expect(resultState(entry, LOCAL)).toBe("contest_locked");
    expect(resultState(entry, LOCAL, { contest: { state: "ready" } })).toBe("contest_locked");
    expect(resultState(entry, LOCAL, { contest: { state: "decided", winnerRunId: "entryA" } })).toBe("ready");
    expect(resultState(entry, LOCAL, { contest: { state: "decided", winnerRunId: "entryB" } })).toBe("none");
  });
});

describe("applyRefusalKey", () => {
  it("maps every gate and runner code to a key both languages have", () => {
    for (const code of [...WORKSPACE_GATE_CODES, ...WORKSPACE_RUNNER_CODES, "expired", ""]) {
      const key = applyRefusalKey(code);
      expect(key.startsWith("result.refuse."), code).toBe(true);
      expect(hasMessage("en", key), key).toBe(true);
      expect(hasMessage("zh-TW", key), key).toBe(true);
    }
  });

  it("says the plain reasons for the common refusals", () => {
    expect(applyRefusalKey("dirty_base")).toBe("result.refuse.dirty_base");
    expect(applyRefusalKey("conflict")).toBe("result.refuse.conflict");
    expect(applyRefusalKey("base_elsewhere")).toBe("result.refuse.base_elsewhere");
    expect(applyRefusalKey("head_moved")).toBe("result.refuse.head_moved");
    expect(applyRefusalKey("running")).toBe("result.refuse.busy");
    expect(applyRefusalKey("offline")).toBe("result.refuse.offline");
    expect(applyRefusalKey("merged_local")).toBe("result.refuse.gone");
    expect(applyRefusalKey("continued")).toBe("result.refuse.gone");
    expect(applyRefusalKey("turn_running")).toBe("result.refuse.busy");
    expect(applyRefusalKey("git")).toBe("result.refuse.other");
  });
});

describe("lookAt", () => {
  it("opens a web page in a new tab without a way back to the app", async () => {
    vi.spyOn(api, "previewRun").mockResolvedValue({
      web: true,
      url: "http://localhost:5799/p/0123456789abcdef0123456789abcdef/index.html",
      folder: "/data/worktrees/run7",
    });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const folder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "" });
    const outcome = await lookAt(ready());
    expect(open).toHaveBeenCalledWith(
      "http://localhost:5799/p/0123456789abcdef0123456789abcdef/index.html",
      "_blank",
      "noopener,noreferrer",
    );
    expect(outcome.kind).toBe("page");
    expect(folder).not.toHaveBeenCalled();
  });

  it("never opens a page that is not on this computer", async () => {
    vi.spyOn(api, "previewRun").mockResolvedValue({ web: true, url: "https://example.com/x", folder: "/w/run7" });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const folder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "" });
    expect((await lookAt(ready())).kind).toBe("folder");
    expect(open).not.toHaveBeenCalled();
    expect(folder).toHaveBeenCalledWith("/w/run7");
  });

  it("opens the run folder when the result is not a web page", async () => {
    vi.spyOn(api, "previewRun").mockResolvedValue({ web: false, folder: "/data/worktrees/run7" });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const folder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "" });
    expect((await lookAt(ready())).kind).toBe("folder");
    expect(folder).toHaveBeenCalledWith("/data/worktrees/run7");
    expect(open).not.toHaveBeenCalled();
  });

  it("opens the folder the workspace names when preview is off", async () => {
    vi.spyOn(api, "previewRun").mockRejectedValue(
      new WorkspaceActionError(409, "FAILED_PRECONDITION", "preview_off", "preview is off"),
    );
    vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView({ info: makeInfo({ path: "/w/from-view" }) }));
    const folder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "" });
    expect((await lookAt(ready())).kind).toBe("off");
    expect(folder).toHaveBeenCalledWith("/w/from-view");
  });

  it("says the files are gone after an apply or a discard", async () => {
    vi.spyOn(api, "previewRun").mockRejectedValue(
      new WorkspaceActionError(409, "FAILED_PRECONDITION", "merged_local", "merged"),
    );
    const folder = vi.spyOn(api, "openFolder").mockResolvedValue({ ok: true, path: "" });
    expect((await lookAt(ready())).kind).toBe("gone");
    expect(folder).not.toHaveBeenCalled();
  });

  it("says why it could not open anything", async () => {
    vi.spyOn(api, "previewRun").mockResolvedValue({ web: false, folder: "/w/run7" });
    vi.spyOn(api, "openFolder").mockRejectedValue(new Error("no such folder"));
    expect(await lookAt(ready())).toEqual({ kind: "failed", reason: "no such folder" });
  });
});

describe("applyRuns", () => {
  const at = (ms: number) => new Date(NOW - ms).toISOString();

  it("picks succeeded results not applied yet, newest first", () => {
    const older = ready({ id: "a", finishedAt: at(3 * 60 * 60_000) });
    const newer = ready({ id: "b", finishedAt: at(60_000) });
    const applied = ready({ id: "c", workspace: "merged_local", finishedAt: at(60_000) });
    expect(applyRuns([older, newer, applied], NOW, LOCAL).map((run) => run.id)).toEqual(["b", "a"]);
  });

  it("skips a PR run, a pending action, anything older than 7 days and other runners", () => {
    const runs = [
      ready({ id: "pr", prUrl: "https://github.com/o/r/pull/1", prState: "open", finishedAt: at(1000) }),
      ready({ id: "pending", workspaceAction: makeAction({ action: "discard" }), finishedAt: at(1000) }),
      ready({ id: "old", finishedAt: at(APPLY_WINDOW_MS + 1000) }),
      ready({ id: "other", runnerId: "r9", finishedAt: at(1000) }),
      ready({ id: "yesterday", finishedAt: at(26 * 60 * 60_000) }),
    ];
    expect(applyRuns(runs, NOW, LOCAL).map((run) => run.id)).toEqual(["yesterday"]);
    expect(applyRuns(runs, NOW, undefined)).toEqual([]);
  });

  it("lists a run once, as its first copy has it", () => {
    const fresh = ready({ id: "g1", global: true, nodeId: "", finishedAt: at(1000) });
    const stale = { ...fresh, workspace: "merged_local" };
    expect(applyRuns([fresh, stale, fresh], NOW, LOCAL)).toEqual([fresh]);
  });

  it("gives two items for two global runs", () => {
    const one = ready({ id: "g1", global: true, nodeId: "", finishedAt: at(1000) });
    const two = ready({ id: "g2", global: true, nodeId: "", finishedAt: at(2000) });
    expect(applyRuns([one, two], NOW, LOCAL).map((run) => run.id)).toEqual(["g1", "g2"]);
  });

  it("skips earlier turns, PR-ended runs, and undecided contest entries, but includes a decided winner", () => {
    const earlier = ready({ id: "t1", global: true, nodeId: "", continuedBy: "t2", finishedAt: at(3000) });
    const latest = ready({ id: "t2", global: true, nodeId: "", continueOf: "t1", workRoot: "t1", finishedAt: at(1000) });
    const prEnded = ready({ id: "p1", global: true, nodeId: "", prUrl: "https://github.com/o/r/pull/4", prState: "merged", finishedAt: at(2000) });
    const contestUndecided = ready({ id: "ca", global: true, nodeId: "", contestId: "c1", contestLabel: "A", finishedAt: at(1500) });
    const contestWinner = ready({ id: "cb", global: true, nodeId: "", contestId: "c2", contestLabel: "B", finishedAt: at(1200) });
    const contestLoser = ready({ id: "cc", global: true, nodeId: "", contestId: "c2", contestLabel: "A", finishedAt: at(1200) });

    expect(
      applyRuns([earlier, latest, prEnded, contestUndecided, contestWinner, contestLoser], NOW, LOCAL, APPLY_WINDOW_MS, {
        c1: { state: "ready" },
        c2: { state: "decided", winnerRunId: "cb" },
      }).map((run) => run.id),
    ).toEqual(["t2", "cb"]);
  });
});
