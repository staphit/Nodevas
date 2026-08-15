/**
 * Logic gate reducers [A-04].
 *
 * A first-class gate (`graph.yaml → ui.logicGates`) owns the `requires`
 * expression of its output node. Incomplete wiring is persisted on purpose —
 * it is an editor draft, so a half-wired gate survives a reload.
 *
 * Ported from the inline helpers in the timeline view so the canvas can stop
 * mutating whole graphs; behaviour is unchanged.
 */

import type {
  EdgeRelation,
  Graph,
  LogicGate,
  LogicGateOperator,
} from "../../types";
import { invalid, notFound } from "../errors";
import { edgeRelation, edgeStylePatch } from "./edgeStyle";
import {
  gateWireKey,
  logicGateInputWireKey,
  logicGateOutputWireKey,
  logicGateWirePrefix,
} from "./keys";

/**
 * The edge relation a gate stands for, or `null` when it is a boolean gate.
 *
 * A relation gate ("選用" / "棄用") writes no `requires` expression: it marks
 * every input→output edge with its relation. It is a gate so the meaning of a
 * fan-in is edited in one place instead of edge by edge.
 */
export function logicGateRelation(
  operator: LogicGateOperator,
): Exclude<EdgeRelation, ""> | null {
  return operator === "optional" || operator === "deprecated" ? operator : null;
}

/**
 * Every node this gate drives. A boolean gate owns one output node's `requires`
 * and stores it in `output`; a relation gate is many-to-many and stores its
 * targets in `outputs`.
 */
export function logicGateOutputs(gate: LogicGate): string[] {
  if (gate.outputs?.length) return gate.outputs;
  return gate.output ? [gate.output] : [];
}

/** The boolean gate, if any, that owns one node's executable condition. */
export function booleanLogicGateForOutput(
  graph: Graph,
  output: string,
): LogicGate | undefined {
  return (graph.ui?.logicGates ?? []).find(
    (gate) => !logicGateRelation(gate.operator) && logicGateOutputs(gate).includes(output),
  );
}

/**
 * The gate whose materialized projection contains this edge. Directly editing
 * such an edge would be overwritten the next time the gate materializes, so
 * callers must change the gate's inputs/outputs instead.
 */
export function logicGateOwningEdge(
  graph: Graph,
  from: string,
  to: string,
): LogicGate | undefined {
  return (graph.ui?.logicGates ?? []).find(
    (gate) => gate.inputs.includes(from) && logicGateOutputs(gate).includes(to),
  );
}

export function logicGateInputRequirement(operator: LogicGateOperator): number {
  if (operator === "must") return 1;
  return logicGateRelation(operator) ? 1 : 2;
}

export function logicGateIsComplete(gate: LogicGate): boolean {
  const required = logicGateInputRequirement(gate.operator);
  const validInputCount =
    gate.operator === "must"
      ? gate.inputs.length === required
      : gate.inputs.length >= required;
  return logicGateOutputs(gate).length > 0 && validInputCount;
}

export function logicGateExpression(gate: LogicGate): string {
  // A relation gate has no condition to express.
  if (logicGateRelation(gate.operator)) return "";
  if (gate.operator === "must") return gate.inputs[0] ?? "";
  if (gate.operator === "nand") return `not (${gate.inputs.join(" and ")})`;
  if (gate.operator === "nor") return `not (${gate.inputs.join(" or ")})`;
  return gate.inputs.join(` ${gate.operator} `);
}

export function findLogicGate(graph: Graph, gateId: string): LogicGate {
  const gate = graph.ui?.logicGates?.find((candidate) => candidate.id === gateId);
  if (!gate) throw notFound(`邏輯閘「${gateId}」`);
  return gate;
}

export function nextLogicGateID(gates: LogicGate[]): string {
  const used = new Set(gates.map((gate) => gate.id));
  for (let index = 1; index <= 100_000; index += 1) {
    const candidate = `gate-${index}`;
    if (!used.has(candidate)) return candidate;
  }
  return `gate-${crypto.randomUUID()}`;
}

/**
 * Why a new dependency edge would be rejected, or `null` when it is fine.
 * Kept as a message-returning check because the canvas shows it live while a
 * connection is being dragged.
 */
