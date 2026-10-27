/**
 * Node types [A-04].
 *
 * A node's type decides how it behaves — whether an agent may work it, who may
 * mark it done, whether it counts as work — and how its card looks by default.
 * Built-in types are presets of exactly the same attributes a project's custom
 * types carry, so there is one model for all of them.
 *
 * Owns `graph.yaml → node_types` (wire `nodeTypes`), a sparse list: one entry
 * per custom type, plus an entry for a built-in only when its shape or colour
 * is overridden. Mirrors `internal/engine/nodetypes.go`; the built-in table is
 * the shared fixture `testdata/node_types_builtin.json`.
 *
 * Everything here is pure. Reducers throw `CommandError`.
 */

import { safeCssColor } from "../../cssColor";
import type {
  AgentMode,
  BuiltinNodeKind,
  CustomNodeTypeId,
  Graph,
  GraphNode,
  NodeCompletion,
  NodeShape,
  NodeType,
  NodeTypeDef,
} from "../../types";
import { invalid, notFound } from "../errors";

export const NODE_SHAPES: readonly NodeShape[] = [
  "rect",
  "round",
  "pill",
  "ellipse",
  "diamond",
  "hexagon",
  "dashed",
  "thick",
];
export const AGENT_MODES: readonly AgentMode[] = ["none", "dispatch", "autopilot"];
export const NODE_COMPLETIONS: readonly NodeCompletion[] = ["anyone", "people", "auto", "none"];

export const CUSTOM_NODE_TYPE_PREFIX = "custom-type-";
export const MAX_CUSTOM_NODE_TYPES = 64;
const CUSTOM_ID = /^custom-type-[A-Za-z0-9_-]{1,48}$/;
const MAX_LABEL = 64;

/** The built-in table, in its canonical order. Equal to the shared fixture. */
export const BUILTIN_NODE_TYPES: readonly NodeType[] = Object.freeze([
  { id: "task", label: "Task", builtin: true, shape: "round", color: "", agentMode: "dispatch", completion: "anyone", countsInProgress: true },
  { id: "goal", label: "Goal", builtin: true, shape: "thick", color: "#62b2c1", agentMode: "autopilot", completion: "people", countsInProgress: true },
  { id: "scene", label: "Scene", builtin: true, shape: "round", color: "", agentMode: "dispatch", completion: "anyone", countsInProgress: true },
  { id: "event", label: "Event", builtin: true, shape: "ellipse", color: "", agentMode: "dispatch", completion: "anyone", countsInProgress: true },
  { id: "gate", label: "Review", builtin: true, shape: "hexagon", color: "#f0a35e", agentMode: "none", completion: "people", countsInProgress: true },
  { id: "choice", label: "Decision", builtin: true, shape: "diamond", color: "", agentMode: "none", completion: "people", countsInProgress: true },
  { id: "start", label: "Start", builtin: true, shape: "pill", color: "", agentMode: "none", completion: "auto", countsInProgress: false },
  { id: "end", label: "End", builtin: true, shape: "pill", color: "", agentMode: "none", completion: "auto", countsInProgress: false },
  { id: "note", label: "Note", builtin: true, shape: "dashed", color: "", agentMode: "none", completion: "none", countsInProgress: false },
] satisfies NodeType[]);

const BUILTIN_BY_ID = new Map(BUILTIN_NODE_TYPES.map((type) => [type.id, type]));
const TASK = BUILTIN_BY_ID.get("task")!;

const KIND_ALIASES: Record<string, BuiltinNodeKind> = {
  review: "gate",
  decision: "choice",
};

export function isBuiltinKind(id: string): id is BuiltinNodeKind {
  return BUILTIN_BY_ID.has(id);
}

export function isCustomNodeTypeId(id: string): id is CustomNodeTypeId {
  return id.startsWith(CUSTOM_NODE_TYPE_PREFIX);
}

/** Trim and lower-case; "" stays ""; `review`→`gate`, `decision`→`choice`. */
export function normalizeKind(kind: string | undefined | null): string {
  const trimmed = (kind ?? "").trim();
  // Custom ids keep their case: they are compared as written.
  if (isCustomNodeTypeId(trimmed)) return trimmed;
  const lower = trimmed.toLowerCase();
  return KIND_ALIASES[lower] ?? lower;
}

