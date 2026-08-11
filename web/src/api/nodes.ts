import { req } from "./http";
import { verifyNodeResponse } from "./verify";

export interface NodePageInfo {
  id: string;
  title: string;
  /** The file the page is stored as; missing means Markdown. */
  format?: PageFormat;
}

/** What a subpage is on disk. The editor always edits text. */
export type PageFormat = "md" | "txt" | "html" | "docx";

/** Copy leaves the originals; cut moves them to the source project's trash. */
export type NodeTransferMode = "copy" | "cut";

export interface NodeTransferRequest {
  ids: string[];
  /** Project name the nodes land in. */
  target: string;
  mode: NodeTransferMode;
  /** Project the nodes come from; defaults to the one this client shows. */
  source?: string;
}

export interface NodeTransferResult {
  ok: boolean;
  mode: NodeTransferMode;
  /** Source node id -> the id it was given in the target project. */
  ids: Record<string, string>;
  /** The new ids in source-graph order. */
  order: string[];
  /** What could not travel — dropped, not failed. */
  warnings?: string[];
  trashFiles?: string[];
  /** Authoritative delete committed; server will retry local artifact cleanup. */
  cleanupPending?: boolean;
}

export const nodesApi = {
  getNode: (id: string) =>
    req<{ id: string; content: string; rev: string }>(
      `/api/nodes/${encodeURIComponent(id)}`,
      undefined,
      verifyNodeResponse,
    ),
  /**
   * Writes a node's markdown. The response carries the composed file as well as
   * its revision — the server rewrites the frontmatter from the graph, so a
   * client that kept what it sent would drift — which is what lets an
   * auto-saving editor stay in step without re-reading after every save.
   */
  putNode: (
    id: string,
    content: string,
    baseRev: string,
    options: { keepalive?: boolean } = {},
  ) =>
    req<{ ok: boolean; rev: string; content?: string }>(`/api/nodes/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ content, baseRev }),
      // `keepalive` is only asked for by the unload save, where the document is
      // outliving the page that is writing it. It caps the body at 64 KB, so it
      // is never the default: a long document would fail the one save the user
      // has no chance to retry.
      ...(options.keepalive ? { keepalive: true } : {}),
    }),
  listNodePages: (id: string) =>
    req<{ pages: NodePageInfo[] }>(
      `/api/nodes/${encodeURIComponent(id)}/pages`,
    ),
  createNodePage: (id: string, title: string, format: PageFormat = "md") =>
    req<{ page: NodePageInfo; content: string; rev: string }>(
      `/api/nodes/${encodeURIComponent(id)}/pages`,
      { method: "POST", body: JSON.stringify({ title, format }) },
    ),
  importNodePage: (id: string, file: File, title = "") => {
    const body = new FormData();
    body.append("file", file);
    if (title.trim()) body.append("title", title.trim());
    return req<{ page: NodePageInfo; content: string; rev: string }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/import`,
      { method: "POST", body },
    );
  },
  convertNodePage: (id: string, pageID: string, format: PageFormat) =>
    req<{ ok: boolean; pages: NodePageInfo[]; content: string; rev: string }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageID)}`,
      { method: "PATCH", body: JSON.stringify({ format }) },
    ),
  getNodePage: (id: string, pageID: string) =>
    req<{
      nodeId: string;
      pageId: string;
      format: PageFormat;
      content: string;
      rev: string;
    }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageID)}`,
    ),
  putNodePage: (
    id: string,
    pageID: string,
    content: string,
    baseRev: string,
  ) =>
    req<{ ok: boolean; rev: string }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageID)}`,
      {
        method: "PUT",
        body: JSON.stringify({ content, baseRev }),
      },
    ),
  updateNodePage: (id: string, pageID: string, title: string, index?: number) =>
    req<{ ok: boolean; pages: NodePageInfo[] }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageID)}`,
      {
        method: "PATCH",
        body: JSON.stringify({ title, ...(index === undefined ? {} : { index }) }),
      },
    ),
  deleteNodePage: (id: string, pageID: string) =>
    req<{ ok: boolean; trashFile: string; cleanupPending?: boolean }>(
      `/api/nodes/${encodeURIComponent(id)}/pages/${encodeURIComponent(pageID)}`,
      { method: "DELETE" },
    ),
  uploadNodeFile: (id: string, file: File) => {
    const body = new FormData();
    body.append("file", file);
    return req<{ ok: boolean; name: string; url: string }>(
      `/api/nodes/${encodeURIComponent(id)}/files`,
      { method: "POST", body },
    );
  },
  createNode: (node: { id?: string; title?: string; kind?: string }, body = "") =>
    req<{ ok: boolean; id: string }>("/api/nodes", {
      method: "POST",
      body: JSON.stringify({ ...node, body }),
    }),
  duplicateNode: (id: string) =>
    req<{ ok: boolean; id: string }>(
      `/api/nodes/${encodeURIComponent(id)}/duplicate`,
      { method: "POST" },
    ),
  deleteNode: (id: string) =>
    req<{ ok: boolean; trashFile: string; cleanupPending?: boolean }>(`/api/nodes/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  /** One graph write for the whole selection, so it cannot half-delete. */
  deleteNodes: (ids: string[]) =>
    req<{ ok: boolean; trashFiles: string[]; cleanupPending?: boolean }>("/api/nodes/delete", {
      method: "POST",
      body: JSON.stringify({ ids }),
    }),
  /**
   * Copies or moves a node selection into another project. `source` names the
   * project the nodes come from; leaving it out means the one this client is
   * showing, which is what a plain "copy to…" does. A paste sends it, because
   * by then the client is showing the target.
   */
  transferNodes: (request: NodeTransferRequest) =>
    req<NodeTransferResult>("/api/nodes/transfer", {
      method: "POST",
      body: JSON.stringify({
        ids: request.ids,
        target: request.target,
        mode: request.mode,
        ...(request.source ? { source: request.source } : {}),
      }),
    }),

  getDraft: (id: string) =>
    req<{ exists: boolean; content?: string }>(`/api/drafts/${encodeURIComponent(id)}`),
  putDraft: (id: string, content: string) =>
    req<{ ok: boolean }>(`/api/drafts/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ content }),
    }),
  deleteDraft: (id: string) =>
    req<{ ok: boolean }>(`/api/drafts/${encodeURIComponent(id)}`, { method: "DELETE" }),

  // Every node in the workspace, for the link picker.
  getNodeIndex: () =>
    req<{ nodes: { project: string; nodeId: string; title?: string }[] }>(
      "/api/links/targets",
    ),

  // Node links: what points at this node, and where its own links go.
  getNodeLinks: (project: string, node: string) =>
    req<{
      backlinks: {
        fromProject: string;
        fromNode: string;
        fromTitle?: string;
        toProject: string;
        toNode: string;
        label?: string;
      }[];
      outgoing: {
        fromProject: string;
        fromNode: string;
        toProject: string;
        toNode: string;
        label?: string;
        missing: boolean;
      }[];
    }>(
      `/api/links?project=${encodeURIComponent(project)}&node=${encodeURIComponent(node)}`,
    ),
};
