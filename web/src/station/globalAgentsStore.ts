import { create } from "zustand";

import {
  api,
  AuthError,
  GlobalRunError,
  runFinal,
  type CreateGlobalRunRequest,
  type Run,
  type RunnerLoad,
} from "../api";
import { translate } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";

/**
 * The viewer's global agents (agent workspace, phase 2): runs in one of
 * their runners' repositories for no project (GET /api/global-runs), and
 * their own runners' load. Loaded while Station is on screen (its Global
 * agents tab, and the war room's second office on either tab) and
 * refetched on global-runs-changed, coalesced. Every run loaded here is held
 * in the run store too (globalById), so the workspace panel, the timeline
 * and the diff find it like any other run.
 */
export interface GlobalAgentsState {
  /** Every unfinished global run, then the newest ended ones, newest first. */
  runs: Run[];
  runners: RunnerLoad[];
  /** Older ended runs exist on the hub ("Load older"). */
  more: boolean;
  loaded: boolean;
  loadingMore: boolean;
  /** Why the last load failed; "" when it did not. */
  error: string;
  /** Station is on screen: global-runs-changed refetches. */
  watching: boolean;
  /** The run the list highlights and scrolls to (a toast, the war room, a
   * start); null for none. */
  focus: string | null;
  /** A capacity change being saved, per runner. */
  saving: Record<string, boolean>;
  /** The outcome of a runner's last capacity change, said under its line. */
  settingNotes: Record<string, { kind: "saved" | "notSaved" | "error"; text: string }>;
  setWatching: (watching: boolean) => void;
  setFocus: (runId: string | null) => void;
  load: () => Promise<void>;
  /** Pages in older ended runs. */
  loadMore: () => Promise<void>;
  /** A global-runs-changed push: reload, coalesced; with nobody watching,
   * only a run some view holds is refreshed. */
  onChanged: (runId?: string) => void;
  /** A run that is not a global one moved somewhere (runs-changed,
   * building-changed): a node's run takes and frees a slot on the viewer's
   * runners too, so their load line reloads, coalesced, while watched. A
   * viewer with no runner of their own has no load line to keep current. */
  onRunnerLoadChanged: () => void;
  /** After a reconnect: pushes sent meanwhile are gone. */
  resync: () => void;
  /** Starts a global agent; throws the API error for the form to explain. */
  create: (body: CreateGlobalRunRequest) => Promise<Run>;
  /** Stops one; false (and the run store's cancelErrors) when refused. */
  cancel: (runId: string) => Promise<boolean>;
  /** Stops every active global run; the number stopped, or null on failure. */
  cancelAll: () => Promise<number | null>;
  /** The owner changes how many runs a runner takes at once. */
  setMaxConcurrent: (runnerId: string, value: number) => Promise<boolean>;
  cancellingAll: boolean;
  cancelAllError: string;
  reset: () => void;
}

/** Ended runs asked for per page. */
export const GLOBAL_PAGE = 50;
/** How long global-runs-changed pushes are gathered before one reload. */
export const GLOBAL_COALESCE_MS = 800;

let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight: Promise<void> | null = null;
let again = false;
// Bumped by reset(): an answer for a previous session is dropped.
let generation = 0;

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Unfinished global runs: the tab's count. */
export function activeGlobalRuns(runs: Run[]): Run[] {
  return runs.filter((run) => !runFinal(run.status));
}

/** Unfinished first, then newest first; one copy per id, the given first. */
export function orderGlobalRuns(runs: Run[]): Run[] {
  const seen = new Set<string>();
  const unique = runs.filter((run) => {
    if (!run?.id || seen.has(run.id)) return false;
    seen.add(run.id);
    return true;
  });
  return unique.sort(
    (a, b) =>
      Number(runFinal(a.status)) - Number(runFinal(b.status)) ||
      b.createdAt.localeCompare(a.createdAt) ||
      b.id.localeCompare(a.id),
  );
}

