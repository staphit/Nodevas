import { create } from "zustand";
import { api, runFinal, type CreateRunRequest, type Run, type RunEvent } from "../api";

/** Pages fetched per events load before yielding; the next push resumes. */
const MAX_EVENT_PAGES = 20;

export interface RunStoreState {
  /** A node's runs, newest first; present only once something asked. */
  runsByNode: Record<string, Run[]>;
  loadingNodes: Record<string, boolean>;
  nodeErrors: Record<string, string>;
  /** A run's events in seq order, without duplicates. */
  eventsByRun: Record<string, RunEvent[]>;
  eventErrors: Record<string, string>;
  /** Runs some view is showing live, with how many views are. Only these
   * follow run-events pushes. */
  watched: Record<string, number>;
  /** The run each node's run view has selected. */
  selectedRun: Record<string, string>;
  cancelling: Record<string, boolean>;
  cancelErrors: Record<string, string>;
  /** Runs whose cancel the hub accepted while they were still running. */
  stopRequested: Record<string, boolean>;
  /** The node the dispatch dialog is open for, or null. */
  dispatchNodeId: string | null;
  /** A request for the node's detail view to switch to its Runs tab. */
  showRunsRequest: { nodeId: string; nonce: number } | null;

  loadRuns: (nodeId: string) => Promise<void>;
  /** Reloads one run from GET /api/runs/:id. */
  refreshRun: (runId: string) => Promise<void>;
  /** Fetches the events after the last one held, page by page. */
  loadEvents: (runId: string) => Promise<void>;
  /** Merges events in, dropping any seq already held. */
  appendEvents: (runId: string, events: RunEvent[]) => void;
  /** Creates a run; throws the API error so the dialog can show it. */
  createRun: (body: CreateRunRequest) => Promise<Run>;
  cancelRun: (runId: string) => Promise<boolean>;
  /** Starts following a run live; returns the matching unwatch. */
  watchRun: (runId: string) => () => void;
  selectRun: (nodeId: string, runId: string | null) => void;
  openDispatch: (nodeId: string) => void;
  closeDispatch: () => void;
  /** Asks the node's detail view to show its runs, optionally one of them. */
  showRuns: (nodeId: string, runId?: string) => void;
  clearShowRunsRequest: () => void;

