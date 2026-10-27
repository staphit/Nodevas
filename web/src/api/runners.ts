import { req } from "./http";

// Mirrors internal/runnerwire/wire.go. A runner is a machine of the user's
// that runs agent CLIs on the hub's behalf; the hub itself never runs them.

export interface RunnerModel {
  /** What the CLI's model flag takes; "default" means pass no flag. */
  id: string;
  label?: string;
  default?: boolean;
}

export interface RunnerAgent {
  /** "claude" | "codex" | "gemini" | "opencode" | "antigravity" */
  kind: string;
  installed: boolean;
  version?: string;
  models: RunnerModel[];
  error?: string;
}

export interface RunnerRepo {
  id: string;
  name: string;
  path?: string;
  remote?: string;
  /** Agent roles: the repo has a test command on its runner (the command
   * itself never leaves the runner). */
  verify?: boolean;
}

/** Mirrors runnerwire.AutoAcceptPolicy (W6): what the runner takes without
 * asking whoever sits at it. The runner enforces it; the hub only shows it. */
export interface AutoAcceptPolicy {
  enabled: boolean;
  /** Auto-accept only jobs whose dispatcher stepped up recently. */
  requireStepUp: boolean;
  /** Repo ids; empty means none (not all). */
  repos?: string[] | null;
  /** Agent kinds; empty means none. */
  agents?: string[] | null;
  /** "" (headless) and/or "interactive"; empty means headless only. */
  modes?: string[] | null;
}

export interface RunnerInfo {
  version: string;
  hostname: string;
  os: string;
  arch: string;
  agents: RunnerAgent[] | null;
  repos: RunnerRepo[] | null;
  capabilities?: string[];
  /** W6: absent from a runner that predates auto-accept policies. */
  autoAccept?: AutoAcceptPolicy | null;
  /** How many runs it takes at once; absent from an older runner. */
  maxConcurrent?: number;
}

export interface Runner {
  id: string;
  name: string;
  ownerId: string;
  ownerName: string;
  createdAt: string;
  /** RFC3339, or "" for a runner that never connected. */
  lastSeenAt: string;
  online: boolean;
  /** The last hello, absent for a runner that never connected. */
  info?: RunnerInfo;
}

export interface RunnerListResponse {
  runners: Runner[] | null;
}

export interface RunnerCreateResponse {
  runner: Runner;
  /** Shown once: the server keeps only its hash. */
  token: string;
}

/** Mirrors runnerwire.CreateLinkResponse: a device link, shown once. */
export interface RunnerLinkResponse {
  /** `nvlink1.…`: a credential (one-time code, maybe the Access token). */
  link: string;
  name: string;
  hub: string;
  /** RFC3339. */
  expiresAt: string;
  /** Whether the link carries the hub's Access service token. */
  access: boolean;
}

/** Mirrors runnerwire.AccessTokenStatus: never the secret itself. */
export interface RunnerAccessTokenStatus {
  clientId: string;
  set: boolean;
}

export const runnersApi = {
  /** Needs a fresh step-up: 403 with code STEP_UP_REQUIRED otherwise. */
  createRunnerLink: (name: string) =>
    req<RunnerLinkResponse>("/api/runners/links", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  /** Administrators only (403 for anyone else). */
  getRunnerAccessToken: () => req<RunnerAccessTokenStatus>("/api/runners/access-token"),
  /** Administrators only, with a fresh step-up. The secret is write-only. */
  setRunnerAccessToken: (clientId: string, clientSecret: string) =>
    req<RunnerAccessTokenStatus>("/api/runners/access-token", {
      method: "PUT",
      body: JSON.stringify({ clientId, clientSecret }),
    }),
  clearRunnerAccessToken: async (): Promise<void> => {
    try {
      await req<unknown>("/api/runners/access-token", { method: "DELETE" });
    } catch (error) {
      // A 204 has no body for req() to parse.
      if (error instanceof SyntaxError) return;
      throw error;
    }
  },
  listRunners: () => req<RunnerListResponse>("/api/runners"),
  createRunner: (name: string) =>
    req<RunnerCreateResponse>("/api/runners", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  revokeRunner: async (id: string): Promise<void> => {
    try {
      await req<unknown>(`/api/runners/${encodeURIComponent(id)}`, { method: "DELETE" });
    } catch (error) {
      // A 204 has no body for req() to parse; the revoke itself succeeded.
      if (error instanceof SyntaxError) return;
      throw error;
    }
  },
};
