import type { AgentItem, RepoSummary, WorkspaceListing } from "../api/explorer";

/** Test data shaped like the hub's Explorer answers. */

export const workspaceListing: WorkspaceListing = {
  entries: [
    { path: "nodes", type: "dir", size: 0 },
    { path: "nodes/login.md", type: "file", size: 120, nodeId: "login", title: "Login form" },
    { path: "docs", type: "dir", size: 0 },
    { path: "docs/notes.txt", type: "file", size: 12 },
    { path: "graph.yaml", type: "file", size: 300 },
  ],
  hidden: 0,
  truncated: false,
};

export function repo(over: Partial<RepoSummary> = {}): RepoSummary {
  return {
    runnerId: "rn1",
    runnerName: "mac-mini",
    online: true,
    repoId: "r1",
    name: "visual-editor",
    browsable: true,
    ...over,
  };
}

export function agentItem(over: Partial<AgentItem> = {}): AgentItem {
  return {
    kind: "run",
    id: "run:r1",
    runId: "r1",
    agent: "codex",
    model: "gpt-5.5",
    role: "executor",
    state: "succeeded",
    nodeId: "login",
    nodeTitle: "Login form",
    tokens: { input: 52_000, output: 3_400, cacheRead: 12_000 },
    costUsd: 0.09,
    costEstimated: true,
    startedAt: "2026-10-28T09:00:00Z",
    finishedAt: "2026-10-28T09:04:00Z",
    runnerName: "mac-mini",
    files: [
      { path: "src/math.js", mark: "M" },
      { path: "src/math.test.js", mark: "A" },
    ],
    ...over,
  };
}
