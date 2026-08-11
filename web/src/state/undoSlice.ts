/**
 * Undo/redo slice [A-05].
 *
 * Both directions share the graph write queue: reverting is itself a graph
 * write, so it must not overtake a save that is still in flight. A failed
 * revert puts its entry back on the stack it came from, so the next
 * Ctrl/⌘+Z (or Ctrl/⌘+Shift+Z) retries the same step instead of silently
 * skipping to an older one.
 *
 * Undo and redo run the *same* code: applying an entry returns the entry that
 * would undo the application, which lands on the opposite stack. That is why
 * redo needs no second set of cases — an undone delete becomes a create, an
 * undone create becomes a delete, and a compensating journal event describes
 * the compensation that would cancel it.
 */

import { api } from "../api";
import { queues } from "./internals";
import {
  clearRedo,
  peekRedo,
  peekUndo,
  popRedo,
  popUndo,
  pushRedo,
  pushUndo,
  redoDepth,
  undoDepth,
  undoEntryLabel,
  type UndoEntry,
} from "./undo";
import type { AppSlice, UndoSlice } from "./types";

type Direction = "undo" | "redo";
type Setter = Parameters<AppSlice<UndoSlice>>[0];
type Getter = Parameters<AppSlice<UndoSlice>>[1];

/** Notes on compensating writes say which gesture produced them. */
const NOTES = {
  undo: { status: "撤銷：回到先前的實際狀態", move: "撤銷：回到先前的時間" },
  redo: { status: "重做：重新套用實際狀態", move: "重做：重新套用時間調整" },
} as const;

/**
 * Carries the two things a failed revert needs to decide what to put back:
 * whether the write landed, and which entry a retry should start from (a
 * partially-applied delete has already consumed its trash files).
 */
type Attempt = { applied: boolean; retry: UndoEntry };

/**
 * Applies one entry and returns the entry that would undo this application, or
 * null when the reverse cannot be described.
 */
async function applyEntry(
  entry: UndoEntry,
  attempt: Attempt,
  direction: Direction,
  set: Setter,
  get: Getter,
): Promise<UndoEntry | null> {
  const notes = NOTES[direction];
  // Journal domains: append the inverse, never rewrite history.
  if (entry.kind === "lifecycle-status") {
    const current = get().runState?.nodes?.[entry.nodeId]?.status;
    const result = await get().setLifecycleStatus(
      entry.nodeId,
      entry.previousStatus,
      notes.status,
      { recordUndo: false },
    );
    if (!result.ok) throw new Error(result.message);
    attempt.applied = true;
    // The status we just left is only restorable when an explicit event stood
    // behind it — the same rule that kept it out of the stack in the first
    // place (README rule 2).
    return current
      ? { kind: "lifecycle-status", nodeId: entry.nodeId, previousStatus: current, label: entry.label }
      : null;
  }
  if (entry.kind === "lifecycle-move") {
    const current = get().runState?.history.find((event) => event.id === entry.eventId)?.t;
    const result = await get().moveActualEvent(
      entry.eventId,
      entry.previousTimestamp,
      notes.move,
      { recordUndo: false },
    );
    if (!result.ok) throw new Error(result.message);
    attempt.applied = true;
    return current
      ? { kind: "lifecycle-move", eventId: entry.eventId, previousTimestamp: current, label: entry.label }
      : null;
  }

  let inverse: UndoEntry | null = null;
  if (entry.kind === "graph") {
    const { graph, graphRev } = get();
    if (!graph) return null;
    const before = structuredClone(graph);
    const restored = structuredClone(entry.graph);
    set({ graph: restored });
    try {
      const res = await api.putGraph(restored, graphRev);
      set({ graphRev: res.rev, issues: res.issues ?? [], error: null });
      attempt.applied = true;
    } catch (error) {
      set({ graph });
      throw error;
    }
    inverse = { kind: "graph", graph: before, label: entry.label };
  } else if (entry.kind === "create-node") {
    const before = get().graph ? structuredClone(get().graph!) : null;
    // One request for the whole batch, same as the delete that made it.
    const result =
      entry.ids.length === 1
        ? { trashFiles: [(await api.deleteNode(entry.ids[0])).trashFile] }
        : await api.deleteNodes(entry.ids);
    attempt.applied = true;
    for (const id of entry.ids) get().closeTab(id);
    inverse = before
      ? {
          kind: "delete-node",
          graph: before,
          trashFiles: result.trashFiles.filter(Boolean),
          label: entry.label,
        }
      : null;
  } else {
    // Restore the documents first; if the graph write then fails, the retry
    // entry no longer needs the trash files it already consumed.
    const restoredIds: string[] = [];
    if (entry.trashFiles.length > 0) {
      for (const trashFile of entry.trashFiles) {
        const restored = await api.restoreTrash(trashFile);
        if (restored.id) restoredIds.push(restored.id);
      }
      attempt.retry = { kind: "graph", graph: entry.graph, label: entry.label };
    }
    const before = get().graph ? structuredClone(get().graph!) : null;
    const current = await api.getGraph();
    const restored = structuredClone(entry.graph);
    await api.putGraph(restored, current.rev);
    set({ graph: restored });
    attempt.applied = true;
    inverse =
      restoredIds.length > 0
        ? { kind: "create-node", ids: restoredIds, label: entry.label }
        : before
          ? { kind: "graph", graph: before, label: entry.label }
          : null;
  }
  try {
    await Promise.all([get().loadAll(), get().refreshTrash()]);
  } catch (error) {
    set({ error: error instanceof Error ? error.message : "重新載入失敗" });
  }
  return inverse;
}

