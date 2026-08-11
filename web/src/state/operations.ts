/**
 * Operation state [A-02].
 *
 * Every mutation the user can trigger reports through one vocabulary, so the
 * UI never has to invent its own `busy` / `error` booleans:
 *
 * ```
 * idle → pending → saved
 *                → error      (rolled back; retryable)
 *                → conflict   (disk moved under us; needs a decision)
 * ```
 *
 * State is keyed by *scope* so two panels editing different things do not
 * overwrite each other's badge. `lastOperation` is the newest transition of any
 * scope — the topbar/status line can render just that.
 */

export type OperationStatus = "idle" | "pending" | "saved" | "error" | "conflict";

export interface OperationState {
  status: OperationStatus;
  /** Stable id of what ran, e.g. `plan.upsert`. Also used as the undo label key. */
  operation?: string;
  /** Human readable failure reason. Only set for `error` / `conflict`. */
  message?: string;
  /** ms epoch of the transition; lets the UI fade a `saved` badge. */
  at?: number;
}

export interface OperationSnapshot extends OperationState {
  scope: OperationScope;
}

export const IDLE_OPERATION: OperationState = { status: "idle" };

export type OperationScope = string;

/**
 * Scope keys. Node-level scopes keep the node id so an inspector shows only its
 * own progress; project-wide edits share one scope on purpose.
 */
export const operationScope = {
  graph: (): OperationScope => "graph",
  node: (nodeId: string): OperationScope => `node:${nodeId}`,
  plan: (nodeId: string): OperationScope => `plan:${nodeId}`,
  lifecycle: (nodeId: string): OperationScope => `lifecycle:${nodeId}`,
  /** Journal-wide edits whose node cannot be resolved (e.g. moving an event). */
  run: (): OperationScope => "run",
  document: (nodeId: string): OperationScope => `document:${nodeId}`,
  workflow: (): OperationScope => "workflow",
  canvas: (): OperationScope => "canvas",
  workspace: (): OperationScope => "workspace",
  preferences: (): OperationScope => "preferences",
  undo: (): OperationScope => "undo",
} as const;

export type OperationMap = Record<OperationScope, OperationState>;

/** Result of a command. Commands report failure by value; they do not throw. */
export type CommandResult<T = void> =
  | {
      ok: true;
      status: "saved";
      operation: string;
      scope: OperationScope;
      value: T;
    }
  | {
      ok: false;
      status: "error" | "conflict";
      operation: string;
      scope: OperationScope;
      message: string;
    };

export function commandSucceeded<T>(
  operation: string,
  scope: OperationScope,
  value: T,
): CommandResult<T> {
  return { ok: true, status: "saved", operation, scope, value };
}

export function commandFailed<T = void>(
  operation: string,
  scope: OperationScope,
  message: string,
  status: "error" | "conflict" = "error",
): CommandResult<T> {
  return { ok: false, status, operation, scope, message };
}

/** Timestamp source, indirected so tests can assert transitions deterministically. */
let clock = () => Date.now();

/** Test seam. Returns the previous clock so a test can restore it. */
export function setOperationClock(next: () => number): () => number {
  const previous = clock;
  clock = next;
  return previous;
}

export function operationAt(): number {
  return clock();
}

/**
 * Applies one transition. `idle` entries are dropped so the map stays the size
 * of what is actually in flight.
 */
export function withOperation(
  map: OperationMap,
  scope: OperationScope,
  next: OperationState,
): OperationMap {
  if (next.status === "idle") {
    if (!(scope in map)) return map;
    const copy = { ...map };
    delete copy[scope];
    return copy;
  }
  return { ...map, [scope]: { ...next, at: next.at ?? clock() } };
}

export function selectOperation(
  map: OperationMap,
  scope: OperationScope,
): OperationState {
  return map[scope] ?? IDLE_OPERATION;
}

/** True while any scope (or one specific scope) is mid-flight. */
export function isBusy(map: OperationMap, scope?: OperationScope): boolean {
  if (scope) return selectOperation(map, scope).status === "pending";
  return Object.values(map).some((entry) => entry.status === "pending");
}

/** Scopes needing a user decision. Conflicts must never be auto-dismissed. */
export function conflictScopes(map: OperationMap): OperationScope[] {
  return Object.keys(map).filter((scope) => map[scope].status === "conflict");
}

/** Maps a thrown value onto the failure vocabulary. */
export function describeFailure(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return fallback;
}
