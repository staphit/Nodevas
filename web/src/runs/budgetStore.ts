import { create } from "zustand";
import { api, type RunBudget, type RunBudgetStatus } from "../api";

/**
 * The viewer's daily run budget and today's use (GET/PUT /api/runs/budget).
 * Fetched when something shows it — the Runners dialog's Budget section, the
 * dispatch dialog's warning — never on load.
 */
export interface BudgetStoreState {
  status: RunBudgetStatus | null;
  loading: boolean;
  error: string | null;
  saving: boolean;
  saveError: string | null;
  /** Set by "adjust budget" links: the Runners dialog opens on its Budget. */
  showRequested: boolean;
  requestShow: (show: boolean) => void;
  load: () => Promise<void>;
  /** Saves the limits; returns whether the hub took them. */
  save: (budget: RunBudget) => Promise<boolean>;
  reset: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

let inFlight: Promise<void> | null = null;

export const useBudget = create<BudgetStoreState>((set) => ({
  status: null,
  loading: false,
  error: null,
  saving: false,
  saveError: null,
  showRequested: false,
  requestShow: (show) => set({ showRequested: show }),

  load: () => {
    if (inFlight) return inFlight;
    set({ loading: true });
    const work = (async () => {
      try {
        const status = await api.getRunBudget();
        set({ status, error: null });
      } catch (error) {
        set({ error: message(error) });
      } finally {
        set({ loading: false });
      }
    })().finally(() => {
      if (inFlight === work) inFlight = null;
    });
    inFlight = work;
    return work;
  },

  save: async (budget) => {
    set({ saving: true, saveError: null });
    try {
      const status = await api.putRunBudget(budget);
      set({ status, error: null });
      return true;
    } catch (error) {
      set({ saveError: message(error) });
      return false;
    } finally {
      set({ saving: false });
    }
  },

  reset: () => {
    inFlight = null;
    set({
      status: null,
      loading: false,
      error: null,
      saving: false,
      saveError: null,
      showRequested: false,
    });
  },
}));
