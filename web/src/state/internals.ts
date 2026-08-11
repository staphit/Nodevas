/**
 * Cross-slice internals [A-05].
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
import type { Tab } from "./types";

export const queues = {
  graphSave: Promise.resolve() as Promise<void>,
  tabSave: new Map<string, Promise<void>>(),
};

export const generations = { load: 0, state: 0 };

/** Invalidates every in-flight load/state response. Call before switching. */
export function invalidateGenerations(): void {
  generations.load += 1;
  generations.state += 1;
}

/** Waits for pending writes so a project switch cannot cut one in half. */
export async function drainWrites(): Promise<void> {
  await Promise.all([...queues.tabSave.values()].map((save) => save.catch(() => undefined)));
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
