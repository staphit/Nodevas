import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../../store";
import { ROW_H, type Col } from "../canvas/geometry";
import { useBoardReveal } from "./useBoardReveal";

// Ctrl+K and following a link both open the node's drawer and scroll the
// board to the card. The drawer is laid over the board's right side, so a card
// centred on the board's full width landed under it. The jump centres it on
// the part of the board that is still showing.

type Rect = { left: number; top: number; right: number; bottom: number };

function rectOf({ left, top, right, bottom }: Rect): DOMRect {
  return {
    left,
    top,
    right,
    bottom,
    x: left,
    y: top,
    width: right - left,
    height: bottom - top,
    toJSON: () => ({}),
  } as DOMRect;
}

const BOARD: Rect = { left: 100, top: 50, right: 1100, bottom: 650 };

function makeBoard() {
  const board = document.createElement("div");
  Object.defineProperty(board, "clientWidth", { value: BOARD.right - BOARD.left });
  Object.defineProperty(board, "clientHeight", { value: BOARD.bottom - BOARD.top });
  board.getBoundingClientRect = () => rectOf(BOARD);
  board.scrollTo = vi.fn() as unknown as typeof board.scrollTo;
  document.body.appendChild(board);
  return board;
}

function overlay(className: string, rect: Rect) {
  const element = document.createElement("aside");
  element.className = className;
  element.getBoundingClientRect = () => rectOf(rect);
  document.body.appendChild(element);
  return element;
}

const CARD_X = 2000;
const CARD_ROW_TOP = 900;

function reveal(board: HTMLDivElement) {
  const column = { node: { id: "n1" }, row: 3, index: 0, width: 160, height: 60 } as unknown as Col;
  useApp.setState({ revealRequest: { nodeId: "n1", at: Date.now() } });
  renderHook(() =>
    useBoardReveal({
      isGraph: true,
      boardRef: { current: board },
      graph: null,
      colOf: new Map([["n1", column]]),
      timelineColOf: new Map(),
      timelineCellWidth: 100,
      zoom: 1,
      centerX: () => CARD_X,
      rowTop: () => CARD_ROW_TOP,
      graphSelection: null,
      setGraphSelection: () => undefined,
    }),
  );
  const scrollTo = board.scrollTo as unknown as ReturnType<typeof vi.fn>;
  expect(scrollTo).toHaveBeenCalledTimes(1);
  return scrollTo.mock.calls[0][0] as { left: number; top: number };
}

describe("revealing a node on the board", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => {
    document.body.innerHTML = "";
    useApp.setState({ revealRequest: null });
  });

  it("centres the card on the whole board when nothing covers it", () => {
    const target = reveal(makeBoard());
    expect(target.left).toBe(CARD_X - 500);
    expect(target.top).toBe(CARD_ROW_TOP + ROW_H / 2 - 300);
  });

  it("centres the card in the part of the board the drawer leaves showing", () => {
    const board = makeBoard();
    // The drawer covers the right 600px of the 1000px board.
    overlay("drawer", { left: 500, top: 50, right: 1100, bottom: 650 });
    const target = reveal(board);
    // Visible: board x 0–400, so the card's centre lands at x 200.
    expect(target.left).toBe(CARD_X - 200);
    expect(target.top).toBe(CARD_ROW_TOP + ROW_H / 2 - 300);
  });

  it("centres above a bottom sheet the same way", () => {
    const board = makeBoard();
    // A phone's drawer is a sheet over the lower half.
    overlay("drawer", { left: 0, top: 350, right: 1200, bottom: 700 });
    const target = reveal(board);
    expect(target.left).toBe(CARD_X - 500);
    // Visible: board y 0–300.
    expect(target.top).toBe(CARD_ROW_TOP + ROW_H / 2 - 150);
  });

  it("leaves out the collapsed drawer's rail and any panel marked as covering the board", () => {
    const board = makeBoard();
    overlay("drawer collapsed-rail", { left: 1070, top: 50, right: 1100, bottom: 650 });
    const panel = overlay("graph-analysis-panel", { left: 100, top: 50, right: 400, bottom: 650 });
    panel.setAttribute("data-board-overlay", "");
    const target = reveal(board);
    // Visible: board x 300–970.
    expect(target.left).toBe(CARD_X - 635);
  });
});
