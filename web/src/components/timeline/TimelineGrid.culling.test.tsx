/**
 * Lane culling on a large timeline. A 2000-node project drew every node's
 * column on every day row — 57,000 elements — so opening the timeline, or a
 * node's drawer beside it, blocked the page for 240–480 ms. Past a few dozen
 * lanes the timeline mounts only the ones near the view, as the graph does
 * with its cards, and stands empty space in for the rest so nothing moves.
 */
import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { installPointerShims, manualFrames, resetBoardStore } from "../canvas/boardTestKit";
import { LaneView } from "../LaneView";

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

const VIEW_W = 1000;
const VIEW_H = 600;

/** n nodes in timeline order n0, n1, … */
function laneGraph(n: number): Graph {
  const nodes = Array.from({ length: n }, (_, i) => ({ id: `n${i}`, title: `N${i}` }));
  return { version: 1, nodes, edges: [], ui: { timelineOrder: nodes.map((node) => node.id) } };
}

function mockBoardSize(width: number, height: number) {
  Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => width });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => height });
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
});

function renderTimeline(graph: Graph, props: Partial<typeof paneProps> = {}) {
  useApp.setState({ graph });
  const view = render(<LaneView variant="timeline" {...paneProps} {...props} />);
  const board = view.container.querySelector<HTMLElement>(".board")!;
  const heads = () => [...view.container.querySelectorAll<HTMLElement>(".lane-head-cell.timeline-order-handle")];
  const head = (id: string) => heads().find((cell) => cell.querySelector(".lane-head-title")?.textContent === id.toUpperCase());
  const rows = () => [...view.container.querySelectorAll<HTMLElement>(".board-row[data-date]")];
  const nodeRows = () => [...view.container.querySelectorAll<HTMLElement>(".horizontal-node-row")];
  const nodeRow = (id: string) =>
    nodeRows().find((row) => row.querySelector(".lane-head-title")?.textContent === id.toUpperCase());
  const cellWidth = () => parseFloat(heads()[0]?.style.width || "0");
  /** The widths a row's children take, cells and spacers alike. */
  const span = (row: Element) =>
    [...row.children]
      .filter((child) => !child.classList.contains("now-line"))
      .reduce((sum, child) => sum + parseFloat((child as HTMLElement).style.width || "0"), 0);
  const scrollTo = (left: number, top: number) => {
    act(() => {
      board.scrollLeft = left;
      board.scrollTop = top;
      fireEvent.scroll(board);
    });
    act(() => frames.flush());
  };
  return { ...view, board, heads, head, rows, nodeRows, nodeRow, cellWidth, span, scrollTo };
}

describe("timeline lane culling", () => {
  it("mounts only the columns near the view on a 2000-node timeline", () => {
    const { heads, head, rows } = renderTimeline(laneGraph(2000));
    expect(heads().length).toBeGreaterThanOrEqual(5);
    expect(heads().length).toBeLessThan(20);
    expect(head("n0")).toBeDefined();
    expect(head("n1999")).toBeUndefined();
    for (const row of rows()) {
      expect(row.querySelectorAll(".board-cell").length).toBe(heads().length);
    }
  });

  it("keeps every column where it was: the skipped ones stand as empty space", () => {
    const { container, rows, cellWidth, span, scrollTo } = renderTimeline(laneGraph(2000));
    const w = cellWidth();
    expect(w).toBeGreaterThan(0);
    scrollTo(1000 * w, 0);
    const headRow = container.querySelector(".lane-head-row")!;
    // Every lane's width, plus the date ruler.
    expect(span(headRow)).toBe(2000 * w + 56);
    for (const row of rows()) expect(span(row)).toBe(2000 * w + 56);
  });

  it("swaps the mounted columns as the timeline scrolls sideways", () => {
    const { heads, head, cellWidth, scrollTo } = renderTimeline(laneGraph(2000));
    scrollTo(1000 * cellWidth(), 0);
    expect(head("n1000")).toBeDefined();
    expect(head("n1003")).toBeDefined();
    expect(head("n0")).toBeUndefined();
    expect(heads().length).toBeLessThan(20);
  });

  it("keeps the selected node's column mounted far outside the view", () => {
    const { head, cellWidth, scrollTo } = renderTimeline(laneGraph(2000), { selectedNodeId: "n1500" });
    // The selected node leads the timeline; scrolled away, it stays.
    scrollTo(1000 * cellWidth(), 0);
    expect(head("n1500")).toHaveClass("node-selected");
  });

  it("keeps a lane mounted while it is dragged to a new place", () => {
    const { head, cellWidth, scrollTo } = renderTimeline(laneGraph(2000));
    const dragged = head("n0")!;
    act(() => {
      fireEvent.pointerDown(dragged, { button: 0, pointerId: 1, clientX: 0, clientY: 0 });
      fireEvent.pointerMove(dragged, { pointerId: 1, clientX: 40, clientY: 0 });
    });
    scrollTo(1000 * cellWidth(), 0);
    expect(head("n0")).toBe(dragged);
    expect(dragged).toHaveClass("order-dragging");
  });

  it("draws every column of a small timeline", () => {
    const { heads } = renderTimeline(laneGraph(12));
    expect(heads().length).toBe(12);
  });

  it("mounts only the node rows near the view on a horizontal timeline", () => {
    useApp.getState().updateUIPreference("timelineOrientation", "horizontal");
    const { nodeRows, nodeRow, scrollTo } = renderTimeline(laneGraph(2000));
    expect(nodeRows().length).toBeGreaterThanOrEqual(5);
    expect(nodeRows().length).toBeLessThan(20);
    expect(nodeRow("n0")).toBeDefined();
    const h = parseFloat(nodeRows()[0].style.height);
    scrollTo(0, 48 + 1000 * h);
    expect(nodeRow("n1000")).toBeDefined();
    expect(nodeRow("n0")).toBeUndefined();
    const rowsBox = nodeRows()[0].parentElement!;
    const height = [...rowsBox.children].reduce(
      (sum, child) => sum + parseFloat((child as HTMLElement).style.height || "0"),
      0,
    );
    expect(height).toBe(2000 * h);
  });
});
