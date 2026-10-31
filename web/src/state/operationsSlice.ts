/** Operation state slice [A-02/A-05]. */

import {
  IDLE_OPERATION,
  operationAt,
  withOperation,
} from "./operations";
import type { AppSlice, OperationsSlice } from "./types";

/**
 * How long the top bar keeps an error up. Whoever ran the edit reports the
 * failure where it happened (a drawer banner, a canvas notice), so the bar's
 * copy is a passing notice rather than a state to clear by hand. A conflict
 * needs a decision and stays.
 */
export const LAST_ERROR_LINGER_MS = 8000;

let lingerTimer: ReturnType<typeof setTimeout> | undefined;

export const createOperationsSlice: AppSlice<OperationsSlice> = (set) => ({
  operations: {},
  lastOperation: null,
  beginOperation: (scope, operation) => {
    const at = operationAt();
    set((state) => ({
      operations: withOperation(state.operations, scope, {
        status: "pending",
        operation,
        at,
      }),
      lastOperation: { scope, status: "pending", operation, at },
    }));
  },
  settleOperation: (scope, next) => {
    const at = next.at ?? operationAt();
    const last = { scope, ...next, at };
    set((state) => ({
      operations: withOperation(state.operations, scope, { ...next, at }),
      lastOperation: last,
    }));
    if (next.status !== "error") return;
    clearTimeout(lingerTimer);
    // Only this error goes: a newer transition is left as it is.
    lingerTimer = setTimeout(() => {
      set((state) => (state.lastOperation === last ? { lastOperation: null } : {}));
    }, LAST_ERROR_LINGER_MS);
  },
  clearOperation: (scope) => {
    set((state) => ({
      operations: withOperation(state.operations, scope, IDLE_OPERATION),
      lastOperation:
        state.lastOperation?.scope === scope ? null : state.lastOperation,
    }));
  },
});
