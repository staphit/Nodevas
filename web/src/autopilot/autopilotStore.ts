import { create } from "zustand";
import {
  api,
  autopilotEnded,
  type AutopilotAction,
  type AutopilotPolicy,
  type AutopilotTaskAction,
  type AutopilotView,
  type HumanReviewRequest,
  type TaskInstance,
} from "../api";
import { nodeTypeErrorMessage } from "../nodeTypeErrors";

/** Where a task node stands in an autopilot run, for its board badge. */
export interface TaskMark {
  autopilotId: string;
  goalNodeId: string;
  state: string;
  stage: string;
}

/** A task a person must review, with the run it belongs to. */
export interface WaitingTask {
  autopilotId: string;
  goalNodeId: string;
  task: TaskInstance;
}

export interface AutopilotStoreState {
  /** Autopilot views held, by autopilot run id. */
  views: Record<string, AutopilotView>;
  /** Goal node → its latest autopilot run id, or null when it has none.
   * Absent until asked. */
  byGoal: Record<string, string | null>;
  /** Goals being loaded, and the last error per goal. */
  loading: Record<string, boolean>;
  errors: Record<string, string>;
  /** Task node → its mark in an active (running or paused) autopilot run. */
  marks: Record<string, TaskMark>;
  /** Tasks waiting for a person in the runs held. */
  waiting: WaitingTask[];
  /** "Autopilot" asked for on a node: the drawer showing it switches tab. */
  showRequest: { nodeId: string; nonce: number } | null;

