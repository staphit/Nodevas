// Mirrors internal/engine JSON shapes.

export type NodeKind = "task" | "scene" | "choice" | "gate" | "event" | "start" | "end" | "";

export type BuiltinStatus =
  | "locked"
  | "ready"
  | "started"
  | "in_progress"
  | "done"
  | "skipped"
  | "failed"
  /** No longer part of the plan; settles the node like done and skipped. */
  | "deprecated";
export type CustomStatus = `custom-status-${string}`;
/**
 * Actual lifecycle state, derived from `run/journal.jsonl`.
 * Domain name: `LifecycleStatus` (see `domain/glossary.ts`). Not a plan value —
 * expected milestones use {@link PlanStatus}.
 */
export type Status = BuiltinStatus | CustomStatus;

export interface StatusDefinition {
  id: CustomStatus;
  label: string;
  color: string;
  shape: "circle" | "square" | "diamond" | "triangle" | "dash";
  /**
   * Counts as finished the way `done` and `skipped` do: a node in this state
   * satisfies whatever depends on it instead of blocking it. Absent means it
   * blocks, so an existing state keeps behaving as it did.
   */
  settled?: boolean;
}

/**
 * A named reference from one node to another in the same workspace. Not a
 * dependency: nothing waits on it. `project` empty means this node's own
 * project, so renaming or moving a project keeps its internal links working.
 */
export interface NodeLinkRef {
  label: string;
  project?: string;
  node: string;
}

export interface GraphNode {
  id: string;
  title?: string;
  kind?: NodeKind;
  priority?: "urgent" | "high" | "medium" | "low" | "";
  assignee?: string;
  /** "YYYY-MM-DDTHH:mm" or "YYYY-MM-DD" (end of that day), local time. */
  deadline?: string;
  requires?: string;
  tags?: string[];
  effects?: { set?: string }[];
  links?: NodeLinkRef[];
}

export interface GraphUser {
  id: string;
  name: string;
  email?: string;
}

/** What an edge means. "" is a required prerequisite. */
export type EdgeRelation = "" | "optional" | "deprecated";
/** How an edge is drawn. "" defers to the relation's default line. */
export type EdgeLine = "" | "solid" | "dashed" | "dotted";

export interface GraphEdge {
  from: string;
  to: string;
  relation?: EdgeRelation;
  line?: EdgeLine;
}

export type BuiltinPlanStatus = "started" | "in_progress" | "done";
/**
 * Expected milestone type, stored in `graph.yaml → ui.plans[].status`.
 * Domain name: `MilestoneType` (see `domain/glossary.ts`). Never an actual
 * lifecycle value — those are {@link Status}.
 */
export type PlanStatus = BuiltinPlanStatus | `custom-${string}`;

export interface PlanStatusDefinition {
  id: `custom-${string}`;
  label: string;
}

export interface PlanMilestone {
  date: string; // local calendar date: YYYY-MM-DD
  /** optional local time "HH:mm"; milestones sort by it inside a day */
  time?: string;
  status: PlanStatus;
  note?: string;
}

/**
 * Boolean operators write a `requires` expression on the output node.
 * `optional` and `deprecated` are relation gates: they write no expression,
 * only edges of that relation from every input to the output.
 */
export type LogicGateOperator =
  | "must"
  | "and"
  | "or"
  | "xor"
  | "nand"
  | "nor"
  | "optional"
  | "deprecated";

export interface LogicGate {
  id: string;
  operator: LogicGateOperator;
  x: number;
  y: number;
  inputs: string[];
  /** Single output of a boolean gate. Relation gates use {@link outputs}. */
  output?: string;
  /**
   * Outputs of a relation gate, which is many-to-many. Read both fields through
   * `logicGateOutputs`; only one of them is ever written for a given gate.
   */
  outputs?: string[];
  /** Last expression written into the output node; used for safe incremental replacement. */
  applied?: string;
}

/** Card outline on the canvas. Absent means the default rectangle. */
export type NodeShape =
  | "rect"
  | "round"
  | "pill"
  | "ellipse"
  | "diamond"
  | "hexagon";

