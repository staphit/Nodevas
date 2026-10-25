import { create } from "zustand";
import { api, RequestError, type SessionChange } from "../api";

/**
 * External sessions' changes per node (W6): agents the owner ran in their own
 * terminal, which reported through `nodevas hook` what they touched while
 * holding the node. Fetched when a node's Runs tab shows them and refreshed
 * by "session-changes" pushes for that node.
 */
export interface SessionStoreState {
  /** Newest first; present only once something asked for the node. */
  byNode: Record<string, SessionChange[]>;
  loading: Record<string, boolean>;
  errors: Record<string, string>;

  load: (nodeId: string) => Promise<void>;
  /** session-changes: the id is a node id. Refreshes it only if loaded. */
  onChanged: (nodeId: string | undefined) => void;
  /** After a reconnect: whatever is on screen may have missed pushes. */
  resync: () => void;
  /** A different project: nothing held belongs to it. */
  reset: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function newestFirst(list: SessionChange[]): SessionChange[] {
  return [...list].sort((a, b) => (b.updatedAt || "").localeCompare(a.updatedAt || ""));
}

// One fetch per node at a time, going round once more if a push lands mid-way.
const inFlight = new Map<string, Promise<void>>();
const dirty = new Set<string>();
let generation = 0;

export const useSessions = create<SessionStoreState>((set, get) => ({
  byNode: {},
  loading: {},
  errors: {},

  load: (nodeId) => {
    const running = inFlight.get(nodeId);
    if (running) {
      dirty.add(nodeId);
      return running;
    }
    const gen = generation;
    const work = (async () => {
      do {
        dirty.delete(nodeId);
        set((state) => ({ loading: { ...state.loading, [nodeId]: true } }));
        try {
          const response = await api.getSessionChanges(nodeId);
          if (gen !== generation) return;
          set((state) => {
            const errors = { ...state.errors };
            delete errors[nodeId];
            return {
              byNode: { ...state.byNode, [nodeId]: newestFirst(response.changes ?? []) },
              errors,
            };
          });
        } catch (error) {
          if (gen !== generation) return;
          // A hub from before W6 has no such route: nothing to show, not an
          // error to put in every node's Runs tab.
          if (error instanceof RequestError && error.status === 404) {
            set((state) => ({ byNode: { ...state.byNode, [nodeId]: [] } }));
            continue;
          }
          set((state) => ({ errors: { ...state.errors, [nodeId]: message(error) } }));
        } finally {
          if (gen === generation) {
            set((state) => {
              const loading = { ...state.loading };
              delete loading[nodeId];
              return { loading };
            });
          }
        }
      } while (dirty.has(nodeId) && gen === generation);
    })().finally(() => {
      if (inFlight.get(nodeId) === work) inFlight.delete(nodeId);
    });
    inFlight.set(nodeId, work);
    return work;
  },

  onChanged: (nodeId) => {
    if (!nodeId || !(nodeId in get().byNode || nodeId in get().errors)) return;
    void get().load(nodeId);
  },

  resync: () => {
    for (const nodeId of Object.keys(get().byNode)) void get().load(nodeId);
  },

  reset: () => {
    generation++;
    inFlight.clear();
    dirty.clear();
    set({ byNode: {}, loading: {}, errors: {} });
  },
}));
