/**
 * The horizontal timeline's sticky node column and its "Nodes / dates" corner
 * cover the day-column resize grips scrolling under them.
 *
 * Each grip lives in the sticky date header and runs the timeline's full
 * height. The header is a stacking context of its own (z 8), above the node
 * heads (z 7), so a grip slid under the 320px node column drew its line and
 * ⋮ over every lane name — and over the corner, which sat inside the same
 * header below the grips' z 12 — and pressing a lane name there started a
 * column resize.
 *
 * jsdom applies no CSS, so the contract is read from the stylesheet and the
 * DOM: the corner stands outside the header, the node heads outrank the
 * header, and the corner outranks both.
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
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

const css = readFileSync(resolve(process.cwd(), "src/styles/12-timeline.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

/** Declarations of the top-level rules whose selector list is exactly `selector`. */
function rules(selector: string): string {
  const out: string[] = [];
  const rule = /([^{}@]+)\{([^{}]*)\}/g;
  for (let r = rule.exec(css); r; r = rule.exec(css)) {
    if (r[1].split(",").some((part) => part.trim() === selector)) out.push(r[2]);
  }
  return out.join(";");
}

const zIndex = (selector: string) => {
  const match = /(?:^|[;\s])z-index:\s*(\d+)/.exec(rules(selector));
  return match ? Number(match[1]) : NaN;
};

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null as string | null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore([]);
  manualFrames();
});

afterEach(() => {
  useApp.getState().updateUIPreference("timelineOrientation", "vertical");
});

function renderHorizontal() {
  const graph: Graph = {
    version: 1,
    nodes: [
      { id: "a", title: "A" },
      { id: "b", title: "B" },
    ],
    edges: [],
    ui: { timelineOrder: ["a", "b"] },
  };
  useApp.setState({ graph });
  useApp.getState().updateUIPreference("timelineOrientation", "horizontal");
  return render(<LaneView variant="timeline" {...paneProps} />);
}

describe("horizontal timeline sticky column over the day grips", () => {
  it("keeps the corner out of the date header, whose grips it must cover", () => {
    const { container } = renderHorizontal();
    const timeline = container.querySelector<HTMLElement>(".horizontal-timeline")!;
    const head = timeline.querySelector<HTMLElement>(".horizontal-date-head")!;
    const corner = timeline.querySelector<HTMLElement>(".horizontal-corner")!;
    expect(head.querySelector(".timeline-column-resize")).not.toBeNull();
    expect(corner).not.toBeNull();
    expect(head.contains(corner)).toBe(false);
    // The header still starts its days one node column in, so they line up
    // with the cells below.
    const nodeHead = timeline.querySelector<HTMLElement>(".horizontal-node-head")!;
    const lead = head.firstElementChild as HTMLElement;
    expect(lead.classList.contains("horizontal-date-label")).toBe(false);
    expect(lead.style.width).toBe(nodeHead.style.width);
    expect(corner.style.width).toBe(nodeHead.style.width);
  });

  it("stacks the corner over the node heads, and the node heads over the date header", () => {
    const header = zIndex(".horizontal-date-head");
    const nodeHead = zIndex(".horizontal-node-head");
    const corner = zIndex(".horizontal-corner");
    expect(nodeHead).toBeGreaterThan(header);
    expect(corner).toBeGreaterThan(nodeHead);
  });
});
