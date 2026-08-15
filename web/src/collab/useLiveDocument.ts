/**
 * A document edited by several people at once [P2].
 *
 * Wires one `DocSession` to the socket, the editor and the store, and answers
 * the one question the rest of the drawer needs: may this window write the file.
 *
 * The CRDT only settles what the text *is*. Getting it onto disk is still the
 * existing save path, run by exactly one participant — the leader — because a
 * file has one writer whatever the editor does. Everyone else adopts the
 * revision that write produced, so the `node-changed` event that follows is not
 * mistaken for somebody editing the file behind their back.
 *
 * Yjs is loaded on demand. A single-user desktop session never opens a live
 * document, and it should not pay ~29 KB for the possibility.
 */

import { useEffect, useReducer, useRef, useState } from "react";

import type * as Y from "yjs";

import { documentKey } from "../api";
import { useApp } from "../store";
import type { DocSession, DocTransport } from "./ydoc";

/** Handlers for one open document, so App can route socket frames to it. */
export interface LiveDocumentSink {
  state: (leader: boolean, seed: boolean) => void;
  update: (payload: string) => void;
  leader: (leader: boolean) => void;
  flushed: (rev: string, from: string) => void;
  compact: (token: string) => void;
  snapshotStart: (token: string, size: number, chunks: number) => void;
  snapshotChunk: (token: string, seq: number, payload: string) => void;
  snapshotCommit: (token: string) => void;
  snapshotReady: (token: string) => void;
  snapshotAccepted: (token: string) => void;
  snapshotRejected: (token: string) => void;
  reopen: () => void;
  /** The file changed on disk while this session was live. */
  externalChange: () => void;
}

const sinks = new Map<string, LiveDocumentSink>();

/**
 * Marks the transaction carrying an edit that came from the file, so it is not
 * mistaken for something this window's editor did.
 */
const EXTERNAL = Symbol("external-file");

/** Routes a `doc-*` frame to whichever window has that document open. */
export function liveDocumentSink(nodeId: string): LiveDocumentSink | undefined {
  return sinks.get(nodeId);
}

export interface LiveDocument {
  /** True once the session exists and the text can be trusted. */
  ready: boolean;
  /**
   * The shared text, for the editor to bind to directly.
   *
   * Binding beats copying: a controlled editor re-set from the outside on every
   * remote change moves the caret and drops whatever was typed in between, so
   * the document has to be the CRDT's rather than React's.
   */
  ytext: Y.Text | null;
  /** Whether this window is the one that writes the file. */
  isLeader: boolean;
  /** Publishes a local edit. Does nothing before the session is ready. */
  push: (text: string) => void;
  /** Tells the room the revision this window's own save produced. */
  reportFlushed: (rev: string) => void;
}

