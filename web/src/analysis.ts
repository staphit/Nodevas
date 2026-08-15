import { edgeBlocks, edgeRelation } from "./domain/graph/edgeStyle";
import {
  logicGateIsComplete,
  logicGateRelation,
} from "./domain/graph/logicGate";
import { evaluateRequires } from "./domain/graph/requires";
import type {
  Graph,
  GraphNode,
  LogicGate,
  PlanMilestone,
  Status,
  StatusDefinition,
} from "./types";

export interface DependencyViolation {
  nodeId: string;
  sourceIds: string[];
  kind: "actual" | "schedule";
  message: string;
}

export interface GraphAnalysis {
  overdue: Map<string, string[]>;
  violations: DependencyViolation[];
  blocked: Map<string, string[]>;
  blocking: Map<string, string[]>;
  criticalNodeIds: Set<string>;
  criticalEdgeKeys: Set<string>;
  criticalPath: string[];
  criticalDays: number;
  hasCycle: boolean;
  /**
   * Nodes nothing points at: the graph's entry points. Every relation counts
   * as incoming — optional and deprecated wires are drawn, so a node at the
   * end of one is not where a reader starts. `ui.entryOverrides` has the last
   * word: an isolated node is not always a beginning, and the author is the
   * one who knows which.
   */
  entryNodeIds: Set<string>;
  /**
   * Nodes only reachable through deprecated wires. They are drawn faded: the
   * graph still records them, but nothing live leads there any more.
   */
  deprecatedNodeIds: Set<string>;
}

const builtinSettled = new Set<Status>(["done", "skipped", "deprecated"]);
const active = new Set<Status>(["started", "in_progress", "done", "failed"]);

/**
 * Reports whether a status counts as finished, meaning a node in it satisfies
 * whatever depends on it instead of blocking it. `done` and `skipped` always
 * do — and so does `deprecated`, which is work that was dropped rather than
 * finished. A project-defined state does when it is marked settled.
 */
export function isSettledStatus(
  status: Status,
  definitions: StatusDefinition[] = [],
): boolean {
  if (builtinSettled.has(status)) return true;
  return definitions.some((definition) => definition.id === status && definition.settled);
}

