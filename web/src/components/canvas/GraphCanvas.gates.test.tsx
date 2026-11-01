/**
 * Condition gates on a culled board. A pan that moves the culling window
 * brings a strip of gates in and drops another; the gates that stay drawn
 * keep their routing and are not worked out or drawn again, and every gate
 * lands where a board opened straight at that view puts it.
 */
import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import type { Graph, GraphEdge, GraphNode } from "../../types";
import { LaneView } from "../LaneView";
import { installPointerShims, manualFrames, resetBoardStore } from "./boardTestKit";
import { forgetBoardViews } from "./boardViewMemory";
import { COL_W, ROW_H } from "./geometry";
import * as geometry from "./geometry";
import * as dependencyLine from "./DependencyLine";

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

// Count the routing work and the wires drawn, without changing either.
vi.mock("./geometry", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./geometry")>();
  return { ...actual, nearestClearRatio: vi.fn(actual.nearestClearRatio) };
});
vi.mock("./DependencyLine", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./DependencyLine")>();
  return { ...actual, DependencyLine: vi.fn(actual.DependencyLine) };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null as string | null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

// Same layout as GraphCanvas.culling.test.tsx: card (0, 0) at the top left.
const ORIGIN_X = 8 * COL_W + 2 * COL_W;
const ORIGIN_Y = 8 * ROW_H + ROW_H;
const GRID_W = 32;
const CARDS = 320;

/**
 * 320 cards, enough to be culled, each wired to its left neighbour; every odd card from the third
 * column on also waits on the card two to its left, under an `or`, so about
 * half the board draws a gate box. A few gates are placed by hand, one with
 * its box on top of a card so it has to be pushed clear.
 */
function gatedGraph(): Graph {
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const positions: Record<string, { x: number; y: number }> = {};
  for (let i = 0; i < CARDS; i++) {
    const id = `n${i}`;
    const col = i % GRID_W;
    const node: GraphNode = { id, title: `Node ${i}` };
    positions[id] = { x: col, y: Math.floor(i / GRID_W) };
    if (col) edges.push({ from: `n${i - 1}`, to: id });
    if (i % 2 === 1 && col >= 2) {
      edges.push({ from: `n${i - 2}`, to: id });
      node.requires = `n${i - 1} or n${i - 2}`;
    }
    nodes.push(node);
  }
  return {
    version: 1,
    nodes,
    edges,
    ui: {
      positions,
      gates: {
        n3: { x: 2 * COL_W, y: 0 },
        n45: { x: 5 * COL_W, y: ROW_H + 10 },
        n91: { x: 9 * COL_W + 40, y: 2 * ROW_H - 30 },
      },
    },
  };
}

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

/*
 * Every test here is synchronous jsdom work, timed against vitest's 5 s limit
 * however busy the machine is, so the board is kept to what the assertions
 * need. A small graph that is still culled and a small view: its window mounts
 * some forty cards and twenty gates, and a pan brings in thirty more. At 1000
 * cards in a 1000 by 600 view the slowest test ran past the limit whenever
 * the machine was busy.
 */
function setUpBoard() {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  mockBoardSize(400, 240);
  frames = manualFrames();
}

function tearDownBoard() {
  Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
  Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
}

// The first board a worker draws also pays for warming up the board's code,
// which under a busy machine took a large part of the first test's time.
// Drawing one up front leaves the tests to time only what they check.
beforeAll(() => {
  setUpBoard();
  renderBoard().unmount();
  forgetBoardViews();
  tearDownBoard();
}, 30_000);

beforeEach(setUpBoard);
afterEach(tearDownBoard);

function renderBoard() {
  useApp.setState({ graph: gatedGraph() });
  const view = render(<LaneView variant="graph" {...paneProps} />);
  const board = view.container.querySelector<HTMLElement>(".board")!;
  const panTo = (col: number, row: number) => {
    act(() => {
      board.scrollLeft = ORIGIN_X + col * COL_W;
      board.scrollTop = ORIGIN_Y + row * ROW_H;
      fireEvent.scroll(board);
    });
    act(() => frames.flush());
  };
  /** Each drawn gate box and the wires into it, by the node it feeds. */
  const gates = () => {
    const drawn = new Map<string, string>();
    // The boxes are looked up in one pass: a selector per gate walked the
    // whole board once for each of them, and in jsdom those walks were close
    // to half of the slowest test's time.
    const boxes = new Map<string, Element>();
    for (const badge of view.container.querySelectorAll("[data-gate-for]")) {
      const target = badge.getAttribute("data-gate-for")!;
      const box = badge.querySelector(".gate-node");
      if (box && !boxes.has(target)) boxes.set(target, box);
    }
    for (const group of view.container.querySelectorAll<SVGGElement>("g.gate")) {
      const keys = [...group.querySelectorAll(".dependency-line[data-edge-key]")].map(
        (line) => line.getAttribute("data-edge-key")!,
      );
      const target = keys[0]?.split("->")[1];
      const box = boxes.get(target)!;
      const points = [...group.querySelectorAll("polyline")].map((line) =>
        line.getAttribute("points"),
      );
      drawn.set(target, `${box.getAttribute("x")},${box.getAttribute("y")} ${points.join(" | ")}`);
    }
    return drawn;
  };
  const lines = () =>
    new Set(
      [...view.container.querySelectorAll(".dependency-line[data-edge-key]")].map(
        (line) => line.getAttribute("data-edge-key")!,
      ),
    );
  return { ...view, board, panTo, gates, lines };
}

describe("gates on a culled board", () => {
  it("works out the routing of only the gates a pan brings in", () => {
    const { panTo, gates } = renderBoard();
    const before = gates();
    expect(before.size).toBeGreaterThan(10);

    vi.mocked(geometry.nearestClearRatio).mockClear();
    panTo(4, 1);
    const after = gates();
    const entered = [...after.keys()].filter((target) => !before.has(target));

    expect(entered.length).toBeGreaterThan(0);
    expect(vi.mocked(geometry.nearestClearRatio).mock.calls.length).toBeLessThanOrEqual(
      entered.length,
    );
  });

  it("draws no wire again that was already drawn before the pan", () => {
    const { panTo, lines } = renderBoard();
    const before = lines();
    vi.mocked(dependencyLine.DependencyLine).mockClear();

    panTo(4, 1);

    const redrawn = vi
      .mocked(dependencyLine.DependencyLine)
      .mock.calls.map(([props]) => props.edgeKey)
      .filter((key): key is string => Boolean(key) && before.has(key!));
    expect(redrawn).toEqual([]);
  });

  it("puts every gate where a board opened at that view does", () => {
    const panned = renderBoard();
    panned.panTo(4, 1);
    panned.panTo(9, 3);
    panned.panTo(6, 2);
    const afterPans = panned.gates();
    panned.unmount();

    const fresh = renderBoard();
    fresh.panTo(6, 2);
    const opened = fresh.gates();

    expect(afterPans.size).toBeGreaterThan(10);
    expect(opened.size).toBeGreaterThan(10);
    for (const [target, drawn] of opened) {
      if (afterPans.has(target)) expect(afterPans.get(target)).toBe(drawn);
    }
    // The hand-placed boxes are on screen and pushed off the card under them.
    expect(afterPans.has("n45")).toBe(true);
    expect(afterPans.has("n91")).toBe(true);
  });

  // A box that cannot sit clear of every card still has to read: it is drawn
  // in a layer over the cards, not under them with the wires.
  it("draws the gate boxes over the cards", () => {
    const { container } = renderBoard();
    const cards = container.querySelectorAll(".col-card");
    const boxes = container.querySelectorAll(".gate-node");
    expect(cards.length).toBeGreaterThan(0);
    expect(boxes.length).toBeGreaterThan(10);
    const lastCard = cards[cards.length - 1];
    for (const box of boxes) {
      expect(box.closest("svg")?.classList.contains("board-gate-badges")).toBe(true);
      expect(
        lastCard.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    }
  });
});
