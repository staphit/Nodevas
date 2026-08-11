import { beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../store";
import type { LiveConnection, NodeLock, Peer } from "../api";

function fakeLive(): LiveConnection {
  return {
    close: vi.fn(),
    subscribe: vi.fn(),
    presence: vi.fn(),
    lock: vi.fn(),
    unlock: vi.fn(),
    awareness: vi.fn(),
    graphDrag: vi.fn(),
    docOpen: vi.fn(),
    docClose: vi.fn(),
    docUpdate: vi.fn(),
    docCompactRequest: vi.fn(),
    docSnapshotStart: vi.fn(),
    docSnapshotChunk: vi.fn(),
    docSnapshotCommit: vi.fn(),
    docSnapshotAbort: vi.fn(),
    docFlushed: vi.fn(),
  };
}

function peer(id: string, nodeId?: string): Peer {
  return { id, actor: { id, name: id, role: "member" }, nodeId, editing: !!nodeId };
}

function lock(nodeId: string, mine = false): NodeLock {
  return {
    nodeId,
    actor: { id: "x", name: "x", role: "member" },
    since: "2026-01-01T00:00:00Z",
    mine,
  };
}

beforeEach(() => {
  useApp.setState({
    live: null,
    selfPeerID: "",
    peers: [],
    locks: [],
    lockDenied: null,
  });
});

describe("simple setters", () => {
  it("mirrors the websocket connection and self id", () => {
    const live = fakeLive();
    useApp.getState().setLive(live);
    useApp.getState().setSelfPeerID("me");

    expect(useApp.getState().live).toBe(live);
    expect(useApp.getState().selfPeerID).toBe("me");

    useApp.getState().setLive(null);
    expect(useApp.getState().live).toBeNull();
  });

  it("replaces the peer list wholesale", () => {
    useApp.getState().setPeers([peer("a"), peer("b")]);
    expect(useApp.getState().peers).toHaveLength(2);
  });

  it("setting locks also clears any pending lockDenied notice", () => {
    useApp.setState({ lockDenied: { nodeId: "a", actor: "someone" } });

    useApp.getState().setLocks([lock("a", true)]);

    expect(useApp.getState().locks).toEqual([lock("a", true)]);
    expect(useApp.getState().lockDenied).toBeNull();
  });

  it("setLockDenied both sets and clears the notice", () => {
    useApp.getState().setLockDenied({ nodeId: "a", actor: "someone" });
    expect(useApp.getState().lockDenied).toEqual({ nodeId: "a", actor: "someone" });

    useApp.getState().setLockDenied(null);
    expect(useApp.getState().lockDenied).toBeNull();
  });
});

describe("presence and lock requests delegate to the live connection", () => {
  it("does nothing before the socket connects, rather than throwing", () => {
    expect(() => useApp.getState().reportPresence("a", true)).not.toThrow();
    expect(() => useApp.getState().requestLock("a")).not.toThrow();
    expect(() => useApp.getState().releaseLock("a")).not.toThrow();
  });

  it("forwards presence, lock and unlock once connected", () => {
    const live = fakeLive();
    useApp.getState().setLive(live);

    useApp.getState().reportPresence("a", true);
    expect(live.presence).toHaveBeenCalledWith("a", true);

    useApp.getState().requestLock("a");
    expect(live.lock).toHaveBeenCalledWith("a", false);

    useApp.getState().requestLock("a", true);
    expect(live.lock).toHaveBeenCalledWith("a", true);

    useApp.getState().releaseLock("a");
    expect(live.unlock).toHaveBeenCalledWith("a");
  });
});
