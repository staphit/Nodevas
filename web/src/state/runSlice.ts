/**
 * Actual lifecycle slice [A-05].
 *
 * The journal is the source of truth: every write is an append through the API,
 * and the store only mirrors the state the server replays back. Nothing here
 * edits history.
 */

import { api } from "../api";
import {
  commandFailed,
  commandSucceeded,
  describeFailure,
  operationScope,
} from "./operations";
import { coalesceRequest } from "./coalesce";
import { generations } from "./internals";
import { pushUndo } from "./undo";
import type { AppSlice, RunSlice } from "./types";

export const createRunSlice: AppSlice<RunSlice> = (set, get) => ({
  runState: null,
  statuses: {},
  stagedLifecycle: {},

  /**
   * Holds a chosen status until somebody explicitly applies it [B-04].
   *
   * This is the one thing the drawer's auto-save deliberately does not carry.
   * A status is an append to `run/journal.jsonl`, which is history: writing one
   * cannot be undone, only compensated by a second entry that also stays
   * forever. Text is not like that — it has version snapshots, drafts and an
   * undo stack — so text can safely be written on a timer and a status cannot.
   * Choosing a value from a select while scrolling would otherwise put a
   * permanent event in the record of a project a second and a half later, with
   * nobody having pressed anything.
   *
   * So it stays staged, and every *explicit* save commits it: the panel's own
   * 套用狀態 button, the drawer's Ctrl/⌘ + S, the global one, save-all and the
   * popout editor. Keeping the staged value in the store rather than the panel
   * is what lets all of those reach it without the panel listening for a
   * keystroke of its own.
   */
  stageLifecycleStatus: (nodeId, status, note) =>
    set((state) => {
      if (!status) {
        if (!(nodeId in state.stagedLifecycle)) return state;
        const next = { ...state.stagedLifecycle };
        delete next[nodeId];
        return { stagedLifecycle: next };
      }
      return { stagedLifecycle: { ...state.stagedLifecycle, [nodeId]: { status, note } } };
    }),

  commitStagedLifecycle: async (nodeId) => {
    const staged = get().stagedLifecycle[nodeId];
    if (!staged) return null;
    const result = await get().setLifecycleStatus(nodeId, staged.status, staged.note.trim());
    // A failure keeps the choice on screen, so a retry does not start over.
    if (result.ok) get().stageLifecycleStatus(nodeId, "", "");
    return result;
  },

  // A graph write, a lifecycle event and a `state-changed` broadcast all ask
  // for the same read, and one edit produces all three within a few
  // milliseconds, so they share a request rather than racing for it.
  refreshState: () =>
    coalesceRequest("state", async () => {
      const generation = ++generations.state;
      const s = await api.getState();
      // A newer request (or a project switch) started while this was in flight.
      if (generation !== generations.state) return;
      set({ runState: s.state, statuses: s.statuses });
    }),

  setLifecycleStatus: async (nodeId, status, note = "", options = {}) => {
    const scope = operationScope.lifecycle(nodeId);
    const name = "lifecycle.set";
    // Only an explicit previous event can be restored by a compensating one.
    const previousStatus = get().runState?.nodes?.[nodeId]?.status;
    get().beginOperation(scope, name);
    try {
      const res = await api.setStatus(nodeId, status, note);
      set({ runState: res.state, statuses: res.statuses });
      if (options.recordUndo !== false && previousStatus && previousStatus !== status) {
        pushUndo({ kind: "lifecycle-status", nodeId, previousStatus });
      }
      get().settleOperation(scope, { status: "saved", operation: name });
      return commandSucceeded(name, scope, undefined);
    } catch (e) {
      const message = describeFailure(e, "套用實際狀態失敗");
      get().settleOperation(scope, { status: "error", operation: name, message });
      return commandFailed(name, scope, message);
    }
  },

  moveActualEvent: async (eventId, timestamp, note = "時間軸拖曳調整", options = {}) => {
    const target = get().runState?.history.find((event) => event.id === eventId);
    const nodeId = target?.node;
    const previousTimestamp = target?.t;
    const scope = nodeId ? operationScope.lifecycle(nodeId) : operationScope.run();
    const name = "lifecycle.move";
    get().beginOperation(scope, name);
    try {
      const res = await api.moveEvent(eventId, timestamp, note);
      set({ runState: res.state, statuses: res.statuses });
      if (options.recordUndo !== false && previousTimestamp) {
        pushUndo({ kind: "lifecycle-move", eventId, previousTimestamp });
      }
      get().settleOperation(scope, { status: "saved", operation: name });
      return commandSucceeded(name, scope, undefined);
    } catch (e) {
      const message = describeFailure(e, "調整實際時間失敗");
      get().settleOperation(scope, { status: "error", operation: name, message });
      return commandFailed(name, scope, message);
    }
  },

  // Compatibility wrappers: same throwing contract the components expect.
  setStatus: async (id, status, note = "") => {
    const result = await get().setLifecycleStatus(id, status, note);
    if (!result.ok) throw new Error(result.message);
  },

  moveStamp: async (eventId, t) => {
    const result = await get().moveActualEvent(eventId, t);
    if (!result.ok) throw new Error(result.message);
  },
});
