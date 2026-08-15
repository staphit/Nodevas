/**
 * Undo/redo stacks [A-05/A-06].
 *
 * Command-based: an entry records what it takes to get back, plus the label the
 * UI shows. Only operations that can actually be reverted are pushed — an
 * operation that cannot be undone must never sit in the stack pretending it
 * can (plan A-06).
 *
 * The journal is append-only, so lifecycle entries revert by appending a
 * compensating event, never by deleting history.
 *
 * Redo is the same vocabulary read the other way: reverting an entry produces
 * the entry that would revert *that*, and it goes on the redo stack. A fresh
 * edit drops the redo stack — its entries describe a future that no longer
 * follows from the current state.
 */

import type { Graph, Status } from "../types";

export const MAX_UNDO_ENTRIES = 50;

export type UndoEntry =
  | { kind: "graph"; graph: Graph; label?: string }
  /** The nodes exist now; reverting deletes them again (into the trash). */
  | { kind: "create-node"; ids: string[]; label?: string }
  | { kind: "delete-node"; graph: Graph; trashFiles: string[]; label?: string }
  /**
   * Journal entries revert by *appending* the previous value again. They are
   * only pushed when the previous value was itself an explicit journal event —
   * a derived state (locked/ready with no event behind it) cannot be restored
   * without inventing history, so those changes stay out of the stack.
   */
  | { kind: "lifecycle-status"; nodeId: string; previousStatus: Status; label?: string }
  | {
      kind: "lifecycle-move";
      eventId: string;
      previousTimestamp: string;
      label?: string;
    };

const DEFAULT_LABELS: Record<UndoEntry["kind"], string> = {
  graph: "圖譜變更",
  "create-node": "新增節點",
  "delete-node": "刪除節點",
  "lifecycle-status": "實際狀態變更",
  "lifecycle-move": "實際時間調整",
};

let stack: UndoEntry[] = [];
let redoStack: UndoEntry[] = [];
let onChange: (() => void) | null = null;

/** The store mirrors depth/label into state so the UI can enable its affordance. */
export function observeUndo(listener: (() => void) | null): void {
  onChange = listener;
}

/**
 * Records a step that can be reverted.
 *
 * A new edit invalidates the redo stack: those entries describe how to get
 * back to a state this edit just branched away from, and applying them would
 * mix two histories. `keepRedo` is for the revert machinery itself, which
 * moves entries between the two stacks without the user having edited
 * anything.
 */
export function pushUndo(entry: UndoEntry, options: { keepRedo?: boolean } = {}): void {
  stack.push(entry);
  if (stack.length > MAX_UNDO_ENTRIES) stack = stack.slice(-MAX_UNDO_ENTRIES);
  if (!options.keepRedo) redoStack = [];
  onChange?.();
}

export function popUndo(): UndoEntry | undefined {
  const entry = stack.pop();
  if (entry) onChange?.();
  return entry;
}

export function peekUndo(): UndoEntry | undefined {
  return stack[stack.length - 1];
}

export function pushRedo(entry: UndoEntry): void {
  redoStack.push(entry);
  if (redoStack.length > MAX_UNDO_ENTRIES) redoStack = redoStack.slice(-MAX_UNDO_ENTRIES);
  onChange?.();
}

export function popRedo(): UndoEntry | undefined {
  const entry = redoStack.pop();
  if (entry) onChange?.();
  return entry;
}

export function peekRedo(): UndoEntry | undefined {
  return redoStack[redoStack.length - 1];
}

/**
 * Drops the redo stack on its own.
 *
 * Used when a revert lands but cannot describe its own inverse: the older redo
 * entries assume a step that no longer exists between them and now, so keeping
 * them would redo the wrong thing.
 */
export function clearRedo(): void {
  if (redoStack.length === 0) return;
  redoStack = [];
  onChange?.();
}

export function clearUndo(): void {
  stack = [];
  redoStack = [];
  onChange?.();
}

export function undoDepth(): number {
  return stack.length;
}

export function redoDepth(): number {
  return redoStack.length;
}

export function undoEntryLabel(entry: UndoEntry | undefined): string | null {
  if (!entry) return null;
  return entry.label ?? DEFAULT_LABELS[entry.kind];
}
