// The glue between the app's stores and the pure world model: selects the
// slices buildWorld reads, loads what the board does not load at start
// (runners, the budget, live autopilots, the project's unfinished runs), and
// keeps a presence clock so sessions age and speech bubbles expire while the
// view stays open. Everything live comes through the stores' existing
// WebSocket handlers (App.tsx); this hook only reads.

import { useEffect, useMemo, useRef, useState } from "react";

import { analyzeGraph } from "../analysis";
import { runFinal, type Run } from "../api/runs";
import { useAgents } from "../agents/agentStore";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { buildWorld } from "./buildWorld";
import { BUBBLE_MS, msUntilNextTick, presenceTick } from "./presence";
import type { World } from "./world";

/** How many node run lists the view asks for at once on mount. */
const LOAD_CONCURRENCY = 4;

/**
 * Every run the stores hold, in the order buildWorld's dedupe wants: the
 * active list first, then the newest per node, then whole node lists (the
 * authority on their node). A run only ever moves toward a final state, so a
 * final copy is newer than any unfinished copy of the same run whatever list
 * it came from: finals go last and win.
 */
export function mergeRuns(
  active: Run[],
  latestByNode: Record<string, Run>,
  runsByNode: Record<string, Run[]>,
): Run[] {
  const all: Run[] = [...active, ...Object.values(latestByNode)];
  for (const list of Object.values(runsByNode)) all.push(...list);
  const open: Run[] = [];
  const done: Run[] = [];
  for (const run of all) {
    if (!run) continue;
    (runFinal(run.status) ? done : open).push(run);
  }
  return [...open, ...done];
}

/** Runs `work` over `items`, at most `limit` at a time. */
async function inBatches<T>(items: T[], limit: number, work: (item: T) => Promise<unknown>) {
  const queue = [...items];
  const workers = Array.from({ length: Math.min(limit, queue.length) }, async () => {
    for (let item = queue.shift(); item !== undefined; item = queue.shift()) {
      await work(item).catch(() => undefined);
    }
  });
  await Promise.all(workers);
}

/** The presence tick the clock is in, advanced on the tick boundaries. */
function usePresenceTick(): number {
  const [tick, setTick] = useState(() => presenceTick(Date.now()));
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      timer = setTimeout(() => {
        setTick(presenceTick(Date.now()));
        schedule();
      }, msUntilNextTick(Date.now()));
    };
    schedule();
    return () => clearTimeout(timer);
  }, []);
  return tick;
}

/** Loads what the game views need and the board does not load by itself. */
function useWorldLoads(activeProject: string, tick: number) {
  useEffect(() => {
    // refresh() also marks the list loaded, which is what lets
    // runners-changed keep it current from now on.
    void useRunners.getState().refresh();
    void useRuns.getState().loadProjectRuns();
  }, [activeProject]);

  // A project switch resets the run and autopilot stores (RunsRoot), which
  // may happen after this view mounted: whenever they come back unloaded,
  // ask again.
  const activeRunsLoaded = useRuns((s) => s.activeRunsLoaded);
  const autopilotsLoaded = useAutopilot((s) => s.activeLoaded);
  useEffect(() => {
    if (!activeRunsLoaded) void useRuns.getState().loadActiveRuns();
  }, [activeRunsLoaded, activeProject]);
  useEffect(() => {
    if (!autopilotsLoaded) void useAutopilot.getState().loadActive();
  }, [autopilotsLoaded, activeProject]);

  // The budget is not pushed: it is read again on every presence tick.
  useEffect(() => {
    void useBudget.getState().load();
  }, [activeProject, tick]);

  // A fallback run's seat swap needs the run it replaced, which has ended and
  // so is not in the active list; a meeting table needs its pipeline. Both
  // are asked for once per node or pipeline while the view is open.
  const activeRuns = useRuns((s) => s.activeRuns);
  const askedNodes = useRef(new Set<string>());
  const askedPipelines = useRef(new Set<string>());
  useEffect(() => {
    askedNodes.current.clear();
    askedPipelines.current.clear();
  }, [activeProject]);
  useEffect(() => {
    const runs = useRuns.getState();
    const nodes: string[] = [];
    const pipelines: string[] = [];
    for (const run of activeRuns) {
      if (run.retryOf && !(run.nodeId in runs.runsByNode) && !askedNodes.current.has(run.nodeId)) {
        askedNodes.current.add(run.nodeId);
        nodes.push(run.nodeId);
      }
      if (run.pipelineId && !runs.pipelines[run.pipelineId] && !askedPipelines.current.has(run.pipelineId)) {
        askedPipelines.current.add(run.pipelineId);
        pipelines.push(run.pipelineId);
      }
    }
    void inBatches(nodes, LOAD_CONCURRENCY, (nodeId) => useRuns.getState().loadRuns(nodeId));
    void inBatches(pipelines, LOAD_CONCURRENCY, (id) => useRuns.getState().loadPipeline(id));
  }, [activeRuns]);
}

/** The world the game views draw, rebuilt when a slice it reads changes,
 * on every presence tick, and when a speech bubble runs out. */
export function useWorld(): World {
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const runState = useApp((s) => s.runState);
  const activeProject = useApp((s) => s.activeProject);

  const activeRuns = useRuns((s) => s.activeRuns);
  const latestByNode = useRuns((s) => s.latestByNode);
  const runsByNode = useRuns((s) => s.runsByNode);
  const pipelines = useRuns((s) => s.pipelines);
  const runners = useRunners((s) => s.runners);
  const sessions = useAgents((s) => s.sessions);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const messages = useAgents((s) => s.messages);
  const views = useAutopilot((s) => s.views);
  const budget = useBudget((s) => s.status);

  const tick = usePresenceTick();
  // Bumped when the first visible speech bubble expires, between ticks.
  const [bubbleClock, setBubbleClock] = useState(0);
  useWorldLoads(activeProject, tick);

  const analysis = useMemo(() => analyzeGraph(graph, statuses), [graph, statuses]);
  const runs = useMemo(
    () => mergeRuns(activeRuns, latestByNode, runsByNode),
    [activeRuns, latestByNode, runsByNode],
  );
  const autopilots = useMemo(() => Object.values(views), [views]);

  const world = useMemo(
    () =>
      buildWorld({
        graph,
        statuses,
        analysis,
        history: runState?.history,
        projectName: activeProject,
        runs,
        runners,
        sessions,
        asks,
        held,
        messages,
        pipelines,
        autopilots,
        budget,
        // The real clock, not the tick: a message that arrived mid-tick must
        // show its bubble now, not at the next boundary.
        now: Date.now(),
      }),
    // tick and bubbleClock are the clock's inputs: they rebuild the world.
    [
      graph,
      statuses,
      analysis,
      runState,
      activeProject,
      runs,
      runners,
      sessions,
      asks,
      held,
      messages,
      pipelines,
      autopilots,
      budget,
      tick,
      bubbleClock,
    ],
  );

  // Wake when the first bubble runs out (bubbles last BUBBLE_MS, well under
  // a tick), so it does not hang on until the next tick.
  useEffect(() => {
    let first = Infinity;
    for (const actor of world.office.actors) {
      if (actor.bubble) first = Math.min(first, actor.bubble.createdAt + BUBBLE_MS);
    }
    if (!Number.isFinite(first)) return;
    const timer = setTimeout(
      () => setBubbleClock((n) => n + 1),
      Math.max(0, first - Date.now()) + 50,
    );
    return () => clearTimeout(timer);
  }, [world]);

  return world;
}
