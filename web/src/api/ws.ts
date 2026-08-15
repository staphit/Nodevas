import type { Actor } from "./auth";
import type { GraphOp } from "./graph";

/** Someone else connected to the same project. */
export interface Peer {
  id: string;
  actor: Actor;
  nodeId?: string;
  editing?: boolean;
  /** Chosen by the server, so everyone sees the same person in the same colour. */
  color?: string;
}

/** A selection somebody is dragging right now. Nothing is written until they drop it. */
export interface DragGhost {
  ids: string[];
  dx: number;
  dy: number;
  active: boolean;
}

/** A document someone holds open for editing. */
export interface NodeLock {
  nodeId: string;
  actor: Actor;
  since: string;
  /** True when this browser is the holder. */
  mine?: boolean;
}

export type WSEvent = {
  type:
    | "doc-state"
    | "doc-update"
    | "doc-leader"
    | "doc-flushed"
    | "doc-compact"
    | "doc-snapshot-start"
    | "doc-snapshot-chunk"
    | "doc-snapshot-commit"
    | "doc-snapshot-ready"
    | "doc-snapshot-accepted"
    | "doc-snapshot-rejected"
    | "doc-reopen"
    | "doc-persistence-error"
    | "doc-persistence-restored"
    | "graph-ops"
    | "graph-drag"
    | "awareness"
    | "graph-changed"
    | "node-changed"
    | "state-changed"
    | "folders-changed"
    | "project-changed"
    | "hello"
    | "presence"
    | "locks"
    | "lock-denied";
  id?: string;
  project?: string;
  peers?: Peer[];
  locks?: NodeLock[];
  actor?: Actor;
  selfId?: string;
  /** Which connection caused this, so its own browser can skip the echo. */
  from?: string;
  /** An opaque awareness update, relayed by the server without being read. */
  payload?: string;
  drag?: DragGhost;
  ops?: GraphOp[];
  /** doc-state / doc-leader: whether this client writes the file. */
  leader?: boolean;
  /** doc-state: this client is the first one here and must load the file. */
  seed?: boolean;
  /** doc-flushed: the revision a leader just wrote. */
  rev?: string;
  /** doc-compact: opaque decimal barrier token chosen by the server. */
  token?: string;
  /** Chunked snapshot envelope fields. */
  size?: number;
  chunks?: number;
  seq?: number;
};

/** The live socket, with the collaboration messages this client can send. */
export interface LiveConnection {
  close: () => void;
  /** Names the project whose events this client wants. */
  subscribe: (project: string) => void;
  /** Reports which document is on screen, and whether it is being edited. */
  presence: (nodeId: string, editing: boolean) => void;
  /** Takes the soft lock on a document; steal overrides another holder. */
  lock: (nodeId: string, steal?: boolean) => void;
  unlock: (nodeId: string) => void;
  /** Relays an opaque awareness update — cursors and selections — to the room. */
  awareness: (payload: string) => void;
  /**
   * Reports where a selection currently sits under this pointer. Sent while the
   * gesture runs and once more, inactive, when it ends; never persisted.
   */
  graphDrag: (ids: string[], dx: number, dy: number, active: boolean) => void;

  /**
   * Live-document session [P2]. `docOpen` joins the session for one document
   * and is replayed after a reconnect — a dropped socket otherwise leaves the
   * browser editing a document the server no longer knows it has open.
   */
  docOpen: (nodeId: string, pageId?: string) => void;
  docClose: (nodeId: string, pageId?: string) => void;
  docUpdate: (nodeId: string, pageId: string | undefined, payload: string) => void;
  docCompactRequest: (nodeId: string, pageId?: string) => void;
  docSnapshotStart: (
    nodeId: string,
    pageId: string | undefined,
    token: string,
    size: number,
    chunks: number,
  ) => void;
  docSnapshotChunk: (
    nodeId: string,
    pageId: string | undefined,
    token: string,
    seq: number,
    payload: string,
  ) => void;
  docSnapshotCommit: (nodeId: string, pageId: string | undefined, token: string) => void;
  docSnapshotAbort: (
    nodeId: string,
    pageId: string | undefined,
    token: string,
    reason: "too-large",
  ) => void;
  /** The leader reporting the file revision it wrote. */
  docFlushed: (nodeId: string, pageId: string | undefined, rev: string) => void;
}

