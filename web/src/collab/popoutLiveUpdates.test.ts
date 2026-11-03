import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WSEvent } from "../api/ws";
import { useApp } from "../store";
import { usePopoutLiveUpdates } from "./popoutLiveUpdates";

const socket = vi.hoisted(() => ({
  handler: null as null | ((ev: WSEvent) => void),
  close: vi.fn(),
  subscribe: vi.fn(),
}));

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    connectWS: vi.fn((onEvent: (ev: WSEvent) => void) => {
      socket.handler = onEvent;
      return { close: socket.close, subscribe: socket.subscribe } as unknown as ReturnType<typeof actual.connectWS>;
    }),
  };
});

// The pop-out editor never opened a socket: an edit saved in the main window
// (or by an agent) never reached it, and its next keystroke was refused as a
// version conflict.
describe("usePopoutLiveUpdates", () => {
  const notifyDiskChange = vi.fn(async () => undefined);
  const loadAll = vi.fn(async () => undefined);

  beforeEach(() => {
    vi.clearAllMocks();
    socket.handler = null;
    useApp.setState({ notifyDiskChange, loadAll });
  });

  it("reloads the document another window saved, and the graph when it changes", () => {
    const { unmount } = renderHook(() => usePopoutLiveUpdates("node-0004"));
    expect(socket.handler).not.toBeNull();

    socket.handler!({ type: "node-changed", id: "node-0004" });
    expect(notifyDiskChange).toHaveBeenCalledWith("node-0004");

    socket.handler!({ type: "graph-changed" });
    expect(loadAll).toHaveBeenCalledTimes(1);

    unmount();
    expect(socket.close).toHaveBeenCalledTimes(1);
  });

  // Node ids repeat across projects: node-0004 of another project changing
  // must not reach this one.
  it("listens in the project it shows", () => {
    useApp.setState({ activeProject: "main" });
    renderHook(() => usePopoutLiveUpdates("node-0004"));
    expect(socket.subscribe).toHaveBeenCalledWith("main");
  });

  it("opens no socket without a node", () => {
    renderHook(() => usePopoutLiveUpdates(""));
    expect(socket.handler).toBeNull();
  });
});
