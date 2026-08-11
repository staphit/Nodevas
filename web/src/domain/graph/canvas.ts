/**
 * Project layout reducers [A-04].
 *
 * Owns the `graph.yaml → ui.*` placement data shared by everyone opening the
 * project: node positions, timeline order, groups, annotations, saved views,
 * wire vertices and edge styling. Personal, per-machine settings are *not*
 * here — those live in `preferences/` and never reach the project file.
 */

import type {
  CanvasAnnotation,
  CanvasGroup,
  EdgeLine,
  EdgeRelation,
  Graph,
  SavedView,
} from "../../types";
import { invalid, notFound } from "../errors";
import { DEFAULT_LINE, edgeRelation, edgeStylePatch } from "./edgeStyle";
import { edgeWireKey, gateWireKey } from "./keys";
import { booleanLogicGateForOutput, logicGateOwningEdge } from "./logicGate";

export interface CanvasPoint {
  x: number;
  y: number;
}

const MAX_WIRE_VERTICES = 64;

function round2(value: number): number {
  return Math.round(value * 100) / 100;
}

function ui(graph: Graph): NonNullable<Graph["ui"]> {
  graph.ui = graph.ui ?? {};
  return graph.ui;
}

/** Batch move. Callers pass final coordinates; partial batches are fine. */
export function moveNodes(
  graph: Graph,
  positions: Record<string, CanvasPoint>,
): void {
  const layout = ui(graph);
  layout.positions = layout.positions ?? {};
  for (const [nodeId, point] of Object.entries(positions)) {
    if (!Number.isFinite(point.x) || !Number.isFinite(point.y)) {
      throw invalid("節點座標無效。");
    }
    layout.positions[nodeId] = { x: point.x, y: point.y };
  }
}

/**
 * Slides every placement on the board by a fixed amount.
 *
 * Node coordinates are grid cells and cannot go negative, so dragging a card
 * past the top-left corner moves the *board* instead: everything else shifts
 * with it and the picture stays put on screen. The caller passes both units —
 * cells for the node grid, pixels for the free-floating decorations — because
 * only the canvas layer knows how wide a cell is.
 */
export function shiftBoard(
  graph: Graph,
  shift: { columns: number; rows: number; x: number; y: number },
): void {
  const layout = ui(graph);
  if (!Number.isFinite(shift.columns) || !Number.isFinite(shift.rows)) {
    throw invalid("位移量無效。");
  }
  for (const position of Object.values(layout.positions ?? {})) {
    position.x = round2(position.x + shift.columns);
    position.y = round2(position.y + shift.rows);
  }
  for (const gate of Object.values(layout.gates ?? {})) {
    if (gate.x !== undefined) gate.x = round2(gate.x + shift.x);
    if (gate.y !== undefined) gate.y = round2(gate.y + shift.y);
  }
  for (const gate of layout.logicGates ?? []) {
    gate.x = round2(gate.x + shift.x);
    gate.y = round2(gate.y + shift.y);
  }
  for (const group of layout.groups ?? []) {
    group.x = round2(group.x + shift.x);
    group.y = round2(group.y + shift.y);
  }
  for (const annotation of layout.annotations ?? []) {
    annotation.x = round2(annotation.x + shift.x);
    annotation.y = round2(annotation.y + shift.y);
  }
  for (const vertices of Object.values(layout.wireVertices ?? {})) {
    for (const vertex of vertices) {
      vertex.x = round2(vertex.x + shift.x);
      vertex.y = round2(vertex.y + shift.y);
    }
  }
}

/** Timeline sequence is independent of canvas positions, by design. */
export function setTimelineOrder(graph: Graph, order: string[]): void {
  ui(graph).timelineOrder = [...order];
}

export function setGatePlacement(
  graph: Graph,
  targetId: string,
  point: CanvasPoint,
): void {
  const layout = ui(graph);
  layout.gates = layout.gates ?? {};
  layout.gates[targetId] = { x: round2(point.x), y: round2(point.y) };
}

export function setWireVertices(
  graph: Graph,
  wireKey: string,
  vertices: CanvasPoint[],
): void {
  const layout = ui(graph);
  layout.wireVertices = layout.wireVertices ?? {};
  if (vertices.length === 0) {
    delete layout.wireVertices[wireKey];
    return;
  }
  layout.wireVertices[wireKey] = vertices
    .slice(0, MAX_WIRE_VERTICES)
    .map((vertex) => ({ x: round2(vertex.x), y: round2(vertex.y) }));
}

export function removeWireVertex(graph: Graph, wireKey: string, index: number): void {
  const vertices = graph.ui?.wireVertices?.[wireKey];
  if (!vertices || index < 0 || index >= vertices.length) return;
  vertices.splice(index, 1);
  if (vertices.length === 0) delete graph.ui!.wireVertices![wireKey];
}

/**
 * Sets what the edges mean and how they are drawn. The two are independent:
 * pass only `relation` to keep the current line, only `line` to restyle a
 * relation without changing its meaning.
 */