function copy(type: NodeType): NodeType {
  return { ...type };
}

const resolvedCache = new WeakMap<NodeTypeDef[], NodeType[]>();
const EMPTY_DEFS: NodeTypeDef[] = [];

function resolveDefs(defs: NodeTypeDef[]): NodeType[] {
  const overrides = new Map(
    defs.filter((def) => isBuiltinKind(def.id)).map((def) => [def.id, def]),
  );
  const builtins = BUILTIN_NODE_TYPES.map((type) => {
    const override = overrides.get(type.id);
    if (!override) return copy(type);
    return {
      ...type,
      shape: override.shape && NODE_SHAPES.includes(override.shape) ? override.shape : type.shape,
      color: override.color !== undefined ? override.color : type.color,
    };
  });
  const customs = defs
    .filter((def) => !isBuiltinKind(def.id))
    .map<NodeType>((def) => {
      const completion = def.completion ?? "anyone";
      return {
        id: def.id,
        label: def.label ?? def.id,
        builtin: false,
        shape: def.shape && NODE_SHAPES.includes(def.shape) ? def.shape : "round",
        color: def.color ?? "",
        agentMode:
          completion === "none" || completion === "auto" ? "none" : (def.agentMode ?? "none"),
        completion,
        countsInProgress: completion === "none" ? false : def.countsInProgress ?? true,
      };
    });
  return [...builtins, ...customs];
}

/**
 * Built-ins with the project's overrides applied, then custom types in stored
 * order. Memoized per `graph.nodeTypes` array, which every write replaces.
 */
export function resolveNodeTypes(graph: Graph | null | undefined): NodeType[] {
  const defs = graph?.nodeTypes ?? EMPTY_DEFS;
  const cached = resolvedCache.get(defs);
  if (cached) return cached;
  const resolved = resolveDefs(defs);
  resolvedCache.set(defs, resolved);
  return resolved;
}

/** Resolved type by id. "" and unknown ids resolve to Task. */
export function nodeTypeById(graph: Graph | null | undefined, id: string | undefined): NodeType {
  const kind = normalizeKind(id);
  const types = resolveNodeTypes(graph);
  return (
    types.find((type) => type.id === kind) ??
    types.find((type) => type.id === "task") ??
    copy(TASK)
  );
}

/** The node's resolved type: "" or an unknown kind is Task. */
export function nodeTypeOf(
  graph: Graph | null | undefined,
  node: Pick<GraphNode, "kind"> | null | undefined,
): NodeType {
  return nodeTypeById(graph, node?.kind);
}

/** "" | a built-in id | a custom id the project defines. */
export function isKnownKind(graph: Graph | null | undefined, kind: string): boolean {
  if (kind === "" || isBuiltinKind(kind)) return true;
  return (graph?.nodeTypes ?? []).some((def) => def.id === kind && !isBuiltinKind(def.id));
}

/** Ids of the nodes whose type is `id` (`""` counts as task). */
export function nodeTypeUsage(graph: Graph, id: string): string[] {
  return (graph.nodes ?? [])
    .filter((node) => (normalizeKind(node.kind) || "task") === id)
    .map((node) => node.id);
}

export function nextCustomNodeTypeID(defs: readonly NodeTypeDef[]): CustomNodeTypeId {
  const used = new Set(defs.map((def) => def.id));
  for (let index = 1; index <= 10_000; index++) {
    const candidate = `custom-type-${index}` as const;
    if (!used.has(candidate)) return candidate;
  }
  return `custom-type-${crypto.randomUUID()}`;
}

const WORKFLOW_ORDER = ["task", "goal", "gate", "choice", "start", "end", "note", "scene", "event"];
const STORY_ORDER = ["scene", "event", "goal", "choice", "gate", "start", "end", "note", "task"];

/** Built-in ids in the order a layout's pickers list them. */
export function typeOrderForLayout(layout: string | undefined): string[] {
  return layout === "story" ? [...STORY_ORDER] : [...WORKFLOW_ORDER];
}

