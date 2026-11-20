import { GlobalRunError, globalRunError } from "./globalRuns";
import { req, send } from "./http";

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
  /** On its machine but not signed in: not installed (usable) until someone
   * signs in there. Only for a CLI that can say so without a run. */
  signedOut?: boolean;
  /** Whether this CLI supports interactive (PTY) runs. */
  interactive?: boolean;
  /** Whether this CLI would show a folder-trust screen for a new worktree. */
  untrusted?: boolean;
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
  /** Auto-accept runs even when the agent may run arbitrary shell commands. */
  allowShell?: boolean;
  /** The dispatcher may type into an auto-accepted interactive run's terminal. */
  allowRemoteTyping?: boolean;
  /** Auto-accept verify jobs (the repo's own test command). */
  allowVerify?: boolean;
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
  /** The most its owner lets the hub set maxConcurrent to (runner setting
   * maxConcurrentCeiling); absent when remote settings are off or the runner
   * predates them. */
  maxConcurrentCeiling?: number;
  /** Relay permission prompts to Nodevas (Claude Code, Antigravity, OpenCode ACP). */
  permissionRelay?: boolean;
  /** The single-user edition's own settings (runnerwire.LocalSettings): sent
   * only by this computer's built-in runner. */
  local?: LocalSettings | null;
}

/** What an agent may do without asking, as the app names it: only read
 * files, edit files in its own copy, or run any command. "custom" is a mode
 * set outside the app that maps to none of them. */
export type PermissionLevel = "read" | "edit" | "full";

/** Mirrors runnerwire.LocalSettings: the built-in runner's settings, read
 * and changed in the app (Settings → Agent, Station's This computer). */
