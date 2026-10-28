import { render } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { GraphMinimap } from "./GraphMinimap";
import type { Col } from "./geometry";

const cols: Col[] = ["a", "b", "c"].map((id, index) => ({
  node: { id, title: id },
  row: 0,
  index,
  width: 152,
  height: 68,
}));

function props(overrides: Record<string, unknown> = {}) {
  return {
    cols,
    selectedGraphNodeIDs: new Set<string>(),
    boardRef: createRef<HTMLDivElement>(),
    boardW: 4000,
    svgH: 2000,
    zoom: 1,
    boardViewport: { width: 1000, height: 600 },
    graphOffsetX: 1640,
    graphOffsetY: 720,
    centerX: vi.fn((column: Col) => 1640 + column.index * 164 + 82),
    rowTop: vi.fn((row: number) => 720 + row * 80),
    ...overrides,
  };
}

describe("GraphMinimap", () => {
  // The board hands down new layout functions on every render; the map's
  // dots only move when the layout they read does.
  it("does not lay its dots out again while the layout is unchanged", () => {
    const first = props();
    const { container, rerender } = render(<GraphMinimap {...first} />);
    expect(container.querySelectorAll(".graph-minimap-node")).toHaveLength(3);

    const same = props({ selectedGraphNodeIDs: first.selectedGraphNodeIDs });
    rerender(<GraphMinimap {...same} />);
    expect(same.centerX).not.toHaveBeenCalled();

    const moved = props({ selectedGraphNodeIDs: first.selectedGraphNodeIDs, graphOffsetX: 1700 });
    rerender(<GraphMinimap {...moved} />);
    expect(moved.centerX).toHaveBeenCalled();
  });
});
