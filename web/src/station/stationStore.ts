import { create } from "zustand";
import { api, type Run } from "../api";
import type { ClickTarget } from "../game/world";

/** Something clicked in another project's office, done once Station has
 * switched there (StationView). */
export interface DeferredTarget {
  project: string;
  target: ClickTarget;
  /** The switch finished: the project's graph is the one loaded. */
  ready: boolean;
}

/** How long a deferred target waits for its project before it is dropped. */
export const DEFER_TIMEOUT_MS = 10_000;

/**
 * The project's recent runs, for Station's timeline and its "waiting on you"
 * strip (GET /api/runs: the newest runs the viewer may see). Loaded when
 * Station opens and refetched on runs-changed while it is open, coalesced.
 */
export interface StationStoreState {
  runs: Run[];
  loaded: boolean;
  /** The autopilot goal Station shows in full, by node id; null: none. */
  goal: string | null;
  /** The project goal is a node of. */
  goalProject: string;
  focusGoal: (nodeId: string | null, project?: string) => void;
  /** The project the runs belong to. */
  project: string;
  /** Station opened on project: its runs are loaded, and a goal from
   * another project is let go. */
  openProject: (project: string) => void;
  /** Station is on screen: runs-changed refetches. */
  watching: boolean;
  setWatching: (watching: boolean) => void;
  load: () => Promise<void>;
  onRunsChanged: () => void;
  reset: () => void;
  deferred: DeferredTarget | null;
  /** Do `target` in `project` once Station has switched to it. */
  deferTarget: (project: string, target: ClickTarget) => void;
  /** The switch to the deferred target's project finished. */
  deferredReady: (project: string) => void;
  clearDeferred: () => void;
}

const COALESCE_MS = 800;
let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight: Promise<void> | null = null;
let deferTimer: ReturnType<typeof setTimeout> | null = null;

export const useStation = create<StationStoreState>((set, get) => ({
  runs: [],
  loaded: false,
  watching: false,
  goal: null,
  goalProject: "",
  focusGoal: (goal, project = "") => set({ goal, goalProject: goal ? project : "" }),
  project: "",
  openProject: (project) => {
    // A manual switch elsewhere drops what was waiting for another project.
    const deferred = get().deferred;
    if (deferred && deferred.project !== project) get().clearDeferred();
    if (get().project !== project) {
      if (timer) clearTimeout(timer);
      timer = null;
      inFlight = null;
      set({ runs: [], loaded: false, project });
    }
    if (get().goal && get().goalProject !== project) set({ goal: null, goalProject: "" });
    void get().load();
  },

  setWatching: (watching) => {
    set({ watching });
    if (watching) void get().load();
  },

  load: () => {
    if (inFlight) return inFlight;
    const work = (async () => {
      try {
        const res = await api.listProjectRuns();
        set({ runs: res.runs ?? [], loaded: true });
      } catch {
        // A hub that went away, or a viewer who may not list runs: the
        // timeline stays as it was.
      }
    })().finally(() => {
      if (inFlight === work) inFlight = null;
    });
    inFlight = work;
    return work;
  },

  onRunsChanged: () => {
    if (!get().watching) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void get().load();
    }, COALESCE_MS);
  },

  reset: () => {
    if (timer) clearTimeout(timer);
    timer = null;
    inFlight = null;
    get().clearDeferred();
    set({ runs: [], loaded: false, goal: null, goalProject: "", project: "" });
  },

  deferred: null,
  deferTarget: (project, target) => {
    if (deferTimer) clearTimeout(deferTimer);
    deferTimer = setTimeout(() => {
      deferTimer = null;
      set({ deferred: null });
    }, DEFER_TIMEOUT_MS);
    set({ deferred: { project, target, ready: false } });
  },
  deferredReady: (project) => {
    const deferred = get().deferred;
    if (deferred && deferred.project === project && !deferred.ready) set({ deferred: { ...deferred, ready: true } });
  },
  clearDeferred: () => {
    if (deferTimer) clearTimeout(deferTimer);
    deferTimer = null;
    if (get().deferred) set({ deferred: null });
  },
}));
