/**
 * Cross-slice internals.
 *
 * Serialization and staleness guards that must be shared by every slice:
 *
 * - `queues.graphSave` serializes graph writes so two edits never race on the
 *   same `baseRev`;
 * - `queues.tabSave` does the same per document;
 * - `generations` invalidates in-flight loads after a project switch, so a slow
 *   response from the previous project cannot overwrite the new one.
 *
 * Module state rather than store state: these are plumbing, not something a
 * component should ever render.
 */

import type { CommandTarget } from "../domain";
import { operationScope, type OperationScope } from "./operations";
import type { PageSaveResult, Tab } from "./types";

export const queues = {
  graphSave: Promise.resolve() as Promise<void>,
  tabSave: new Map<string, Promise<void>>(),
  pageSave: new Map<string, Promise<PageSaveResult>>(),
};

/** Serialize autosave, manual save and project-switch writes to each subpage. */
export function enqueuePageSave(
  key: string,
  save: () => Promise<PageSaveResult>,
): Promise<PageSaveResult> {
  const previous = queues.pageSave.get(key) ?? Promise.resolve();
  const operation = previous.catch(() => undefined).then(save);
  queues.pageSave.set(key, operation);
  const cleanup = () => {
    if (queues.pageSave.get(key) === operation) queues.pageSave.delete(key);
  };
  void operation.then(cleanup, cleanup);
  return operation;
}

export const generations = { load: 0, state: 0 };

/**
 * Graph writes sent, and those still waiting for their answer. A read of the
 * graph that was on the wire beside a write cannot tell whether it saw that
 * write, so loadAll does not show it: a graph from before a save, kept with
 * the revision that save answered, let the next whole-file save pass its
 * baseRev check with the first save's edit missing.
 */
export const graphWrites = { sent: 0, open: 0 };

/** Sends one graph write, counted in `graphWrites` until it is answered. */
export async function sendGraphWrite<T>(write: () => Promise<T>): Promise<T> {
  graphWrites.sent += 1;
  graphWrites.open += 1;
  try {
    return await write();
  } finally {
    graphWrites.open -= 1;
  }
}

/** Invalidates every in-flight load/state response. Call before switching. */
export function invalidateGenerations(): void {
  generations.load += 1;
  generations.state += 1;
}

/** Waits for pending writes so a project switch cannot cut one in half. */
export async function drainWrites(): Promise<void> {
  await Promise.all(
    [...queues.tabSave.values(), ...queues.pageSave.values()]
      .map((save) => save.catch(() => undefined)),
  );
  await queues.graphSave.catch(() => undefined);
}

export const patchTab = (tabs: Tab[], id: string, patch: Partial<Tab>): Tab[] =>
  tabs.map((t) => (t.id === id ? { ...t, ...patch } : t));

/** Registry domain + node → operation scope, so badges land on the right panel. */
export function scopeForCommand(target: CommandTarget): OperationScope {
  switch (target.domain) {
    case "plan-milestone":
      return target.nodeId ? operationScope.plan(target.nodeId) : operationScope.graph();
    case "workflow":
      return operationScope.workflow();
    case "project-layout":
      return operationScope.canvas();
    case "node-metadata":
      return target.nodeId ? operationScope.node(target.nodeId) : operationScope.graph();
    default:
      return operationScope.graph();
  }
}
