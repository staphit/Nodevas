/**
 * The live document, client side [P2].
 *
 * One `Y.Doc` per open node, driven by the phase-2c document protocol: the hub
 * relays opaque Yjs updates, elects one leader to write the file, and asks that
 * leader for a snapshot when its log grows. Everything the browser has to get
 * right about that lives here.
 *
 * The transport is injected and the callbacks are plain functions, so this file
 * has no socket, no React and no DOM in it. That is not tidiness: the failures
 * this module exists to prevent — a whole-document replace that wipes a peer's
 * concurrent edit, a seed applied twice, a remote update echoed back — are all
 * invisible through an editor and obvious against a fake transport with two
 * sessions wired to each other.
 *
 * Two things are deliberately *not* here. Reconciling the CRDT text with the
 * file on disk is the caller's job (it owns autosave and the revision), and so
 * is the CodeMirror binding, which takes `ytext` and lives with the editor.
 */

import * as Y from "yjs";

/** How long local edits pile up before one merged update goes out. */
export const DOC_UPDATE_BATCH_MS = 50;

/**
 * `doc-update` payload ceiling from the protocol, in base64 characters.
 *
 * Yjs updates merge, so a batch is normally far under this; a paste is what
 * gets close. An update cannot be split — merging is the only lever and it only
 * makes things bigger — so what happens above the ceiling is `send` below.
 */
export const DOC_UPDATE_MAX_B64 = 48 * 1024;

/** Server contract: one snapshot transfer is at most 24 MiB of base64. */
export const DOC_SNAPSHOT_MAX_B64 = 24 * 1024 * 1024;

/** Chunks stay below the existing 64 KiB WebSocket frame ceiling. */
export const DOC_SNAPSHOT_CHUNK_B64 = 48 * 1024;

export const DOC_SNAPSHOT_MAX_CHUNKS = 512;

/** What `DocSession` needs from the socket. Payloads are base64. */
export interface DocTransport {
  open(nodeId: string): void;
  close(nodeId: string): void;
  update(nodeId: string, payload: string): void;
  requestCompact(nodeId: string): void;
  snapshotStart(nodeId: string, token: string, size: number, chunks: number): void;
  snapshotChunk(nodeId: string, token: string, seq: number, payload: string): void;
  snapshotCommit(nodeId: string, token: string): void;
  snapshotAbort(nodeId: string, token: string, reason: "too-large"): void;
  flushed(nodeId: string, rev: string): void;
}

interface DocSnapshotLimits {
  maxBytes: number;
  chunkBytes: number;
}

interface IncomingSnapshot {
  token: string;
  size: number;
  chunks: number;
  nextSeq: number;
  received: number;
  payloads: string[];
}

interface OutgoingSnapshot {
  token: string;
  payload: string;
  chunks: number;
}

const DEFAULT_SNAPSHOT_LIMITS: DocSnapshotLimits = {
  maxBytes: DOC_SNAPSHOT_MAX_B64,
  chunkBytes: DOC_SNAPSHOT_CHUNK_B64,
};

export interface DocSessionEvents {
  /** Told to seed: load the file and call `seed()` with its text. */
  onSeedNeeded: () => void;
  /** The text changed because of somebody else. */
  onRemoteText: (text: string) => void;
  onLeaderChange: (leader: boolean) => void;
  /** A leader wrote the file; this is the revision it produced. */
  onFlushed: (rev: string) => void;
}

/**
 * Transaction origins.
 *
 * `REMOTE` is the whole reason a relayed update does not bounce back out and
 * turn into a loop between two clients, and the reason a change somebody else
 * made can be told apart from one the local editor just made.
 */
const REMOTE = Symbol("remote");
const LOCAL = Symbol("local");

/** The single shared text; the key is part of the wire format between clients. */
const TEXT_KEY = "text";

export class DocSession {
  readonly ytext: Y.Text;

