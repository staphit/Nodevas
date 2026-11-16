import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useConnectionDrag } from "./useConnectionDrag";

function event(currentTarget: Element, overrides: Record<string, unknown> = {}) {
  return {
    button: 0,
    altKey: false,
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

/** The card the pointer is over for the whole of a test. */
function hoverCard(nodeId: string) {
  const card = document.createElement("div");
  card.className = "col-card";
  card.dataset.nodeId = nodeId;
  document.body.append(card);
  Object.defineProperty(document, "elementFromPoint", {
    configurable: true,
    value: vi.fn(() => card),
  });
}

function setup(options: { withoutModifier?: boolean } = {}) {
  const connectNodes = vi.fn();
  const connectLogicGateToNode = vi.fn();
  const { result } = renderHook(() =>
    useConnectionDrag({
      enabled: true,
      ...options,
      graphPointFromClient: (x, y) => ({ x, y }),
      connectionPointForNode: () => ({ x: 10, y: 10 }),
      onSelectGate: vi.fn(),
      connectNodes,
      connectNodeToLogicGate: vi.fn(),
      connectLogicGateToNode,
    }),
  );
  return { result, connectNodes, connectLogicGateToNode };
}

/** A press on the source, a drag onto the hovered card, and a release. */
function dragFrom(
  handlers: ReturnType<typeof setup>["result"]["current"]["handlers"],
  source: HTMLElement,
  start: () => boolean,
) {
  let started = false;
  act(() => {
    started = start();
    handlers.onPointerMove(event(source, { clientX: 40, clientY: 40 }));
    handlers.onPointerUp(event(source, { clientX: 40, clientY: 40 }), true);
  });
  return started;
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe("useConnectionDrag", () => {
  it("routes a node-to-node connection on pointer release", () => {
    hoverCard("target");
    const source = pointerTarget();
    const { result, connectNodes } = setup();
    const { handlers } = result.current;

    dragFrom(handlers, source, () => handlers.startNode(event(source, { altKey: true }), "source"));

    expect(connectNodes).toHaveBeenCalledWith("source", "target");
    expect(result.current.state).toBeNull();
  });

  // A plain drag from a card moves it: only Alt, the connect mode or the
  // card's connect dot make it a connection.
  it("leaves a plain drag from a card to the card drag", () => {
    hoverCard("target");
    const source = pointerTarget();
    const { result, connectNodes } = setup();
    const { handlers } = result.current;

    const started = dragFrom(handlers, source, () => handlers.startNode(event(source), "source"));

    expect(started).toBe(false);
    expect(connectNodes).not.toHaveBeenCalled();
  });

  // The toolbar's connect mode is for a finger, which has no Alt to hold. The
  // mode was let through by the board and then refused here, so a card in
  // connect mode neither connected nor moved.
  it("connects on a plain drag in connect mode", () => {
    hoverCard("target");
    const source = pointerTarget();
    const { result, connectNodes } = setup({ withoutModifier: true });
    const { handlers } = result.current;

    const started = dragFrom(handlers, source, () => handlers.startNode(event(source), "source"));

    expect(started).toBe(true);
    expect(connectNodes).toHaveBeenCalledWith("source", "target");
  });

  it("connects from a logic gate on a plain drag in connect mode", () => {
    hoverCard("target");
    const gate = pointerTarget();
    const start = (handlers: ReturnType<typeof setup>["result"]["current"]["handlers"]) =>
      handlers.startGate(
        event(gate) as unknown as React.PointerEvent<SVGGElement>,
        "gate-1",
        { x: 10, y: 10 },
      );

    const plain = setup();
    expect(dragFrom(plain.result.current.handlers, gate, () => start(plain.result.current.handlers))).toBe(false);
    expect(plain.connectLogicGateToNode).not.toHaveBeenCalled();

    const mode = setup({ withoutModifier: true });
    expect(dragFrom(mode.result.current.handlers, gate, () => start(mode.result.current.handlers))).toBe(true);
    expect(mode.connectLogicGateToNode).toHaveBeenCalledWith("gate-1", "target");
  });

  // The dot on a card's edge is the visible way to connect: a drag from it
  // needs no key and no mode.
  it("connects from the card's connect dot with nothing held", () => {
    hoverCard("target");
    const port = pointerTarget();
    const { result, connectNodes } = setup();
    const { handlers } = result.current;

    const started = dragFrom(handlers, port, () => handlers.startNode(event(port), "source", true));

    expect(started).toBe(true);
    expect(connectNodes).toHaveBeenCalledWith("source", "target");
  });

  it("does nothing for a press on the dot that never moves", () => {
    hoverCard("target");
    const port = pointerTarget();
    const { result, connectNodes } = setup();
    const { handlers } = result.current;

    act(() => {
      handlers.startNode(event(port), "source", true);
      handlers.onPointerUp(event(port), true);
    });

    expect(connectNodes).not.toHaveBeenCalled();
    expect(result.current.state).toBeNull();
  });
});
