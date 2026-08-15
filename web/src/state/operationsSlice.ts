/** Operation state slice [A-02/A-05]. */

import {
  IDLE_OPERATION,
  operationAt,
  withOperation,
} from "./operations";
import type { AppSlice, OperationsSlice } from "./types";

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
    set((state) => ({
      operations: withOperation(state.operations, scope, { ...next, at }),
      lastOperation: { scope, ...next, at },
    }));
  },
  clearOperation: (scope) => {
    set((state) => ({
      operations: withOperation(state.operations, scope, IDLE_OPERATION),
      lastOperation:
        state.lastOperation?.scope === scope ? null : state.lastOperation,
    }));
  },
});
