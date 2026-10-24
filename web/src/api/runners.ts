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

export const runnersApi = {
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
