/**
 * What the page does when its socket comes back after a drop.
 *
 * The server was stopped or restarted under an open page: everything it
 * broadcast meanwhile is gone, and every request made meanwhile failed with
 * "can't reach the server". Live updates resume by themselves; this does the
 * rest. The failures that only said the connection was down are cleared —
 * leaving them up after the server is back reads as a fault that is still
 * there — while every other error stays, and a conflict, being a question, is
 * never answered on anyone's behalf. Then the page reloads what it may have
 * missed, the way a `graph-changed` does.
 */

import { isNetworkFailure } from "../api";
import { useApp } from "../store";
import type { OperationMap } from "./operations";

export async function resyncAfterReconnect(): Promise<void> {
  useApp.setState((state) => {
    const operations: OperationMap = {};
    for (const [scope, entry] of Object.entries(state.operations)) {
      if (entry.status === "error" && isNetworkFailure(entry.message)) continue;
      operations[scope] = entry;
    }
    const last = state.lastOperation;
    return {
      operations,
      lastOperation:
        last && last.status === "error" && isNetworkFailure(last.message) ? null : last,
      error: isNetworkFailure(state.error) ? null : state.error,
    };
  });
  const { loadAll, refreshState, refreshTrash, refreshProjects } = useApp.getState();
  await Promise.allSettled([loadAll(), refreshState(), refreshTrash(), refreshProjects()]);
}