  /** runs-changed: the id is a node id. Refreshes it only if loaded. */
  onRunsChanged: (nodeId: string | undefined) => void;
  /** run-events: the id is a run id. Fetches new events if it is watched. */
  onRunEvents: (runId: string | undefined) => void;
  /** After a reconnect: whatever is on screen may have missed pushes. */
  resync: () => void;
  /** A different project: nothing held belongs to it. */
  reset: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Merges two seq-ordered event lists, keeping the first copy of each seq. */
export function mergeEvents(held: RunEvent[], incoming: RunEvent[]): RunEvent[] {
  if (incoming.length === 0) return held;
  const seen = new Set(held.map((event) => event.seq));
  const fresh = incoming.filter((event) => {
    if (typeof event.seq !== "number" || seen.has(event.seq)) return false;
    seen.add(event.seq);
    return true;
  });
  if (fresh.length === 0) return held;
  return [...held, ...fresh].sort((a, b) => a.seq - b.seq);
}

function lastSeq(events: RunEvent[] | undefined): number {
  return events && events.length > 0 ? events[events.length - 1].seq : 0;
}

/** Replaces a run wherever a node list holds it, or puts it on top. */
function upsertRun(runsByNode: Record<string, Run[]>, run: Run): Record<string, Run[]> {
  const list = runsByNode[run.nodeId] ?? [];
  const index = list.findIndex((item) => item.id === run.id);
  const next =
    index === -1
      ? [run, ...list]
      : list.map((item, i) => (i === index ? run : item));
  return { ...runsByNode, [run.nodeId]: next };
}

export function findRun(state: Pick<RunStoreState, "runsByNode">, runId: string): Run | undefined {
  for (const list of Object.values(state.runsByNode)) {
    const found = list.find((run) => run.id === runId);
    if (found) return found;
  }
  return undefined;
}

// One events fetch per run at a time. A push that lands mid-fetch marks the
// run dirty, and the fetch goes round once more instead of racing itself.
const eventsInFlight = new Map<string, Promise<void>>();
const eventsDirty = new Set<string>();
// Same for node lists, so a burst of runs-changed costs one or two requests.
const nodesInFlight = new Map<string, Promise<void>>();
const nodesDirty = new Set<string>();
// Bumped by reset(): a response for the previous project is dropped.
let generation = 0;

const initial = {
  runsByNode: {},
  loadingNodes: {},
  nodeErrors: {},
  eventsByRun: {},
  eventErrors: {},
  watched: {},
  selectedRun: {},
  cancelling: {},
  cancelErrors: {},
  stopRequested: {},
  dispatchNodeId: null,
  showRunsRequest: null,
} satisfies Partial<RunStoreState>;

export const useRuns = create<RunStoreState>((set, get) => ({
  ...initial,

  loadRuns: (nodeId) => {
    const running = nodesInFlight.get(nodeId);
    if (running) {
      nodesDirty.add(nodeId);
      return running;
    }
    const gen = generation;
    const work = (async () => {
      do {
        nodesDirty.delete(nodeId);
        set((state) => ({ loadingNodes: { ...state.loadingNodes, [nodeId]: true } }));
        try {
          const response = await api.listRuns(nodeId);
          if (gen !== generation) return;
          set((state) => {
            const nodeErrors = { ...state.nodeErrors };
            delete nodeErrors[nodeId];
            return {
              runsByNode: { ...state.runsByNode, [nodeId]: response.runs ?? [] },
              nodeErrors,
            };
          });
        } catch (error) {
          if (gen !== generation) return;
          set((state) => ({ nodeErrors: { ...state.nodeErrors, [nodeId]: message(error) } }));
        } finally {
          if (gen === generation) {
            set((state) => {
              const loadingNodes = { ...state.loadingNodes };
              delete loadingNodes[nodeId];
              return { loadingNodes };
            });
          }
        }
      } while (nodesDirty.has(nodeId) && gen === generation);
    })().finally(() => {
      if (nodesInFlight.get(nodeId) === work) nodesInFlight.delete(nodeId);
    });
    nodesInFlight.set(nodeId, work);
    return work;
  },

  refreshRun: async (runId) => {
    const gen = generation;
    try {
      const run = await api.getRun(runId);
      if (gen !== generation) return;
      set((state) => ({ runsByNode: upsertRun(state.runsByNode, run) }));
    } catch {
      // The list refresh reports failures; a single run's is not worth a
      // second message for the same outage.
    }
  },

  loadEvents: (runId) => {
    const running = eventsInFlight.get(runId);
    if (running) {
      eventsDirty.add(runId);
      return running;
    }
    const gen = generation;
    const work = (async () => {
      do {
        eventsDirty.delete(runId);
        try {
          for (let page = 0; page < MAX_EVENT_PAGES; page++) {
            const after = lastSeq(get().eventsByRun[runId]);
            const response = await api.getRunEvents(runId, after);
            if (gen !== generation) return;
            const events = response.events ?? [];
            get().appendEvents(runId, events);
            if (!response.more || events.length === 0) break;
          }
          set((state) => {
            if (!(runId in state.eventErrors)) return {};
            const eventErrors = { ...state.eventErrors };
            delete eventErrors[runId];
            return { eventErrors };
          });
        } catch (error) {
          if (gen !== generation) return;
          set((state) => ({ eventErrors: { ...state.eventErrors, [runId]: message(error) } }));
          return;
        }
      } while (eventsDirty.has(runId) && gen === generation);
    })().finally(() => {
      if (eventsInFlight.get(runId) === work) eventsInFlight.delete(runId);
    });
    eventsInFlight.set(runId, work);
    return work;
  },

  appendEvents: (runId, events) => {
    set((state) => {
      const held = state.eventsByRun[runId] ?? [];
      const merged = mergeEvents(held, events);
      if (merged === held && runId in state.eventsByRun) return {};
      return { eventsByRun: { ...state.eventsByRun, [runId]: merged } };
    });
  },

  createRun: async (body) => {
    const run = await api.createRun(body);
    set((state) => ({ runsByNode: upsertRun(state.runsByNode, run) }));
    return run;
  },

  cancelRun: async (runId) => {
    set((state) => {
      const cancelErrors = { ...state.cancelErrors };
      delete cancelErrors[runId];
      return { cancelling: { ...state.cancelling, [runId]: true }, cancelErrors };
    });
    try {
      const run = await api.cancelRun(runId);
      // A running run stays "running" until the runner reports it ended; the
      // view says the stop is on its way instead of offering cancel again.
      set((state) => ({
        runsByNode: upsertRun(state.runsByNode, run),
        stopRequested: runFinal(run.status)
          ? state.stopRequested
          : { ...state.stopRequested, [runId]: true },
      }));
      return true;
    } catch (error) {
      set((state) => ({ cancelErrors: { ...state.cancelErrors, [runId]: message(error) } }));
      return false;
    } finally {
      set((state) => {
        const cancelling = { ...state.cancelling };
        delete cancelling[runId];
        return { cancelling };
      });
    }
  },

  watchRun: (runId) => {
    set((state) => ({ watched: { ...state.watched, [runId]: (state.watched[runId] ?? 0) + 1 } }));
    void get().loadEvents(runId);
    let done = false;
    return () => {
      if (done) return;
      done = true;
      set((state) => {
        const watched = { ...state.watched };
        const count = (watched[runId] ?? 1) - 1;
        if (count > 0) watched[runId] = count;
        else delete watched[runId];
        return { watched };
      });
    };
  },

  selectRun: (nodeId, runId) =>
    set((state) => {
      const selectedRun = { ...state.selectedRun };
      if (runId) selectedRun[nodeId] = runId;
      else delete selectedRun[nodeId];
      return { selectedRun };
    }),

  openDispatch: (nodeId) => set({ dispatchNodeId: nodeId }),
  closeDispatch: () => set({ dispatchNodeId: null }),

  showRuns: (nodeId, runId) =>
    set((state) => ({
      showRunsRequest: { nodeId, nonce: (state.showRunsRequest?.nonce ?? 0) + 1 },
      selectedRun: runId ? { ...state.selectedRun, [nodeId]: runId } : state.selectedRun,
    })),
  clearShowRunsRequest: () => set({ showRunsRequest: null }),

  onRunsChanged: (nodeId) => {
    if (!nodeId || !(nodeId in get().runsByNode)) return;
    void get().loadRuns(nodeId);
  },

  onRunEvents: (runId) => {
    if (!runId || !get().watched[runId]) return;
    void get().loadEvents(runId);
  },

  resync: () => {
    const state = get();
    for (const nodeId of Object.keys(state.runsByNode)) void state.loadRuns(nodeId);
    for (const runId of Object.keys(state.watched)) {
      const run = findRun(state, runId);
      if (!run || !runFinal(run.status)) void state.loadEvents(runId);
    }
  },

  reset: () => {
    generation++;
    eventsInFlight.clear();
    eventsDirty.clear();
    nodesInFlight.clear();
    nodesDirty.clear();
    // Live views keep their watch counts; they re-register on remount.
    set({ ...initial, watched: get().watched });
  },
}));
