// Test fixtures for the single-user edition: this computer's built-in runner
// as the hub describes it, and the text a reader sees with Advanced closed.

import type { Run, Runner } from "../api";

/** What the built-in runner advertises (runnerwire caps). */
export const LOCAL_CAPS = [
  "headless",
  "interactive",
  "global-runs",
  "global-runs-on",
  "remote-settings",
  "repo-admin",
  "app-accept",
];

export const LOCAL_CONFIG = "C:\\Users\\me\\AppData\\Roaming\\nodevas\\local-runners\\abc.json";

export function localRunner(overrides: Partial<Runner> = {}): Runner {
  return {
    id: "local1",
    name: "this machine",
    ownerId: "local",
    ownerName: "",
    createdAt: "2026-11-01T00:00:00Z",
    lastSeenAt: "2026-11-14T08:00:00Z",
    online: true,
    builtIn: true,
    hubLocal: true,
    info: {
      version: "2.1.0",
      hostname: "PC-01",
      os: "windows",
      arch: "amd64",
      capabilities: LOCAL_CAPS,
      maxConcurrent: 2,
      maxConcurrentCeiling: 4,
      agents: [
        {
          kind: "claude",
          installed: true,
          version: "2.1.0",
          models: [
            { id: "opus", label: "Opus" },
            { id: "sonnet", label: "Sonnet", default: true },
          ],
        },
        { kind: "codex", installed: true, version: "0.40.0", models: [{ id: "gpt-5", default: true }] },
        { kind: "gemini", installed: false, models: [] },
      ],
      repos: [{ id: "repo1", name: "my-app", path: "D:\\code\\my-app", verify: true }],
      local: {
        permission: { claude: "edit", codex: "edit" },
        permissionDetail: { claude: "acceptEdits", codex: "workspace-write" },
        askEveryRun: false,
        shellWithoutAsking: [],
        autoPR: false,
        prDraft: false,
        configPath: LOCAL_CONFIG,
      },
    },
    ...overrides,
  };
}

/** The same runner as the hub lists it before it has connected since the
 * hub started: registered as the hub's own, not online, not yet welcomed as
 * built in. This is a first start, where it never connected at all;
 * restartedRunner is any later one. */
export function startingRunner(overrides: Partial<Runner> = {}): Runner {
  return {
    id: "local1",
    name: "this machine",
    ownerId: "local",
    ownerName: "",
    createdAt: "2026-11-01T00:00:00Z",
    lastSeenAt: "",
    online: false,
    hubLocal: true,
    ...overrides,
  };
}

/** startingRunner after a restart of the hub: the database still holds when
 * it was last seen and its last hello, from before the restart. */
export function restartedRunner(): Runner {
  const { lastSeenAt, info } = localRunner();
  return startingRunner({ lastSeenAt, info });
}

/** A run of the built-in runner's, offered and asked about in the app. */
export function askedRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run1",
    project: "p",
    nodeId: "alpha",
    runnerId: "local1",
    runnerName: "this machine",
    agent: "codex",
    model: "gpt-5",
    repoId: "repo1",
    repoName: "my-app",
    base: "",
    branch: "",
    prompt: "",
    status: "offered",
    exitCode: 0,
    origin: "autopilot",
    ask: {
      runId: "run1",
      where: "app",
      reasons: [{ code: "shell_agent", agent: "codex", setting: "codexSandbox=danger-full-access" }],
      expiresAt: "2026-11-14T14:32:00Z",
    },
    createdBy: "local",
    createdByName: "",
    createdAt: "2026-11-14T14:02:00Z",
    ...overrides,
  };
}

/** The text a reader sees: what collapsed <details> hold, beyond their
 * summary, is left out (Advanced keeps the command lines). */
export function visibleText(root: HTMLElement): string {
  const copy = root.cloneNode(true) as HTMLElement;
  for (const details of Array.from(copy.querySelectorAll("details:not([open])"))) {
    for (const child of Array.from(details.children)) {
      if (child.tagName !== "SUMMARY") child.remove();
    }
  }
  return copy.textContent ?? "";
}