  private readonly doc: Y.Doc;
  private readonly pending: Uint8Array[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;
  private leader = false;
  private seeded = false;
  private destroyed = false;
  private compactRequested = false;
  private snapshotToken: string | null = null;
  private snapshotBlocked = false;
  private snapshotMayNeedReplay = false;
  private incomingSnapshot: IncomingSnapshot | null = null;
  private outgoingSnapshot: OutgoingSnapshot | null = null;
  private snapshotRetryTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly nodeId: string,
    private readonly transport: DocTransport,
    private readonly events: DocSessionEvents,
    private readonly snapshotLimits: DocSnapshotLimits = DEFAULT_SNAPSHOT_LIMITS,
  ) {
    this.doc = new Y.Doc();
    this.ytext = this.doc.getText(TEXT_KEY);

    this.doc.on("update", this.onDocUpdate);
    this.ytext.observe(this.onTextChange);

    // Joining is the constructor's business because a session that exists but
    // has not opened receives nothing and looks, from the caller's side, like a
    // document that simply never loads.
    this.transport.open(nodeId);
  }

  get text(): string {
    return this.ytext.toString();
  }

  get isLeader(): boolean {
    return this.leader;
  }

  /**
   * Puts the file's text into an empty document.
   *
   * Refused when anything is already in it. Seeding on top of stored updates is
   * the classic Yjs duplication bug — the file's text ends up in the document
   * twice, interleaved, and no later edit can undo it — and the server only
   * says `seed: true` to the first client in a session for exactly that reason.
   */
  seed(text: string): void {
    if (this.destroyed) return;
    if (this.seeded || this.ytext.length > 0) {
      console.warn(`[collab] refusing to seed ${this.nodeId}: already seeded`);
      return;
    }
    this.seeded = true;
    if (text.length === 0) return;
    this.doc.transact(() => this.ytext.insert(0, text), LOCAL);
    // Sent now rather than at the end of the batch window: until it arrives the
    // server has a session with no stored updates, so anyone who joins in the
    // meantime is told not to seed and sits on an empty document. Fifty
    // milliseconds is a small window and this is a whole document.
    this.flush();
  }

  /**
   * A local edit, given as the editor's whole new text.
   *
   * Applied as one delete plus one insert over the changed span. Clearing the
   * Y.Text and re-inserting would be far simpler and is the thing that must
   * never happen: to the CRDT it is "every character was deleted", so a peer
   * typing at the same moment loses their edit, every remote caret collapses to
   * the start, and the update carries the whole document instead of the one
   * character that changed.
   */
  setText(next: string): void {
    if (this.destroyed) return;
    const current = this.ytext.toString();
    if (current === next) return;
    this.seeded = true;

    const limit = Math.min(current.length, next.length);
    let start = 0;
    while (start < limit && current.charCodeAt(start) === next.charCodeAt(start)) start += 1;
    let end = 0;
    while (
      end < limit - start &&
      current.charCodeAt(current.length - 1 - end) === next.charCodeAt(next.length - 1 - end)
    ) {
      end += 1;
    }
    // A boundary that lands between the halves of a surrogate pair would ship a
    // lone surrogate and corrupt the emoji for everyone, so the split backs off
    // to the start of the pair.
    if (start > 0 && isLowSurrogate(current.charCodeAt(start))) start -= 1;
    if (end > 0 && isLowSurrogate(current.charCodeAt(current.length - end))) end -= 1;

    const removed = current.length - start - end;
    const inserted = next.slice(start, next.length - end);
    this.doc.transact(() => {
      if (removed > 0) this.ytext.delete(start, removed);
      if (inserted.length > 0) this.ytext.insert(start, inserted);
    }, LOCAL);
  }

