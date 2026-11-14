import { send } from "./http";
import { rememberGlobalRuns, type Run } from "./runs";

// Mirrors internal/runnerwire/global.go (agent workspace, phase 2). A global
// agent is a run in one of the owner's runner's registered repositories for
// no project and no node: it sees no board and asks nothing (no `nodevas
// mcp`). Its routes live under /api/global-runs, outside any project's
// members gate; the per-run ones mirror /api/runs/:id (api/runs.ts runPath).

/** runnerwire.MaxGlobalTaskBytes: the whole task, UTF-8 bytes after trim. */
export const MAX_GLOBAL_TASK_BYTES = 32 << 10;
/** runnerwire.MaxGlobalTitleRunes: the task's first line, as the title. */
export const MAX_GLOBAL_TITLE_CHARS = 120;
/** runnerwire.MaxSlugBytes: a branch slug's length. */
export const MAX_SLUG_BYTES = 40;
/** runnerwire.DefaultGlobalSlug: a title with no ASCII letters or digits. */
export const DEFAULT_GLOBAL_SLUG = "task";

/** Mirrors runnerwire.CreateGlobalRunRequest: POST /api/global-runs. */
export interface CreateGlobalRunRequest {
  runnerId: string;
  repoId: string;
  agent: string;
  model?: string;
  /** "" (headless) or "interactive". */
  mode?: "" | "interactive";
  /** The branch to start from; omitted means the repository's default. */
  base?: string;
  /** What the agent should do; its first line becomes the run's title. */
  task: string;
  /** Names the branch agent/<slug>-<run8>; omitted derives it from the title. */
  slug?: string;
  /** Push and open a pull request when done. Off (the default) keeps the
   * branch on the runner for a local merge or a discard. */
  openPR: boolean;
}

/** Why a runner cannot take global runs now: "" when it can. */
export type GlobalRunsBlock = "" | "offline" | "outdated" | "off" | "headless";
/** Why its maxConcurrent cannot be changed from the hub: "" when it can. */
export type SettableBlock = "" | "offline" | "outdated" | "settings_off";

/** Mirrors runnerwire.RunnerToday: the runner's use so far today (UTC). */
export interface RunnerToday {
  runs: number;
  /** Input + output, as the budget counts them. */
  tokens: number;
  costUsd: number;
}

/** Mirrors runnerwire.RunnerLoad: one of the viewer's own runners. */
export interface RunnerLoad {
  runnerId: string;
  name: string;
  online: boolean;
  /** From its hello (≤ 0 there counts as 1). */
  maxConcurrent: number;
  /** The most the hub may set; 0: it cannot be set from here. */
  ceiling: number;
  settable: SettableBlock | string;
  globalRuns: GlobalRunsBlock | string;
  /** Offered and running runs, every project's and global. */
  busy: number;
  queued: number;
  /** Its queued, offered and running global runs. */
  globalActive: number;
  today: RunnerToday;
}

/** Mirrors runnerwire.GlobalRunsResponse: GET /api/global-runs. */
export interface GlobalRunsResponse {
  /** Every unfinished global run of the viewer's, then the newest ended. */
  runs: Run[] | null;
  /** Older ended runs exist: page with `before`. */
  more: boolean;
  runners: RunnerLoad[] | null;
}

/** Mirrors runnerwire.CancelAllResponse: POST /api/global-runs/cancel-all. */
export interface CancelAllResponse {
  cancelled: number;
  runs: Run[] | null;
}

/**
 * A refused global-run or runner-settings request, with what the hub's body
 * said beyond its message: its code (INVALID_ARGUMENT, GLOBAL_RUNS_OFF,
 * RUNNER_OUTDATED…), the gate that blocked it (`blocked`), the field it
 * refused (`field`), a bound (`max`) and which budget limit ran out
 * (`exceeded`).
 */
export class GlobalRunError extends Error {
  status: number;
  code: string;
  blocked: string;
  field: string;
  max: number;
  exceeded: string;
  constructor(
    status: number,
    code: string,
    blocked: string,
    field: string,
    max: number,
    message: string,
    exceeded = "",
  ) {
    super(message);
    this.name = "GlobalRunError";
    this.status = status;
    this.code = code;
    this.blocked = blocked;
    this.field = field;
    this.max = max;
    this.exceeded = exceeded;
  }
}

