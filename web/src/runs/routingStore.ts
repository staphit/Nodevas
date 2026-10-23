import { create } from "zustand";
import {
  api,
  AuthError,
  RequestError,
  type RouteRule,
  type RouteSuggestion,
} from "../api";

/** Why the hub refused a save: "invalid" is its 400 INVALID_ARGUMENT, "denied"
 * a 403 (an agent session may not change routing). */
export interface RoutingRefusal {
  kind: "invalid" | "denied" | "other";
  message: string;
}

/**
 * The viewer's routing rules (GET/PUT /api/runs/routing). Fetched when the
 * Runners dialog's Routing section opens, or when the dispatch dialog needs a
 * matched rule's value for its note; never on load.
 */
export interface RoutingStoreState {
  /** The rules as the hub last answered; null until loaded. */
  rules: RouteRule[] | null;
  loading: boolean;
  error: string | null;
  saving: boolean;
  saveError: RoutingRefusal | null;
  load: (force?: boolean) => Promise<void>;
  /** Replaces the rules; returns whether the hub took them. */
  save: (rules: RouteRule[]) => Promise<boolean>;
  clearSaveError: () => void;
  reset: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function routingRefusal(error: unknown): RoutingRefusal {
  if (error instanceof AuthError && error.status === 403) {
    return { kind: "denied", message: error.message };
  }
  if (error instanceof RequestError && error.status === 400) {
    return { kind: "invalid", message: error.message };
  }
  return { kind: "other", message: message(error) };
}

let inFlight: Promise<void> | null = null;
let generation = 0;

export const useRouting = create<RoutingStoreState>((set, get) => ({
  rules: null,
  loading: false,
  error: null,
  saving: false,
  saveError: null,

  load: (force = false) => {
    if (inFlight) return inFlight;
    if (get().rules && !force) return Promise.resolve();
    const gen = generation;
    set({ loading: true });
    const work = (async () => {
      try {
        const config = await api.getRouting();
        if (gen !== generation) return;
        set({ rules: config?.rules ?? [], error: null });
      } catch (error) {
        if (gen !== generation) return;
        set({ error: message(error) });
      } finally {
        if (gen === generation) set({ loading: false });
      }
    })().finally(() => {
      if (inFlight === work) inFlight = null;
    });
    inFlight = work;
    return work;
  },

  save: async (rules) => {
    const gen = generation;
    set({ saving: true, saveError: null });
    try {
      const config = await api.putRouting(rules);
      if (gen !== generation) return false;
      set({ rules: config?.rules ?? rules, error: null });
      return true;
    } catch (error) {
      if (gen !== generation) return false;
      set({ saveError: routingRefusal(error) });
      return false;
    } finally {
      if (gen === generation) set({ saving: false });
    }
  },

  clearSaveError: () => set({ saveError: null }),

  reset: () => {
    generation++;
    inFlight = null;
    set({ rules: null, loading: false, error: null, saving: false, saveError: null });
  },
}));

/** What the rules pick for a node, or null when the hub could not say (an
 * older hub, an outage): the dialog then simply starts unfilled. */
export async function fetchRouteSuggestion(nodeId: string): Promise<RouteSuggestion | null> {
  try {
    const suggestion = await api.getRoute(nodeId);
    return suggestion && typeof suggestion === "object" ? suggestion : null;
  } catch {
    return null;
  }
}
