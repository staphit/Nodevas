/**
 * The vertical timeline's lane header is as tall with no lane mounted as with
 * lanes in it.
 *
 * The header is sticky but in flow, so the day rows start below it, and the
 * opening scroll puts today's row right under it. On a project switch the
 * header first holds only the date ruler's corner (18px) — a large timeline
 * mounts its lanes once the view is known, an empty project has none — and
 * the lanes' title and assignee lines then grow it to ~45px. The browser's
 * scroll anchoring kept the day rows where they were, so today's row ended up
 * 27px (40px at 150%) under the header, its date hidden.
 *
 * jsdom has no layout, so the contract is the DOM's: the ruler's corner
 * carries the same two lines as a lane head, unseen, so it alone holds the
 * header at a lane head's height.
 */
import { render } from "@testing-library/react";
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

function laneGraph(n: number): Graph {
  const nodes = Array.from({ length: n }, (_, i) => ({ id: `n${i}`, title: `N${i}` }));
  return { version: 1, nodes, edges: [], ui: { timelineOrder: nodes.map((node) => node.id) } };
}

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => 1000 });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => 600 });
  manualFrames();
});

afterEach(() => {
  Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
  Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
});

function rulerCorner(graph: Graph) {
  useApp.setState({ graph });
  const { container } = render(<LaneView variant="timeline" {...paneProps} />);
  const corner = container.querySelector<HTMLElement>(".lane-head-row > .lane-head-cell.ruler");
  expect(corner).not.toBeNull();
  return corner!;
}

describe("vertical timeline lane header height", () => {
  it.each([
    ["an empty project", 0],
    ["a project with lanes", 3],
    ["a culled 2000-lane project", 2000],
  ])("the ruler's corner holds a lane head's two lines, unseen, on %s", (_, lanes) => {
    const corner = rulerCorner(laneGraph(lanes));
    const copy = corner.querySelector<HTMLElement>(".lane-head-copy");
    expect(copy).not.toBeNull();
    expect(copy).toHaveAttribute("aria-hidden", "true");
    expect(copy!.style.visibility).toBe("hidden");
    expect(copy!.querySelector(".lane-head-title")).not.toBeNull();
    expect(copy!.querySelector(".lane-head-assignee")).not.toBeNull();
  });
});

// The header is as wide as what it heads: its lanes and the ruler's corner.
// An empty project's header was a lane wider than its corner (the board keeps
// one lane's room), so a 200px strip with nothing in it sat beside the ruler,
// its bottom rule stopping short of the day rows' grid.
describe("vertical timeline lane header width", () => {
  it.each([
    ["an empty project", 0],
    ["a project with lanes", 3],
    ["a culled 2000-lane project", 2000],
  ])("is the width of its lanes and the ruler's corner on %s", (_, lanes) => {
    const corner = rulerCorner(laneGraph(lanes));
    const row = corner.parentElement!;
    const children = Array.from(row.children) as HTMLElement[];
    const sum = children.reduce((total, child) => total + parseFloat(child.style.width || "0"), 0);
    expect(parseFloat(row.style.width)).toBe(sum);
  });
});
