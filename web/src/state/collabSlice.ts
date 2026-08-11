/**
 * Live collaboration state [P2].
 *
 * Everything here mirrors what the server pushes down the websocket: who else
 * is in this project, and which documents they hold open. It is display and
 * courtesy state only — the hard guarantee against two people overwriting each
 * other stays the optimistic `Rev` check on save.
 */

import type { DragGhost, LiveConnection, NodeLock, Peer } from "../api";
import { encodePresence, throttled, type PresenceState } from "../collab/presence";
import type { AppSlice, CollabSlice } from "./types";

export const createCollabSlice: AppSlice<CollabSlice> = (set, get) => ({
  live: null,
  selfPeerID: "",
  peers: [],
  locks: [],
  lockDenied: null,
  ghosts: {},
  presences: {},

  setLive: (live: LiveConnection | null) => set({ live }),
  setSelfPeerID: (selfPeerID: string) => set({ selfPeerID }),
  // Somebody who left takes their ghost with them, so a card cannot be left
  // hovering under a pointer that is no longer there.
  setPeers: (peers: Peer[]) =>
    set((state) => ({
      peers,
      ghosts: pruneToPeers(state.ghosts, peers),
      presences: pruneToPeers(state.presences, peers),
    })),
  setLocks: (locks: NodeLock[]) => set({ locks, lockDenied: null }),
  setLockDenied: (lockDenied: { nodeId: string; actor: string } | null) =>
    set({ lockDenied }),
  setPresence: (peerId: string, presence: PresenceState | null) =>
    set((state) => {
      const presences = { ...state.presences };
      if (presence) presences[peerId] = presence;
      else delete presences[peerId];
      return { presences };
    }),

  setGhost: (peerId: string, ghost: DragGhost | null) =>
    set((state) => {
      const ghosts = { ...state.ghosts };
      if (ghost && ghost.active) ghosts[peerId] = ghost;
      else delete ghosts[peerId];
      return { ghosts };
    }),

  /** Tells the server which document this browser shows. */
  reportPresence: (nodeId: string, editing: boolean) => {
    get().live?.presence(nodeId, editing);
  },

  /**
   * Asks for the soft lock on a document. `steal` is the "force edit" path: it
   * always succeeds, because refusing would leave the user stuck behind a
   * colleague who went to lunch with a tab open.
   */
  requestLock: (nodeId: string, steal = false) => {
    get().live?.lock(nodeId, steal);
  },
  releaseLock: (nodeId: string) => {
    get().live?.unlock(nodeId);
  },

  reportDrag: (ids: string[], dx: number, dy: number, active: boolean) => {
    get().live?.graphDrag(ids, dx, dy, active);
  },

  // The editor reports a caret and the board reports a pointer, independently
  // and often. Merging here means neither has to know about the other, and
  // throttling here means a fast pointer cannot spend the socket's whole
  // stream budget on its own.
  reportPresenceState: (patch: PresenceState) => {
    const next = { ...localPresence, ...patch };
    if (encodePresence(next) === encodePresence(localPresence)) return;
    localPresence = next;
    awareness.push({ state: next, send: (payload) => get().live?.awareness(payload) });
  },
});

/** What this browser last told the room about itself. */
let localPresence: PresenceState = {};

const awareness = throttled<{
  state: PresenceState;
  send: (payload: string) => void;
}>((value) => value.send(encodePresence(value.state)), 50);

/** Drops the entries of peers who are no longer here. */
function pruneToPeers<T>(entries: Record<string, T>, peers: Peer[]): Record<string, T> {
  return Object.fromEntries(
    Object.entries(entries).filter(([id]) => peers.some((peer) => peer.id === id)),
  );
}

/** The lock on a document, or null when nobody holds it. */
export function lockFor(locks: NodeLock[], nodeId: string): NodeLock | null {
  return locks.find((lock) => lock.nodeId === nodeId) ?? null;
}

/** Who else is looking at a document. */
export function peersOnNode(peers: Peer[], selfID: string, nodeId: string): Peer[] {
  return peers.filter((peer) => peer.id !== selfID && peer.nodeId === nodeId);
}