export function dependencyConnectionError(
  edges: { from: string; to: string }[],
  sourceId: string,
  targetId: string,
): string | null {
  if (sourceId === targetId) return "不可連接節點自身。";
  if (edges.some((edge) => edge.from === sourceId && edge.to === targetId)) {
    return "此關係已存在。";
  }
  const outgoing = new Map<string, string[]>();
  for (const edge of edges) {
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
  }
  const pending = [targetId];
  const visited = new Set<string>();
  while (pending.length > 0) {
    const current = pending.pop()!;
    if (current === sourceId) return "此連線會產生循環依賴。";
    if (visited.has(current)) continue;
    visited.add(current);
    pending.push(...(outgoing.get(current) ?? []));
  }
  return null;
}

/** Undoes what {@link materializeLogicGate} wrote into the output nodes. */
export function detachLogicGateOutput(graph: Graph, gate: LogicGate): void {
  const outputs = logicGateOutputs(gate);
  if (outputs.length === 0) return;
  const relation = logicGateRelation(gate.operator);
  if (relation) {
    graph.edges = (graph.edges ?? []).filter(
      (edge) =>
        !outputs.includes(edge.to) ||
        edgeRelation(edge) !== relation ||
        !gate.inputs.includes(edge.from),
    );
    gate.applied = undefined;
    return;
  }
  const target = graph.nodes?.find((node) => node.id === outputs[0]);
  if (target && gate.applied) {
    const current = target.requires?.trim() ?? "";
    if (current === gate.applied) target.requires = "";
    else if (current.endsWith(` and (${gate.applied})`)) {
      target.requires = current.slice(0, -` and (${gate.applied})`.length);
    }
  }
  graph.edges = (graph.edges ?? []).filter(
    (edge) =>
      edge.to !== outputs[0] ||
      edgeRelation(edge) !== "" ||
      !gate.inputs.includes(edge.from),
  );
  gate.applied = undefined;
}

/** Writes the gate's expression and dependency edges onto its output nodes. */
export function materializeLogicGate(graph: Graph, gate: LogicGate): void {
  const relation = logicGateRelation(gate.operator);
  if (relation) {
    materializeRelationGate(graph, gate, relation);
    return;
  }
  const output = logicGateOutputs(gate)[0];
  if (!output) return;
  const target = graph.nodes?.find((node) => node.id === output);
  if (!target) return;

  // A first-class gate owns the required conditions of its output. This
  // removes the inline gate so one target never displays two gates.
  target.requires = "";
  graph.edges = (graph.edges ?? []).filter(
    (edge) => edge.to !== output || edgeRelation(edge) !== "",
  );
  gate.applied = undefined;
  if (graph.ui?.gates) delete graph.ui.gates[output];
  if (graph.ui?.wireVertices) {
    delete graph.ui.wireVertices[gateWireKey(output)];
  }

  if (!logicGateIsComplete(gate)) return;
  const expression = logicGateExpression(gate);
  if (!expression) return;
  target.requires = expression;
  gate.applied = expression;

  const edges = graph.edges ?? [];
  for (const input of gate.inputs) {
    if (!edges.some((edge) => edge.from === input && edge.to === output)) {
      edges.push({ from: input, to: output });
    }
  }
  graph.edges = edges;
}

/**
 * A relation gate owns every edge of its relation that reaches one of its
 * outputs, the way a boolean gate owns its output's required prerequisites. It
 * is many-to-many: every input is marked towards every output. Edges are
 * restyled in place, so a link that already existed keeps its bend points
 * instead of being dropped and drawn again.
 */
function materializeRelationGate(
  graph: Graph,
  gate: LogicGate,
  relation: Exclude<EdgeRelation, "">,
): void {
  const inputs = new Set(gate.inputs);
  const outputs = logicGateOutputs(gate);
  graph.edges = (graph.edges ?? []).filter(
    (edge) =>
      !outputs.includes(edge.to) ||
      edgeRelation(edge) !== relation ||
      inputs.has(edge.from),
  );
  gate.applied = undefined;
  if (!logicGateIsComplete(gate)) return;

  const edges = graph.edges ?? [];
  const patch = edgeStylePatch(relation, "");
  for (const output of outputs) {
    for (const input of gate.inputs) {
      if (input === output) continue;
      const existing = edges.find(
        (edge) => edge.from === input && edge.to === output,
      );
      if (existing) Object.assign(existing, patch);
      else edges.push({ from: input, to: output, ...patch });
    }
  }
  graph.edges = edges;
}