  /** Reply to `doc-open`. */
  handleState(state: { leader: boolean; seed: boolean }): void {
    if (this.destroyed) return;
    this.setLeader(state.leader);
    // `seed: false` means the updates that follow *are* the document; asking for
    // the file here is how the duplication bug starts.
    if (state.seed && !this.seeded) this.events.onSeedNeeded();
    // A socket generation can disappear after the browser queued a compact
    // request or transfer but before the server committed it. The Y.Doc still
    // holds the unsent large update, so ask the replacement session for a fresh
    // barrier instead of assuming the old token survived reconnect.
    if (this.snapshotMayNeedReplay && !this.snapshotBlocked) {
      // doc-state belongs to a freshly opened server session. Tokens and upload
      // reservations are scoped to the socket that issued them, so neither a
      // start awaiting ready nor a commit awaiting accepted can survive here.
      this.snapshotToken = null;
      this.outgoingSnapshot = null;
      this.compactRequested = true;
      this.clearSnapshotRetry();
      this.transport.requestCompact(this.nodeId);
    }
  }

  handleUpdate(payload: string): void {
    if (this.destroyed) return;
    // Anything the server sends counts as content, seeded or not: a client that
    // joined second must never be told to seed afterwards.
    this.seeded = true;
    try {
      Y.applyUpdate(this.doc, fromBase64(payload), REMOTE);
    } catch (error) {
      // A malformed update from another browser must not take the editor down;
      // the CRDT converges again from the next one it can read.
      console.warn(`[collab] dropped an unreadable update for ${this.nodeId}`, error);
    }
  }

  handleLeader(leader: boolean): void {
    if (this.destroyed) return;
    this.setLeader(leader);
  }

  /**
   * A leader wrote the file.
   *
   * `from` is carried by the protocol but unused: the hub does not echo the
   * event to its sender, so anything that arrives here was written by somebody
   * else and is a revision this client has to adopt rather than treat as an
   * external change.
   */
  handleFlushed(rev: string, _from: string): void {
    if (this.destroyed) return;
    this.events.onFlushed(rev);
  }

  /** The stored log got long; replace its barriered prefix with this full state. */
  handleCompact(token: string): void {
    if (this.destroyed || !/^[1-9]\d*$/.test(token)) return;
    if (this.snapshotToken === token) return;
    this.compactRequested = false;
    this.snapshotMayNeedReplay = true;
    this.snapshotToken = token;
    // Every local update already lives in the Y.Doc. Clear the pending wire batch
    // at the exact encoding barrier: it is represented by this snapshot, while
    // edits made after encoding remain queued until the server acknowledges it.
    this.clearTimer();
    this.pending.splice(0, this.pending.length);
    const payload = toBase64(Y.encodeStateAsUpdate(this.doc));
    if (payload.length > this.snapshotLimits.maxBytes) {
      // Encoding does not mutate the Y.Doc. Keep the full local state available
      // for save/export, tell the server to hold the session fail-closed, and let
      // its generic persistence event drive the sticky warning.
      this.snapshotBlocked = true;
      this.transport.snapshotAbort(this.nodeId, token, "too-large");
      this.snapshotToken = null;
      return;
    }
    const chunks = Math.ceil(payload.length / this.snapshotLimits.chunkBytes);
    this.outgoingSnapshot = { token, payload, chunks };
    this.transport.snapshotStart(this.nodeId, token, payload.length, chunks);
  }

  /** The server reserved memory and authorized this token's chunk stream. */
  handleSnapshotReady(token: string): void {
    const outgoing = this.outgoingSnapshot;
    if (
      this.destroyed ||
      outgoing === null ||
      outgoing.token !== token ||
      this.snapshotToken !== token
    )
      return;
    const { payload, chunks } = outgoing;
    for (let seq = 0; seq < chunks; seq += 1) {
      const start = seq * this.snapshotLimits.chunkBytes;
      this.transport.snapshotChunk(
        this.nodeId,
        token,
        seq,
        payload.slice(start, start + this.snapshotLimits.chunkBytes),
      );
    }
    this.transport.snapshotCommit(this.nodeId, token);
    this.outgoingSnapshot = null;
  }

