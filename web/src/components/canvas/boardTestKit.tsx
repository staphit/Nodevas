/**
 * Shared set-up for the board's render-count tests: a LaneView on a small
 * graph, pointer events jsdom lacks, and a frame queue drained by hand.
 */
import { vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph, RunState } from "../../types";

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

export function boardGraph(ids: string[]): Graph {
  return {
    version: 1,
    nodes: ids.map((id) => ({ id, title: id.toUpperCase() })),
    edges: [],
    ui: {},
  };
}

export function resetBoardStore(ids: string[]) {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: boardGraph(ids),
    graphRev: "rev-1",
    runState: EMPTY_RUN,
    statuses: {},
    issues: [],
    operations: {},
    error: null,
    tabs: [],
    activeTab: null,
    peers: [],
    presences: {},
    ghosts: {},
  });
}

/** jsdom has neither PointerEvent nor pointer capture. */
export function installPointerShims() {
  if (!("PointerEvent" in window)) {
    class PointerEventShim extends MouseEvent {
      pointerId: number;
      pointerType: string;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.pointerId = init.pointerId ?? 1;
        this.pointerType = init.pointerType ?? "mouse";
      }
    }
    Object.assign(window, { PointerEvent: PointerEventShim });
    Object.assign(globalThis, { PointerEvent: PointerEventShim });
  }
  const proto = HTMLElement.prototype as unknown as Record<string, unknown>;
  proto.setPointerCapture ??= () => undefined;
  proto.releasePointerCapture ??= () => undefined;
  proto.hasPointerCapture ??= () => false;
}

/** requestAnimationFrame that waits for `flush()`. */
export function manualFrames() {
  let next = 0;
  let queue = new Map<number, FrameRequestCallback>();
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
    queue.set(++next, callback);
    return next;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation((handle) => {
    queue.delete(handle);
  });
  return {
    flush() {
      const pending = queue;
      queue = new Map();
      for (const callback of pending.values()) callback(performance.now());
    },
  };
}