/**
 * Names one live document the way the server names it.
 *
 * Wire format rather than a local convention: this string is the `id` on every
 * `doc-*` event, so a browser that builds it differently silently receives
 * nothing at all.
 */
export function documentKey(nodeId: string, pageId?: string): string {
  return pageId ? `${nodeId}/${pageId}` : nodeId;
}

/** Opens the live-update socket; reconnects with backoff. */
export function connectWS(onEvent: (ev: WSEvent) => void): LiveConnection {
  let ws: WebSocket | null = null;
  let closed = false;
  let retry = 500;
  // What this client asked for, replayed after a reconnect: a dropped socket
  // otherwise leaves the browser in no room and holding no lock, silently.
  let project = "";
  let presenceState: { nodeId: string; editing: boolean } | null = null;
  const heldLocks = new Set<string>();
  // Keyed the way the server keys a session, so a reconnect reopens exactly
  // what was open: a node's main document and each of its subpages are
  // separate sessions.
  const openDocs = new Map<string, { nodeId: string; pageId?: string }>();

  const post = (message: Record<string, unknown>) => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message));
  };

  const open = () => {
    if (closed) return;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws`);
    ws.onmessage = (m) => {
      try {
        onEvent(JSON.parse(m.data));
      } catch {
        /* ignore malformed */
      }
    };
    ws.onopen = () => {
      retry = 500;
      if (project) post({ type: "subscribe", project });
      if (presenceState) post({ type: "presence", ...presenceState });
      for (const nodeId of heldLocks) post({ type: "lock", nodeId });
      // Rejoining is not enough on its own: the server treats a reopened
      // session as a new participant and will say whether to seed.
      for (const doc of openDocs.values()) post({ type: "doc-open", ...doc });
    };
    ws.onclose = () => {
      if (!closed) setTimeout(open, (retry = Math.min(retry * 2, 10000)));
    };
  };
  open();
  return {
    close: () => {
      closed = true;
      ws?.close();
    },
    subscribe: (next: string) => {
      project = next;
      // A room change drops the locks the server held for this client.
      heldLocks.clear();
      openDocs.clear();
      presenceState = null;
      post({ type: "subscribe", project });
    },
    presence: (nodeId: string, editing: boolean) => {
      presenceState = { nodeId, editing };
      post({ type: "presence", nodeId, editing });
    },
    lock: (nodeId: string, steal = false) => {
      heldLocks.add(nodeId);
      post({ type: "lock", nodeId, steal });
    },
    unlock: (nodeId: string) => {
      heldLocks.delete(nodeId);
      post({ type: "unlock", nodeId });
    },
    // Neither of these is replayed after a reconnect: both describe where a
    // pointer is *now*, and a stale one would draw a cursor that has moved.
    awareness: (payload: string) => post({ type: "awareness", payload }),
    graphDrag: (ids: string[], dx: number, dy: number, active: boolean) =>
      post({ type: "graph-drag", ids, dx, dy, active }),
    docOpen: (nodeId: string, pageId?: string) => {
      openDocs.set(documentKey(nodeId, pageId), { nodeId, pageId });
      post({ type: "doc-open", nodeId, pageId });
    },
    docClose: (nodeId: string, pageId?: string) => {
      openDocs.delete(documentKey(nodeId, pageId));
      post({ type: "doc-close", nodeId, pageId });
    },
    docUpdate: (nodeId: string, pageId: string | undefined, payload: string) =>
      post({ type: "doc-update", nodeId, pageId, payload }),
    docCompactRequest: (nodeId: string, pageId?: string) =>
      post({ type: "doc-compact-request", nodeId, pageId }),
    docSnapshotStart: (nodeId, pageId, token, size, chunks) =>
      post({ type: "doc-snapshot-start", nodeId, pageId, token, size, chunks }),
    docSnapshotChunk: (nodeId, pageId, token, seq, payload) =>
      post({ type: "doc-snapshot-chunk", nodeId, pageId, token, seq, payload }),
    docSnapshotCommit: (nodeId, pageId, token) =>
      post({ type: "doc-snapshot-commit", nodeId, pageId, token }),
    docSnapshotAbort: (nodeId, pageId, token, reason) =>
      post({ type: "doc-snapshot-abort", nodeId, pageId, token, reason }),
    docFlushed: (nodeId: string, pageId: string | undefined, rev: string) =>
      post({ type: "doc-flushed", nodeId, pageId, rev }),
  };
}
