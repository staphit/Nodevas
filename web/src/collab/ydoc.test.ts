import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DOC_SNAPSHOT_CHUNK_B64,
  DOC_UPDATE_BATCH_MS,
  DocSession,
  type DocTransport,
} from "./ydoc";

const NODE_ID = "node-1";

interface Peer {
  session: DocSession;
  /** What the caller was told to render, i.e. remote text only. */
  remote: string[];
  updates: string[];
  snapshots: string[];
  compactRequests: number;
  snapshotStarts: Array<{ token: string; size: number; chunks: number }>;
  snapshotChunks: Array<{ token: string; seq: number; payload: string }>;
  snapshotCommits: string[];
  snapshotAborts: Array<{ token: string; reason: "too-large" }>;
  leaders: boolean[];
  flushes: string[];
  seedRequests: number;
}

/**
 * Two sessions wired to each other the way the hub wires them: an update from
 * one is relayed to everybody except its sender.
 */
function makeHub() {
  const peers: Peer[] = [];

  function join(): Peer {
    const peer: Peer = {
      session: undefined as unknown as DocSession,
      remote: [],
      updates: [],
      snapshots: [],
      compactRequests: 0,
      snapshotStarts: [],
      snapshotChunks: [],
      snapshotCommits: [],
      snapshotAborts: [],
      leaders: [],
      flushes: [],
      seedRequests: 0,
    };
    const transport: DocTransport = {
      open: () => undefined,
      close: () => undefined,
      update(nodeId, payload) {
        expect(nodeId).toBe(NODE_ID);
        peer.updates.push(payload);
        for (const other of peers) {
          if (other !== peer) other.session.handleUpdate(payload);
        }
      },
      requestCompact() {
        peer.compactRequests += 1;
      },
      snapshotStart(_nodeId, token, size, chunks) {
        peer.snapshotStarts.push({ token, size, chunks });
      },
      snapshotChunk(_nodeId, token, seq, payload) {
        peer.snapshotChunks.push({ token, seq, payload });
      },
      snapshotCommit(_nodeId, token) {
        peer.snapshotCommits.push(token);
        peer.snapshots.push(
          peer.snapshotChunks
            .filter((chunk) => chunk.token === token)
            .sort((left, right) => left.seq - right.seq)
            .map((chunk) => chunk.payload)
            .join(""),
        );
      },
      snapshotAbort(_nodeId, token, reason) {
        peer.snapshotAborts.push({ token, reason });
      },
      flushed(_nodeId, rev) {
        peer.flushes.push(rev);
      },
    };
    peer.session = new DocSession(NODE_ID, transport, {
      onSeedNeeded: () => {
        peer.seedRequests += 1;
      },
      onRemoteText: (text) => {
        peer.remote.push(text);
      },
      onLeaderChange: (leader) => {
        peer.leaders.push(leader);
      },
      onFlushed: (rev) => {
        peer.flushes.push(rev);
      },
    });
    peers.push(peer);
    return peer;
  }

  return { join, peers };
}

/** Lets the batch window expire so whatever is pending goes out. */
function settle() {
  vi.advanceTimersByTime(DOC_UPDATE_BATCH_MS + 1);
}

