import type { StatusDefinition } from "../types";
import { csrfToken, notifyUnauthorized, req, UnauthorizedError } from "./http";
import { verifyProjectsResponse, type ProjectsResponse } from "./verify";

/** File formats a document can be saved as, beyond the stored Markdown. */
export type ExportFormat = "md" | "txt" | "html" | "docx";
/** How much of the project one export covers. */
export type ExportScope = "page" | "node" | "project";

export interface ExportRequest {
  format: ExportFormat;
  scope: ExportScope;
  nodeId?: string;
  pageId?: string;
  /** The unsaved editor buffer, so an export matches what is on screen. */
  content?: string;
}

export interface ExportedFile {
  blob: Blob;
  name: string;
}

/**
 * Where an imported archive's contents land. "root" only means anything for a
 * whole-workspace bundle, whose projects are then restored side by side with
 * the ones already there — name clashes are renamed by the server, never
 * overwritten.
 */
export type ProjectImportMode = "root" | "folder";

/** Reads the download name the server chose, including UTF-8 filenames. */
export function fileNameFromDisposition(header: string | null): string {
  if (!header) return "";
  const encoded = /filename\*=\s*(?:UTF-8|utf-8)''([^;]+)/i.exec(header);
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1].trim());
    } catch {
      /* fall through to the plain form */
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header);
  return plain ? plain[1].trim() : "";
}

export const workspaceApi = {
  getTrash: () =>
    req<{
      items: {
        file: string;
        kind: "node" | "page";
        nodeId: string;
        parentId?: string;
        title?: string;
        at: string;
      }[];
    }>("/api/trash"),
  restoreTrash: (file: string) =>
    req<{ ok: boolean; id: string }>("/api/trash/restore", {
      method: "POST",
      body: JSON.stringify({ file }),
    }),

  getProjects: () =>
    req<ProjectsResponse>("/api/projects", undefined, verifyProjectsResponse),
  addWorkspace: (path: string) =>
    req<{ ok: boolean; path: string; label: string }>("/api/workspaces/add", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  openWorkspace: (path: string) =>
    req<{ ok: boolean; path: string }>("/api/workspaces/open", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  removeWorkspace: (path: string) =>
    req<{ ok: boolean; workspace: string }>("/api/workspaces/remove", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  // Workspace-wide lifecycle vocabulary; shared by every project in it.
  getWorkspaceStatuses: () =>
    req<{ customStatuses: StatusDefinition[] }>("/api/workspaces/statuses"),
  saveWorkspaceStatuses: (customStatuses: StatusDefinition[]) =>
    req<{ ok: boolean; customStatuses: StatusDefinition[] }>(
      "/api/workspaces/statuses",
      { method: "PUT", body: JSON.stringify({ customStatuses }) },
    ),
  // The explorer's manual project order. Advisory on purpose: the server stores
  // the names it was given without pruning, so a reply can still mention a
  // deleted project and can omit one created elsewhere.
  getProjectOrder: () =>
    req<{ projectOrder: string[] }>("/api/workspaces/order"),
  saveProjectOrder: (projectOrder: string[]) =>
    req<{ ok: boolean; projectOrder: string[] }>("/api/workspaces/order", {
      method: "PUT",
      body: JSON.stringify({ projectOrder }),
    }),
  createProjectFolder: (name: string) =>
    req<{ ok: boolean; name: string }>("/api/projects/folder", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  listDirs: (path = "") =>
    req<{
      path: string;
      parent: string;
      dirs: { name: string; path: string }[] | null;
    }>(`/api/fs/dirs?path=${encodeURIComponent(path)}`),
  createDir: (path: string, name: string) =>
    req<{ ok: boolean; path: string }>("/api/fs/mkdir", {
      method: "POST",
      body: JSON.stringify({ path, name }),
    }),
  openFolder: (path: string) =>
    req<{ ok: boolean; path: string }>("/api/fs/open", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  importProjectPath: (path: string, name = "") =>
    req<{ ok: boolean; name: string; files: number }>("/api/projects/import-path", {
      method: "POST",
      body: JSON.stringify({ path, name }),
    }),
  openProject: (name: string, create = false, template = "") =>
    req<{ root: string; active: string }>("/api/projects/open", {
      method: "POST",
      body: JSON.stringify({ name, create, template }),
    }),
  moveProject: (
    name: string,
    newParent: string,
    newName = "",
    newWorkspace = "",
  ) =>
    req<{ ok: boolean; name: string; active?: string; workspace?: string }>(
      "/api/projects/move",
      {
      method: "POST",
        body: JSON.stringify({ name, newParent, newName, newWorkspace }),
      },
    ),
  copyProject: (name: string, newName: string, newParent = "", open = true) =>
    req<{ ok: boolean; name: string; active: string; files: number }>(
      "/api/projects/copy",
      {
        method: "POST",
        body: JSON.stringify({ name, newName, newParent, open }),
      },
    ),
  removeProject: (name: string, mode: "detach" | "disk") =>
    req<{ ok: boolean; active: string; deletedFiles: boolean }>(
      "/api/projects/remove",
      {
        method: "POST",
        body: JSON.stringify({ name, mode }),
      },
    ),
  importProject: (
    file: File,
    name?: string,
    mode?: ProjectImportMode,
    parent?: string,
  ) => {
    const body = new FormData();
    body.append("file", file);
    if (name?.trim()) body.append("name", name.trim());
    // Absent means the workspace's top level, which is where every import
    // landed before a row in the tree could be named as the destination.
    if (parent?.trim()) body.append("parent", parent.trim());
    // Left off entirely rather than defaulted here: the server reads an absent
    // mode as "folder", so every caller that has no reason to ask the question
    // keeps the behaviour it has always had.
    if (mode) body.append("mode", mode);
    return req<{ root: string; active: string }>("/api/projects/import", {
      method: "POST",
      body,
    });
  },
  projectExportURL: (project?: string) =>
    project
      ? `/api/projects/export?project=${encodeURIComponent(project)}`
      : "/api/projects/export",
  /** Not routed through req(): the reply is a file, not JSON. The CSRF header
   * and the 401 handling req() would have applied are repeated here from the
   * same helpers, so an export behaves like every other call. */
  exportDocument: async (request: ExportRequest): Promise<ExportedFile> => {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    const token = csrfToken();
    if (token) headers["X-CSRF-Token"] = token;
    const response = await fetch("/api/export", {
      method: "POST",
      headers,
      body: JSON.stringify(request),
    });
    if (response.status === 401) {
      notifyUnauthorized();
      throw new UnauthorizedError();
    }
    if (!response.ok) {
      let message = `${response.status}`;
      try {
        const body = await response.json();
        if (body.error) message = body.error;
      } catch {
        /* keep the status */
      }
      throw new Error(message);
    }
    const blob = await response.blob();
    const name =
      fileNameFromDisposition(response.headers.get("Content-Disposition")) ||
      `document.${request.format}`;
    return { blob, name };
  },
  search: (query: string) =>
    req<{
      results: {
        project: string;
        nodeId?: string;
        title: string;
        snippet?: string;
        kind: "project" | "node";
      }[];
    }>(`/api/search?q=${encodeURIComponent(query)}`),
};