/** One step in either direction, queued behind whatever graph write is running. */
function revert(
  direction: Direction,
  set: Setter,
  get: Getter,
): Promise<boolean> {
  const operation = queues.graphSave.then(async () => {
    const entry = direction === "undo" ? popUndo() : popRedo();
    if (!entry) return false;
    const attempt: Attempt = { applied: false, retry: entry };
    try {
      const inverse = await applyEntry(entry, attempt, direction, set, get);
      if (!attempt.applied) {
        // Nothing was written (no graph on screen to restore over): put the
        // entry back so the affordance keeps describing a real step.
        if (direction === "undo") pushUndo(entry, { keepRedo: true });
        else pushRedo(entry);
        return false;
      }
      if (!inverse) {
        // The step landed but cannot describe its own reverse. Everything on
        // the opposite stack assumed that reverse, so it goes too.
        if (direction === "undo") clearRedo();
        return true;
      }
      if (direction === "undo") pushRedo(inverse);
      else pushUndo(inverse, { keepRedo: true });
      return true;
    } catch (error) {
      if (!attempt.applied) {
        if (direction === "undo") pushUndo(attempt.retry, { keepRedo: true });
        else pushRedo(attempt.retry);
      }
      set({ error: error instanceof Error ? error.message : "還原失敗" });
      throw error;
    }
  });
  queues.graphSave = operation.then(() => undefined).catch(() => undefined);
  return operation;
}

export const createUndoSlice: AppSlice<UndoSlice> = (set, get) => ({
  canUndo: false,
  undoLabel: null,
  canRedo: false,
  redoLabel: null,

  undoLast: () => revert("undo", set, get),
  redoLast: () => revert("redo", set, get),
});

/** Current stack summary, mirrored into the store on every push/pop. */
export function undoMirror(): {
  canUndo: boolean;
  undoLabel: string | null;
  canRedo: boolean;
  redoLabel: string | null;
} {
  return {
    canUndo: undoDepth() > 0,
    undoLabel: undoEntryLabel(peekUndo()),
    canRedo: redoDepth() > 0,
    redoLabel: undoEntryLabel(peekRedo()),
  };
}
