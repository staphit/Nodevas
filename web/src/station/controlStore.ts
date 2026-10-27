import { create } from "zustand";
import { api, type PauseAllResponse } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useRuns } from "../runs/runStore";
import { useStation } from "./stationStore";

/**
 * Station's emergency stop (POST /api/control/pause-all). What it changed
 * arrives as runs-changed and autopilot-changed anyway; the lists are
 * refetched at once too, so the table does not lag the answer.
 */
export interface ControlStoreState {
  pausing: boolean;
  /** What the last pause-all did, until dismissed. */
  result: PauseAllResponse | null;
  error: string;
  pauseAll: (cancelRunning: boolean) => Promise<boolean>;
  /** "Resume all": resumes each paused autopilot given, one by one, the way
   * its goal's own Resume does. The hub has no resume-all of its own, and
   * runs pause-all withdrew stay withdrawn. */
  resumeAll: (ids: string[]) => Promise<boolean>;
  dismiss: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export const useControl = create<ControlStoreState>((set, get) => ({
  pausing: false,
  result: null,
  error: "",

  pauseAll: async (cancelRunning) => {
    if (get().pausing) return false;
    set({ pausing: true, error: "" });
    try {
      const result = await api.pauseAll({ cancelRunning });
      set({ result });
      void useRuns.getState().loadActiveRuns();
      void useStation.getState().load();
      void useAutopilot.getState().loadActive();
      return true;
    } catch (error) {
      set({ error: message(error) });
      return false;
    } finally {
      set({ pausing: false });
    }
  },

  resumeAll: async (ids) => {
    if (get().pausing || ids.length === 0) return false;
    set({ pausing: true, error: "", result: null });
    try {
      const act = useAutopilot.getState().act;
      const done = await Promise.all(ids.map((id) => act(id, "resume")));
      return done.every(Boolean);
    } finally {
      set({ pausing: false });
    }
  },

  dismiss: () => set({ result: null, error: "" }),
}));