  /** The server durably accepted this exact barrier; dependent deltas may resume. */
  handleSnapshotAccepted(token: string): void {
    if (this.destroyed || this.snapshotToken !== token) return;
    this.snapshotToken = null;
    this.outgoingSnapshot = null;
    this.snapshotBlocked = false;
    this.snapshotMayNeedReplay = false;
    this.compactRequested = false;
    this.clearSnapshotRetry();
    this.flush();
  }

  /** A rejected transfer keeps local state and retries on this socket. */
  handleSnapshotRejected(token: string): void {
    if (this.destroyed || (token !== "" && this.snapshotToken !== token)) return;
    this.snapshotToken = null;
    this.outgoingSnapshot = null;
    this.compactRequested = false;
    if (!this.snapshotMayNeedReplay || this.snapshotBlocked) return;
    this.scheduleSnapshotRetry();
  }

  /** Begins one bounded full-state relay from another participant. */
  handleSnapshotStart(token: string, size: number, chunks: number): void {
    // A new start supersedes any abandoned transfer, including a malformed one.
    this.incomingSnapshot = null;
    if (
      this.destroyed ||
      !/^[1-9]\d*$/.test(token) ||
      !Number.isSafeInteger(size) ||
      size <= 0 ||
      size > this.snapshotLimits.maxBytes ||
      !Number.isSafeInteger(chunks) ||
      chunks !== Math.ceil(size / this.snapshotLimits.chunkBytes) ||
      chunks > DOC_SNAPSHOT_MAX_CHUNKS
    ) {
      return;
    }
    this.incomingSnapshot = {
      token,
      size,
      chunks,
      nextSeq: 0,
      received: 0,
      payloads: [],
    };
  }

  /** Accepts chunks only in the strict order promised by snapshot-start. */
  handleSnapshotChunk(token: string, seq: number, payload: string): void {
    const incoming = this.incomingSnapshot;
    const expectedSize = incoming
      ? Math.min(this.snapshotLimits.chunkBytes, incoming.size - seq * this.snapshotLimits.chunkBytes)
      : 0;
    if (
      this.destroyed ||
      incoming === null ||
      incoming.token !== token ||
      seq !== incoming.nextSeq ||
      payload.length !== expectedSize ||
      incoming.received + payload.length > incoming.size
    ) {
      this.incomingSnapshot = null;
      return;
    }
    incoming.payloads.push(payload);
    incoming.received += payload.length;
    incoming.nextSeq += 1;
  }

  /** Applies a complete snapshot atomically; an incomplete relay changes nothing. */
  handleSnapshotCommit(token: string): void {
    const incoming = this.incomingSnapshot;
    this.incomingSnapshot = null;
    if (
      this.destroyed ||
      incoming === null ||
      incoming.token !== token ||
      incoming.nextSeq !== incoming.chunks ||
      incoming.received !== incoming.size
    ) {
      return;
    }
    this.handleUpdate(incoming.payloads.join(""));
  }

  /** Reports the revision this client's own write produced. */
  reportFlushed(rev: string): void {
    if (this.destroyed) return;
    this.transport.flushed(this.nodeId, rev);
  }

  destroy(): void {
    if (this.destroyed) return;
    // The last keystrokes are still sitting in the batch window; closing without
    // sending them loses work that the editor already showed as typed.
    this.flush();
    this.teardown();
  }

  /**
   * Tears down a session the server explicitly rejected.
   *
   * Unlike a normal unmount, none of its pending updates may be flushed: the
   * server already told every participant to return to durable state, and
   * sending one more update can overflow the replacement session as well.
   */
  abort(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.pending.splice(0, this.pending.length);
    this.teardown();
  }

  private teardown(): void {
    // abort marks this first so no queued callback can publish while the
    // observers and transport are being detached. A normal destroy gets here
    // only after its intentional final flush.
    this.destroyed = true;
    this.incomingSnapshot = null;
    this.snapshotToken = null;
    this.outgoingSnapshot = null;
    this.clearSnapshotRetry();
    this.clearTimer();
    this.doc.off("update", this.onDocUpdate);
    this.ytext.unobserve(this.onTextChange);
    this.transport.close(this.nodeId);
    this.doc.destroy();
  }

