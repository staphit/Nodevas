/**
 * What other people are pointing at [P2].
 *
 * The socket relays an opaque `awareness` payload between clients; this is what
 * this app puts in it. One shape covers both surfaces — a caret in a document
 * and a pointer on the board — because they are the same question asked twice,
 * and because a peer who is doing neither should cost one message that clears
 * both.
 *
 * The payload is JSON today. When the editor moves to a CRDT the transport
 * becomes y-protocols' awareness encoding, but `PeerPresence` is what the UI
 * reads either way, so only `encodePresence`/`decodePresence` change.
 */

/** Where somebody's caret sits, as document offsets. */
export interface PresenceSelection {
  anchor: number;
  head: number;
}

/** A point on the graph board, in the same offset-free space snapping uses. */
export interface PresencePoint {
  x: number;
  y: number;
}

export interface PresenceState {
  /** The document this person has open, if any. */
  nodeId?: string;
  /** The subpage inside it, so two people on different pages do not collide. */
  pageId?: string;
  selection?: PresenceSelection;
  pointer?: PresencePoint;
}

/** A peer's state plus who they are, which comes from the presence list. */
export interface PeerPresence extends PresenceState {
  peerId: string;
  name: string;
  color: string;
}

const MAX_PAYLOAD = 4096;

export function encodePresence(state: PresenceState): string {
  return JSON.stringify(state);
}

/**
 * Reads a payload from another client.
 *
 * Everything is checked rather than trusted: this arrives from another browser,
 * and a caret offset that is not a number would be handed straight to
 * CodeMirror.
 */
export function decodePresence(payload: string | undefined): PresenceState | null {
  if (!payload || payload.length > MAX_PAYLOAD) return null;
  let raw: unknown;
  try {
    raw = JSON.parse(payload);
  } catch {
    return null;
  }
  if (!raw || typeof raw !== "object") return null;
  const value = raw as Record<string, unknown>;
  const state: PresenceState = {};
  if (typeof value.nodeId === "string" && value.nodeId.length <= 256) {
    state.nodeId = value.nodeId;
  }
  if (typeof value.pageId === "string" && value.pageId.length <= 256) {
    state.pageId = value.pageId;
  }
  const selection = value.selection as Record<string, unknown> | undefined;
  if (
    selection &&
    isOffset(selection.anchor) &&
    isOffset(selection.head)
  ) {
    state.selection = { anchor: selection.anchor, head: selection.head };
  }
  const pointer = value.pointer as Record<string, unknown> | undefined;
  if (pointer && isFinitePoint(pointer.x) && isFinitePoint(pointer.y)) {
    state.pointer = { x: pointer.x, y: pointer.y };
  }
  return state;
}

function isOffset(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function isFinitePoint(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/**
 * Rate-limits what is sent, keeping the last value rather than dropping it.
 *
 * A pointer that stops moving still has to arrive: the interesting message is
 * usually the final one, and a plain "ignore anything too soon" throttle is
 * exactly the one that loses it.
 */
export function throttled<T>(send: (value: T) => void, intervalMs: number) {
  let timer: number | null = null;
  let pending: T | null = null;
  let lastSentAt = 0;

  const flush = () => {
    timer = null;
    if (pending === null) return;
    const value = pending;
    pending = null;
    lastSentAt = Date.now();
    send(value);
  };

  return {
    push(value: T) {
      const now = Date.now();
      const wait = intervalMs - (now - lastSentAt);
      if (wait <= 0 && timer === null) {
        lastSentAt = now;
        send(value);
        return;
      }
      pending = value;
      if (timer === null) timer = window.setTimeout(flush, Math.max(wait, 0));
    },
    cancel() {
      if (timer !== null) window.clearTimeout(timer);
      timer = null;
      pending = null;
    },
  };
}
