import { create } from "zustand";
import {
  api,
  AuthError,
  ConflictError,
  pipelineEnded,
  runFinal,
  type CreateRunRequest,
  type PipelineState,
  type Run,
  type RunChanges,
  type RunEvent,
} from "../api";

/** Pages fetched per events load before yielding; the next push resumes. */
const MAX_EVENT_PAGES = 20;

/** A run's changes as fetched, with the commit they were fetched for: a new
 * commit on the run means the held copy is stale. */
export interface HeldChanges {
  commit: string;
  changes: RunChanges | null;
}

export interface RunStoreState {
  /** A node's runs, newest first; present only once something asked. */
  runsByNode: Record<string, Run[]>;
  /** The newest run known for each node, from node lists and the project's
   * recent-runs list. Feeds the graph cards' run badges. */
  latestByNode: Record<string, Run>;
  /** Whether the project's recent runs were asked for (once per project). */
  projectRunsLoaded: boolean;
  /** A run's changes, fetched only when someone opens its diff. */
  changesByRun: Record<string, HeldChanges>;
  changesLoading: Record<string, boolean>;
  changesErrors: Record<string, string>;
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
  /** Runs whose merge approval is being posted. */
  merging: Record<string, boolean>;
  /** Why the hub refused a run's merge approval. */
  mergeErrors: Record<string, MergeRefusal>;
  /** Pipelines by id (W5), fetched when a run view shows one of their runs
   * and refreshed by runs-changed for their node until they end. */
  pipelines: Record<string, PipelineState>;
  pipelineErrors: Record<string, string>;
  /** The node the dispatch dialog is open for, or null. */
  dispatchNodeId: string | null;
  /** A request for the node's detail view to switch to its Runs tab. */
  showRunsRequest: { nodeId: string; nonce: number } | null;

