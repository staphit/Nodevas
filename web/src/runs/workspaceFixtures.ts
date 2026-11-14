// Shared by the agent workspace tests: a finished run and the hub's view of
// its workspace, owner's by default.
import type { Run, RunWorkspaceAction, RunWorkspaceInfo, RunWorkspaceView } from "../api";

export function makeWorkspaceRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run7",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha-run7abcd",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    commit: "c0ffee1234567",
    filesChanged: 2,
    additions: 5,
    deletions: 1,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-12T08:00:00Z",
    finishedAt: "2026-11-12T08:10:00Z",
    ...overrides,
  };
}

export function makeInfo(overrides: Partial<RunWorkspaceInfo> = {}): RunWorkspaceInfo {
  return {
    exists: true,
    path: "/data/worktrees/run7",
    branch: "agent/alpha-run7abcd",
    head: "c0ffee1234567",
    ownBranch: true,
    branchExists: true,
    uncommitted: 3,
    running: false,
    kept: true,
    baseCheckout: "none",
    baseClean: true,
    ...overrides,
  };
}

export function makeView(overrides: Partial<RunWorkspaceView> = {}): RunWorkspaceView {
  return {
    runId: "run7",
    owner: true,
    runnerOnline: true,
    files: { allowed: true, blocked: "" },
    merge: { mode: "local", blocked: "" },
    discard: { allowed: true, blocked: "" },
    info: makeInfo(),
    infoError: "",
    ...overrides,
  };
}

/** What a non-owner is shown (§4.2): no actions, no files, no live info. */
export function notOwnerView(overrides: Partial<RunWorkspaceView> = {}): RunWorkspaceView {
  return makeView({
    owner: false,
    files: { allowed: false, blocked: "not_owner" },
    merge: { mode: "", blocked: "not_owner" },
    discard: { allowed: false, blocked: "not_owner" },
    info: null,
    infoError: "not_owner",
    ...overrides,
  });
}

export function makeAction(overrides: Partial<RunWorkspaceAction> = {}): RunWorkspaceAction {
  return {
    id: "wa1",
    runId: "run7",
    action: "merge_local",
    status: "pending",
    requestedBy: "alice",
    createdAt: new Date(Date.now() - 1000).toISOString(),
    ...overrides,
  };
}