export interface LocalSettings {
  /** Agent kind → "read" | "edit" | "full" | "custom". */
  permission: Record<string, PermissionLevel | "custom" | string> | null;
  /** Agent kind → the exact CLI mode ("acceptEdits", "danger-full-access"…). */
  permissionDetail: Record<string, string> | null;
  /** Ask in the app before every run starts. */
  askEveryRun: boolean;
  /** Agent kinds that run any command and still start automatic runs
   * without asking. */
  shellWithoutAsking: string[] | null;
  autoPR: boolean;
  prDraft: boolean;
  /** Where the runner keeps these settings (shown under Advanced). */
  configPath: string;
  /** Agent kind → the program file the person added for it by hand
   * (the path as picked; the runner checks where it points at every use),
   * used before the one on PATH. Sent to the owner only. */
  agentPaths?: Record<string, string> | null;
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
  /** This computer's built-in runner (single-user edition): the one `nodevas
   * serve` started for the person at this machine. The hub's word. */
  builtIn?: boolean;
  /** The runner this hub registered for itself at this startup, connected
   * or not; only the runner list says it. `builtIn` follows once the runner
   * has connected and been welcomed, so this alone is "still starting". It
   * allows nothing: what the built-in runner may do stays on `builtIn`. */
  hubLocal?: boolean;
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

/** Mirrors runnerwire.RunnerSettingsResponse: PUT /api/runners/:id/settings. */
export interface RunnerSettingsResponse {
  /** The runner as its new hello describes it. */
  runner: Runner;
  /** The runner wrote the change to its runner.json; false: in effect only
   * until it restarts. */
  persisted: boolean;
}

/** Runner capabilities of phase 2 (runnerwire.CapGlobalRuns, CapGlobalRunsOn,
 * CapRemoteSettings). */
export const CAP_GLOBAL_RUNS = "global-runs";
export const CAP_GLOBAL_RUNS_ON = "global-runs-on";
export const CAP_REMOTE_SETTINGS = "remote-settings";
/** Single-user edition (runnerwire.CapRepoAdmin, CapAppAccept): repos are
 * added in the app, and its asks are answered in the app. Built-in runner
 * only. */
export const CAP_REPO_ADMIN = "repo-admin";
export const CAP_APP_ACCEPT = "app-accept";
/** Follow-up, End, view-only, work registry and PR-update rules (runnerwire.CapContinue). */
export const CAP_CONTINUE = "continue";
/** Runner reports touched files for overlap detection (runnerwire.CapTouched). */
export const CAP_TOUCHED = "touched";
/** Coordinated node runs, contests, compare jobs and open_pr (runnerwire.CapCoord). */
export const CAP_COORD = "coord";

/** Mirrors the extended runnerwire.SettingsRequest: PUT
 * /api/runners/:id/settings. Only maxConcurrent reaches a classic runner;
 * the rest is the built-in runner's (a classic one answers local_only). */
export interface RunnerSettingsRequest {
  maxConcurrent?: number;
  /** Agent kind → what it may do without asking. */
  permission?: Record<string, PermissionLevel>;
  askEveryRun?: boolean;
  /** Agent kind → whether its automatic runs start without asking while it
   * may run any command. */
  shellWithoutAsking?: Record<string, boolean>;
  autoPR?: boolean;
  prDraft?: boolean;
  /** Ask in the app each time an agent that may only edit files wants to run
   * a command (RunnerInfo.permissionRelay). */
  permissionRelay?: boolean;
  /** Agent kind → the program file to use for it; at most one entry, and ""
   * stops using the one added. The runner checks the file and asks it for
   * its version before it keeps it; a refusal says why in
   * RunnerRequestError.refused (an AgentPathRefusal). */
  agentPath?: Record<string, string>;
  /** Look for installed agent CLIs again. */
  redetect?: boolean;
}

/** Why the runner would not use a program file (runnerwire
 * AgentPathRefused*): not named like the agent's program, kept in a folder
 * agents work in, not something this computer can start, gone, no answer to
 * --version, or anything else. */
export type AgentPathRefusal = "name" | "place" | "not_program" | "missing" | "no_answer" | "invalid";
export const AGENT_PATH_REFUSALS: readonly AgentPathRefusal[] = [
  "name",
  "place",
  "not_program",
  "missing",
  "no_answer",
  "invalid",
];

/** Whether a settings change sets a program file: the hub then looks for the
 * agents again whatever `redetect` says, so the app waits as long for it. */
function setsAgentPath(body: RunnerSettingsRequest): boolean {
  return Object.values(body.agentPath ?? {}).some((path) => path !== "");
}

/** Mirrors runnerwire.InitPlan: what "make it a Git project" would save. */
export interface InitPlan {
  files: number;
  bytes: number;
  /** What the first version leaves out (node_modules/, .env…). */
  ignored: string[] | null;
  /** A .gitignore listing them is written (none exists yet). */
  writesGitignore: boolean;
  /** Over the limits: no first version is made here. */
  tooBig: boolean;
  /** Sent back with the add, so the runner refuses if the folder changed. */
  hash: string;
}

/** What a picked folder is, to the runner: a Git project, a folder inside
 * one, a folder that is not one yet, or one it refuses. */
export type RepoInspectState = "repo" | "inside_repo" | "not_repo" | "refused";

/** Why a folder is refused (runnerwire.RepoInspect.Refused). */
export type RepoRefusal = "home" | "root" | "secret" | "system" | "nodevas" | "unreadable";

/** Mirrors runnerwire.RepoInspect: POST /api/runners/:id/repos/inspect. */
export interface RepoInspect {
  path: string;
  /** The Git project's top folder, for inside_repo (and repo). */
  top?: string;
  state: RepoInspectState | string;
  refused?: RepoRefusal | string;
  /** Added already: the repo is `repo`. */
  already?: boolean;
  /** The repo as it would be (or is) added. */
  repo?: RunnerRepo | null;
  /** not_repo: the first version's plan. */
  init?: InitPlan | null;
}

/** POST /api/runners/:id/repos. */
export interface RepoAddResponse {
  repo: RunnerRepo;
  /** The runner as its new hello describes it. */
  runner: Runner;
}

/** DELETE /api/runners/:id/repos/:repoId, PUT …/test. */
export interface RepoChangeResponse {
  runner: Runner;
}

/** A refused runner request: the hub's code (the runner's own, for repo
 * changes), plus the folder class a `sensitive` refusal names. */
export class RunnerRequestError extends GlobalRunError {
  /** RepoRefusal, when the hub said which; "" otherwise. */
  refused: string;
  constructor(base: GlobalRunError, refused: string) {
    super(base.status, base.code, base.blocked, base.field, base.max, base.message, base.exceeded);
    this.name = "RunnerRequestError";
    this.refused = refused;
  }
}

/** How long the app waits for a change the runner takes long over: making
 * a folder a Git project (the hub waits up to 5 minutes for it,
 * runnerwire.RepoAdminInitTimeoutSeconds) and looking for the agent CLIs
 * again (60 s, SettingsRedetectTimeoutSeconds). A little past the hub's
 * wait, so the hub's own answer (a timeout included) arrives first. */
export const REPO_INIT_WAIT_MS = 5 * 60_000 + 15_000;
export const REDETECT_WAIT_MS = 60_000 + 15_000;

/** Sends a JSON request and reads its JSON answer; a refusal is a
 * RunnerRequestError (a GlobalRunError). `waitMs` replaces the usual 30 s
 * wait for an answer. */
async function call<T>(url: string, init: RequestInit, waitMs = 0): Promise<T> {
  let res: Response;
  if (waitMs > 0) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), waitMs);
    try {
      res = await send(url, { ...init, signal: controller.signal });
    } finally {
      clearTimeout(timer);
    }
  } else {
    res = await send(url, init);
  }
  if (!res.ok) {
    const copy = res.clone();
    const base = await globalRunError(res);
    const body = (await copy.json().catch(() => ({}))) as Record<string, unknown>;
    throw new RunnerRequestError(base, typeof body.refused === "string" ? body.refused : "");
  }
  return (await res.json()) as T;
}

