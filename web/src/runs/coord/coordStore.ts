import { create } from "zustand";
import {
  api,
  RequestError,
  type BusyRepo,
  type BusyResponse,
  type ContestDetailResponse,
  type ContestResponse,
  type GlobalBusyResponse,
  type JudgeContestRequest,
  type PickActionResult,
  type PickContestRequest,
  type PickContestResponse,
  type RunOverlap,
} from "../../api";
import { useRuns } from "../runStore";

export interface NodeBusyParams {
  runnerId: string;
  repoId: string;
  nodeId?: string;
}

export function nodeBusyKey(params: NodeBusyParams): string {
  return `${params.runnerId}:${params.repoId}:${params.nodeId ?? ""}`;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export interface CoordStoreState {
  /** Preflight busy answers per `${runnerId}:${repoId}:${nodeId}`. */
  nodeBusy: Record<string, BusyResponse | undefined>;
  /** Preflight global busy answers per runnerId -> repoId -> BusyRepo. */
  globalBusy: Record<string, Record<string, BusyRepo>>;
  /** Active file overlaps per runId. */
  overlapsByRun: Record<string, RunOverlap[]>;
  overlapsLoading: Record<string, boolean>;
  /** Loaded contest detail responses per contestId. */
  contests: Record<string, ContestDetailResponse>;
  contestLoading: Record<string, boolean>;
  contestErrors: Record<string, string>;
  /** Latest pick action results per contestId (including refused discards). */
  pickActions: Record<string, PickActionResult[]>;
  picking: Record<string, boolean>;

  loadNodeBusy: (params: NodeBusyParams) => Promise<BusyResponse | null>;
  loadGlobalBusy: (runnerId: string, repoIds: string[]) => Promise<GlobalBusyResponse | null>;
  loadOverlaps: (runId: string) => Promise<RunOverlap[]>;
  refreshLoadedOverlaps: (runId?: string) => void;
  loadContest: (contestId: string) => Promise<ContestDetailResponse | null>;
  refreshLoadedContests: () => void;
  pickContest: (contestId: string, req: PickContestRequest) => Promise<PickContestResponse | null>;
  judgeContest: (contestId: string, req: JudgeContestRequest) => Promise<boolean>;
  cancelContest: (contestId: string) => Promise<ContestResponse | null>;
  reset: () => void;
}

const overlapInFlight = new Map<string, Promise<RunOverlap[]>>();
const contestInFlight = new Map<string, Promise<ContestDetailResponse | null>>();
let generation = 0;

export const useCoordStore = create<CoordStoreState>((set, get) => ({
  nodeBusy: {},
  globalBusy: {},
  overlapsByRun: {},
  overlapsLoading: {},
  contests: {},
  contestLoading: {},
  contestErrors: {},
  pickActions: {},
  picking: {},

  loadNodeBusy: async (params) => {
    if (!params.runnerId || !params.repoId) return null;
    const gen = generation;
    const key = nodeBusyKey(params);
    try {
      const res = await api.getRunBusy(params);
      if (gen !== generation) return null;
      set((state) => ({ nodeBusy: { ...state.nodeBusy, [key]: res } }));
      return res;
    } catch {
      return null;
    }
  },

  loadGlobalBusy: async (runnerId, repoIds) => {
    if (!runnerId || repoIds.length === 0) return null;
    const gen = generation;
    try {
      const res = await api.getGlobalRunBusy(runnerId, repoIds);
      if (gen !== generation) return null;
      const byRepo: Record<string, BusyRepo> = {};
      for (const repo of res.repos ?? []) {
        byRepo[repo.repoId] = repo;
      }
      set((state) => ({
        globalBusy: {
          ...state.globalBusy,
          [runnerId]: { ...(state.globalBusy[runnerId] ?? {}), ...byRepo },
        },
      }));
      return res;
    } catch {
      return null;
    }
  },

  loadOverlaps: (runId) => {
    if (!runId) return Promise.resolve([]);
    const existing = overlapInFlight.get(runId);
    if (existing) return existing;
    const gen = generation;
    const work = (async () => {
      set((state) => ({ overlapsLoading: { ...state.overlapsLoading, [runId]: true } }));
      try {
        const res = await api.getRunOverlaps(runId);
        const list = res.overlaps ?? [];
        if (gen === generation) {
          set((state) => ({ overlapsByRun: { ...state.overlapsByRun, [runId]: list } }));
        }
        return list;
      } catch (error) {
        if (gen === generation && error instanceof RequestError && (error.status === 404 || error.status === 501)) {
          set((state) => ({ overlapsByRun: { ...state.overlapsByRun, [runId]: [] } }));
        }
        return get().overlapsByRun[runId] ?? [];
      } finally {
        if (gen === generation) {
          set((state) => {
            const next = { ...state.overlapsLoading };
            delete next[runId];
            return { overlapsLoading: next };
          });
        }
      }
    })().finally(() => {
      if (overlapInFlight.get(runId) === work) overlapInFlight.delete(runId);
    });
    overlapInFlight.set(runId, work);
    return work;
  },

  refreshLoadedOverlaps: (runId) => {
    if (runId) {
      if (runId in get().overlapsByRun) void get().loadOverlaps(runId);
      return;
    }
    for (const id of Object.keys(get().overlapsByRun)) {
      void get().loadOverlaps(id);
    }
  },

  loadContest: (contestId) => {
    if (!contestId) return Promise.resolve(null);
    const existing = contestInFlight.get(contestId);
    if (existing) return existing;
    const gen = generation;
    const work = (async () => {
      set((state) => ({ contestLoading: { ...state.contestLoading, [contestId]: true } }));
      try {
        const res = await api.getContest(contestId);
        if (gen !== generation) return null;
        for (const run of res.runs ?? []) {
          if (run?.id) useRuns.getState().putRun(run);
        }
        set((state) => {
          const errors = { ...state.contestErrors };
          delete errors[contestId];
          return {
            contests: { ...state.contests, [contestId]: res },
            contestErrors: errors,
          };
        });
        return res;
      } catch (error) {
        if (gen === generation) {
          set((state) => ({
            contestErrors: { ...state.contestErrors, [contestId]: message(error) },
          }));
        }
        return null;
      } finally {
        if (gen === generation) {
          set((state) => {
            const loading = { ...state.contestLoading };
            delete loading[contestId];
            return { contestLoading: loading };
          });
        }
      }
    })().finally(() => {
      if (contestInFlight.get(contestId) === work) contestInFlight.delete(contestId);
    });
    contestInFlight.set(contestId, work);
    return work;
  },

  refreshLoadedContests: () => {
    for (const id of Object.keys(get().contests)) {
      void get().loadContest(id);
    }
  },

  pickContest: async (contestId, req) => {
    if (!contestId || get().picking[contestId]) return null;
    const gen = generation;
    set((state) => ({
      picking: { ...state.picking, [contestId]: true },
      contestErrors: (() => {
        const next = { ...state.contestErrors };
        delete next[contestId];
        return next;
      })(),
    }));
    try {
      const res = await api.pickContest(contestId, req);
      if (gen !== generation) return null;
      set((state) => {
        const prev = state.contests[contestId];
        return {
          contests: prev ? { ...state.contests, [contestId]: { ...prev, contest: res.contest } } : state.contests,
          pickActions: { ...state.pickActions, [contestId]: res.actions ?? [] },
        };
      });
      void get().loadContest(contestId);
      return res;
    } catch (error) {
      if (gen === generation) {
        set((state) => ({
          contestErrors: { ...state.contestErrors, [contestId]: message(error) },
        }));
      }
      return null;
    } finally {
      if (gen === generation) {
        set((state) => {
          const next = { ...state.picking };
          delete next[contestId];
          return { picking: next };
        });
      }
    }
  },

  judgeContest: async (contestId, req) => {
    if (!contestId) return false;
    const gen = generation;
    try {
      const res = await api.judgeContest(contestId, req);
      if (gen !== generation) return false;
      set((state) => {
        const prev = state.contests[contestId];
        return prev
          ? { contests: { ...state.contests, [contestId]: { ...prev, contest: res.contest } } }
          : {};
      });
      void get().loadContest(contestId);
      return true;
    } catch (error) {
      if (gen === generation) {
        set((state) => ({
          contestErrors: { ...state.contestErrors, [contestId]: message(error) },
        }));
      }
      return false;
    }
  },

  cancelContest: async (contestId) => {
    if (!contestId) return null;
    const gen = generation;
    try {
      const res = await api.cancelContest(contestId);
      if (gen !== generation) return null;
      for (const run of res.runs ?? []) {
        if (run?.id) useRuns.getState().putRun(run);
      }
      set((state) => {
        const prev = state.contests[contestId];
        return prev
          ? {
              contests: {
                ...state.contests,
                [contestId]: { ...prev, contest: res.contest, runs: res.runs ?? prev.runs },
              },
            }
          : {};
      });
      return res;
    } catch (error) {
      if (gen === generation) {
        set((state) => ({
          contestErrors: { ...state.contestErrors, [contestId]: message(error) },
        }));
      }
      return null;
    }
  },

  reset: () => {
    generation++;
    overlapInFlight.clear();
    contestInFlight.clear();
    set({
      nodeBusy: {},
      globalBusy: {},
      overlapsByRun: {},
      overlapsLoading: {},
      contests: {},
      contestLoading: {},
      contestErrors: {},
      pickActions: {},
      picking: {},
    });
  },
}));