/** Resolved types for pickers: built-ins in layout order, then customs. */
export function nodeTypesForLayout(graph: Graph | null | undefined): NodeType[] {
  const order = typeOrderForLayout(graph?.type);
  const types = resolveNodeTypes(graph);
  const builtins = types
    .filter((type) => type.builtin)
    .sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id));
  return [...builtins, ...types.filter((type) => !type.builtin)];
}

// --- validation -------------------------------------------------------------

function sameLabel(a: string, b: string): boolean {
  return a.localeCompare(b, undefined, { sensitivity: "accent" }) === 0;
}

function assertShape(shape: string | undefined) {
  if (shape !== undefined && shape !== "" && !NODE_SHAPES.includes(shape as NodeShape)) {
    throw invalid("不支援的圖形。");
  }
}

function assertColor(color: string | undefined) {
  if (color && !safeCssColor(color)) throw invalid("顏色格式不正確。");
}

/** §1.5 rule 4, for one custom entry. */
function assertCustomBehaviour(def: NodeTypeDef) {
  if (!def.agentMode || !AGENT_MODES.includes(def.agentMode)) {
    throw invalid("請選擇 agent 模式。");
  }
  if (!def.completion || !NODE_COMPLETIONS.includes(def.completion)) {
    throw invalid("請選擇完成方式。");
  }
  if (typeof def.countsInProgress !== "boolean") {
    throw invalid("請指定是否計入進度。");
  }
  if ((def.completion === "auto" || def.completion === "none") && def.agentMode !== "none") {
    throw invalid("自動完成或沒有狀態的類型不能派給 agent。");
  }
  if (def.completion === "none" && def.countsInProgress) {
    throw invalid("沒有狀態的類型不能計入進度。");
  }
  if (def.agentMode === "autopilot" && def.completion !== "anyone" && def.completion !== "people") {
    throw invalid("Autopilot 類型須由人或成功的 run 完成。");
  }
}

function assertLabel(label: string | undefined, customs: NodeTypeDef[], id?: string): string {
  const trimmed = (label ?? "").trim();
  if (!trimmed) throw invalid("名稱不可空白。");
  if (trimmed.length > MAX_LABEL) throw invalid(`名稱不可超過 ${MAX_LABEL} 字。`);
  if (customs.some((def) => def.id !== id && sameLabel(def.label ?? "", trimmed))) {
    throw invalid(`已存在名稱「${trimmed}」。`);
  }
  return trimmed;
}

/** Stored-form check of a whole list, the same rules the hub applies on save. */
export function validateNodeTypeDefs(defs: readonly NodeTypeDef[]): void {
  const ids = new Set<string>();
  const customs = defs.filter((def) => !isBuiltinKind(def.id));
  if (customs.length > MAX_CUSTOM_NODE_TYPES) {
    throw invalid(`自訂類型最多 ${MAX_CUSTOM_NODE_TYPES} 種。`);
  }
  for (const def of defs) {
    if (ids.has(def.id)) throw invalid(`類型代號「${def.id}」重複。`);
    ids.add(def.id);
    assertShape(def.shape);
    assertColor(def.color);
    if (isBuiltinKind(def.id)) {
      if (
        def.label !== undefined ||
        def.agentMode !== undefined ||
        def.completion !== undefined ||
        def.countsInProgress !== undefined
      ) {
        throw invalid("內建類型只能修改形狀與顏色。");
      }
      continue;
    }
    if (!CUSTOM_ID.test(def.id)) throw invalid(`類型代號「${def.id}」格式不正確。`);
    assertLabel(def.label, customs, def.id);
    assertCustomBehaviour(def);
  }
}

// --- reducers ---------------------------------------------------------------

/** Drops a built-in override that no longer changes anything. */
function compact(defs: NodeTypeDef[]): NodeTypeDef[] {
  return defs.filter(
    (def) => !isBuiltinKind(def.id) || Boolean(def.shape) || def.color !== undefined,
  );
}

function store(graph: Graph, defs: NodeTypeDef[]) {
  const next = compact(defs);
  if (next.length) graph.nodeTypes = next;
  else delete graph.nodeTypes;
}