describe("DocSession", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("seeds when told to and carries the text to the other client", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();

    a.session.handleState({ leader: true, seed: true });
    b.session.handleState({ leader: false, seed: false });
    expect(a.seedRequests).toBe(1);
    expect(b.seedRequests).toBe(0);

    a.session.seed("hello");
    settle();

    expect(b.session.text).toBe("hello");
    expect(b.remote.at(-1)).toBe("hello");
  });

  it("carries typing from one client to the other", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("hello");
    settle();

    a.session.setText("hello world");
    settle();

    expect(b.session.text).toBe("hello world");
    expect(a.session.text).toBe("hello world");
  });

  it("keeps both edits when two people type at once at different offsets", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("one two three");
    settle();

    a.session.setText("ONE two three");
    b.session.setText("one two THREE");
    settle();

    expect(a.session.text).toBe("ONE two THREE");
    expect(b.session.text).toBe("ONE two THREE");
  });

  it("sends the changed character, not the document", () => {
    const hub = makeHub();
    const a = hub.join();
    hub.join();
    const long = "x".repeat(4000);
    a.session.seed(long);
    settle();
    const seedPayload = a.updates.at(-1) ?? "";
    a.updates.length = 0;

    a.session.setText(`${long.slice(0, 2000)}y${long.slice(2001)}`);
    settle();

    expect(a.updates).toHaveLength(1);
    expect(seedPayload.length).toBeGreaterThan(4000);
    expect(a.updates[0].length).toBeLessThan(200);
  });

  it("edits an emoji without splitting it", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("a😀b");
    settle();

    a.session.setText("a😃b");
    settle();

    expect(b.session.text).toBe("a😃b");
  });

  it("does not echo a remote update back out", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("hello");
    settle();
    expect(b.session.text).toBe("hello");

    b.updates.length = 0;
    a.session.setText("hello!");
    settle();
    settle();

    expect(b.updates).toEqual([]);
    expect(b.remote.at(-1)).toBe("hello!");
    // The receiver's own callback is for somebody else's changes only.
    expect(a.remote).toEqual([]);
  });

  it("refuses a second seed instead of duplicating the text", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const hub = makeHub();
    const a = hub.join();
    a.session.seed("hello");
    a.session.seed("hello");
    settle();

    expect(a.session.text).toBe("hello");
    expect(warn).toHaveBeenCalled();
  });

  it("refuses to seed a document that arrived over the wire", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("hello");
    settle();

    b.session.seed("hello");
    settle();

    expect(b.session.text).toBe("hello");
    expect(a.session.text).toBe("hello");
    expect(warn).toHaveBeenCalled();
  });

  it("batches a burst of edits into one update", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("");
    settle();
    a.updates.length = 0;

    a.session.setText("h");
    a.session.setText("he");
    a.session.setText("hel");
    expect(a.updates).toEqual([]);
    settle();

    expect(a.updates).toHaveLength(1);
    expect(b.session.text).toBe("hel");
  });

  it("sends pending edits before it closes", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("hi");

    a.session.destroy();

    expect(b.session.text).toBe("hi");
  });

  it("reports leader changes once per change", () => {
    const hub = makeHub();
    const a = hub.join();

    a.session.handleState({ leader: false, seed: true });
    expect(a.session.isLeader).toBe(false);
    a.session.handleLeader(true);
    a.session.handleLeader(true);
    expect(a.session.isLeader).toBe(true);
    a.session.handleLeader(false);

    expect(a.leaders).toEqual([true, false]);
  });

  it("passes a peer's flush through to the caller", () => {
    const hub = makeHub();
    const a = hub.join();

    a.session.handleFlushed("rev-7", "peer-2");

    expect(a.flushes).toEqual(["rev-7"]);
  });

  it("answers a compact request with the whole state", () => {
    const hub = makeHub();
    const a = hub.join();
    const b = hub.join();
    a.session.seed("hello");
    settle();

    a.session.setText("hello world");
    a.session.handleCompact("7");
    a.session.handleSnapshotReady("7");

    expect(a.snapshots).toHaveLength(1);
    expect(a.snapshotStarts).toEqual([
      { token: "7", size: a.snapshots[0].length, chunks: 1 },
    ]);
    expect(a.snapshotCommits).toEqual(["7"]);
    // The pending edit is captured by the snapshot and is not sent as a
    // dependent delta before the server acknowledges that base.
    expect(a.updates).toHaveLength(1);
    expect(b.session.text).toBe("hello");

    const restored = new DocSession(NODE_ID, stubTransport(), {
      onSeedNeeded: () => undefined,
      onRemoteText: () => undefined,
      onLeaderChange: () => undefined,
      onFlushed: () => undefined,
    });
    restored.handleUpdate(a.snapshots[0]);
    expect(restored.text).toBe("hello world");
    restored.destroy();
  });

  it("requests a barrier for one large update and uploads strict chunks", () => {
    const hub = makeHub();
    const a = hub.join();
    const large = "x".repeat(50_000);

    a.session.setText(large);
    settle();

    expect(a.updates).toEqual([]);
    expect(a.compactRequests).toBe(1);
    expect(a.snapshots).toEqual([]);

    a.session.handleCompact("42");
    a.session.handleSnapshotReady("42");

    expect(a.snapshotStarts).toHaveLength(1);
    expect(a.snapshotStarts[0]).toEqual({
      token: "42",
      size: a.snapshots[0].length,
      chunks: Math.ceil(a.snapshots[0].length / DOC_SNAPSHOT_CHUNK_B64),
    });
    expect(a.snapshotChunks.length).toBeGreaterThan(1);
    expect(a.snapshotChunks.map((chunk) => chunk.seq)).toEqual(
      a.snapshotChunks.map((_, index) => index),
    );
    expect(a.snapshotChunks.every((chunk) => chunk.payload.length <= DOC_SNAPSHOT_CHUNK_B64)).toBe(
      true,
    );
    expect(a.snapshotCommits).toEqual(["42"]);

    // A server barrier keeps updates accepted after the snapshot prefix as a
    // tail. Late-open replay delivers the committed snapshot before that tail.
    a.session.setText(`${large}!`);
    settle();
    expect(a.updates).toHaveLength(0);
    a.session.handleSnapshotAccepted("42");
    expect(a.updates).toHaveLength(1);

    const restored = new DocSession(NODE_ID, stubTransport(), {
      onSeedNeeded: () => undefined,
      onRemoteText: () => undefined,
      onLeaderChange: () => undefined,
      onFlushed: () => undefined,
    });
    restored.handleSnapshotStart("42", a.snapshots[0].length, a.snapshotChunks.length);
    for (const chunk of a.snapshotChunks) {
      restored.handleSnapshotChunk(chunk.token, chunk.seq, chunk.payload);
    }
    restored.handleSnapshotCommit("42");
    restored.handleUpdate(a.updates[0]);
    expect(restored.text).toBe(`${large}!`);
    restored.destroy();
  });

  it("keeps dependent deltas frozen and retries a rejected snapshot on the same socket", () => {
    const hub = makeHub();
    const a = hub.join();
    const large = "x".repeat(50_000);

    a.session.setText(large);
    settle();
    a.session.handleCompact("71");
    a.session.setText(`${large}!`);
    settle();
    expect(a.updates).toEqual([]);

    a.session.handleSnapshotRejected("71");
    vi.advanceTimersByTime(999);
    expect(a.compactRequests).toBe(1);
    vi.advanceTimersByTime(1);
    expect(a.compactRequests).toBe(2);

    a.session.handleCompact("72");
    a.session.handleSnapshotReady("72");
    expect(a.snapshotCommits).toEqual(["72"]);
    a.session.handleSnapshotAccepted("72");
    // The second snapshot contains the post-reject edit; it must not also be
    // emitted against a base the server rejected.
    expect(a.updates).toEqual([]);
  });

  it("invalidates a start awaiting ready when a replacement socket opens", () => {
    const hub = makeHub();
    const a = hub.join();
    a.session.setText("x".repeat(50_000));
    settle();
    a.session.handleCompact("81");
    expect(a.snapshotStarts).toHaveLength(1);
    expect(a.snapshotChunks).toEqual([]);

    a.session.handleState({ leader: false, seed: false });
    expect(a.compactRequests).toBe(2);
    a.session.handleSnapshotReady("81");
    expect(a.snapshotChunks).toEqual([]);

    a.session.handleCompact("82");
    a.session.handleSnapshotReady("82");
    expect(a.snapshotCommits).toEqual(["82"]);
  });

  it("replays a commit that lost its accepted ack after reconnect", () => {
    const hub = makeHub();
    const a = hub.join();
    const large = "x".repeat(50_000);
    a.session.setText(large);
    settle();
    a.session.handleCompact("91");
    a.session.handleSnapshotReady("91");
    expect(a.snapshotCommits).toEqual(["91"]);

    a.session.setText(`${large}!`);
    settle();
    expect(a.updates).toEqual([]);
    a.session.handleState({ leader: false, seed: false });
    expect(a.compactRequests).toBe(2);
    a.session.handleCompact("92");
    a.session.handleSnapshotReady("92");
    a.session.handleSnapshotAccepted("92");
    expect(a.snapshotCommits).toEqual(["91", "92"]);
    expect(a.updates).toEqual([]);
  });

  it("keeps an over-cap snapshot local and aborts without starting an upload", () => {
    const transport = stubTransport();
    const requestCompact = vi.spyOn(transport, "requestCompact");
    const snapshotStart = vi.spyOn(transport, "snapshotStart");
    const snapshotAbort = vi.spyOn(transport, "snapshotAbort");
    const session = new DocSession(
      NODE_ID,
      transport,
      {
        onSeedNeeded: () => undefined,
        onRemoteText: () => undefined,
        onLeaderChange: () => undefined,
        onFlushed: () => undefined,
      },
      { maxBytes: 64, chunkBytes: 16 },
    );
    const local = "x".repeat(50_000);

    session.setText(local);
    settle();
    expect(requestCompact).toHaveBeenCalledTimes(1);
    session.handleCompact("9");

    expect(session.text).toBe(local);
    expect(snapshotStart).not.toHaveBeenCalled();
    expect(snapshotAbort).toHaveBeenCalledWith(NODE_ID, "9", "too-large");
    session.destroy();
  });

  it("drops an incomplete or out-of-order incoming snapshot without applying it", () => {
    const hub = makeHub();
    const source = hub.join();
    source.session.seed("safe remote state");
    settle();
    source.session.handleCompact("11");
    source.session.handleSnapshotReady("11");
    const payload = source.snapshots[0];
    const receiver = new DocSession(NODE_ID, stubTransport(), {
      onSeedNeeded: () => undefined,
      onRemoteText: () => undefined,
      onLeaderChange: () => undefined,
      onFlushed: () => undefined,
    });
    const chunks = Math.ceil(payload.length / DOC_SNAPSHOT_CHUNK_B64);

    receiver.handleSnapshotStart("11", payload.length, chunks);
    receiver.handleSnapshotChunk("11", 1, payload);
    receiver.handleSnapshotCommit("11");

    expect(receiver.text).toBe("");
    receiver.destroy();
  });
});

function stubTransport(): DocTransport {
  return {
    open: () => undefined,
    close: () => undefined,
    update: () => undefined,
    requestCompact: () => undefined,
    snapshotStart: () => undefined,
    snapshotChunk: () => undefined,
    snapshotCommit: () => undefined,
    snapshotAbort: () => undefined,
    flushed: () => undefined,
  };
}