export type NodeAlign = "left" | "center" | "right";
export type NodeVAlign = "top" | "middle" | "bottom";

export interface NodeStyle {
  width?: number;
  height?: number;
  /** Card background. */
  color?: string;
  /** Card text colour; defaults to the theme's text colour. */
  textColor?: string;
  /** Card outline colour; also colours the shaped cards' drawn edge. */
  borderColor?: string;
  /** Horizontal placement of the card's text. */
  align?: NodeAlign;
  /** Vertical placement of the card's text. */
  valign?: NodeVAlign;
  shape?: NodeShape;
}

export interface CanvasGroup {
  id: string;
  title: string;
  x: number;
  y: number;
  width: number;
  height: number;
  color?: string;
}

export interface CanvasAnnotation {
  id: string;
  text: string;
  x: number;
  y: number;
  width: number;
  height: number;
  color?: string;
}

export interface SavedView {
  id: string;
  name: string;
  statuses?: string[];
  assignees?: string[];
  tags?: string[];
  priorities?: string[];
  sort?: "manual" | "title" | "priority" | "status" | "assignee";
}

export interface Graph {
  version: number;
  type?: string;
  users?: GraphUser[];
  nodes: GraphNode[] | null;
  edges?: GraphEdge[] | null;
  flags?: Record<string, unknown>;
  ui?: {
    positions?: Record<string, { x: number; y: number }>;
    /** Timeline-only node order. Graph positions never affect this sequence. */
    timelineOrder?: string[];
    /** Dependency-gate placement on the canvas. */
    gates?: Record<string, { x?: number; y?: number; ratio?: number }>;
    /** First-class logic gates. Incomplete wiring is intentionally persisted as an editor draft. */
    logicGates?: LogicGate[];
    /** Optional-edge label position along the edge, keyed by "from->to". */
    edgeLabels?: Record<string, { ratio: number }>;
    /** User-defined bend points, keyed by "from->to" or "gate:target". */
    wireVertices?: Record<string, { x: number; y: number }[]>;
    /** Expected milestones only. Actual lifecycle events live in run/journal.jsonl. */
    plans?: Record<string, PlanMilestone[]>;
    /** Project-wide custom expected milestone types. */
    planStatuses?: PlanStatusDefinition[];
    /** Project-wide custom node lifecycle states. */
    customStatuses?: StatusDefinition[];
    nodeStyles?: Record<string, NodeStyle>;
    /**
     * Manual answers to "is this a starting point?", keyed by node id. A node
     * with no entry counts as one when nothing points at it; `false` says it is
     * merely isolated, and `true` calls it a start even with wires coming in.
     */
    entryOverrides?: Record<string, boolean>;
    groups?: CanvasGroup[];
    annotations?: CanvasAnnotation[];
    savedViews?: SavedView[];
  };
}

export interface Issue {
  severity: "error" | "warning";
  nodeId?: string;
  field?: string;
  offset?: number;
  msg: string;
}

/** Latest actual lifecycle state of one node. Derived cache of the journal. */
export interface NodeState {
  status: Status;
  at?: string;
  by?: string;
}

export interface HistoryEvent {
  id?: string;
  t: string;
  event: string;
  node?: string;
  from?: Status;
  to?: Status;
  by?: string;
  note?: string;
  recordedAt?: string;
  flags?: Record<string, unknown>;
  ref?: string;
  snapshot?: string;
}

/**
 * Replay of `run/journal.jsonl`. Source of truth for actual lifecycle; the
 * server keeps `run/state.json` as a cache of this. Never edited directly —
 * writes append events through the status/move endpoints.
 */
export interface RunState {
  startedAt?: string;
  nodes: Record<string, NodeState>;
  flags?: Record<string, unknown>;
  history: HistoryEvent[];
}

export interface DSLCheckResult {
  ok: boolean;
  error?: { offset: number; msg: string };
  nodeRefs?: string[];
  flagRefs?: string[];
  canonical?: string;
}
