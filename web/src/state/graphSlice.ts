/**
 * Graph slice [A-05].
 *
 * Owns `graph.yaml`: the expected plan, node metadata, workflow definitions and
 * project layout. Every write goes through the same shape — clone, apply,
 * optimistic set, PUT with `baseRev`, roll back on failure — so an optimistic
 * update can never survive a rejected write.
 */

import { api, ConflictError, type GraphOp } from "../api";
import { applyGraphCommand, commandTarget, describeGraphCommand } from "../domain";
import { applyGraphOps } from "../domain/graph/ops";
import type { Graph } from "../types";
import {
  commandFailed,
  commandSucceeded,
  describeFailure,
  operationScope,
  type CommandResult,
  type OperationScope,
} from "./operations";
import { graphCommandToOps, graphOpsNeedServerGraph } from "./graphOps";
import { coalesceRequest } from "./coalesce";
import { generations, queues, scopeForCommand } from "./internals";
import { pushUndo } from "./undo";
import type { AppSlice, AppState, GraphSlice } from "./types";

type SetGraphState = (
  partial: Partial<AppState> | ((state: AppState) => Partial<AppState>),
) => void;

const CONFLICT_MESSAGE = "圖譜已被外部修改，已重新載入；請再套用一次變更。";


/**
 * Internal whole-graph write [A-04 / plan §10.7].
 *
 * Not exposed on the store: components use the typed commands. It stays as the
 * primitive that `runGraphCommand` and node creation are built on, because both
 * still need "mutate this draft and PUT it" semantics.
 */
function writeGraph(
  set: SetGraphState,
  get: () => AppState,
  mutate: (draft: Graph) => void,
  recordUndo = true,
  options: { operation?: string; scope?: OperationScope } = {},
): Promise<void> {
  const name = options.operation ?? "graph.save";
  const scope = options.scope ?? operationScope.graph();
  const operation = queues.graphSave.then(async () => {
    const { graph, graphRev } = get();
    if (!graph) return;
    get().beginOperation(scope, name);
    const previous = graph;
    const copy: Graph = structuredClone(graph);
    try {
      mutate(copy);
    } catch (e) {
      const message = describeFailure(e, "無法套用變更");
      get().settleOperation(scope, { status: "error", operation: name, message });
      set({ error: message });
      throw e;
    }
    set({ graph: copy });
    try {
      const res = await api.putGraph(copy, graphRev);
      set({ graphRev: res.rev, issues: res.issues ?? [], error: null });
      if (recordUndo) pushUndo({ kind: "graph", graph: structuredClone(previous) });
      get().settleOperation(scope, { status: "saved", operation: name });
    } catch (e) {
      if (e instanceof ConflictError) {
        await get().loadAll();
        set({ error: CONFLICT_MESSAGE });
        get().settleOperation(scope, {
          status: "conflict",
          operation: name,
          message: CONFLICT_MESSAGE,
        });
        return;
      }
      const message = describeFailure(e, "儲存圖譜失敗");
      set((state) => ({
        graph: state.graph === copy ? previous : state.graph,
        error: message,
      }));
      get().settleOperation(scope, { status: "error", operation: name, message });
      throw e;
    }
    try {
      await get().refreshState();
    } catch (e) {
      set({ error: describeFailure(e, "更新狀態失敗") });
    }
  });
  queues.graphSave = operation.catch(() => undefined);
  return operation;
}