  loadForGoal: (goalNodeId: string) => Promise<void>;
  /** Holds every live (running or paused) autopilot of the project, so a
   * view can show the ones started before the page loaded. Later
   * autopilot-changed pushes keep them current. */
  loadActive: () => Promise<void>;
  /** Whether loadActive was asked for since the last reset. */
  activeLoaded: boolean;
  refresh: (id: string) => Promise<void>;
  start: (goalNodeId: string, policy: AutopilotPolicy) => Promise<boolean>;
  act: (id: string, action: AutopilotAction) => Promise<boolean>;
  taskAct: (id: string, nodeId: string, action: AutopilotTaskAction) => Promise<boolean>;
  review: (id: string, pipelineId: string, body: HumanReviewRequest) => Promise<boolean>;
  /** A person's decision on a task held for its sensitive paths. */
  reviewHeld: (id: string, nodeId: string, body: HumanReviewRequest) => Promise<boolean>;
  /** autopilot-changed: the id is an autopilot run's. */
  onChanged: (id: string | undefined, goalNodeId?: string) => void;
  /** runs-changed: a task's run moved; active runs held refetch, coalesced. */
  onRunsChanged: () => void;
  showAutopilot: (nodeId: string) => void;
  clearShowRequest: () => void;
  setError: (goalNodeId: string, error: string) => void;
  /** A different project: nothing held belongs to it. */
  reset: () => void;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Realtime events come in bursts (a run's status, its PR, its CI): one
 * refetch per held run covers a burst. */
export const AUTOPILOT_EVENT_DELAY_MS = 600;
const pending = new Map<string, ReturnType<typeof setTimeout>>();
let runsTimer: ReturnType<typeof setTimeout> | null = null;

function clearTimers(): void {
  for (const timer of pending.values()) clearTimeout(timer);
  pending.clear();
  if (runsTimer) clearTimeout(runsTimer);
  runsTimer = null;
}

/** Marks and the waiting list, rebuilt from the views held. A task node in
 * several active runs keeps the most recently updated run's mark. */
export function deriveIndexes(views: Record<string, AutopilotView>): {
  marks: Record<string, TaskMark>;
  waiting: WaitingTask[];
} {
  const marks: Record<string, TaskMark> = {};
  const markAt: Record<string, string> = {};
  const waiting: WaitingTask[] = [];
  for (const view of Object.values(views)) {
    const run = view.run;
    if (autopilotEnded(run.status)) continue;
    for (const task of view.tasks ?? []) {
      if (!markAt[task.nodeId] || run.updatedAt > markAt[task.nodeId]) {
        markAt[task.nodeId] = run.updatedAt;
        marks[task.nodeId] = {
          autopilotId: run.id,
          goalNodeId: run.goalNodeId,
          state: task.state,
          stage: task.stage,
        };
      }
      if (task.state === "waiting_human" && task.pipelineId) {
        waiting.push({ autopilotId: run.id, goalNodeId: run.goalNodeId, task });
      }
    }
  }
  return { marks, waiting };
}

/** Bumped by reset so answers for the previous project are dropped. */
let generation = 0;

export const useAutopilot = create<AutopilotStoreState>((set, get) => {
  /** Holds a view and rebuilds the indexes. */
  const hold = (view: AutopilotView) => {
    set((state) => {
      const views = { ...state.views, [view.run.id]: view };
      // A goal's newest run wins its slot.
      const current = state.byGoal[view.run.goalNodeId];
      const held = current ? state.views[current] : undefined;
      const byGoal =
        !held || held.run.id === view.run.id || held.run.createdAt <= view.run.createdAt
          ? { ...state.byGoal, [view.run.goalNodeId]: view.run.id }
          : state.byGoal;
      return { views, byGoal, ...deriveIndexes(views) };
    });
  };

  return {
    views: {},
    byGoal: {},
    loading: {},
    errors: {},
    marks: {},
    waiting: [],
    showRequest: null,
    activeLoaded: false,

    loadForGoal: async (goalNodeId) => {
      const mine = generation;
      set((s) => ({ loading: { ...s.loading, [goalNodeId]: true } }));
      try {
        const view = await api.getAutopilotForGoal(goalNodeId);
        if (mine !== generation) return;
        if (view) {
          hold(view);
          // The run the hub answered for this goal is this goal's, whatever
          // node id it names (a goal renamed or moved keeps its run).
          set((s) => ({ byGoal: { ...s.byGoal, [goalNodeId]: view.run.id } }));
        } else {
          set((s) => ({ byGoal: { ...s.byGoal, [goalNodeId]: null } }));
        }
        set((s) => {
          const errors = { ...s.errors };
          delete errors[goalNodeId];
          return { errors };
        });
      } catch (error) {
        if (mine !== generation) return;
        set((s) => ({ errors: { ...s.errors, [goalNodeId]: message(error) } }));
      } finally {
        if (mine === generation) {
          set((s) => ({ loading: { ...s.loading, [goalNodeId]: false } }));
        }
      }
    },

    loadActive: async () => {
      const mine = generation;
      set({ activeLoaded: true });
      try {
        const views = await api.listActiveAutopilots();
        if (mine !== generation) return;
        for (const view of views) if (view?.run?.id) hold(view);
      } catch {
        // An older hub without the list: the views fall back to what the
        // board loaded goal by goal.
      }
    },

    refresh: async (id) => {
      const mine = generation;
      try {
        const view = await api.getAutopilot(id);
        if (mine !== generation) return;
        hold(view);
      } catch (error) {
        if (mine !== generation) return;
        const goal = get().views[id]?.run.goalNodeId;
        if (goal) set((s) => ({ errors: { ...s.errors, [goal]: message(error) } }));
      }
    },

    start: async (goalNodeId, policy) => {
      try {
        const run = await api.createAutopilot({ goalNodeId, policy });
        set((s) => {
          const errors = { ...s.errors };
          delete errors[goalNodeId];
          return { errors };
        });
        await get().refresh(run.id);
        // Without a view yet (the hub answered the run only), show the run.
        if (!get().views[run.id]) hold({ run, tasks: [] });
        return true;
      } catch (error) {
        const text = nodeTypeErrorMessage(error, goalNodeId) ?? message(error);
        set((s) => ({ errors: { ...s.errors, [goalNodeId]: text } }));
        return false;
      }
    },

    act: async (id, action) => {
      const goal = get().views[id]?.run.goalNodeId ?? "";
      try {
        await api.autopilotAction(id, action);
        await get().refresh(id);
        return true;
      } catch (error) {
        if (goal) set((s) => ({ errors: { ...s.errors, [goal]: message(error) } }));
        return false;
      }
    },

    taskAct: async (id, nodeId, action) => {
      const goal = get().views[id]?.run.goalNodeId ?? "";
      try {
        await api.autopilotTaskAction(id, nodeId, action);
        await get().refresh(id);
        return true;
      } catch (error) {
        if (goal) set((s) => ({ errors: { ...s.errors, [goal]: message(error) } }));
        return false;
      }
    },

    reviewHeld: async (id, nodeId, body) => {
      const goal = get().views[id]?.run.goalNodeId ?? "";
      try {
        await api.reviewAutopilotTask(id, nodeId, body);
        await get().refresh(id);
        return true;
      } catch (error) {
        if (goal) set((s) => ({ errors: { ...s.errors, [goal]: message(error) } }));
        return false;
      }
    },

    review: async (id, pipelineId, body) => {
      const goal = get().views[id]?.run.goalNodeId ?? "";
      try {
        await api.reviewPipeline(pipelineId, body);
        await get().refresh(id);
        return true;
      } catch (error) {
        if (goal) set((s) => ({ errors: { ...s.errors, [goal]: message(error) } }));
        return false;
      }
    },

    onChanged: (id, goalNodeId) => {
      if (!id) {
        if (goalNodeId && goalNodeId in get().byGoal) void get().loadForGoal(goalNodeId);
        return;
      }
      const old = pending.get(id);
      if (old) clearTimeout(old);
      pending.set(
        id,
        setTimeout(() => {
          pending.delete(id);
          // A run nobody holds yet is fetched too: it is how a board learns
          // of an autopilot started elsewhere, and its tasks get their badges.
          void get().refresh(id);
        }, AUTOPILOT_EVENT_DELAY_MS),
      );
    },

    onRunsChanged: () => {
      const active = Object.values(get().views).filter((view) => !autopilotEnded(view.run.status));
      if (active.length === 0) return;
      if (runsTimer) clearTimeout(runsTimer);
      runsTimer = setTimeout(() => {
        runsTimer = null;
        for (const view of Object.values(get().views)) {
          if (!autopilotEnded(view.run.status)) void get().refresh(view.run.id);
        }
      }, AUTOPILOT_EVENT_DELAY_MS);
    },

    showAutopilot: (nodeId) => set({ showRequest: { nodeId, nonce: Date.now() } }),
    clearShowRequest: () => set({ showRequest: null }),
    setError: (goalNodeId, error) =>
      set((s) => {
        const errors = { ...s.errors };
        if (error) errors[goalNodeId] = error;
        else delete errors[goalNodeId];
        return { errors };
      }),

    reset: () => {
      generation++;
      clearTimers();
      set({
        views: {},
        byGoal: {},
        loading: {},
        errors: {},
        marks: {},
        waiting: [],
        showRequest: null,
        activeLoaded: false,
      });
    },
  };
});
