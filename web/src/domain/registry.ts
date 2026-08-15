/**
 * State registry [A-01].
 *
 * One row per domain: where the truth lives on disk, which API touches it, who
 * is allowed to write it, and how a write is confirmed. This is the machine
 * readable version of §4 of `PROJECT_REDESIGN_PLAN.md`; UI code can render it
 * (Settings → about/debug) and reviews can diff it.
 *
 * Adding a new persisted concept means adding a row here first.
 */

export type DomainId =
  | "node-metadata"
  | "plan-milestone"
  | "lifecycle"
  | "document"
  | "workflow"
  | "project-layout"
  | "ui-preference"
  | "ui-ephemeral";

/**
 * How a change becomes durable.
 *
 * - `commit-autosave` — value is written when the field commits (blur/enter).
 * - `manual-save` — user presses save; dirty state is visible until then.
 * - `explicit-apply` — user presses an apply button; appends to the journal.
 * - `instant-local` — written to local storage immediately, never to the project.
 * - `none` — not persisted at all.
 */
export type SavePolicy =
  | "commit-autosave"
  | "manual-save"
  | "explicit-apply"
  | "instant-local"
  | "none";

/** How the change is reverted. Journal-backed domains never delete history. */
export type UndoPolicy = "command" | "editor" | "compensating" | "reset" | "none";

export interface DomainDescriptor {
  id: DomainId;
  /** Domain name in UI copy. */
  title: string;
  /** Single source of truth, in prose. */
  sourceOfTruth: string;
  /** Path relative to the project root; empty when not a project file. */
  diskPath: string;
  /** HTTP endpoints that read or write it. */
  api: string[];
  /** The UI surface that owns editing this domain. */
  owner: string;
  /** Surfaces allowed to read it. */
  readers: string[];
  /** Store entry points allowed to write it. Anything else is a bug. */
  writers: string[];
  savePolicy: SavePolicy;
  undo: UndoPolicy;
  /** Derived caches that must never be edited directly. */
  derived?: string[];
}

export const DOMAIN_REGISTRY: Record<DomainId, DomainDescriptor> = {
  "node-metadata": {
    id: "node-metadata",
    title: "節點 metadata",
    sourceOfTruth:
      "graph.yaml → nodes[] / ui.entryOverrides，同步至 nodes/{id}.md frontmatter",
    diskPath: "graph.yaml",
    api: ["GET /api/graph", "PUT /api/graph"],
    owner: "Node Inspector／基本資料",
    readers: ["Graph", "Timeline", "Sidebar", "Command Palette"],
    writers: [
      "updateNodeMetadata",
      "setNodeRequires",
      "assignNode",
      "setNodeStyle",
      "setNodeEntryOverride",
    ],
    savePolicy: "commit-autosave",
    undo: "command",
  },
  "plan-milestone": {
    id: "plan-milestone",
    title: "預期里程碑",
    sourceOfTruth: "graph.yaml → ui.plans",
    diskPath: "graph.yaml",
    api: ["GET /api/graph", "PUT /api/graph"],
    owner: "Node Inspector／預期計畫",
    readers: ["Timeline", "Node Inspector"],
    writers: ["upsertPlanMilestone", "removePlanMilestone", "movePlanMilestone"],
    savePolicy: "commit-autosave",
    undo: "command",
  },
  lifecycle: {
    id: "lifecycle",
    title: "實際狀態",
    sourceOfTruth: "run/journal.jsonl（append-only）",
    diskPath: "run/journal.jsonl",
    api: [
      "GET /api/state",
      "POST /api/nodes/{id}/status",
      "POST /api/events/move",
    ],
    owner: "Node Inspector／實際狀態",
    readers: ["Graph", "Timeline", "Sidebar", "Analysis"],
    writers: ["setLifecycleStatus", "moveActualEvent"],
    savePolicy: "explicit-apply",
    undo: "compensating",
    derived: ["run/state.json", "statuses"],
  },
  document: {
    id: "document",
    title: "文件內容",
    sourceOfTruth: "nodes/{id}.md（子頁：nodes/{id}/{page}.md）",
    diskPath: "nodes/",
    api: [
      "GET /api/nodes/{id}",
      "PUT /api/nodes/{id}",
      "GET|PUT /api/nodes/{id}/pages/{page}",
      "GET|PUT|DELETE /api/drafts/{id}",
    ],
    owner: "Node Inspector／文件、Popout",
    readers: ["Drawer", "Popout", "Search"],
    writers: ["setTabContent", "saveTab", "saveDraft", "applyDraft", "resolveConflict"],
    savePolicy: "manual-save",
    undo: "editor",
    derived: [".vised/drafts"],
  },
  workflow: {
    id: "workflow",
    title: "工作流程定義",
    sourceOfTruth: "graph.yaml → ui.customStatuses / ui.planStatuses",
    diskPath: "graph.yaml",
    api: ["GET /api/graph", "PUT /api/graph"],
    owner: "Project Settings／Workflow",
    readers: ["Graph", "Timeline", "Node Inspector", "Sidebar 圖例"],
    writers: ["updateWorkflowDefinition"],
    savePolicy: "commit-autosave",
    undo: "command",
  },
  "project-layout": {
    id: "project-layout",
    title: "專案版面",
    sourceOfTruth:
      "graph.yaml → ui.positions / ui.timelineOrder / ui.groups / ui.annotations / ui.savedViews / ui.wireVertices / ui.gates / ui.logicGates / ui.edgeLabels / ui.nodeStyles",
    diskPath: "graph.yaml",
    api: ["GET /api/graph", "PUT /api/graph"],
    owner: "Graph／Timeline 工具",
    readers: ["Graph", "Timeline"],
    writers: ["updateCanvasLayout"],
    savePolicy: "commit-autosave",
    undo: "command",
  },
  "ui-preference": {
    id: "ui-preference",
    title: "個人版面偏好",
    sourceOfTruth: "localStorage（versioned adapter）",
    diskPath: "",
    api: [],
    owner: "Settings／Appearance",
    readers: ["App", "Drawer", "Timeline", "Sidebar"],
    writers: ["updateUIPreference", "resetUIPreferences"],
    savePolicy: "instant-local",
    undo: "reset",
  },
  "ui-ephemeral": {
    id: "ui-ephemeral",
    title: "UI 暫態",
    sourceOfTruth: "component state 或 UI slice",
    diskPath: "",
    api: [],
    owner: "當前畫面",
    readers: ["當前畫面"],
    writers: [],
    savePolicy: "none",
    undo: "none",
  },
};

export const DOMAIN_IDS = Object.keys(DOMAIN_REGISTRY) as DomainId[];

/** Domains whose writes go through `PUT /api/graph` and share its revision lock. */
export const GRAPH_BACKED_DOMAINS: DomainId[] = DOMAIN_IDS.filter((id) =>
  DOMAIN_REGISTRY[id].api.includes("PUT /api/graph"),
);

export function describeDomain(id: DomainId): DomainDescriptor {
  return DOMAIN_REGISTRY[id];
}
