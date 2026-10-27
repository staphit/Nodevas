import { create } from "zustand";
import { api, type Run } from "../api";

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
  /** What the "In progress" card draws: its list, or the game view the
   * stageSkin preference names. Remembered per browser. */
  liveView: StationLiveView;
  setLiveView: (view: StationLiveView) => void;
}

export type StationLiveView = "list" | "game";

const LIVE_VIEW_KEY = "nodevas-station-view";

function readLiveView(): StationLiveView {
  try {
    return localStorage.getItem(LIVE_VIEW_KEY) === "game" ? "game" : "list";
  } catch {
    return "list";
  }
}

const COALESCE_MS = 800;
let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight: Promise<void> | null = null;

export const useStation = create<StationStoreState>((set, get) => ({
  runs: [],
  loaded: false,
  watching: false,
  goal: null,
  goalProject: "",
  liveView: readLiveView(),
  setLiveView: (liveView) => {
    set({ liveView });
    try {
      localStorage.setItem(LIVE_VIEW_KEY, liveView);
    } catch {
      /* remembered for this visit only */
    }
  },
  focusGoal: (goal, project = "") => set({ goal, goalProject: goal ? project : "" }),
  project: "",
  openProject: (project) => {
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
    set({ runs: [], loaded: false, goal: null, goalProject: "", project: "" });
  },
}));