  loadRuns: (nodeId: string) => Promise<void>;
  /** One request for the project's most recent runs, for the graph badges.
   * Repeated calls are free until the next reset (or a forced reload). */
  loadProjectRuns: (force?: boolean) => Promise<void>;
  /** Fetches a run's changes unless they are held for this commit already. */
  loadChanges: (runId: string, commit: string | undefined) => Promise<void>;
  /** Reloads one run from GET /api/runs/:id. */
  refreshRun: (runId: string) => Promise<void>;
  /** Fetches a pipeline unless it is held already (or `force`). */
  loadPipeline: (id: string, force?: boolean) => Promise<void>;
  /** Holds a pipeline fetched elsewhere (the run-notify handler). */
  putPipeline: (pipeline: PipelineState) => void;
  /** Fetches the events after the last one held, page by page. */
  loadEvents: (runId: string) => Promise<void>;
  /** Merges events in, dropping any seq already held. */
  appendEvents: (runId: string, events: RunEvent[]) => void;
  /** Creates a run; throws the API error so the dialog can show it. */
  createRun: (body: CreateRunRequest) => Promise<Run>;
  cancelRun: (runId: string) => Promise<boolean>;
  /** Approves merging a run's PR at the head the approver saw. */
  mergeRun: (runId: string, headSha: string) => Promise<boolean>;
  clearMergeError: (runId: string) => void;
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

/** A refused merge approval: "denied" is a 403 (an agent session may not
 * approve merges), "precondition" a 409 with the hub's reason (CI not green,
 * the PR changed since, no open PR, a merge already pending). */
export interface MergeRefusal {
  kind: "denied" | "precondition" | "other";
  message: string;
}

export function mergeRefusal(error: unknown): MergeRefusal {
  if (error instanceof ConflictError) {
    return { kind: "precondition", message: error.serverMessage };
  }
  if (error instanceof AuthError && error.status === 403) {
    return { kind: "denied", message: error.message };
  }
  return { kind: "other", message: message(error) };
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

function newer(a: Run, b: Run | undefined): boolean {
  if (!b) return true;
  if (a.id === b.id) return true;
  return a.createdAt > b.createdAt || (a.createdAt === b.createdAt && a.id > b.id);
}

/** Folds runs into the newest-per-node map; a run already held is replaced. */
export function foldLatest(latest: Record<string, Run>, runs: Run[]): Record<string, Run> {
  let next = latest;
  for (const run of runs) {
    if (!run?.nodeId) continue;
    if (!newer(run, next[run.nodeId])) continue;
    if (next === latest) next = { ...latest };
    next[run.nodeId] = run;
  }
  return next;
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
// One changes fetch per run at a time.
const changesInFlight = new Map<string, Promise<void>>();
// One pipeline fetch at a time, going round once more if a push lands mid-way.
const pipelinesInFlight = new Map<string, Promise<void>>();
const pipelinesDirty = new Set<string>();
let projectRunsInFlight: Promise<void> | null = null;
// Bumped by reset(): a response for the previous project is dropped.
let generation = 0;

const initial = {
  runsByNode: {},
  latestByNode: {},
  projectRunsLoaded: false,
  changesByRun: {},
  changesLoading: {},
  changesErrors: {},
  loadingNodes: {},
  nodeErrors: {},
  eventsByRun: {},
  eventErrors: {},
  watched: {},
  selectedRun: {},
  cancelling: {},
  cancelErrors: {},
  stopRequested: {},
  merging: {},
  mergeErrors: {},
  pipelines: {},
  pipelineErrors: {},
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
            const runs = response.runs ?? [];
            // The node's own list is the authority on its newest run.
            const latestByNode = { ...state.latestByNode };
            delete latestByNode[nodeId];
            return {
              runsByNode: { ...state.runsByNode, [nodeId]: runs },
              latestByNode: foldLatest(latestByNode, runs),
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

  loadProjectRuns: (force = false) => {
    if (projectRunsInFlight) return projectRunsInFlight;
    if (get().projectRunsLoaded && !force) return Promise.resolve();
    const gen = generation;
    set({ projectRunsLoaded: true });
    const work = (async () => {
      try {
        const response = await api.listProjectRuns();
        if (gen !== generation) return;
        const runs = response?.runs ?? [];
        set((state) => ({
          // A node whose list is loaded already knows better.
          latestByNode: foldLatest(
            state.latestByNode,
            runs.filter((run) => !(run.nodeId in state.runsByNode)),
          ),
        }));
      } catch {
        // Badges are a convenience: a hub without runs (or a viewer who may
        // not list them) simply shows none.
      }
    })().finally(() => {
      if (projectRunsInFlight === work) projectRunsInFlight = null;
    });
    projectRunsInFlight = work;
    return work;
  },

  loadChanges: (runId, commit) => {
    const key = commit ?? "";
    const held = get().changesByRun[runId];
    if (held && held.commit === key) return Promise.resolve();
    const running = changesInFlight.get(runId);
    if (running) return running;
    const gen = generation;
    set((state) => {
      const changesErrors = { ...state.changesErrors };
      delete changesErrors[runId];
      return { changesLoading: { ...state.changesLoading, [runId]: true }, changesErrors };
    });
    const work = (async () => {
      try {
        const response = await api.getRunChanges(runId);
        if (gen !== generation) return;
        const changes = response?.changes ?? null;
        set((state) => ({
          changesByRun: {
            ...state.changesByRun,
            // Held under the commit the response describes, so a run row
            // that is a push behind does not fetch a second time.
            [runId]: { commit: changes?.commit || key, changes },
          },
        }));
      } catch (error) {
        if (gen !== generation) return;
        set((state) => ({ changesErrors: { ...state.changesErrors, [runId]: message(error) } }));
      } finally {
        if (gen === generation) {
          set((state) => {
            const changesLoading = { ...state.changesLoading };
            delete changesLoading[runId];
            return { changesLoading };
          });
        }
      }
    })().finally(() => {
      if (changesInFlight.get(runId) === work) changesInFlight.delete(runId);
    });
    changesInFlight.set(runId, work);
    return work;
  },

  refreshRun: async (runId) => {
    const gen = generation;
    try {
      const run = await api.getRun(runId);
      if (gen !== generation) return;
      set((state) => ({
        runsByNode: upsertRun(state.runsByNode, run),
        latestByNode: foldLatest(state.latestByNode, [run]),
      }));
    } catch {
      // The list refresh reports failures; a single run's is not worth a
      // second message for the same outage.
    }
  },

  loadPipeline: (id, force = false) => {
    if (!id) return Promise.resolve();
    const running = pipelinesInFlight.get(id);
    if (running) {
      if (force) pipelinesDirty.add(id);
      return running;
    }
    if (!force && id in get().pipelines) return Promise.resolve();
    const gen = generation;
    const work = (async () => {
      do {
        pipelinesDirty.delete(id);
        try {
          const pipeline = await api.getPipeline(id);
          if (gen !== generation) return;
          if (pipeline?.id) get().putPipeline(pipeline);
        } catch (error) {
          if (gen !== generation) return;
          set((state) => ({ pipelineErrors: { ...state.pipelineErrors, [id]: message(error) } }));
          return;
        }
      } while (pipelinesDirty.has(id) && gen === generation);
    })().finally(() => {
      if (pipelinesInFlight.get(id) === work) pipelinesInFlight.delete(id);
    });
    pipelinesInFlight.set(id, work);
    return work;
  },

  putPipeline: (pipeline) =>
    set((state) => {
      const pipelineErrors = { ...state.pipelineErrors };
      delete pipelineErrors[pipeline.id];
      return { pipelines: { ...state.pipelines, [pipeline.id]: pipeline }, pipelineErrors };
    }),

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
    set((state) => ({
      runsByNode: upsertRun(state.runsByNode, run),
      latestByNode: foldLatest(state.latestByNode, [run]),
    }));
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
        latestByNode: foldLatest(state.latestByNode, [run]),
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

  mergeRun: async (runId, headSha) => {
    if (get().merging[runId]) return false;
    get().clearMergeError(runId);
    set((state) => ({ merging: { ...state.merging, [runId]: true } }));
    const gen = generation;
    try {
      const run = await api.mergeRun(runId, headSha);
      if (gen !== generation) return true;
      if (run?.id) {
        set((state) => ({
          runsByNode: upsertRun(state.runsByNode, run),
          latestByNode: foldLatest(state.latestByNode, [run]),
        }));
      }
      return true;
    } catch (error) {
      if (gen !== generation) return false;
      set((state) => ({ mergeErrors: { ...state.mergeErrors, [runId]: mergeRefusal(error) } }));
      // A 409 often means the run moved on (a new head, a merge already
      // pending): show what the hub knows now.
      if (error instanceof ConflictError) void get().refreshRun(runId);
      return false;
    } finally {
      set((state) => {
        const merging = { ...state.merging };
        delete merging[runId];
        return { merging };
      });
    }
  },

  clearMergeError: (runId) =>
    set((state) => {
      if (!(runId in state.mergeErrors)) return {};
      const mergeErrors = { ...state.mergeErrors };
      delete mergeErrors[runId];
      return { mergeErrors };
    }),

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
    if (!nodeId) return;
    const state = get();
    // A pipeline's next step or its end arrives as a change on its node.
    for (const pipeline of Object.values(state.pipelines)) {
      if (pipeline.nodeId === nodeId && !pipelineEnded(pipeline.status)) {
        void state.loadPipeline(pipeline.id, true);
      }
    }
    // Once the board asked for the project's runs, every node has a card
    // that may show a badge: a node nobody opened gets its list loaded here,
    // so a PR or CI change long after its run finished reaches the badge.
    if (!(nodeId in state.runsByNode) && !state.projectRunsLoaded) return;
    void state.loadRuns(nodeId);
  },

  onRunEvents: (runId) => {
    if (!runId || !get().watched[runId]) return;
    void get().loadEvents(runId);
  },

  resync: () => {
    const state = get();
    for (const nodeId of Object.keys(state.runsByNode)) void state.loadRuns(nodeId);
    if (state.projectRunsLoaded) void state.loadProjectRuns(true);
    for (const pipeline of Object.values(state.pipelines)) {
      if (!pipelineEnded(pipeline.status)) void state.loadPipeline(pipeline.id, true);
    }
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
    changesInFlight.clear();
    pipelinesInFlight.clear();
    pipelinesDirty.clear();
    projectRunsInFlight = null;

    // Live views keep their watch counts; they re-register on remount.
    set({ ...initial, watched: get().watched });
  },
}));