/** Reads a refusal's body into a GlobalRunError. */
export async function globalRunError(res: Response): Promise<GlobalRunError> {
  const parsed = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  const text = (key: string) => (typeof parsed[key] === "string" ? (parsed[key] as string) : "");
  const max = typeof parsed.max === "number" && Number.isFinite(parsed.max) ? parsed.max : 0;
  return new GlobalRunError(
    res.status,
    text("code"),
    text("blocked"),
    text("field"),
    max,
    text("error") || `${res.status}`,
    text("exceeded"),
  );
}

/** Sends a JSON request and reads its JSON answer; a refusal is a
 * GlobalRunError. */
async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await send(url, init);
  if (!res.ok) throw await globalRunError(res);
  return (await res.json()) as T;
}

/** Whether `slug` may name a global run's branch (runnerwire.ValidSlug):
 * lowercase ASCII letters and digits in groups joined by single dashes, at
 * most MAX_SLUG_BYTES. */
export function validSlug(slug: string): boolean {
  return slug.length <= MAX_SLUG_BYTES && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(slug);
}

/** A branch slug from a title, by the runner's rules (runnerwire.SlugFrom):
 * ASCII letters lowercased and digits kept, every run of anything else one
 * dash, cut to MAX_SLUG_BYTES at a dash when it can be; "task" when nothing
 * is left (a title in Chinese, say). */
export function slugFrom(title: string): string {
  let out = "";
  let dash = false;
  for (const char of title) {
    const c = char >= "A" && char <= "Z" ? char.toLowerCase() : char;
    if ((c >= "a" && c <= "z") || (c >= "0" && c <= "9")) {
      out += c;
      dash = false;
    } else if (!dash && out.length > 0) {
      out += "-";
      dash = true;
    }
  }
  let slug = out.replace(/^-+|-+$/g, "");
  if (slug.length > MAX_SLUG_BYTES) {
    let cut = slug.slice(0, MAX_SLUG_BYTES);
    const at = cut.lastIndexOf("-");
    if (at > 0 && slug[MAX_SLUG_BYTES] !== "-") cut = cut.slice(0, at);
    slug = cut.replace(/^-+|-+$/g, "");
  }
  return slug || DEFAULT_GLOBAL_SLUG;
}

/** A task's title as the hub takes it (runnerwire.GlobalTitle): its first
 * non-empty line, trimmed, at most MAX_GLOBAL_TITLE_CHARS characters. */
export function globalTitle(task: string): string {
  for (const raw of task.replace(/\r\n/g, "\n").split("\n")) {
    const line = raw.replace(/\r/g, " ").trim();
    if (!line) continue;
    const chars = [...line];
    return chars.length > MAX_GLOBAL_TITLE_CHARS ? chars.slice(0, MAX_GLOBAL_TITLE_CHARS).join("").trim() : line;
  }
  return "";
}

export const globalRunsApi = {
  /** Starts a global agent on one of the viewer's own runners: 201 with the
   * run, queued until the runner (or someone at it) accepts the offer. */
  createGlobalRun: async (body: CreateGlobalRunRequest): Promise<Run> => {
    const run = await call<Run>("/api/global-runs", { method: "POST", body: JSON.stringify(body) });
    rememberGlobalRuns([run]);
    return run;
  },
  /** The viewer's global runs and own runners' load. `before` pages the
   * ended runs (a createdAt). */
  listGlobalRuns: async (options: { limit?: number; before?: string } = {}): Promise<GlobalRunsResponse> => {
    const query = new URLSearchParams();
    if (options.limit) query.set("limit", String(options.limit));
    if (options.before) query.set("before", options.before);
    const qs = query.toString();
    const res = await call<GlobalRunsResponse>(`/api/global-runs${qs ? `?${qs}` : ""}`, { cache: "no-store" });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
  /** Cancels every active global run the viewer may see. */
  cancelAllGlobalRuns: async (): Promise<CancelAllResponse> => {
    const res = await call<CancelAllResponse>("/api/global-runs/cancel-all", { method: "POST", body: "{}" });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
};
