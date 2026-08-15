import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useConnectionDrag } from "./useConnectionDrag";

function event(currentTarget: HTMLElement, overrides: Record<string, unknown> = {}) {
  return {
    button: 0,
    altKey: true,
    pointerId: 1,
    clientX: 10,
    clientY: 10,
    currentTarget,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
    ...overrides,
  } as unknown as React.PointerEvent<HTMLElement>;
}

function pointerTarget() {
  const target = document.createElement("div");
  Object.assign(target, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn(() => true),
    releasePointerCapture: vi.fn(),
  });
  return target;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useConnectionDrag", () => {
  it("routes a node-to-node connection on pointer release", () => {
    const connectNodes = vi.fn();
    const target = document.createElement("div");
    target.className = "col-card";
    target.dataset.nodeId = "target";
    document.body.append(target);
    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: vi.fn(() => target),
    });

    const source = pointerTarget();
    const { result } = renderHook(() =>
      useConnectionDrag({
        enabled: true,
        graphPointFromClient: (x, y) => ({ x, y }),
        connectionPointForNode: () => ({ x: 10, y: 10 }),
        onSelectGate: vi.fn(),
        connectNodes,
        connectNodeToLogicGate: vi.fn(),
        connectLogicGateToNode: vi.fn(),
      }),
    );

    act(() => {
      result.current.handlers.startNode(event(source), "source");
      result.current.handlers.onPointerMove(
        event(source, { clientX: 40, clientY: 40 }),
      );
      result.current.handlers.onPointerUp(
        event(source, { clientX: 40, clientY: 40 }),
        true,
      );
    });

    expect(connectNodes).toHaveBeenCalledWith("source", "target");
    expect(result.current.state).toBeNull();
  });
});
