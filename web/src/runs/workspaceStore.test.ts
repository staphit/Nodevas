import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, AuthError, WorkspaceActionError, WORKSPACE_CODES } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { openRunWorkspace } from "./openWorkspace";
import { useRuns } from "./runStore";
import { useWorkspace, workspaceRefusal } from "./workspaceStore";
import { makeAction, makeView, makeWorkspaceRun } from "./workspaceFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRuns.getState().reset();
  useWorkspace.getState().reset();
  useWorkspace.setState({ open: null });
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView());
});

afterEach(() => vi.restoreAllMocks());

describe("workspace store", () => {
  it("opens on the tab asked for, else by the run's state, and closes", async () => {
    const running = makeWorkspaceRun({ id: "live", status: "running" });
    const done = makeWorkspaceRun();
    useRuns.setState({ activeRuns: [running], runsByNode: { alpha: [done] } });

    openRunWorkspace("run7", "files");
    expect(useWorkspace.getState().open).toEqual({ runId: "run7", tab: "files" });
    openRunWorkspace("run7");
    expect(useWorkspace.getState().open).toEqual({ runId: "run7", tab: "changes" });
    openRunWorkspace("live");
    expect(useWorkspace.getState().open).toEqual({ runId: "live", tab: "terminal" });

    await vi.waitFor(() => expect(useWorkspace.getState().views.live).toEqual(makeView()));
    useWorkspace.getState().setTab("files");
    expect(useWorkspace.getState().open?.tab).toBe("files");
    useWorkspace.getState().close();
    expect(useWorkspace.getState().open).toBeNull();
  });

  it("puts the run a merge returns into the run lists", async () => {
    const run = makeWorkspaceRun();
    useRuns.setState({ runsByNode: { alpha: [run] }, latestByNode: { alpha: run } });
    const pending = { ...run, workspaceAction: makeAction() };
    const merge = vi.spyOn(api, "mergeRunLocal").mockResolvedValue(pending);
    expect(await useWorkspace.getState().mergeLocal(run)).toBe(true);
    expect(merge).toHaveBeenCalledWith("run7", "c0ffee1234567");
    expect(useRuns.getState().runsByNode.alpha[0].workspaceAction?.status).toBe("pending");
    expect(useRuns.getState().latestByNode.alpha.workspaceAction?.status).toBe("pending");
    expect(useWorkspace.getState().acting.run7).toBeUndefined();
  });

  it("puts the run a discard returns into the run lists", async () => {
    const run = makeWorkspaceRun();
    useRuns.setState({ runsByNode: { alpha: [run] } });
    vi.spyOn(api, "discardRun").mockResolvedValue({ ...run, workspace: "discarded" });
    expect(await useWorkspace.getState().discard(run)).toBe(true);
    expect(useRuns.getState().runsByNode.alpha[0].workspace).toBe("discarded");
  });

  it("keeps a refusal in the reader's words, and asks the hub for the run again", async () => {
    const run = makeWorkspaceRun();
    useRuns.setState({ runsByNode: { alpha: [run] } });
    vi.spyOn(api, "discardRun").mockRejectedValue(
      new WorkspaceActionError(409, "FAILED_PRECONDITION", "running", "the run is still running"),
    );
    const refresh = vi.spyOn(api, "getRun").mockResolvedValue(run);
    expect(await useWorkspace.getState().discard(run)).toBe(false);
    expect(useWorkspace.getState().actionErrors.run7).toBe("The run is still running");
    expect(refresh).toHaveBeenCalledWith("run7");
  });
});

describe("workspaceRefusal", () => {
  const run = makeWorkspaceRun();
  it("translates a gate, the outdated and offline codes, and keeps the hub's other words", () => {
    expect(workspaceRefusal(new WorkspaceActionError(409, "FAILED_PRECONDITION", "head_moved", "x"), run)).toBe(
      "The branch changed since you looked; reload",
    );
    expect(workspaceRefusal(new WorkspaceActionError(412, "RUNNER_OUTDATED", "", "x"), run)).toBe(
      "Update nodevas on the runner first",
    );
    expect(workspaceRefusal(new WorkspaceActionError(503, "RUNNER_OFFLINE", "", "x"), run)).toBe(
      "The runner is offline",
    );
    expect(
      workspaceRefusal(new WorkspaceActionError(403, "PERMISSION_DENIED", "", "only the runner's owner can do this"), run),
    ).toBe("only the runner's owner can do this");
    expect(workspaceRefusal(new AuthError(403, "agents cannot change or browse run workspaces"), run)).toBe(
      "agents cannot change or browse run workspaces",
    );
  });
});

describe("workspace codes", () => {
  // Every code the hub or the runner can answer with has words in both
  // languages, so a refusal never shows a raw key.
  it.each(["en", "zh-TW"] as const)("has %s words for every WorkspaceCode", (language) => {
    const missing = WORKSPACE_CODES.filter((code) => {
      const key = `runWorkspace.code.${code}`;
      return translate(key, language) === key;
    });
    expect(missing).toEqual([]);
  });

  it("covers the gates, the runner's refusals, head_moved and expired", () => {
    for (const code of ["not_owner", "offline", "conflict", "dirty_base", "files_busy", "head_moved", "expired"]) {
      expect(WORKSPACE_CODES).toContain(code);
    }
    expect(translate("runWorkspace.code.conflict", "zh-TW")).toBe("合併有衝突；沒有任何變更");
  });
});
