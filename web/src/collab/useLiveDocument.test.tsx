import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { LiveConnection } from "../api";
import { useApp } from "../store";
import { liveDocumentSink, useLiveDocument } from "./useLiveDocument";

function fakeConnection(): LiveConnection {
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

afterEach(() => {
  useApp.getState().setLive(null);
});

describe("useLiveDocument reopen", () => {
  it("opens a fresh session without applying a payload rejected with the old one", async () => {
    const live = fakeConnection();
    const onRemoteText = vi.fn();
    useApp.getState().setLive(live);

    const { result, unmount } = renderHook(() =>
      useLiveDocument({
        nodeId: "alpha",
        enabled: true,
        initialText: () => "",
        onRemoteText,
      }),
    );

    await waitFor(() => expect(live.docOpen).toHaveBeenCalledTimes(1));
    const rejectedSession = liveDocumentSink("alpha");
    expect(rejectedSession).toBeDefined();

    // This local update is inside the batching window when the server rejects
    // the session. Normal unmount flushes that window; reopen must not, because
    // the rejected payload would immediately be sent into the replacement.
    act(() => {
      result.current.push("rejected overflow");
    });
    expect(live.docUpdate).not.toHaveBeenCalled();

    act(() => {
      rejectedSession?.reopen();
    });

    await waitFor(() => expect(live.docOpen).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(result.current.ready).toBe(true));
    expect(live.docUpdate).not.toHaveBeenCalled();
    expect(live.docClose).toHaveBeenCalledTimes(1);
    expect(liveDocumentSink("alpha")).not.toBe(rejectedSession);
    expect(result.current.ytext?.toString()).toBe("");
    expect(onRemoteText).not.toHaveBeenCalled();

    unmount();
  });
});
