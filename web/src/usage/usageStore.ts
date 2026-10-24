import { create } from "zustand";
import { api, AuthError, type ModelPrice, type UsagePrices, type UsageReport } from "../api";

export type EditablePrice = Omit<ModelPrice, "source">;

export interface UsageStoreState {
  /** Whether anything has asked for usage yet. */
  loaded: boolean;
  /** Whether the Usage dialog is showing: realtime events refetch only then. */
  open: boolean;
  loading: boolean;
  days: number;
  report: UsageReport | null;
  error: string | null;

  pricesLoaded: boolean;
  pricesLoading: boolean;
  prices: UsagePrices | null;
  pricesError: string | null;
  saving: boolean;
  /** Set when a save was refused for want of rights (403). */
  pricesForbidden: boolean;

  refresh: () => Promise<void>;
  /** Refetches only if the report was loaded before. */
  refreshIfLoaded: () => Promise<void>;
  setOpen: (open: boolean) => void;
  /** usage-changed (a session reported) and runs-changed (a run finished
   * with its usage): refetch, coalesced, while the dialog is open. */
  onUsageEvent: () => void;
  setDays: (days: number) => Promise<void>;
  loadPrices: () => Promise<void>;
  /** Replaces the custom prices, then refetches prices and the report. */
  savePrices: (custom: EditablePrice[]) => Promise<boolean>;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Bumped by every fetch so a slow answer for an old range cannot land
 * over the answer for the range picked after it. */
let generation = 0;

/** Realtime events arrive in bursts (a run's status, then its usage); one
 * refetch covers a burst. */
export const USAGE_EVENT_DELAY_MS = 800;
let eventTimer: ReturnType<typeof setTimeout> | null = null;

export const useUsage = create<UsageStoreState>((set, get) => ({
  loaded: false,
  open: false,
  loading: false,
  days: 30,
  report: null,
  error: null,

  pricesLoaded: false,
  pricesLoading: false,
  prices: null,
  pricesError: null,
  saving: false,
  pricesForbidden: false,

  refresh: async () => {
    const mine = ++generation;
    const days = get().days;
    set({ loading: true, loaded: true });
    try {
      const report = await api.getUsage(days);
      if (mine !== generation) return;
      set({ report, error: null });
    } catch (error) {
      if (mine !== generation) return;
      set({ error: message(error) });
    } finally {
      if (mine === generation) set({ loading: false });
    }
  },

  refreshIfLoaded: async () => {
    if (get().loaded) await get().refresh();
  },

  setOpen: (open) => {
    set({ open });
    if (!open && eventTimer) {
      clearTimeout(eventTimer);
      eventTimer = null;
    }
  },

  onUsageEvent: () => {
    if (!get().open || eventTimer) return;
    eventTimer = setTimeout(() => {
      eventTimer = null;
      if (get().open) void get().refresh();
    }, USAGE_EVENT_DELAY_MS);
  },

  setDays: async (days) => {
    if (days === get().days && get().report) return;
    set({ days });
    await get().refresh();
  },

  loadPrices: async () => {
    set({ pricesLoading: true, pricesLoaded: true });
    try {
      const prices = await api.getUsagePrices();
      set({ prices, pricesError: null });
    } catch (error) {
      set({ pricesError: message(error) });
    } finally {
      set({ pricesLoading: false });
    }
  },

  savePrices: async (custom) => {
    set({ saving: true, pricesError: null, pricesForbidden: false });
    try {
      await api.putUsagePrices(custom);
      await Promise.all([get().loadPrices(), get().refresh()]);
      return true;
    } catch (error) {
      if (error instanceof AuthError && error.status === 403) {
        set({ pricesForbidden: true });
      } else {
        set({ pricesError: message(error) });
      }
      return false;
    } finally {
      set({ saving: false });
    }
  },
}));
