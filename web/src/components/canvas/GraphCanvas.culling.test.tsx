/**
 * Viewport culling on large boards: past a few hundred cards only the ones in
 * (or near) the visible part of the board are mounted, and a pan swaps them by
 * frame. Whatever the user is holding — a selection, keyboard focus, a drag —
 * stays mounted however far away the view goes.
 */
import { act, fireEvent, render } from "@testing-library/react";
import { Profiler } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import type { Graph, GraphEdge } from "../../types";
import { LaneView } from "../LaneView";
import { installPointerShims, manualFrames, resetBoardStore } from "./boardTestKit";
import { COL_W, ROW_H } from "./geometry";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      putGraph: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      createNode: vi.fn(),
    },
  };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null as string | null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

// The board opens scrolled to the pan buffer (8 columns, 8 rows) and draws the
// graph two columns and one row further in: card (0, 0) is the top-left one
// on screen.
const BUFFER_X = 8 * COL_W;
const BUFFER_Y = 8 * ROW_H;
const ORIGIN_X = BUFFER_X + 2 * COL_W;
const ORIGIN_Y = BUFFER_Y + ROW_H;
const GRID_W = 40;
const VIEW_W = 1000;
const VIEW_H = 600;

/** n nodes on a grid GRID_W cards wide, n0 at the top left. */
function gridGraph(n: number, edges: GraphEdge[] = []): Graph {
  const nodes = Array.from({ length: n }, (_, i) => ({ id: `n${i}`, title: `Node ${i}` }));
  const positions: Record<string, { x: number; y: number }> = {};
  nodes.forEach((node, i) => {
    positions[node.id] = { x: i % GRID_W, y: Math.floor(i / GRID_W) };
  });
  return { version: 1, nodes, edges, ui: { positions } };
}

const idAt = (col: number, row: number) => `n${row * GRID_W + col}`;

function mockBoardSize(width: number, height: number) {
  Object.defineProperty(HTMLElement.prototype, "clientWidth", {
    configurable: true,
    get: () => width,
  });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get: () => height,
  });
}

let frames: ReturnType<typeof manualFrames>;

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  mockBoardSize(VIEW_W, VIEW_H);
  frames = manualFrames();
});

afterEach(() => {
  Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
  Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
  Reflect.deleteProperty(HTMLElement.prototype, "scrollTo");
});

function renderBoard(graph: Graph, props: Partial<typeof paneProps> = {}) {
  useApp.setState({ graph });
  const view = render(<LaneView variant="graph" {...paneProps} {...props} />);
  const board = view.container.querySelector<HTMLElement>(".board")!;
  const card = (id: string) =>
    view.container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"]`);
  const mounted = () => view.container.querySelectorAll(".col-card[data-node-id]").length;
  const wire = (from: string, to: string) =>
    view.container.querySelector(`.dependency-line[data-edge-key="${from}->${to}"]`);
  /** Scroll so card (col, row) is at the top-left of the view, then one frame. */
  const panTo = (col: number, row: number, flush = true) => {
    act(() => {
      board.scrollLeft = ORIGIN_X + col * COL_W;
      board.scrollTop = ORIGIN_Y + row * ROW_H;
      fireEvent.scroll(board);
    });
    if (flush) act(() => frames.flush());
  };
  return { ...view, board, card, mounted, wire, panTo };
}