/** The oldest ended run's createdAt: where the next page starts. */
function oldestEnded(runs: Run[]): string {
  let oldest = "";
  for (const run of runs) {
    if (!runFinal(run.status)) continue;
    if (!oldest || run.createdAt < oldest) oldest = run.createdAt;
  }
  return oldest;
}

/** Why a capacity change was refused, in the reader's words. */
export function capacityErrorText(error: unknown, runner: string): string {
  const t = (key: string, values?: Record<string, string | number>) => translate(key, undefined, values);
  if (error instanceof GlobalRunError) {
    if (error.code === "INVALID_ARGUMENT" && error.max > 0) return t("globalAgents.capacity.ceiling", { max: error.max, runner });
    if (error.blocked === "settings_off") return t("globalAgents.capacity.settings_off", { runner });
    if (error.code === "RUNNER_OUTDATED" || error.status === 412) return t("globalAgents.capacity.outdated", { runner });
    if (error.code === "RUNNER_OFFLINE" || error.status === 503) return t("globalAgents.capacity.offline", { runner });
    if (error.code === "TIMEOUT" || error.status === 504) return t("globalAgents.capacity.timeout", { runner });
    return error.message;
  }
  if (error instanceof AuthError && error.status === 403) return error.message;
  return message(error);
}

const initial = {
  runs: [] as Run[],
  runners: [] as RunnerLoad[],
  more: false,
  loaded: false,
  loadingMore: false,
  error: "",
  focus: null as string | null,
  saving: {} as Record<string, boolean>,
  settingNotes: {} as GlobalAgentsState["settingNotes"],
  cancellingAll: false,
  cancelAllError: "",
};

