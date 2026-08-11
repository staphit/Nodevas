import { req } from "./http";

export const foldersApi = {
  /**
   * Node folders organise the sidebar only: they move files inside nodes/ and
   * never touch graph.yaml, so edges, requires and logic gates are unaffected.
   */
  getNodeFolders: () =>
    req<{ folders: string[]; nodes: Record<string, string> }>("/api/folders"),
  createNodeFolder: (path: string) =>
    req<{ ok: boolean; path: string }>("/api/folders", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  renameNodeFolder: (path: string, name: string) =>
    req<{ ok: boolean; path: string }>("/api/folders/rename", {
      method: "POST",
      body: JSON.stringify({ path, name }),
    }),
  moveNodeFolder: (path: string, parent: string) =>
    req<{ ok: boolean; path: string }>("/api/folders/move", {
      method: "POST",
      body: JSON.stringify({ path, parent }),
    }),
  /** Removes the folder and lifts whatever it held into the parent folder. */
  deleteNodeFolder: (path: string) =>
    req<{ ok: boolean }>("/api/folders/delete", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  moveNodesToFolder: (ids: string[], folder: string) =>
    req<{ ok: boolean }>("/api/nodes/folder", {
      method: "POST",
      body: JSON.stringify({ ids, folder }),
    }),
};