describe("viewport culling", () => {
  it("mounts only the cards near the view on a 1000-card board", () => {
    const { card, mounted } = renderBoard(gridGraph(1000));

    // About 5×7 cards are on screen; the margin may add a few rings, never
    // the board.
    expect(mounted()).toBeGreaterThanOrEqual(35);
    expect(mounted()).toBeLessThan(150);
    expect(card(idAt(0, 0))).not.toBeNull();
    expect(card(idAt(4, 6))).not.toBeNull();
    expect(card(idAt(39, 24))).toBeNull();
    expect(card(idAt(20, 0))).toBeNull();
  });

  it("swaps the mounted cards on a pan, once per frame", () => {
    const { card, mounted, panTo } = renderBoard(gridGraph(1000));

    panTo(35, 20, false);
    // Scrolling alone schedules the work; nothing moves before the frame.
    expect(card(idAt(0, 0))).not.toBeNull();
    expect(card(idAt(39, 24))).toBeNull();

    act(() => frames.flush());
    expect(card(idAt(39, 24))).not.toBeNull();
    expect(card(idAt(35, 20))).not.toBeNull();
    expect(card(idAt(0, 0))).toBeNull();
    expect(mounted()).toBeLessThan(150);

    panTo(0, 0);
    expect(card(idAt(0, 0))).not.toBeNull();
    expect(card(idAt(39, 24))).toBeNull();
  });

  it("keeps every card below the threshold", () => {
    const { mounted } = renderBoard(gridGraph(200));
    expect(mounted()).toBe(200);
  });

  it("keeps a selected card mounted far outside the view", () => {
    const { card } = renderBoard(gridGraph(1000), { selectedNodeId: idAt(39, 24) });
    expect(card(idAt(39, 24))).toHaveClass("selected");
  });

  it("keeps the focused card and its tab neighbours mounted", () => {
    const { card, panTo, rerender } = renderBoard(gridGraph(1000));
    act(() => card(idAt(0, 0))!.focus());
    // Selection moves on; keyboard focus stays where it was.
    rerender(<LaneView variant="graph" {...paneProps} selectedNodeId={idAt(20, 20)} />);
    expect(card(idAt(0, 0))).not.toHaveClass("selected");

    panTo(30, 18);
    expect(card(idAt(0, 0))).not.toBeNull();
    expect(document.activeElement).toBe(card(idAt(0, 0)));
    // Tab lands on a mounted card: the next one in draw order, which runs
    // down each column of placed cards before the next column.
    expect(card(idAt(0, 1))).not.toBeNull();
    expect(card(idAt(5, 5))).toBeNull();
  });

  it("keeps a card mounted while it is dragged out of view", () => {
    const { card, panTo } = renderBoard(gridGraph(1000));
    const dragged = card(idAt(0, 0))!;
    act(() => {
      fireEvent.pointerDown(dragged, { button: 0, pointerId: 1, clientX: 0, clientY: 0 });
      fireEvent.pointerMove(dragged, { pointerId: 1, clientX: 40, clientY: 0 });
    });
    expect(dragged).toHaveClass("lifting");

    panTo(30, 18);
    expect(card(idAt(0, 0))).toBe(dragged);
    expect(dragged).toHaveClass("lifting");
  });

  it("mounts the target of a jump before it scrolls there", () => {
    Object.defineProperty(HTMLElement.prototype, "scrollTo", {
      configurable: true,
      value(this: HTMLElement, options: ScrollToOptions) {
        this.scrollLeft = options.left ?? this.scrollLeft;
        this.scrollTop = options.top ?? this.scrollTop;
        fireEvent.scroll(this);
      },
    });
    const { card } = renderBoard(gridGraph(1000));
    expect(card(idAt(39, 24))).toBeNull();

    act(() => useApp.getState().revealNode(idAt(39, 24)));
    expect(card(idAt(39, 24))).toHaveClass("selected");

    act(() => frames.flush());
    expect(card(idAt(38, 24))).not.toBeNull();
    expect(card(idAt(0, 0))).toBeNull();
  });

  it("pans inside the window without rendering the board, moving only the minimap frame", () => {
    useApp.setState({ graph: gridGraph(1000) });
    let commits = 0;
    const { container } = render(
      <Profiler id="board" onRender={() => commits++}>
        <LaneView variant="graph" {...paneProps} />
      </Profiler>,
    );
    const board = container.querySelector<HTMLElement>(".board")!;
    const frame = container.querySelector<HTMLElement>(".graph-minimap-viewport")!;
    const before = { left: frame.style.left, top: frame.style.top };
    commits = 0;

    act(() => {
      board.scrollLeft += 40;
      board.scrollTop += 20;
      fireEvent.scroll(board);
    });
    act(() => frames.flush());

    expect(commits).toBe(0);
    expect(frame.style.left).not.toBe(before.left);
    expect(frame.style.top).not.toBe(before.top);
  });

  it("draws a wire whose ends are both off screen when it crosses the view", () => {
    const across = { from: idAt(2, 12), to: idAt(35, 12) };
    const far = { from: idAt(37, 24), to: idAt(38, 24) };
    const { card, wire, panTo } = renderBoard(gridGraph(1000, [across, far]));

    panTo(15, 10);
    expect(card(across.from)).toBeNull();
    expect(card(across.to)).toBeNull();
    expect(wire(across.from, across.to)).not.toBeNull();
    expect(wire(far.from, far.to)).toBeNull();
  });
});
