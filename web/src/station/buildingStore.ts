import { create } from "zustand";

import { api, type BuildingResponse } from "../api";

/**
 * Every project's office for Station's building (GET /api/station/building):
 * one request for the whole workspace, loaded when Station opens or switches
 * project, and refetched on building-changed (the hub's throttled "something
 * changed somewhere" ping, App.tsx) while Station is on screen. Pings are
 * coalesced here too, one request at a time; a hidden tab waits until it is
 * shown again.
 */
export interface BuildingState {
  data: BuildingResponse | null;
  loaded: boolean;
  /** Station is on screen: pings refetch. */
  watching: boolean;
  setWatching: (watching: boolean) => void;
  load: () => Promise<void>;
  /** A building-changed ping. */
  onChanged: () => void;
  reset: () => void;
}

export const COALESCE_MS = 1500;

let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight: Promise<void> | null = null;
/** A load was asked for while one was in flight: run once more after it. */
let again = false;

function visible(): boolean {
  return typeof document === "undefined" || document.visibilityState !== "hidden";
}

function clearTimer() {
  if (timer) clearTimeout(timer);
  timer = null;
}

export const useBuilding = create<BuildingState>((set, get) => ({
  data: null,
  loaded: false,
  watching: false,

  setWatching: (watching) => {
    set({ watching });
    if (!watching) clearTimer();
    else void get().load();
  },

  load: () => {
    if (inFlight) {
      again = true;
      return inFlight;
    }
    const work = (async () => {
      try {
        const data = await api.getBuilding();
        set({ data, loaded: true });
      } catch {
        // A hub without the building (older), or one that went away: the
        // current office still draws from the project's own stores.
        set({ loaded: true });
      }
    })().finally(() => {
      if (inFlight === work) inFlight = null;
      if (again) {
        again = false;
        if (get().watching) void get().load();
      }
    });
    inFlight = work;
    return work;
  },

  onChanged: () => {
    // A fixed window from the first ping, not a quiet period: several busy
    // projects ping more often than COALESCE_MS between them, and a timer
    // reset on every ping would never fire.
    if (!get().watching || !visible() || timer) return;
    timer = setTimeout(() => {
      timer = null;
      void get().load();
    }, COALESCE_MS);
  },

  reset: () => {
    clearTimer();
    inFlight = null;
    again = false;
    set({ data: null, loaded: false, watching: false });
  },
}));

// Back from a background tab: what changed meanwhile was not fetched.
if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (visible() && useBuilding.getState().watching) void useBuilding.getState().load();
  });
}
