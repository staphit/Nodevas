import { create } from "zustand";
import {
  api,
  AuthError,
  forgetRunWorkspace,
  runFinal,
  WorkspaceActionError,
  WORKSPACE_CODES,
  type Run,
  type RunWorkspaceView,
} from "../api";
import { translate } from "../i18n";
import { selectRunById, useRuns } from "./runStore";

/** The agent workspace panel's tabs. */
export type WorkspaceTab = "files" | "changes" | "terminal";

export interface WorkspaceState {
  /** The run whose workspace is open, and on which tab; null when closed. */
  open: { runId: string; tab: WorkspaceTab } | null;
  /** GET /api/runs/:id/workspace, per run, once asked for. */
  views: Record<string, RunWorkspaceView | undefined>;
  viewErrors: Record<string, string>;
  /** The action being posted for a run. */
  acting: Record<string, "merge" | "discard" | undefined>;
  /** Why the hub refused a run's last action, translated. */
  actionErrors: Record<string, string>;
  openWorkspace: (runId: string, tab?: WorkspaceTab) => void;
  setTab: (tab: WorkspaceTab) => void;
  close: () => void;
  loadView: (runId: string) => Promise<void>;
  /** Asks to merge the run's branch into its base locally, at run.commit. */
  mergeLocal: (run: Run) => Promise<boolean>;
  discard: (run: Run) => Promise<boolean>;
  /** A different project: drops the views and errors held. */
  reset: () => void;
}

const KNOWN_CODES = new Set<string>(WORKSPACE_CODES);

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** A workspace code in the reader's words, or "" when it has none. */
export function workspaceCodeText(code: string | undefined, values: Record<string, string | number> = {}): string {
  if (!code || !KNOWN_CODES.has(code)) return "";
  return translate(`runWorkspace.code.${code}`, undefined, values);
}

/** Why the hub refused an action, in the reader's words: its gate (a 409's
 * `blocked`), its code, else its own (already localized) message. */
export function workspaceRefusal(error: unknown, run: Pick<Run, "base">): string {
  const values = { base: run.base };
  if (error instanceof WorkspaceActionError) {
    const gate = workspaceCodeText(error.blocked, values);
    if (gate) return gate;
    if (error.code === "RUNNER_OUTDATED") return workspaceCodeText("outdated");
    if (error.code === "RUNNER_OFFLINE") return workspaceCodeText("offline");
    if (error.status === 403 && !error.message) return workspaceCodeText("not_owner");
    return error.message;
  }
  if (error instanceof AuthError && error.status === 403) return error.message || workspaceCodeText("not_owner");
  return message(error);
}

const without = <T>(record: Record<string, T>, key: string): Record<string, T> => {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
};

/** Where the panel opens when the caller does not say: a run at work shows
 * what it is doing, a finished one what it changed. */
function defaultTab(runId: string): WorkspaceTab {
  const run = selectRunById(useRuns.getState(), runId);
  return run && !runFinal(run.status) ? "terminal" : "changes";
}

// Bumped by reset(): an answer for the previous project is dropped.
let generation = 0;

export const useWorkspace = create<WorkspaceState>((set, get) => {
  const act = async (run: Run, kind: "merge" | "discard"): Promise<boolean> => {
    if (get().acting[run.id]) return false;
    const gen = generation;
    set((state) => ({
      acting: { ...state.acting, [run.id]: kind },
      actionErrors: without(state.actionErrors, run.id),
    }));
    try {
      const updated =
        kind === "merge" ? await api.mergeRunLocal(run.id, run.commit ?? "") : await api.discardRun(run.id);
      if (gen !== generation) return true;
      // The rows update now, before the realtime push.
      if (updated?.id) useRuns.getState().putRun(updated);
      return true;
    } catch (error) {
      if (gen !== generation) return false;
      set((state) => ({ actionErrors: { ...state.actionErrors, [run.id]: workspaceRefusal(error, run) } }));
      // A 409 often means the run moved on: show what the hub knows now.
      void useRuns.getState().refreshRun(run.id);
      return false;
    } finally {
      if (gen === generation) {
        set((state) => ({ acting: without(state.acting, run.id) }));
        // The worktree is not what was read, and the gates moved.
        forgetRunWorkspace(run.id);
        void get().loadView(run.id);
      }
    }
  };

  return {
    open: null,
    views: {},
    viewErrors: {},
    acting: {},
    actionErrors: {},

    openWorkspace: (runId, tab) => {
      if (!runId) return;
      set({ open: { runId, tab: tab ?? defaultTab(runId) } });
      void get().loadView(runId);
    },

    setTab: (tab) =>
      set((state) => (state.open ? { open: { ...state.open, tab } } : {})),

    close: () => set({ open: null }),

    loadView: async (runId) => {
      const gen = generation;
      try {
        const view = await api.getRunWorkspace(runId);
        if (gen !== generation) return;
        set((state) => ({
          views: { ...state.views, [runId]: view },
          viewErrors: without(state.viewErrors, runId),
        }));
      } catch (error) {
        if (gen !== generation) return;
        set((state) => ({ viewErrors: { ...state.viewErrors, [runId]: message(error) } }));
      }
    },

    mergeLocal: (run) => act(run, "merge"),
    discard: (run) => act(run, "discard"),

    // What was held belongs to the previous project. An open panel stays:
    // a room in another project switches first and opens it after.
    reset: () => {
      generation++;
      set({ views: {}, viewErrors: {}, acting: {}, actionErrors: {} });
    },
  };
});