export function useLiveDocument({
  nodeId,
  pageId,
  enabled,
  initialText,
  onRemoteText,
  onFlushed,
  readFileText,
  onExternalGiveUp,
}: {
  nodeId: string;
  /** The subpage being edited, or undefined for the node's own document. */
  pageId?: string;
  /** False for a closed drawer, or a room with nobody else in it. */
  enabled: boolean;
  /** What the file said, for the first participant to seed from. */
  initialText: () => string;
  /** Only for a surface that is not bound to `ytext` directly. */
  onRemoteText?: (text: string) => void;
  /**
   * The revision another participant's leader wrote. Which document that
   * belongs to is the caller's business: a subpage's revision adopted onto the
   * node's own tab would mark work clean that nobody has written.
   */
  onFlushed?: (rev: string) => void;
  /** Reads what the file says now, for merging an edit made outside. */
  readFileText?: () => Promise<string>;
  /** The change was a replacement rather than an edit; the session stands down. */
  onExternalGiveUp?: () => void;
}): LiveDocument {
  const live = useApp((state) => state.live);
  const [ready, setReady] = useState(false);
  const [isLeader, setIsLeader] = useState(false);
  const sessionRef = useRef<DocSession | null>(null);
  const [ytext, setYtext] = useState<Y.Text | null>(null);
  // `doc-reopen` does not change any caller prop: the drawer and active tab
  // stay mounted. A local generation is therefore the signal that tears down
  // the rejected session and performs a fresh doc-open handshake.
  const [restartGeneration, restartSession] = useReducer((value: number) => value + 1, 0);
  // Read through refs: rebuilding the session when a callback identity changes
  // would drop the document and re-seed it, which is a duplicate-text bug.
  const latest = useRef({
    initialText,
    onRemoteText,
    onFlushed,
    readFileText,
    onExternalGiveUp,
  });
  latest.current = {
    initialText,
    onRemoteText,
    onFlushed,
    readFileText,
    onExternalGiveUp,
  };
  const docId = documentKey(nodeId, pageId);

  useEffect(() => {
    if (!enabled || !live) return;
    let cancelled = false;

    // The session is named by the server's key for it, but what goes on the
    // wire stays the pair — that is what the server keys from.
    const transport: DocTransport = {
      open: () => live.docOpen(nodeId, pageId),
      close: () => live.docClose(nodeId, pageId),
      update: (_id, payload) => live.docUpdate(nodeId, pageId, payload),
      requestCompact: () => live.docCompactRequest(nodeId, pageId),
      snapshotStart: (_id, token, size, chunks) =>
        live.docSnapshotStart(nodeId, pageId, token, size, chunks),
      snapshotChunk: (_id, token, seq, payload) =>
        live.docSnapshotChunk(nodeId, pageId, token, seq, payload),
      snapshotCommit: (_id, token) => live.docSnapshotCommit(nodeId, pageId, token),
      snapshotAbort: (_id, token, reason) =>
        live.docSnapshotAbort(nodeId, pageId, token, reason),
      flushed: (_id, rev) => live.docFlushed(nodeId, pageId, rev),
    };

    void import("./ydoc").then(({ DocSession }) => {
      if (cancelled) return;
      const session = new DocSession(docId, transport, {
        onSeedNeeded: () => session.seed(latest.current.initialText()),
        onRemoteText: (text) => latest.current.onRemoteText?.(text),
        onLeaderChange: setIsLeader,
        onFlushed: (rev) => latest.current.onFlushed?.(rev),
      });
      sessionRef.current = session;
      setYtext(session.ytext);
      sinks.set(docId, {
        state: (leader, seed) => session.handleState({ leader, seed }),
        update: (payload) => session.handleUpdate(payload),
        leader: (leader) => session.handleLeader(leader),
        flushed: (rev, from) => session.handleFlushed(rev, from),
        compact: (token) => session.handleCompact(token),
        snapshotStart: (token, size, chunks) =>
          session.handleSnapshotStart(token, size, chunks),
        snapshotChunk: (token, seq, payload) =>
          session.handleSnapshotChunk(token, seq, payload),
        snapshotCommit: (token) => session.handleSnapshotCommit(token),
        snapshotReady: (token) => session.handleSnapshotReady(token),
        snapshotAccepted: (token) => session.handleSnapshotAccepted(token),
        snapshotRejected: (token) => session.handleSnapshotRejected(token),
        // Somebody edited the file outside this session: vim, an agent, a git
        // checkout, a restored backup. It cannot be reloaded over the top —
        // there is no way to replace a shared text with a string without
        // throwing away what everybody else is typing — so the difference is
        // merged in as operations instead, by the one participant that owns
        // the file. If it is too big to be an edit rather than a replacement,
        // the session gives up and the ordinary conflict banner takes over.
        externalChange: () => {
          if (!session.isLeader) return;
          void (async () => {
            const [text, { reconcileText }] = await Promise.all([
              latest.current.readFileText?.() ?? Promise.resolve(null),
              import("./reconcile"),
            ]);
            if (text === null || sessionRef.current !== session) return;
            const result = reconcileText(session.ytext, text, { origin: EXTERNAL });
            if (result.outcome !== "too-large") return;
            latest.current.onExternalGiveUp?.();
          })().catch(() => {
            // A failed read is not a merge decision: the file is still there
            // and the next change will ask again.
          });
        },
        // The server gave up on the stored log. Starting over is safe — the
        // file is still the truth at rest — and is the only answer that cannot
        // leave a document missing the part that was dropped.
        reopen: () => {
          session.abort();
          sinks.delete(docId);
          sessionRef.current = null;
          setYtext(null);
          setReady(false);
          setIsLeader(false);
          restartSession();
        },
      });
      setReady(true);
    });

    return () => {
      cancelled = true;
      sinks.delete(docId);
      sessionRef.current?.destroy();
      sessionRef.current = null;
      setYtext(null);
      setReady(false);
      setIsLeader(false);
    };
  }, [enabled, live, nodeId, pageId, docId, restartGeneration]);

  return {
    ready,
    ytext,
    isLeader,
    push: (text: string) => sessionRef.current?.setText(text),
    reportFlushed: (rev: string) => sessionRef.current?.reportFlushed(rev),
  };
}
