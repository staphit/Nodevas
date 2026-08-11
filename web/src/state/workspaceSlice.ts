/**
 * Workspace / project slice [A-05].
 *
 * Switching a project is the one place where every other slice has to be told
 * "that was the old project": unsaved documents are parked as drafts, pending
 * writes are drained, generations are bumped so late responses are dropped, and
 * tabs, undo history and operation badges are cleared.
 */

import { api, AuthError, setProjectOverride } from "../api";
import { coalesceRequest } from "./coalesce";
import { describeFailure, operationScope } from "./operations";
import { drainWrites, generations, invalidateGenerations } from "./internals";
import { clearUndo } from "./undo";
import type { AppSlice, AppState, WorkspaceSlice } from "./types";

/** Never lose unsaved text on a switch: park it as a draft first. */
async function parkDirtyTabs(get: () => AppState): Promise<void> {
  const dirtyTabs = get().tabs.filter((tab) => tab.dirty);
  await Promise.all(
    dirtyTabs.map((tab) => api.putDraft(tab.id, tab.content).catch(() => undefined)),
  );
  await drainWrites();
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
  refreshProjects: () =>
    coalesceRequest("projects", async () => {
      const p = await api.getProjects();
      set({
        workspace: p.workspace,
        workspaces: p.workspaces,
        activeProject: p.active,
        projects: p.projects,
      });
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
        message: describeFailure(e, "儲存專案排序失敗"),
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
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
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
    clearUndo();
    invalidateGenerations();
    set({ tabs: [], activeTab: null, workspace: path, activeProject: "", operations: {} });
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
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
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
    clearUndo();
    invalidateGenerations();
    set({
      tabs: [],
      activeTab: null,
      workspace: result.workspace ?? path,
      activeProject: result.active ?? "",
      operations: {},
    });
    await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
    return { name: result.name, workspace: result.workspace ?? path };
  },

  switchProject: async (name, create = false, template = "") => {
    const scope = operationScope.workspace();
    get().beginOperation(scope, "workspace.switchProject");
    try {
      await parkDirtyTabs(get);
      try {
        await api.openProject(name, create, template);
        // The server's active project moved; per-request scoping would now
        // shadow it with something stale.
        setProjectOverride("");
      } catch (refusal) {
        // 403 means "you may not move the server's active project" — it is
        // shared state, and visitors (and members, on this admin-only route)
        // are refused. Looking at another project is not that: reads resolve
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
      set({ tabs: [], activeTab: null, activeProject: name, operations: {} });
      await Promise.all([get().loadAll(), get().refreshTrash(), get().refreshProjects()]);
      get().settleOperation(scope, {
        status: "saved",
        operation: "workspace.switchProject",
      });
    } catch (e) {
      get().settleOperation(scope, {
        status: "error",
        operation: "workspace.switchProject",
        message: describeFailure(e, "切換專案失敗"),
      });
      throw e;
    }
  },
});

// Re-exported so tests can assert the guard without reaching into internals.
export { generations };