/** A custom entry with every behaviour field present, `false` included. */
function normalizeCustom(def: NodeTypeDef): NodeTypeDef {
  const completion = def.completion;
  const agentMode = completion === "auto" || completion === "none" ? "none" : def.agentMode;
  const countsInProgress = completion === "none" ? false : def.countsInProgress;
  const out: NodeTypeDef = { id: def.id, label: def.label };
  if (def.shape) out.shape = def.shape;
  if (def.color) out.color = def.color;
  out.agentMode = agentMode;
  out.completion = completion;
  out.countsInProgress = countsInProgress;
  return out;
}

export function addNodeType(
  graph: Graph,
  definition: Omit<NodeTypeDef, "id"> & { id?: CustomNodeTypeId },
): CustomNodeTypeId {
  const current = graph.nodeTypes ?? [];
  const customs = current.filter((def) => !isBuiltinKind(def.id));
  if (customs.length >= MAX_CUSTOM_NODE_TYPES) {
    throw invalid(`自訂類型最多 ${MAX_CUSTOM_NODE_TYPES} 種。`);
  }
  const id = definition.id ?? nextCustomNodeTypeID(current);
  if (!CUSTOM_ID.test(id)) throw invalid(`類型代號「${id}」格式不正確。`);
  if (current.some((def) => def.id === id)) throw invalid(`類型代號「${id}」已存在。`);
  const label = assertLabel(definition.label, customs);
  assertShape(definition.shape);
  assertColor(definition.color);
  const next = normalizeCustom({
    ...definition,
    id,
    label,
    shape: definition.shape || "round",
    agentMode: definition.agentMode ?? "none",
    completion: definition.completion ?? "anyone",
    countsInProgress: definition.countsInProgress ?? true,
  });
  assertCustomBehaviour(next);
  store(graph, [...current, next]);
  return id;
}

export function updateNodeType(
  graph: Graph,
  id: string,
  patch: Partial<Omit<NodeTypeDef, "id">>,
): void {
  const current = graph.nodeTypes ?? [];
  assertShape(patch.shape);
  assertColor(patch.color);
  if (isBuiltinKind(id)) {
    const behaviour = (["label", "agentMode", "completion", "countsInProgress"] as const).some(
      (key) => key in patch && patch[key] !== undefined,
    );
    if (behaviour) throw invalid("內建類型只能修改形狀與顏色。");
    const existing = current.find((def) => def.id === id);
    const next: NodeTypeDef = { ...(existing ?? { id }) };
    if ("shape" in patch) {
      if (patch.shape) next.shape = patch.shape;
      else delete next.shape;
    }
    if ("color" in patch) {
      // A cleared colour goes back to the built-in default; "" stored would
      // instead mean "no colour", which is what the default already is for
      // most types, so the override is dropped rather than kept as "".
      if (patch.color) next.color = patch.color;
      else delete next.color;
    }
    store(
      graph,
      existing ? current.map((def) => (def.id === id ? next : def)) : [...current, next],
    );
    return;
  }
  const target = current.find((def) => def.id === id);
  if (!target) throw notFound(`節點類型「${id}」`);
  const customs = current.filter((def) => !isBuiltinKind(def.id));
  const merged: NodeTypeDef = { ...target };
  if (patch.label !== undefined) merged.label = assertLabel(patch.label, customs, id);
  if ("shape" in patch) merged.shape = patch.shape || "round";
  if ("color" in patch) merged.color = patch.color || undefined;
  if (patch.agentMode !== undefined) merged.agentMode = patch.agentMode;
  if (patch.completion !== undefined) merged.completion = patch.completion;
  if (patch.countsInProgress !== undefined) merged.countsInProgress = patch.countsInProgress;
  const next = normalizeCustom(merged);
  assertCustomBehaviour(next);
  store(
    graph,
    current.map((def) => (def.id === id ? next : def)),
  );
}

/**
 * Deletes a custom type. Nodes of that type fall back to Task, so nothing is
 * left pointing at a type that no longer exists.
 */
export function removeNodeType(graph: Graph, id: string): void {
  if (isBuiltinKind(id)) throw invalid("內建類型不能刪除。");
  const current = graph.nodeTypes ?? [];
  if (!current.some((def) => def.id === id)) throw notFound(`節點類型「${id}」`);
  for (const node of graph.nodes ?? []) {
    if (node.kind === id) node.kind = "task";
  }
  store(
    graph,
    current.filter((def) => def.id !== id),
  );
}
