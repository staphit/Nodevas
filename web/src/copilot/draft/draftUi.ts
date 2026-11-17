/**
 * What the screen shows about drafts that is nobody's data: where the
 * overlay drew the ghosts, the board's dry-run answer, a Create in flight,
 * whether this person may write, and the toast after a Create.
 *
 * Its own small store, so the board's draft overlay, the panel's card and
 * the toast can each listen to just their part.
 */

import { create } from "zustand";
import type { Cell } from "./draftLayout";

export type DraftCheck =
  | { status: "checking" }
  | { status: "ok" }
  /** The board refused the draft; `message` is the server's, in the reader's language. */
  | { status: "error"; message: string };

export type DraftToast =
  | { kind: "created"; entryId: string; count: number; undoable: boolean; at: number }
  | { kind: "undone"; count: number; refused: number; at: number }
  | { kind: "error"; text: string; at: number };

export interface DraftUiState {
  /** The active draft's ghost cells, as the overlay laid them out. */
  ghost: { entryId: string; cells: Record<string, Cell> } | null;
  /** The board's answer to the newest dry run, by draft entry id. */
  checks: Record<string, DraftCheck>;
  /** A Create or Discard on its way, by draft entry id. */
  busy: Record<string, "creating" | "discarding">;
  /** Whether this person may write to the open project (null: not known yet). */
  canWrite: { project: string; value: boolean } | null;
  toast: DraftToast | null;

  setGhost: (ghost: DraftUiState["ghost"]) => void;
  setCheck: (entryId: string, check: DraftCheck | null) => void;
  setBusy: (entryId: string, busy: "creating" | "discarding" | null) => void;
  setCanWrite: (project: string, value: boolean) => void;
  showToast: (toast: DraftToast | null) => void;
}

const sameCells = (a: Record<string, Cell>, b: Record<string, Cell>) => {
  const keys = Object.keys(a);
  return (
    keys.length === Object.keys(b).length &&
    keys.every((key) => b[key] && a[key].col === b[key].col && a[key].row === b[key].row)
  );
};

export const useDraftUi = create<DraftUiState>((set, get) => ({
  ghost: null,
  checks: {},
  busy: {},
  canWrite: null,
  toast: null,

  setGhost: (ghost) => {
    const current = get().ghost;
    if (ghost === null ? current === null : current?.entryId === ghost.entryId && sameCells(current.cells, ghost.cells)) {
      return;
    }
    set({ ghost });
  },
  setCheck: (entryId, check) =>
    set((state) => {
      const checks = { ...state.checks };
      if (check) checks[entryId] = check;
      else delete checks[entryId];
      return { checks };
    }),
  setBusy: (entryId, busy) =>
    set((state) => {
      const next = { ...state.busy };
      if (busy) next[entryId] = busy;
      else delete next[entryId];
      return { busy: next };
    }),
  setCanWrite: (project, value) => set({ canWrite: { project, value } }),
  showToast: (toast) => set({ toast }),
}));

/** The board's extent the ghosts need, so it grows to show them. */
export function ghostExtent(state: Pick<DraftUiState, "ghost">): { col: number; row: number } | null {
  const cells = state.ghost ? Object.values(state.ghost.cells) : [];
  if (cells.length === 0) return null;
  return {
    col: Math.max(...cells.map((cell) => cell.col)),
    row: Math.max(...cells.map((cell) => cell.row)),
  };
}
