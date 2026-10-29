/**
 * Workspace / project slice [A-05].
 *
 * Switching a project is the one place where every other slice has to be told
 * "that was the old project": unsaved documents are parked as drafts, pending
 * writes are drained, generations are bumped so late responses are dropped, and
 * tabs, undo history and operation badges are cleared.
 */

import { api, AuthError, getProjectOverride, RequestError, setProjectOverride } from "../api";
import { translate } from "../i18n";
import { localizeGraphRefusal } from "../issueMessages";
import type { Language } from "../preferences";
import { coalesceRequest } from "./coalesce";
import { describeFailure, operationScope } from "./operations";
import { drainWrites, generations, invalidateGenerations } from "./internals";
import { clearUndo } from "./undo";
import type { AppSlice, AppState, WorkspaceSlice } from "./types";

/** Never lose unsaved text on a switch: park it as a draft first. */
async function parkDirtyTabs(get: () => AppState): Promise<void> {
  // Let autosaves finish before drafting: a successful save removes its draft.
  await drainWrites();
  await Promise.all(
    Object.entries(get().pageDocs)
      .filter(([, page]) => page.dirty && !page.loading)
      .map(async ([key]) => {
        if (!(await get().savePageDoc(key)).ok) {
          throw new Error(translate("workspace.error.pagesUnsaved"));
        }
      }),
  );
  const dirtyTabs = get().tabs.filter((tab) => tab.dirty);
  const drafted = new Map(dirtyTabs.map((tab) => [tab.id, tab.content]));
  await Promise.all(
    dirtyTabs.map((tab) => api.putDraft(tab.id, tab.content)),
  );
  await drainWrites();
  if (
    get().tabs.some((tab) => tab.dirty && drafted.get(tab.id) !== tab.content) ||
    Object.values(get().pageDocs).some((page) => page.dirty)
  ) {
    throw new Error(translate("workspace.error.changedWhileSaving"));
  }
}

/**
 * Project switches this browser has under way. The hub's active project moves
 * in the middle of one, and the project list read meanwhile must not take
 * that for another tab's switch.
 */
let switchesInFlight = 0;

/**
 * Another tab moved the hub's active project and this one is following it,
 * which is a switch whose server half already happened: requests now reach
 * the new project. The old project's tabs close at once (a pending autosave
 * finds no tab, so it cannot write their text into the new project) and
 * their unsaved text is parked as drafts in the project it was written for.
 */
function followProjectSwitch(
  set: Parameters<AppSlice<WorkspaceSlice>>[0],
  get: () => AppState,
  from: string,
): void {
  const unsaved = get().tabs.filter((tab) => tab.dirty);
  clearUndo();
  invalidateGenerations();
  set({ tabs: [], pageDocs: {}, activeTab: null, operations: {} });
  for (const tab of unsaved) {
    void api.putDraft(tab.id, tab.content, from).catch(() => undefined);
  }
  // Loads already in flight were answered for the old generation and are
  // dropped; this one is for the project now on screen.
  void Promise.all([get().loadAll(), get().refreshTrash()]).catch(() => undefined);
}