export const useGlobalAgents = create<GlobalAgentsState>((set, get) => {
  /** Holds runs in this list and in the run store. */
  const hold = (runs: Run[]) => {
    if (runs.length === 0) return;
    useRuns.getState().putRuns(runs);
    set((state) => {
      const byId = new Map(runs.map((run) => [run.id, run]));
      const kept = state.runs.map((run) => byId.get(run.id) ?? run);
      const fresh = runs.filter((run) => !state.runs.some((held) => held.id === run.id));
      return { runs: orderGlobalRuns([...fresh, ...kept]) };
    });
  };

  /** One reload at the end of a fixed window from the first ask. */
  const reloadSoon = () => {
    if (timer) return;
    timer = setTimeout(() => {
      timer = null;
      void get().load();
    }, GLOBAL_COALESCE_MS);
  };

  return {
    ...initial,
    watching: false,

    setWatching: (watching) => {
      set({ watching });
      if (watching) void get().load();
      else if (timer) {
        clearTimeout(timer);
        timer = null;
      }
    },

    setFocus: (focus) => set({ focus }),

    load: () => {
      if (inFlight) {
        again = true;
        return inFlight;
      }
      const gen = generation;
      const work = (async () => {
        try {
          const res = await api.listGlobalRuns({ limit: GLOBAL_PAGE });
          if (gen !== generation) return;
          const runs = res.runs ?? [];
          useRuns.getState().putRuns(runs);
          set((state) => {
            // Older pages paged in stay; this page is the authority on its own.
            const fresh = new Set(runs.map((run) => run.id));
            const firstOldest = oldestEnded(runs);
            const older = state.runs.filter(
              (run) => !fresh.has(run.id) && runFinal(run.status) && firstOldest && run.createdAt < firstOldest,
            );
            return {
              runs: orderGlobalRuns([...runs, ...older]),
              runners: res.runners ?? [],
              more: older.length > 0 ? state.more : res.more,
              loaded: true,
              error: "",
            };
          });
        } catch (error) {
          if (gen !== generation) return;
          set({ loaded: true, error: message(error) });
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

    loadMore: async () => {
      const state = get();
      if (state.loadingMore || !state.more) return;
      const before = oldestEnded(state.runs);
      if (!before) return;
      const gen = generation;
      set({ loadingMore: true });
      try {
        const res = await api.listGlobalRuns({ limit: GLOBAL_PAGE, before });
        if (gen !== generation) return;
        const older = (res.runs ?? []).filter((run) => runFinal(run.status));
        useRuns.getState().putRuns(older);
        set((current) => ({
          runs: orderGlobalRuns([...current.runs, ...older]),
          more: res.more,
        }));
      } catch (error) {
        if (gen !== generation) return;
        set({ error: message(error) });
      } finally {
        if (gen === generation) set({ loadingMore: false });
      }
    },

    onChanged: (runId) => {
      if (!get().watching) {
        // An open workspace panel (a toast's) still follows its run.
        if (runId && useRuns.getState().globalById[runId]) void useRuns.getState().refreshRun(runId);
        return;
      }
      reloadSoon();
    },

    onRunnerLoadChanged: () => {
      const state = get();
      if (state.watching && state.runners.length > 0) reloadSoon();
    },

    resync: () => {
      if (get().watching) void get().load();
    },

    create: async (body) => {
      const run = await api.createGlobalRun(body);
      hold([run]);
      set({ focus: run.id });
      return run;
    },

    cancel: async (runId) => {
      const ok = await useRuns.getState().cancelRun(runId);
      const updated = useRuns.getState().globalById[runId];
      if (ok && updated) hold([updated]);
      return ok;
    },

    cancelAll: async () => {
      if (get().cancellingAll) return null;
      set({ cancellingAll: true, cancelAllError: "" });
      try {
        const res = await api.cancelAllGlobalRuns();
        const runs = res.runs ?? [];
        hold(runs);
        // A running agent stays "running" until its runner says it ended.
        useRuns.setState((state) => {
          const stopRequested = { ...state.stopRequested };
          for (const run of runs) if (!runFinal(run.status)) stopRequested[run.id] = true;
          return { stopRequested };
        });
        return res.cancelled;
      } catch (error) {
        set({ cancelAllError: message(error) });
        return null;
      } finally {
        set({ cancellingAll: false });
      }
    },

    setMaxConcurrent: async (runnerId, value) => {
      if (get().saving[runnerId]) return false;
      const load = get().runners.find((item) => item.runnerId === runnerId);
      const name = runnerDisplayName((key, values) => translate(key, undefined, values), load?.name) || runnerId;
      set((state) => {
        const settingNotes = { ...state.settingNotes };
        delete settingNotes[runnerId];
        return { saving: { ...state.saving, [runnerId]: true }, settingNotes };
      });
      try {
        const res = await api.setRunnerSettings(runnerId, { maxConcurrent: value });
        const now = res.runner?.info?.maxConcurrent || value;
        set((state) => ({
          runners: state.runners.map((item) => (item.runnerId === runnerId ? { ...item, maxConcurrent: now } : item)),
          settingNotes: {
            ...state.settingNotes,
            [runnerId]: res.persisted
              ? { kind: "saved", text: translate("globalAgents.capacity.saved", undefined, { runner: name, count: now }) }
              : { kind: "notSaved", text: translate("globalAgents.capacity.notSaved", undefined, { runner: name }) },
          },
        }));
        // The runner's new hello: the runner list (dispatch, Settings) catches up.
        void useRunners.getState().refreshIfLoaded();
        return true;
      } catch (error) {
        set((state) => ({
          settingNotes: { ...state.settingNotes, [runnerId]: { kind: "error", text: capacityErrorText(error, name) } },
        }));
        return false;
      } finally {
        set((state) => {
          const saving = { ...state.saving };
          delete saving[runnerId];
          return { saving };
        });
      }
    },

    reset: () => {
      generation++;
      if (timer) clearTimeout(timer);
      timer = null;
      inFlight = null;
      again = false;
      set({ ...initial, watching: false });
    },
  };
});