export const createGraphSlice: AppSlice<GraphSlice> = (set, get) => ({
  error: null,
  clearError: () => set({ error: null }),
  graph: null,
  graphRev: "",
  issues: [],

  // Reloading a project is two independent reads, and the second one is also
  // what a `state-changed` event asks for on its own. Going through the same
  // coalescing keys as those events is what lets one logical change cost one
  // GET per resource instead of one per trigger [A-05].
  loadAll: async () => {
    await Promise.all([
      coalesceRequest("graph", async () => {
        const generation = ++generations.load;
        const g = await api.getGraph();
        // Dropped when a project switch happened while this was in flight.
        if (generation !== generations.load) return;
        set({
          graph: g.graph,
          graphRev: g.rev,
          issues: g.issues ?? [],
          error: null,
        });
        // Where the node files sit is disk state rather than part of the graph,
        // so it is fetched alongside rather than inside the graph response. A
        // failure here only costs the folder grouping, never the graph.
        await get().refreshNodeFolders().catch(() => undefined);
      }),
      get().refreshState(),
    ]);
  },

  applyPeerGraphOps: (ops: GraphOp[]) => {
    const { graph } = get();
    if (!graph) return false;
    const next = applyGraphOps(graph, ops);
    if (!next) return false;
    // Only the graph: statuses and issues are derived from it plus the run
    // state, and a move or a rename changes neither. A peer edit that could
    // change them arrives as graph-changed instead.
    set({ graph: next });
    return true;
  },

  runGraphCommand: (command, options = {}) => {
    const scope = scopeForCommand(commandTarget(command));
    const name = command.type;
    const label = describeGraphCommand(command);
    const run = queues.graphSave.then(async (): Promise<CommandResult> => {
      const { graph, graphRev } = get();
      if (!graph) return commandFailed(name, scope, "尚未載入專案。");
      get().beginOperation(scope, name);
      const previous = graph;
      const copy: Graph = structuredClone(graph);
      try {
        applyGraphCommand(copy, command);
      } catch (e) {
        const message = describeFailure(e, `${label}失敗`);
        get().settleOperation(scope, { status: "error", operation: name, message });
        return commandFailed(name, scope, message);
      }
      const ops = graphCommandToOps(command, copy);
      // Dependency semantics are materialized from the Store's current graph
      // under its write lock. Showing the edge-only draft before that response
      // would briefly make UI readiness disagree with agents and Go readiness.
      if (!graphOpsNeedServerGraph(ops)) set({ graph: copy });
      // Op-able edits (canvas drag, node resize, node fields, timeline order,
      // edge removal) go through POST /api/graph/ops: no baseRev, so an unrelated
      // concurrent edit no longer 409s, and the server's merged graph is adopted
      // so the other person's change shows up straight away. Everything else
      // stays on the whole-file PUT.
      try {
        if (ops) {
          // The peer id travels with the write so the server can tell the room
          // what changed without this browser being told about its own edit.
          const res = await api.graphOps(ops, get().selfPeerID);
          set({
            graph: res.graph,
            graphRev: res.rev,
            statuses: res.statuses,
            issues: res.issues ?? [],
            error: null,
          });
          if (options.recordUndo !== false) {
            pushUndo({ kind: "graph", graph: structuredClone(previous), label });
          }
          get().settleOperation(scope, { status: "saved", operation: name });
          // The ops response already carries fresh statuses; no refreshState.
          return commandSucceeded(name, scope, undefined);
        }
        const res = await api.putGraph(copy, graphRev);
        set({ graphRev: res.rev, issues: res.issues ?? [], error: null });
        if (options.recordUndo !== false) {
          pushUndo({ kind: "graph", graph: structuredClone(previous), label });
        }
        get().settleOperation(scope, { status: "saved", operation: name });
      } catch (e) {
        if (e instanceof ConflictError) {
          // Someone else wrote graph.yaml. Reload rather than clobber, and let
          // the caller decide whether to re-apply.
          await get().loadAll().catch(() => undefined);
          get().settleOperation(scope, {
            status: "conflict",
            operation: name,
            message: CONFLICT_MESSAGE,
          });
          return commandFailed(name, scope, CONFLICT_MESSAGE, "conflict");
        }
        const message = describeFailure(e, `${label}失敗`);
        set((state) => ({ graph: state.graph === copy ? previous : state.graph }));
        get().settleOperation(scope, { status: "error", operation: name, message });
        return commandFailed(name, scope, message);
      }
      try {
        await get().refreshState();
      } catch {
        /* statuses refresh is best-effort; the graph write already landed */
      }
      return commandSucceeded(name, scope, undefined);
    });
    queues.graphSave = run.then(() => undefined).catch(() => undefined);
    return run;
  },

  updateNodeMetadata: (nodeId, patch) =>
    get().runGraphCommand({ type: "node.updateMetadata", nodeId, patch }),
  updateNode: (command) => get().runGraphCommand(command),
  upsertPlanMilestone: (nodeId, milestone) =>
    get().runGraphCommand({ type: "plan.upsert", nodeId, milestone }),
  removePlanMilestone: (nodeId, milestoneType) =>
    get().runGraphCommand({ type: "plan.remove", nodeId, milestoneType }),
  updatePlan: (command) => get().runGraphCommand(command),
  updateWorkflowDefinition: (command) => get().runGraphCommand(command),
  updateCanvasLayout: (command) => get().runGraphCommand(command),

  // Save a node's requires and keep incoming edges in sync with the node
  // refs of the expression (edges are the visual cache of the gate logic).
  commitRequires: async (id, requires) => {
    let refs: string[] | null = null;
    if (requires.trim() === "") {
      refs = [];
    } else {
      try {
        const res = await api.checkDSL(requires);
        if (res.ok) refs = res.nodeRefs ?? [];
      } catch {
        /* server unreachable: save text only */
      }
    }
    const result = await get().runGraphCommand({
      type: "node.setRequires",
      nodeId: id,
      requires,
      refs,
    });
    if (!result.ok) throw new Error(result.message);
  },

  createNode: async (node, options = {}) => {
    const created = await api.createNode(node);
    const id = created.id;
    pushUndo({ kind: "create-node", ids: [id] });
    await get().loadAll();
    const style =
      options.style && Object.keys(options.style).length > 0 ? options.style : undefined;
    if (
      options.position ||
      (options.plans?.length ?? 0) > 0 ||
      options.planStatuses ||
      style
    ) {
      // Placement, initial plan and chosen looks are part of creating the node,
      // so they must not push a second undo entry. POST /api/nodes writes the
      // node file and the graph entry only; `ui.nodeStyles` is graph state the
      // client already owns, so the appearance rides along in this same write
      // rather than costing another request.
      await writeGraph(set, get, (g) => {
        g.ui = g.ui ?? {};
        if (options.position) {
          g.ui.positions = g.ui.positions ?? {};
          g.ui.positions[id] = options.position;
        }
        if (options.plans?.length) {
          g.ui.plans = g.ui.plans ?? {};
          g.ui.plans[id] = options.plans;
        }
        if (options.planStatuses) {
          g.ui.planStatuses = options.planStatuses;
        }
        if (style) {
          g.ui.nodeStyles = g.ui.nodeStyles ?? {};
          g.ui.nodeStyles[id] = style;
        }
      }, false);
    }
    if (options.openDocument !== false) await get().openTab(id);
    return id;
  },

  duplicateNode: async (sourceID) => {
    const result = await api.duplicateNode(sourceID);
    pushUndo({ kind: "create-node", ids: [result.id] });
    await get().loadAll();
    await get().openTab(result.id);
    return result.id;
  },

  deleteNode: async (id) => {
    await get().deleteNodes([id]);
  },

  // A selection is deleted in one request: the server rewrites the graph once,
  // so the board never shows a half-cleared selection, and one undo brings the
  // whole batch back.
  deleteNodes: async (ids) => {
    const graph = get().graph;
    if (!graph || ids.length === 0) return;
    const before = structuredClone(graph);
    const result =
      ids.length === 1
        ? { trashFiles: [(await api.deleteNode(ids[0])).trashFile] }
        : await api.deleteNodes(ids);
    pushUndo({
      kind: "delete-node",
      graph: before,
      trashFiles: result.trashFiles.filter(Boolean),
      label: ids.length > 1 ? `刪除 ${ids.length} 個節點` : undefined,
    });
    for (const id of ids) get().closeTab(id);
    await Promise.all([get().loadAll(), get().refreshTrash()]);
  },

  // Cross-project clipboard. It outlives a project switch on purpose: cutting
  // in one project and pasting in another is the whole point, and the switch
  // happens between the two halves of that gesture.
  nodeClipboard: null,
  setNodeClipboard: (clipboard) => set({ nodeClipboard: clipboard }),
  clearNodeClipboard: () => set({ nodeClipboard: null }),

  transferNodes: async (request) => {
    const result = await api.transferNodes(request);
    const active = get().activeProject;
    if (request.mode === "cut" && (request.source ?? active) === active) {
      for (const id of request.ids) get().closeTab(id);
    }
    // Either end of the transfer may be the project on screen, and both node
    // counts moved, so everything the sidebar shows is refreshed.
    await Promise.all([
      get().loadAll(),
      get().refreshTrash(),
      get().refreshProjects(),
    ]);
    return result;
  },

  restoreTrash: async (file) => {
    const result = await api.restoreTrash(file);
    pushUndo({ kind: "create-node", ids: [result.id] });
    await Promise.all([get().loadAll(), get().refreshTrash()]);
  },
});