const runnerPath = (id: string) => `/api/runners/${encodeURIComponent(id)}`;

export const runnersApi = {
  /** The owner changes how many runs their runner takes at once (1 to its
   * maxConcurrentCeiling), or the built-in runner's own settings; the hub
   * asks the runner and waits for its word. A refusal keeps its code, gate
   * and bound (GlobalRunError). */
  setRunnerSettings: (id: string, body: RunnerSettingsRequest): Promise<RunnerSettingsResponse> =>
    call<RunnerSettingsResponse>(
      `${runnerPath(id)}/settings`,
      { method: "PUT", body: JSON.stringify(body) },
      body.redetect || setsAgentPath(body) ? REDETECT_WAIT_MS : 0,
    ),
  /** What the built-in runner makes of a folder on this computer. */
  inspectRepo: (id: string, path: string): Promise<RepoInspect> =>
    call<RepoInspect>(`${runnerPath(id)}/repos/inspect`, { method: "POST", body: JSON.stringify({ path }) }),
  /** Adds the folder as a repo; with `initPlan` (the plan's hash the person
   * confirmed) it is made a Git project first. */
  addRepo: (id: string, path: string, initPlan?: string): Promise<RepoAddResponse> =>
    call<RepoAddResponse>(
      `${runnerPath(id)}/repos`,
      { method: "POST", body: JSON.stringify(initPlan ? { path, initPlan } : { path }) },
      initPlan ? REPO_INIT_WAIT_MS : 0,
    ),
  /** Forgets a repo; the folder itself is not touched. */
  removeRepo: (id: string, repoId: string): Promise<RepoChangeResponse> =>
    call<RepoChangeResponse>(`${runnerPath(id)}/repos/${encodeURIComponent(repoId)}`, { method: "DELETE" }),
  /** Sets the repo's test command; "" clears it. */
  setRepoTestCommand: (id: string, repoId: string, command: string, timeoutMinutes = 0): Promise<RepoChangeResponse> =>
    call<RepoChangeResponse>(`${runnerPath(id)}/repos/${encodeURIComponent(repoId)}/test`, {
      method: "PUT",
      body: JSON.stringify({ command, timeoutMinutes }),
    }),
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
