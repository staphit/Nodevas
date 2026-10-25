import { create } from "zustand";
import { api, type Runner } from "../api";

export interface RunnerStoreState {
  /** Whether anything has asked for runners yet. The realtime refresh only
   * runs once something has, so a board that never opens the dialog never
   * polls for it. */
  loaded: boolean;
  loading: boolean;
  runners: Runner[];
  error: string | null;
  creating: boolean;
  /** Ids being revoked, so their buttons can show it. */
  revoking: Record<string, boolean>;

  refresh: () => Promise<void>;
  /** Refreshes only if the list was loaded before; used by runners-changed. */
  refreshIfLoaded: () => Promise<void>;
  /** Creates a runner and returns its one-time token, or null on failure. */
  create: (name: string) => Promise<{ runner: Runner; token: string } | null>;
  revoke: (id: string) => Promise<boolean>;
  setError: (error: string | null) => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export const useRunners = create<RunnerStoreState>((set, get) => ({
  loaded: false,
  loading: false,
  runners: [],
  error: null,
  creating: false,
  revoking: {},

  refresh: async () => {
    set({ loading: true, loaded: true });
    try {
      const response = await api.listRunners();
      set({ runners: response.runners ?? [], error: null });
    } catch (error) {
      set({ error: message(error) });
    } finally {
      set({ loading: false });
    }
  },

  refreshIfLoaded: async () => {
    if (get().loaded) await get().refresh();
  },

  create: async (name) => {
    set({ creating: true, error: null });
    try {
      const created = await api.createRunner(name.trim());
      await get().refresh();
      return created;
    } catch (error) {
      set({ error: message(error) });
      return null;
    } finally {
      set({ creating: false });
    }
  },

  revoke: async (id) => {
    set((state) => ({ revoking: { ...state.revoking, [id]: true }, error: null }));
    try {
      await api.revokeRunner(id);
      await get().refresh();
      return true;
    } catch (error) {
      set({ error: message(error) });
      return false;
    } finally {
      set((state) => {
        const revoking = { ...state.revoking };
        delete revoking[id];
        return { revoking };
      });
    }
  },

  setError: (error) => set({ error }),
}));