function localDay(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(
    date.getDate(),
  ).padStart(2, "0")}`;
}

function milestone(plans: PlanMilestone[] | undefined, status: string): string | undefined {
  return plans?.find((plan) => plan.status === status)?.date;
}

function gateSatisfied(gate: LogicGate, done: Set<string>): boolean {
  const completed = gate.inputs.filter((id) => done.has(id)).length;
  switch (gate.operator) {
    case "must":
      return gate.inputs.length === 1 && completed === 1;
    case "and":
      return gate.inputs.length >= 2 && completed === gate.inputs.length;
    case "or":
      return gate.inputs.length >= 2 && completed >= 1;
    case "xor":
      return gate.inputs.length >= 2 && completed === 1;
    case "nand":
      return gate.inputs.length >= 2 && completed < gate.inputs.length;
    case "nor":
      return gate.inputs.length >= 2 && completed === 0;
    // Relation gates hold nothing back; they are filtered out before this.
    case "optional":
    case "deprecated":
      return true;
  }
}

// A false gate explains itself using node ids only. Waiting-style gates point
// at unfinished inputs; negative gates point at the completed inputs that make
// their condition false. An unwired gate has no input ids, so callers must use
// gateSatisfied — not array length — to decide whether it blocks.
function gateBlockers(gate: LogicGate, done: Set<string>): string[] {
  if (gateSatisfied(gate, done)) return [];

  const inputs = [...new Set(gate.inputs)];
  const completed = inputs.filter((id) => done.has(id));
  const pending = inputs.filter((id) => !done.has(id));
  switch (gate.operator) {
    case "xor":
      return completed.length > 1 ? completed : pending.length ? pending : inputs;
    case "nand":
    case "nor":
      return completed.length ? completed : pending.length ? pending : inputs;
    case "must":
    case "and":
    case "or":
      return pending.length ? pending : inputs;
    case "optional":
    case "deprecated":
      return [];
  }
}

function nodeDuration(node: GraphNode, graph: Graph): number {
  const plans = graph.ui?.plans?.[node.id];
  const start = milestone(plans, "started");
  const done = milestone(plans, "done");
  if (!start || !done) return 1;
  const startMs = new Date(`${start}T00:00:00`).getTime();
  const doneMs = new Date(`${done}T00:00:00`).getTime();
  if (!Number.isFinite(startMs) || !Number.isFinite(doneMs) || doneMs < startMs) return 1;
  return Math.max(1, Math.round((doneMs - startMs) / 86_400_000) + 1);
}

export function analyzeGraph(
  graph: Graph | null | undefined,
  statuses: Record<string, Status>,
  now = new Date(),
  runtimeFlags: Record<string, unknown> = {},
): GraphAnalysis {
  const result: GraphAnalysis = {
    overdue: new Map(),
    violations: [],
    blocked: new Map(),
    blocking: new Map(),
    criticalNodeIds: new Set(),
    criticalEdgeKeys: new Set(),
    criticalPath: [],
    criticalDays: 0,
    hasCycle: false,
    entryNodeIds: new Set(),
    deprecatedNodeIds: new Set(),
  };
  if (!graph) return result;

  const nodes = graph.nodes ?? [];
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const statusDefinitions = graph.ui?.customStatuses ?? [];
  const settled = (status: Status) => isSettledStatus(status, statusDefinitions);
  const requiredEdges = (graph.edges ?? []).filter(
    (edge) => edgeBlocks(edge) && byId.has(edge.from) && byId.has(edge.to),
  );
  const done = new Set(
    nodes.filter((node) => settled(statuses[node.id] ?? "ready")).map((node) => node.id),
  );
  const effectiveFlags = { ...(graph.flags ?? {}), ...runtimeFlags };
  const today = localDay(now);

  // Any incoming wire disqualifies a node as an entry point, deprecated ones
  // included: something visibly points at it, so calling it a starting point
  // would contradict the picture.
  const pointedAt = new Set(
    (graph.edges ?? [])
      .filter((edge) => byId.has(edge.from) && byId.has(edge.to))
      .map((edge) => edge.to),
  );
  const entryOverrides = graph.ui?.entryOverrides ?? {};
  for (const node of nodes) {
    const override = entryOverrides[node.id];
    const isEntry = typeof override === "boolean" ? override : !pointedAt.has(node.id);
    if (isEntry) result.entryNodeIds.add(node.id);
  }

  // A node someone marked deprecated is out of play, and so is everything
  // only reachable through it or through a deprecated wire — a whole chain
  // fades together. Rather than re-scan the graph until it settles, each node
  // counts the ways in that are still live and the count is decremented as
  // parents fade; a node fades the moment its last live way in disappears.
  // Every edge is therefore visited once, and a cycle cannot spin forever
  // because a count only ever falls and a node is queued only when it fades.
  const wiredEdges = (graph.edges ?? []).filter(
    (edge) => byId.has(edge.from) && byId.has(edge.to),
  );
  const liveParentCount = new Map<string, number>();
  const liveChildren = new Map<string, string[]>();
  const hasIncoming = new Set<string>();
  for (const edge of wiredEdges) {
    hasIncoming.add(edge.to);
    if (edgeRelation(edge) === "deprecated") continue;
    liveParentCount.set(edge.to, (liveParentCount.get(edge.to) ?? 0) + 1);
    liveChildren.set(edge.from, [...(liveChildren.get(edge.from) ?? []), edge.to]);
  }

  const pending: string[] = [];
  const fade = (id: string) => {
    if (result.deprecatedNodeIds.has(id)) return;
    result.deprecatedNodeIds.add(id);
    pending.push(id);
  };
  for (const node of nodes) {
    // Nothing points at an entry node, so it can only fade by its own status.
    if ((statuses[node.id] ?? "ready") === "deprecated") fade(node.id);
    else if (hasIncoming.has(node.id) && !liveParentCount.get(node.id)) fade(node.id);
  }
  while (pending.length) {
    const id = pending.pop()!;
    for (const child of liveChildren.get(id) ?? []) {
      const remaining = (liveParentCount.get(child) ?? 1) - 1;
      liveParentCount.set(child, remaining);
      if (remaining === 0) fade(child);
    }
  }

  for (const node of nodes) {
    const status = statuses[node.id] ?? "ready";
    const plans = graph.ui?.plans?.[node.id];
    const messages: string[] = [];
    const start = milestone(plans, "started");
    const progress = milestone(plans, "in_progress");
    const finish = milestone(plans, "done");
    if (start && start < today && (status === "ready" || status === "locked")) {
      messages.push(`開始日 ${start}，目前仍未開始`);
    }
    if (
      progress &&
      progress < today &&
      !(new Set<Status>(["in_progress", "failed"]).has(status) || settled(status))
    ) {
      messages.push(`預計 ${progress} 進行，目前尚未進入進行中`);
    }
    if (finish && finish < today && !settled(status)) {
      messages.push(`死線 ${finish}，目前尚未完成`);
    }
    if (messages.length) result.overdue.set(node.id, messages);
  }

  // A relation gate states what its edges mean, not when its output may run,
  // so the output's `requires` expression decides as it would without it.
  const gatesByOutput = new Map(
    (graph.ui?.logicGates ?? [])
      .filter(
        (gate) =>
          gate.output && byId.has(gate.output) && !logicGateRelation(gate.operator),
      )
      .map((gate) => [gate.output!, gate]),
  );
  const incoming = new Map<string, string[]>();
  for (const edge of requiredEdges) {
    incoming.set(edge.to, [...(incoming.get(edge.to) ?? []), edge.from]);
  }

  for (const node of nodes) {
    // `requires` is executable truth; required edges are only its canvas
    // projection. Evaluating the expression directly keeps this browser in
    // lock-step with Go even when an old graph still carries stale wires.
    const condition = evaluateRequires(node.requires, done, effectiveFlags);
    const parents = [...new Set(incoming.get(node.id) ?? [])];
    const gate = gatesByOutput.get(node.id);
    // A half-wired boolean gate is an editor draft and fails closed. Complete
    // gates are already materialized into `requires` and use the same DSL as
    // ordinary conditions; relation gates were filtered out above.
    const gateIncomplete = Boolean(gate && !logicGateIsComplete(gate));
    const unsatisfied = gateIncomplete ? gateBlockers(gate!, done) : condition.blockedBy;
    const isBlocked = gateIncomplete || !condition.ok || !condition.satisfied;
    if (isBlocked && !settled(statuses[node.id] ?? "ready")) {
      result.blocked.set(node.id, unsatisfied);
      for (const sourceId of unsatisfied) {
        result.blocking.set(sourceId, [...(result.blocking.get(sourceId) ?? []), node.id]);
      }
    }
    if (active.has(statuses[node.id] ?? "ready") && isBlocked) {
      const gateConditionFalse = Boolean(gate && !gateIncomplete);
      result.violations.push({
        nodeId: node.id,
        sourceIds: unsatisfied,
        kind: "actual",
        message: gateIncomplete
          ? `已開始，但邏輯閘 ${gate!.operator.toUpperCase()} 尚未接妥`
          : gateConditionFalse
            ? `已開始，但邏輯閘 ${gate!.operator.toUpperCase()} 條件不成立：${unsatisfied
                .map((id) => byId.get(id)?.title || id)
                .join("、")}`
            : !condition.ok
              ? "已開始，但前置條件語法無法解析"
              : unsatisfied.length === 0
                ? "已開始，但前置條件不成立"
                : `已開始，但前置條件尚未完成：${unsatisfied
                    .map((id) => byId.get(id)?.title || id)
                    .join("、")}`,
      });
    }

    const targetStart = milestone(graph.ui?.plans?.[node.id], "started");
    if (!targetStart) continue;
    const lateParents = parents.filter((sourceId) => {
      const sourceDone = milestone(graph.ui?.plans?.[sourceId], "done");
      return sourceDone && sourceDone > targetStart;
    });
    const scheduleBroken = gate?.operator === "or"
      ? lateParents.length === parents.length
      : lateParents.length > 0;
    if (scheduleBroken) {
      result.violations.push({
        nodeId: node.id,
        sourceIds: lateParents,
        kind: "schedule",
        message: `預計開始日 ${targetStart} 早於必要前置節點的預計完成日`,
      });
    }
  }

  // Longest weighted path on the dependency DAG. If cycles exist, report them
  // and still analyze the acyclic portion instead of hiding all insight.
  const outgoing = new Map<string, string[]>();
  const indegree = new Map(nodes.map((node) => [node.id, 0]));
  for (const edge of requiredEdges) {
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
  }
  const queue = nodes.filter((node) => (indegree.get(node.id) ?? 0) === 0).map((node) => node.id);
  const order: string[] = [];
  while (queue.length) {
    const id = queue.shift()!;
    order.push(id);
    for (const child of outgoing.get(id) ?? []) {
      indegree.set(child, (indegree.get(child) ?? 1) - 1);
      if (indegree.get(child) === 0) queue.push(child);
    }
  }
  result.hasCycle = order.length !== nodes.length;
  const distance = new Map<string, number>();
  const previous = new Map<string, string>();
  for (const id of order) {
    const own = nodeDuration(byId.get(id)!, graph);
    if (!distance.has(id)) distance.set(id, own);
    for (const child of outgoing.get(id) ?? []) {
      const candidate = (distance.get(id) ?? own) + nodeDuration(byId.get(child)!, graph);
      if (candidate > (distance.get(child) ?? 0)) {
        distance.set(child, candidate);
        previous.set(child, id);
      }
    }
  }
  let end = "";
  for (const [id, days] of distance) {
    if (!end || days > (distance.get(end) ?? 0)) end = id;
  }
  if (end) {
    const path: string[] = [];
    for (let current: string | undefined = end; current; current = previous.get(current)) {
      path.push(current);
    }
    path.reverse();
    result.criticalPath = path;
    result.criticalDays = distance.get(end) ?? 0;
    path.forEach((id) => result.criticalNodeIds.add(id));
    for (let index = 1; index < path.length; index += 1) {
      result.criticalEdgeKeys.add(`${path[index - 1]}->${path[index]}`);
    }
  }
  return result;
}
