/**
 * Node folder slice [B-06].
 *
 * Folders organise the explorer, nothing else. The server moves files inside
 * nodes/ and never opens graph.yaml, so this slice deliberately holds no graph
 * state and never invalidates one: renaming a folder cannot change an edge.
 *
 * The layout is disk truth, not a document, so every mutation re-reads it
 * rather than patching a local copy — that also heals a folder someone moved
 * in Explorer or through git.
 */

import { api } from "../api";
import { coalesceRequest } from "./coalesce";
import { generations } from "./internals";
import type { AppSlice, NodeFolderSlice } from "./types";

export const createFolderSlice: AppSlice<NodeFolderSlice> = (set, get) => ({
  nodeFolders: [],
  nodeFolderOf: {},

  // Every folder mutation re-reads the layout and the server broadcasts
  // `folders-changed` for the same write, so the two share one read.
  refreshNodeFolders: () =>
    coalesceRequest("folders", async () => {
      const generation = generations.load;
      const result = await api.getNodeFolders();
      // A project switch while this was in flight would leave the previous
      // project's folders on screen.
      if (generation !== generations.load) return;
      set({ nodeFolders: result.folders, nodeFolderOf: result.nodes });
    }),

  createNodeFolder: async (path) => {
    await api.createNodeFolder(path);
    await get().refreshNodeFolders();
  },

  renameNodeFolder: async (path, name) => {
    await api.renameNodeFolder(path, name);
    await get().refreshNodeFolders();
  },

  moveNodeFolder: async (path, parent) => {
    await api.moveNodeFolder(path, parent);
    await get().refreshNodeFolders();
  },

  deleteNodeFolder: async (path) => {
    await api.deleteNodeFolder(path);
    await get().refreshNodeFolders();
  },

  moveNodesToFolder: async (ids, folder) => {
    await api.moveNodesToFolder(ids, folder);
    await get().refreshNodeFolders();
  },
});

/** Direct children of a folder ("" is the root of nodes/). */
export function childFolders(folders: string[], parent: string): string[] {
  const prefix = parent ? `${parent}/` : "";
  return folders.filter(
    (folder) =>
      folder.startsWith(prefix) && !folder.slice(prefix.length).includes("/"),
  );
}

/** True when `candidate` is `folder` itself or sits inside it. */
export function isInsideFolder(candidate: string, folder: string): boolean {
  return candidate === folder || candidate.startsWith(`${folder}/`);
}