export function createLogicGate(
  graph: Graph,
  input: { operator: LogicGateOperator; x: number; y: number },
): string {
  graph.ui = graph.ui ?? {};
  graph.ui.logicGates = graph.ui.logicGates ?? [];
  const id = nextLogicGateID(graph.ui.logicGates);
  graph.ui.logicGates.push({
    id,
    operator: input.operator,
    x: input.x,
    y: input.y,
    inputs: [],
  });
  return id;
}

/**
 * Turns existing relation edges into the gate that owns them: one gate whose
 * operator is their shared relation, with every source as an input and every
 * target as an output. The gate is many-to-many, so it also draws the pairs the
 * selection did not contain — that is what makes it one gate and not several.
 */
export function convertEdgesToLogicGate(
  graph: Graph,
  edges: { from: string; to: string }[],
  point: { x: number; y: number },
): string {
  if (edges.length === 0) throw invalid("沒有可轉換的關係線。");
  const existing = edges.map((target) => {
    const edge = (graph.edges ?? []).find(
      (candidate) => candidate.from === target.from && candidate.to === target.to,
    );
    if (!edge) throw notFound(`關係線「${target.from} → ${target.to}」`);
    return edge;
  });
  const relation = edgeRelation(existing[0]);
  if (existing.some((edge) => edgeRelation(edge) !== relation)) {
    throw invalid("選取的關係線語意不一致，請先統一為同一種關係。");
  }
  if (!logicGateRelation(relation as LogicGateOperator)) {
    throw invalid("必要關係請改用 AND／OR 等邏輯閘。");
  }
  const outputs = [...new Set(edges.map((edge) => edge.to))];
  // The gate owns every edge of this relation reaching its outputs, so the ones
  // that were not selected are taken in rather than overwritten.
  const inputs = [
    ...new Set(
      (graph.edges ?? [])
        .filter(
          (edge) => outputs.includes(edge.to) && edgeRelation(edge) === relation,
        )
        .map((edge) => edge.from),
    ),
  ];
  if (inputs.some((id) => outputs.includes(id))) {
    throw invalid("同一個節點不可同時作為此閘門的輸入與輸出。");
  }
  for (const gate of graph.ui?.logicGates ?? []) {
    const owned = logicGateOutputs(gate).find((id) => outputs.includes(id));
    if (owned) {
      throw invalid(`「${owned}」已由邏輯閘「${gate.id}」控制。`);
    }
  }

  const gateId = createLogicGate(graph, {
    operator: relation as LogicGateOperator,
    x: point.x,
    y: point.y,
  });
  const gate = findLogicGate(graph, gateId);
  gate.inputs = inputs;
  writeOutputs(gate, outputs);
  materializeLogicGate(graph, gate);
  return gateId;
}

export function moveLogicGate(
  graph: Graph,
  gateId: string,
  point: { x: number; y: number },
): void {
  const gate = findLogicGate(graph, gateId);
  gate.x = Math.round(point.x * 100) / 100;
  gate.y = Math.round(point.y * 100) / 100;
}

export function connectLogicGateInput(
  graph: Graph,
  gateId: string,
  sourceId: string,
): void {
  const gate = findLogicGate(graph, gateId);
  if (gate.inputs.includes(sourceId)) return;
  if (gate.operator === "must" && gate.inputs.length >= 1) {
    throw invalid("MUST 僅接受一個輸入節點。");
  }
  gate.inputs.push(sourceId);
  materializeLogicGate(graph, gate);
}

export function disconnectLogicGateInput(
  graph: Graph,
  gateId: string,
  sourceId: string,
): void {
  const gate = findLogicGate(graph, gateId);
  gate.inputs = gate.inputs.filter((id) => id !== sourceId);
  if (graph.ui?.wireVertices) {
    delete graph.ui.wireVertices[logicGateInputWireKey(gateId, sourceId)];
  }
  materializeLogicGate(graph, gate);
}