  private setLeader(leader: boolean): void {
    if (this.leader === leader) return;
    this.leader = leader;
    this.events.onLeaderChange(leader);
  }

  private readonly onDocUpdate = (update: Uint8Array, origin: unknown) => {
    // Relayed updates are already everybody's; sending them back is a loop.
    if (origin === REMOTE) return;
    this.pending.push(update);
    if (this.timer === null) {
      this.timer = setTimeout(() => {
        this.timer = null;
        this.flush();
      }, DOC_UPDATE_BATCH_MS);
    }
  };

  private readonly onTextChange = (_event: Y.YTextEvent, transaction: Y.Transaction) => {
    // The editor already has its own text; telling it again would fight the
    // cursor. Only somebody else's change is news.
    if (transaction.origin !== REMOTE) return;
    this.events.onRemoteText(this.ytext.toString());
  };

  /**
   * Sends what has piled up as one message.
   *
   * A keystroke is its own Yjs update, and one socket frame per keystroke is
   * both the traffic and the wake-up cost of every other client in the session.
   * `Y.mergeUpdates` collapses a burst into the one update it amounts to.
   */
  private flush(): void {
    this.clearTimer();
    if (this.snapshotMayNeedReplay) {
      if (!this.snapshotBlocked && this.snapshotToken === null && !this.compactRequested) {
        this.compactRequested = true;
        this.transport.requestCompact(this.nodeId);
      }
      return;
    }
    if (this.pending.length === 0) return;
    const batch = this.pending.splice(0, this.pending.length);
    this.send(batch);
  }

  private send(batch: Uint8Array[]): void {
    if (this.snapshotMayNeedReplay) return;
    const payload = toBase64(batch.length === 1 ? batch[0] : Y.mergeUpdates(batch));
    if (payload.length <= DOC_UPDATE_MAX_B64) {
      this.transport.update(this.nodeId, payload);
      return;
    }
    if (batch.length > 1) {
      // Merging made it too big, so the batch is split instead. Order is kept:
      // Yjs tolerates updates arriving out of order, peers' undo history does not.
      const half = Math.ceil(batch.length / 2);
      this.send(batch.slice(0, half));
      this.send(batch.slice(half));
      return;
    }
    // One update over the ceiling, which a large paste produces and which
    // nothing can divide. Ask the server for a barrier before encoding the whole
    // state: replacing its log without that barrier can discard a peer update
    // accepted while the snapshot was in flight.
    if (!this.snapshotBlocked && this.snapshotToken === null && !this.compactRequested) {
      this.compactRequested = true;
      this.snapshotMayNeedReplay = true;
      this.transport.requestCompact(this.nodeId);
    }
  }

  private clearTimer(): void {
    if (this.timer === null) return;
    clearTimeout(this.timer);
    this.timer = null;
  }

  private scheduleSnapshotRetry(): void {
    if (this.snapshotRetryTimer !== null) return;
    this.snapshotRetryTimer = setTimeout(() => {
      this.snapshotRetryTimer = null;
      if (
        this.destroyed ||
        this.snapshotBlocked ||
        !this.snapshotMayNeedReplay ||
        this.snapshotToken !== null ||
        this.compactRequested
      )
        return;
      this.compactRequested = true;
      this.transport.requestCompact(this.nodeId);
    }, 1_000);
  }

  private clearSnapshotRetry(): void {
    if (this.snapshotRetryTimer === null) return;
    clearTimeout(this.snapshotRetryTimer);
    this.snapshotRetryTimer = null;
  }
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** Chunked because spreading a paste-sized update overflows the argument list. */
export function toBase64(bytes: Uint8Array): string {
  const CHUNK = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(i, i + CHUNK));
  }
  return btoa(binary);
}

export function fromBase64(payload: string): Uint8Array {
  const binary = atob(payload);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
