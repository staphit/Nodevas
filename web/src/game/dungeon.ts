// The boss map: the project as a boss whose HP is its unfinished work, each
// node a monster, each boolean logic gate a door, each live autopilot a party.
//
// Attackable is decided here, from analyzeGraph().blocked, never from the
// server's status: "ready" only means nobody has touched the node yet
// (internal/engine/ready.go), and an untouched node can still be blocked.

import { analyzeGraph, isSettledStatus, type GraphAnalysis } from "../analysis";
import type { AutopilotView } from "../api/autopilot";
import { runFinal, type Run } from "../api/runs";
import { logicGateRelation } from "../domain/graph/logicGate";
import { nodeTypeOf } from "../domain/graph/nodeTypes";
import type { Graph, GraphNode, HistoryEvent, Status } from "../types";
import type { Boss, Door, Dungeon, Monster, MonsterState, Party } from "./world";

/** Task states a party stands on. */
const PARTY_STATES: ReadonlySet<string> = new Set(["running", "waiting_review", "waiting_human"]);
/** Task states that have a run at work, or about to be. */
const ENGAGED_TASK_STATES: ReadonlySet<string> = new Set([
  "queued",
  "running",
  "waiting_review",
  "waiting_human",
]);
const LIVE_AUTOPILOT: ReadonlySet<string> = new Set(["running", "paused"]);

export interface DungeonInputs {
  graph: Graph | null | undefined;
  statuses: Record<string, Status>;
  /** analyzeGraph(graph, statuses), when the caller already has it. */
  analysis?: GraphAnalysis;
  /** Runs in view, any source; used for "engaged" and merged PRs. */
  runs: Run[];
  autopilots: AutopilotView[];
  /** runState.history, for XP. */
  history?: HistoryEvent[];
  projectName: string;
  now: number;
}

/**
 * Whether a node counts toward the boss's HP and is drawn as a monster: its
 * type counts in progress. Start, End and Note mark or annotate the flow
 * rather than hold work, and neither does a custom type that says so.
 */
export function countsAsWork(
  node: Pick<GraphNode, "kind">,
  graph?: Graph | null,
): boolean {
  return nodeTypeOf(graph, node).countsInProgress;
}

export function monsterState(
  nodeId: string,
  status: Status,
  analysis: Pick<GraphAnalysis, "blocked">,
  graph: Graph | null | undefined,
): MonsterState {
  if (isSettledStatus(status, graph?.ui?.customStatuses ?? [])) return "defeated";
  return analysis.blocked.has(nodeId) ? "locked" : "attackable";
}

export function bossOf(monsters: Pick<Monster, "state">[], name: string): Boss {
  const maxHp = monsters.length;
  const hp = monsters.filter((monster) => monster.state !== "defeated").length;
  return { name, hp, maxHp, ratio: maxHp ? hp / maxHp : 0, defeated: maxHp > 0 && hp === 0 };
}

/** Completions in the lifecycle history: every event that reached done. */
export function xpOf(history: HistoryEvent[] | undefined): number {
  return (history ?? []).filter((event) => event.to === "done").length;
}

export function partiesOf(autopilots: AutopilotView[]): Party[] {
  const parties: Party[] = [];
  for (const view of autopilots) {
    if (!view?.run || !LIVE_AUTOPILOT.has(view.run.status)) continue;
    const tasks = view.tasks ?? [];
    const counts = view.run.counts;
    parties.push({
      autopilotId: view.run.id,
      goalNodeId: view.run.goalNodeId,
      status: view.run.status,
      positions: tasks.filter((task) => PARTY_STATES.has(task.state)).map((task) => task.nodeId),
      waitingHuman: tasks.filter((task) => task.state === "waiting_human").map((task) => task.nodeId),
      total: counts?.total || tasks.length,
      done:
        (counts?.success ?? 0) + (counts?.skipped ?? 0) ||
        tasks.filter((task) => task.state === "success" || task.state === "skipped").length,
      failed: counts?.failed ?? tasks.filter((task) => task.state === "failed").length,
      target: { kind: "autopilot", autopilotId: view.run.id, goalNodeId: view.run.goalNodeId },
    });
  }
  return parties;
}

export function buildDungeon(inputs: DungeonInputs): Dungeon {
  const { graph, statuses } = inputs;
  const analysis = inputs.analysis ?? analyzeGraph(graph, statuses, new Date(inputs.now));
  const nodes = graph?.nodes ?? [];
  const known = new Set(nodes.map((node) => node.id));
  const settled = (id: string) =>
    isSettledStatus(statuses[id] ?? "ready", graph?.ui?.customStatuses ?? []);

  const engaged = new Set<string>();
  const merged = new Set<string>();
  for (const run of inputs.runs) {
    if (!run?.nodeId) continue;
    if (!runFinal(run.status)) engaged.add(run.nodeId);
    if (run.prUrl && run.prState === "merged") merged.add(run.nodeId);
  }
  for (const view of inputs.autopilots) {
    if (!view?.run || !LIVE_AUTOPILOT.has(view.run.status)) continue;
    for (const task of view.tasks ?? []) {
      if (ENGAGED_TASK_STATES.has(task.state)) engaged.add(task.nodeId);
    }
  }

  const monsters: Monster[] = nodes.filter((node) => countsAsWork(node, graph)).map((node) => {
    const status = statuses[node.id] ?? "ready";
    const state = monsterState(node.id, status, analysis, graph);
    return {
      nodeId: node.id,
      title: node.title ?? "",
      kind: node.kind ?? "",
      agentMode: nodeTypeOf(graph, node).agentMode,
      priority: node.priority ?? "",
      status,
      state,
      blockers: state === "locked" ? [...(analysis.blocked.get(node.id) ?? [])] : [],
      entry: analysis.entryNodeIds.has(node.id),
      critical: analysis.criticalNodeIds.has(node.id),
      faded: analysis.deprecatedNodeIds.has(node.id),
      engaged: engaged.has(node.id),
      merged: merged.has(node.id),
      target: { kind: "node", nodeId: node.id },
    };
  });

  const doors: Door[] = [];
  for (const gate of graph?.ui?.logicGates ?? []) {
    // Relation gates say what their wires mean; they hold nothing back.
    if (logicGateRelation(gate.operator) || !gate.output || !known.has(gate.output)) continue;
    const output = gate.output;
    const open = settled(output) || !analysis.blocked.has(output);
    const blockers = open ? [] : [...(analysis.blocked.get(output) ?? [])];
    const opener = blockers.find((id) => known.has(id));
    doors.push({
      gateId: gate.id,
      operator: gate.operator,
      inputs: [...gate.inputs],
      output,
      open,
      blockers,
      target: { kind: "node", nodeId: open || !opener ? output : opener },
    });
  }

  return {
    boss: bossOf(monsters, inputs.projectName),
    monsters,
    doors,
    parties: partiesOf(inputs.autopilots),
    xp: xpOf(inputs.history),
    mergedNodeIds: monsters.filter((monster) => monster.merged).map((monster) => monster.nodeId),
  };
}