export const createWorkspaceSlice: AppSlice<WorkspaceSlice> = (set, get) => ({
  workspace: "",
  workspaces: [],
  activeProject: "",
  projects: [],
  projectOrder: [],
  trash: [],

  // Deleting a node refreshes the trash, and the `graph-changed` broadcast it
  // causes asks for the same thing again a moment later; one read answers both.
  refreshTrash: () =>
    coalesceRequest("trash", async () => {
      const t = await api.getTrash();
      set({ trash: t.items });
    }),

  // Three reads, and a project switch triggers this both directly and through
  // the `project-changed` broadcast it emits, so the whole trio is coalesced.
  changeProjects: async (change) => {
    try {
      return await change();
    } finally {
      // Whatever happened, the workspace may hold a project more or less than
      // the tree shows: a create that landed and then failed to open still
      // made one.
      await get()
        .refreshProjects()
        .catch(() => {});
    }
  },

  refreshProjects: () =>
    coalesceRequest("projects", async () => {
      const p = await api.getProjects();
      // An older server, or one with nothing to list, sent null for the
      // lists; the explorer and the top bar iterate them unchecked.
      // The list is also re-read on focus and on a timer (projectListSync);
      // an answer with nothing new keeps the arrays the tree already drew.
      const current = get();
      const projects = p.projects ?? [];
      const workspaces = p.workspaces ?? [];
      const override = getProjectOverride();
      const followed =
        !override &&
        switchesInFlight === 0 &&
        current.activeProject !== "" &&
        Boolean(p.active) &&
        p.active !== current.activeProject;
      set({
        workspace: p.workspace,
        workspaces: sameJSON(current.workspaces, workspaces) ? current.workspaces : workspaces,
        activeProject: override || p.active,
        projects: sameJSON(current.projects, projects) ? current.projects : projects,
      });
      if (followed) followProjectSwitch(set, get, current.activeProject);
      // The manual order belongs to the workspace the list just came from, so it
      // is refreshed with the list rather than on its own schedule. A workspace
      // that never had one is not an error, it simply has no manual order.
      try {
        const order = await api.getProjectOrder();
        set({ projectOrder: order.projectOrder ?? [] });
      } catch {
        set({ projectOrder: [] });
      }
      await get().refreshWorkspaceStatuses();
    }),

  // Applied before the request so the dragged row lands where it was dropped
  // instead of snapping back for the duration of the round trip.
  saveProjectOrder: async (order) => {
    const scope = operationScope.workspace();
    const previous = get().projectOrder;
    set({ projectOrder: order });
    get().beginOperation(scope, "workspace.saveProjectOrder");
    try {
      const result = await api.saveProjectOrder(order);
      set({ projectOrder: result.projectOrder ?? order });
      get().settleOperation(scope, {
        status: "saved",
        operation: "workspace.saveProjectOrder",
      });
    } catch (e) {
      set({ projectOrder: previous });
      get().settleOperation(scope, {
        status: "error",
        operation: "workspace.saveProjectOrder",
        message: describeFailure(e, translate("workspace.error.orderSaveFailed")),
      });
      throw e;
    }
  },

  workspaceStatuses: [],

  refreshWorkspaceStatuses: async () => {
    try {
      const result = await api.getWorkspaceStatuses();
      set({ workspaceStatuses: result.customStatuses ?? [] });
    } catch {
      // A workspace without the shared file is not an error: it has none yet.
      set({ workspaceStatuses: [] });
    }
  },

  // Writing the whole list keeps the server authoritative for ordering and
  // duplicate checks, and one reload republishes it to every open project.
  saveWorkspaceStatuses: async (statuses) => {
    const result = await api.saveWorkspaceStatuses(statuses);
    set({ workspaceStatuses: result.customStatuses ?? statuses });
    await get().loadAll();
  },

  addWorkspace: async (path) => {
    await parkDirtyTabs(get);
    const result = await api.addWorkspace(path);
    setProjectOverride("");
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
      pageDocs: {},
      activeTab: null,
      workspace: result.path,
      activeProject: "",
      operations: {},
    });
    await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
    return { path: result.path, label: result.label };
  },

  switchWorkspace: async (path) => {
    await parkDirtyTabs(get);
    await api.openWorkspace(path);
    setProjectOverride("");
    clearUndo();
    invalidateGenerations();
    set({ tabs: [], pageDocs: {}, activeTab: null, workspace: path, activeProject: "", operations: {} });
    await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
  },

  removeWorkspace: async (path) => {
    const removingActive = get().workspace === path;
    if (removingActive) await parkDirtyTabs(get);
    const result = await api.removeWorkspace(path);
    if (!removingActive) {
      await get().refreshProjects();
      return { workspace: result.workspace };
    }
    setProjectOverride("");
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
      pageDocs: {},
      activeTab: null,
      workspace: result.workspace,
      activeProject: "",
      operations: {},
    });
    await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
    return { workspace: result.workspace };
  },

  moveProjectToWorkspace: async (name, path) => {
    await parkDirtyTabs(get);
    const result = await api.moveProject(name, "", "", path);
    setProjectOverride("");
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
      pageDocs: {},
      activeTab: null,
      workspace: result.workspace ?? path,
      activeProject: result.active ?? "",
      operations: {},
    });
    await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
    return { name: result.name, workspace: result.workspace ?? path };
  },

  openExample: async (example) => {
    // Parked before the server moves: once it has switched, a draft put for
    // the old project would land in the new one.
    await parkDirtyTabs(get);
    switchesInFlight += 1;
    try {
      const result = await api.openExample(example, get().preferences.language);
      // The server has switched already; this does the browser's half — tabs,
      // undo and badges cleared, everything reloaded — as any switch does.
      await get().switchProject(result.name);
      return result.name;
    } finally {
      switchesInFlight -= 1;
    }
  },

  switchProject: async (name, create = false, template = "") => {
    const scope = operationScope.workspace();
    get().beginOperation(scope, "workspace.switchProject");
    switchesInFlight += 1;
    try {
      await parkDirtyTabs(get);
      try {
        await api.openProject(name, create, template, get().preferences.language);
        // The server's active project moved; per-request scoping would now
        // shadow it with something stale.
        setProjectOverride("");
      } catch (refusal) {
        // 403 means "you may not move the server's active project" — it is
        // shared state, and members are refused on this admin-only route. Looking at another project is not that: reads resolve
        // ?project= per request, so this browser scopes itself and browses.
        // Creation cannot be scoped around; a refused create stays refused.
        if (create || !(refusal instanceof AuthError) || refusal.status !== 403) {
          throw refusal;
        }
        setProjectOverride(name);
      }
      clearUndo();
      // Different project: tabs belong to the old one, and so does every
      // in-flight operation badge.
      invalidateGenerations();
      set({ tabs: [], pageDocs: {}, activeTab: null, activeProject: name, operations: {} });
      await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
      get().settleOperation(scope, {
        status: "saved",
        operation: "workspace.switchProject",
      });
    } catch (e) {
      const label = get().projects.find((project) => project.name === name)?.label || name;
      const unreadable = unreadableGraphMessage(e, label, get().preferences.language);
      get().settleOperation(scope, {
        status: "error",
        operation: "workspace.switchProject",
        // The banner explains an unreadable graph.yaml; the badge only says
        // the switch failed, so the bar does not repeat the explanation.
        message: unreadable
          ? translate("workspace.error.switchFailed")
          : describeFailure(e, translate("workspace.error.switchFailed")),
      });
      if (unreadable) throw new Error(unreadable, { cause: e });
      throw e;
    } finally {
      switchesInFlight -= 1;
    }
  },
});