/**
 * Points the gate at one output node, or clears it when `targetId` is empty.
 * A relation gate is many-to-many, so this replaces its whole output set; use
 * {@link toggleLogicGateOutput} to add one without dropping the others.
 */
export function setLogicGateOutput(
  graph: Graph,
  gateId: string,
  targetId: string | undefined,
): void {
  const gate = findLogicGate(graph, gateId);
  detachLogicGateOutput(graph, gate);
  forgetOutputWires(graph, gate, logicGateOutputs(gate));
  writeOutputs(gate, targetId ? [targetId] : []);
  materializeLogicGate(graph, gate);
}

/**
 * Adds or removes one output of a relation gate, leaving its other outputs
 * alone. A boolean gate has room for a single output, so enabling one there
 * replaces what was connected before.
 */
export function toggleLogicGateOutput(
  graph: Graph,
  gateId: string,
  targetId: string,
  enabled: boolean,
): void {
  const gate = findLogicGate(graph, gateId);
  const current = logicGateOutputs(gate);
  if (!logicGateRelation(gate.operator)) {
    setLogicGateOutput(graph, gateId, enabled ? targetId : undefined);
    return;
  }
  if (enabled === current.includes(targetId)) return;
  detachLogicGateOutput(graph, gate);
  const next = enabled
    ? [...current, targetId]
    : current.filter((id) => id !== targetId);
  if (!enabled) forgetOutputWires(graph, gate, [targetId]);
  writeOutputs(gate, next);
  materializeLogicGate(graph, gate);
}

/** Keeps `output`/`outputs` from ever both being set. */
function writeOutputs(gate: LogicGate, outputs: string[]): void {
  const unique = [...new Set(outputs)];
  if (logicGateRelation(gate.operator)) {
    gate.output = undefined;
    gate.outputs = unique.length > 0 ? unique : undefined;
    return;
  }
  gate.outputs = undefined;
  gate.output = unique[0];
}

function forgetOutputWires(
  graph: Graph,
  gate: LogicGate,
  outputs: string[],
): void {
  if (!graph.ui?.wireVertices) return;
  for (const output of outputs) {
    delete graph.ui.wireVertices[logicGateOutputWireKey(gate.id, output)];
  }
}

export function setLogicGateOperator(
  graph: Graph,
  gateId: string,
  operator: LogicGateOperator,
): void {
  const gate = findLogicGate(graph, gateId);
  if (gate.operator === operator) return;
  // What the previous shape of this gate wrote — an expression or relation
  // edges — is undone under the old operator before the new one is applied.
  detachLogicGateOutput(graph, gate);
  const outputs = logicGateOutputs(gate);
  gate.operator = operator;
  // A boolean gate drives one node, so the extra outputs of a relation gate
  // are dropped on the way over — and stored under the new operator's field.
  if (outputs.length > 1 && !logicGateRelation(operator)) {
    forgetOutputWires(graph, gate, outputs.slice(1));
  }
  writeOutputs(gate, outputs);
  // MUST takes exactly one input; drop the extras and their wires.
  if (operator === "must" && gate.inputs.length > 1) {
    const removed = gate.inputs.slice(1);
    gate.inputs = gate.inputs.slice(0, 1);
    for (const sourceId of removed) {
      if (graph.ui?.wireVertices) {
        delete graph.ui.wireVertices[logicGateInputWireKey(gateId, sourceId)];
      }
    }
  }
  materializeLogicGate(graph, gate);
}

export function removeLogicGate(graph: Graph, gateId: string): void {
  const gate = graph.ui?.logicGates?.find((candidate) => candidate.id === gateId);
  if (gate) detachLogicGateOutput(graph, gate);
  if (graph.ui?.logicGates) {
    graph.ui.logicGates = graph.ui.logicGates.filter(
      (candidate) => candidate.id !== gateId,
    );
  }
  if (graph.ui?.wireVertices) {
    const prefix = logicGateWirePrefix(gateId);
    for (const key of Object.keys(graph.ui.wireVertices)) {
      if (key.startsWith(prefix)) delete graph.ui.wireVertices[key];
    }
  }
}