export function setEdgeStyle(
  graph: Graph,
  edges: { from: string; to: string }[],
  patch: { relation?: EdgeRelation; line?: EdgeLine },
): void {
  for (const target of edges) {
    const edge = graph.edges?.find(
      (candidate) => candidate.from === target.from && candidate.to === target.to,
    );
    if (!edge) continue;
    if (patch.relation !== undefined) {
      const owner = logicGateOwningEdge(graph, target.from, target.to);
      if (owner) {
        throw invalid(`關係線「${target.from} → ${target.to}」由邏輯閘「${owner.id}」控制。`);
      }
      const booleanOwner = booleanLogicGateForOutput(graph, target.to);
      if (booleanOwner && (edgeRelation(edge) === "" || patch.relation === "")) {
        throw invalid(`「${target.to}」由邏輯閘「${booleanOwner.id}」控制，請改為編輯該閘門。`);
      }
    }
    const relation = patch.relation ?? edgeRelation(edge);
    // A line that only matched the old relation's default follows the new
    // relation instead of freezing the previous look.
    const line =
      patch.line ??
      (edge.line && edge.line !== DEFAULT_LINE[edgeRelation(edge)]
        ? edge.line
        : "");
    const next = edgeStylePatch(relation, line);
    edge.relation = next.relation;
    edge.line = next.line;
  }
}

/**
 * Drops placements whose wire no longer exists.
 *
 * Edges are not only removed by {@link removeEdge}: rewriting a node's
 * `requires` or wiring a logic gate rebuilds the edge list too, and a label or
 * bend point left behind then fails validation with "wire vertices reference
 * unknown edge". Running this after every command keeps the file consistent no
 * matter which path changed the edges.
 */
export function pruneEdgeDecorations(graph: Graph): void {
  const layout = graph.ui;
  if (!layout) return;
  const liveEdges = new Set(
    (graph.edges ?? []).map((edge) => edgeWireKey(edge.from, edge.to)),
  );
  const liveNodes = new Set((graph.nodes ?? []).map((node) => node.id));
  const liveGates = new Set(
    (layout.logicGates ?? []).map((gate) => gate.id),
  );
  for (const key of Object.keys(layout.edgeLabels ?? {})) {
    if (!liveEdges.has(key)) delete layout.edgeLabels![key];
  }
  for (const key of Object.keys(layout.wireVertices ?? {})) {
    const gateTarget = key.startsWith("gate:") ? key.slice("gate:".length) : null;
    const logicGateID = key.startsWith("logic:")
      ? key.slice("logic:".length).split(":")[0]
      : null;
    const alive =
      gateTarget !== null
        ? liveNodes.has(gateTarget)
        : logicGateID !== null
          ? liveGates.has(logicGateID)
          : liveEdges.has(key);
    if (!alive) delete layout.wireVertices![key];
  }
}

/** Removes an edge and every placement keyed to it. */
export function removeEdge(graph: Graph, from: string, to: string): void {
  const owner = logicGateOwningEdge(graph, from, to);
  if (owner) {
    throw invalid(`關係線「${from} → ${to}」由邏輯閘「${owner.id}」控制。`);
  }
  const selected = graph.edges?.find((edge) => edge.from === from && edge.to === to);
  const booleanOwner = booleanLogicGateForOutput(graph, to);
  if (selected && edgeRelation(selected) === "" && booleanOwner) {
    throw invalid(`「${to}」由邏輯閘「${booleanOwner.id}」控制，請改為編輯該閘門。`);
  }
  graph.edges = (graph.edges ?? []).filter(
    (edge) => edge.from !== from || edge.to !== to,
  );
  const key = edgeWireKey(from, to);
  if (graph.ui?.edgeLabels) delete graph.ui.edgeLabels[key];
  if (graph.ui?.wireVertices) delete graph.ui.wireVertices[key];
  // The inline gate glyph only exists while the target still has dependencies.
  if (graph.ui?.gates && !(graph.edges ?? []).some((edge) => edge.to === to)) {
    delete graph.ui.gates[to];
    if (graph.ui.wireVertices) delete graph.ui.wireVertices[gateWireKey(to)];
  }
}

export function upsertGroup(graph: Graph, group: CanvasGroup): void {
  const layout = ui(graph);
  const groups = layout.groups ?? [];
  const index = groups.findIndex((item) => item.id === group.id);
  layout.groups =
    index === -1
      ? [...groups, group]
      : groups.map((item) => (item.id === group.id ? { ...item, ...group } : item));
}

export function removeGroup(graph: Graph, id: string): void {
  if (!graph.ui?.groups) return;
  graph.ui.groups = graph.ui.groups.filter((item) => item.id !== id);
}

export function upsertAnnotation(graph: Graph, annotation: CanvasAnnotation): void {
  const layout = ui(graph);
  const annotations = layout.annotations ?? [];
  const index = annotations.findIndex((item) => item.id === annotation.id);
  layout.annotations =
    index === -1
      ? [...annotations, annotation]
      : annotations.map((item) =>
          item.id === annotation.id ? { ...item, ...annotation } : item,
        );
}

export function removeAnnotation(graph: Graph, id: string): void {
  if (!graph.ui?.annotations) return;
  graph.ui.annotations = graph.ui.annotations.filter((item) => item.id !== id);
}

export function saveView(graph: Graph, view: SavedView): void {
  const layout = ui(graph);
  const views = layout.savedViews ?? [];
  if (!view.name.trim()) throw invalid("檢視名稱不可空白。");
  const index = views.findIndex((item) => item.id === view.id);
  layout.savedViews =
    index === -1
      ? [...views, view]
      : views.map((item) => (item.id === view.id ? { ...item, ...view } : item));
}

export function removeView(graph: Graph, id: string): void {
  if (!graph.ui?.savedViews) return;
  if (!graph.ui.savedViews.some((view) => view.id === id)) throw notFound("檢視");
  graph.ui.savedViews = graph.ui.savedViews.filter((view) => view.id !== id);
}