/**
 * The hub's own sentence around the parser's or the validator's words
 * (store/graph_unreadable.go), in either language: "line 3 has an error. Fix
 * the file, then reload. (yaml: ...)". The banner gives its own advice, so
 * only the bracketed words after the last sentence are kept.
 */
const HUB_GRAPH_FRAME = /^[^(\uFF08]*[.\u3002] ?[(\uFF08]([\s\S]*)[)\uFF09]$/;

/**
 * The hub refuses to open a project whose graph.yaml does not parse, and says
 * so with the YAML parser's English kept ('load project "x": graph.yaml: line
 * 16 has an error. Fix the file, then reload. (yaml: unmarshal errors: line
 * 16: ...)'). The reader gets that in their language, keeping the line to fix.
 * A file that parses but fails validation ('graph.yaml: ... (node "c" has
 * invalid priority "zzz")') is readable: the reader gets the reason, which
 * names the node and field to fix. Null for any other failure.
 */
function unreadableGraphMessage(error: unknown, label: string, language: Language): string | null {
  if (!(error instanceof RequestError)) return null;
  const said = /(?:^|[:\uFF1A] ?)graph\.yaml: ([\s\S]*)$/.exec(error.message)?.[1].trim();
  if (said === undefined) return null;
  const reason = (HUB_GRAPH_FRAME.exec(said)?.[1] ?? said).trim();
  // engine.ParseGraph: the decoder's own "yaml: ..." and its one other refusal.
  if (!reason || /^yaml: |^multiple YAML documents/.test(reason)) {
    const line = /\bline (\d+)\b/.exec(reason)?.[1];
    return line
      ? translate("workspace.error.graphUnreadableLine", language, { name: label, line })
      : translate("workspace.error.graphUnreadable", language, { name: label });
  }
  // The frame ends the sentence itself; a localized finding brings its own 。
  const localized = localizeGraphRefusal(reason, language).replace(/[\u3002.]$/, "");
  return translate("workspace.error.graphInvalid", language, { name: label, reason: localized });
}

function sameJSON(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

// Re-exported so tests can assert the guard without reaching into internals.
export { generations };
