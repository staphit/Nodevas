import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { LaneView } from "./LaneView";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
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

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

function graph(nodes: Graph["nodes"]): Graph {
  return { version: 1, nodes, edges: [], ui: {} };
}

beforeEach(() => {
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
  useApp.setState({
    graph: graph([{ id: "a", title: "設計稿" }]),
    graphRev: "rev-1",
    runState: EMPTY_RUN,
    statuses: {},
    issues: [],
    operations: {},
    error: null,
    tabs: [],
    activeTab: null,
  });
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

describe("LaneView", () => {
  it("offers node creation without a right click", async () => {
    const user = userEvent.setup();
    render(<LaneView variant="graph" {...paneProps} />);

    const create = screen.getByRole("button", { name: /新增節點/ });
    await user.click(create);
    // The creation form opens in place; the context menu stays a shortcut.
    expect(screen.getByLabelText(/節點標題/)).toBeInTheDocument();
  });

  it("remembers whether alignment is on", async () => {
    const user = userEvent.setup();
    render(<LaneView variant="graph" {...paneProps} />);

    const toggle = screen.getByRole("button", { name: /對齊輔助/ });
    // On by default: lining a board up by hand is the tedious part.
    expect(toggle).toHaveAttribute("aria-pressed", "true");

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(useApp.getState().preferences.graphSnapGuides).toBe(false);
  });

  // The node scale rides the board's CSS zoom, and the text divides it back
  // out — which is what lets the cards grow while the words stay put.
  it("scales the cards and the card text separately", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const inner = container.querySelector<HTMLElement>(".board-inner")!;

    await user.click(screen.getByText(/節點 100% · 文字 100%/));
    const [node, font] = screen.getAllByRole("slider");

    fireEvent.change(node, { target: { value: "1.5" } });
    // The cards are drawn half again as large, and their text divides that
    // back out so the words are the size they always were.
    expect(inner.style.zoom).toBe("1.5");
    expect(inner.style.getPropertyValue("--card-font-scale")).toBe(
      String(1 / 1.5),
    );

    fireEvent.change(font, { target: { value: "1.5" } });
    expect(inner.style.getPropertyValue("--card-font-scale")).toBe("1");

    await user.click(screen.getByRole("button", { name: "重設為 100%" }));
    expect(inner.style.zoom).toBe("1");
    expect(inner.style.getPropertyValue("--card-font-scale")).toBe("1");
  });

  // jsdom applies no CSS, so what these assert is the contract with
  // 04-board.css: the attribute and the variable, not the hiding itself.
  it("steps the cards down to less detail as the board is drawn smaller", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const inner = container.querySelector<HTMLElement>(".board-inner")!;

    expect(inner).not.toHaveAttribute("data-lod");

    await user.click(screen.getByText(/節點 100% · 文字 100%/));
    const [node] = screen.getAllByRole("slider");

    fireEvent.change(node, { target: { value: "0.7" } });
    expect(inner).toHaveAttribute("data-lod", "compact");
    expect(inner.style.getPropertyValue("--view-scale")).toBe("0.7");

    fireEvent.change(node, { target: { value: "0.5" } });
    expect(inner).toHaveAttribute("data-lod", "minimal");

    fireEvent.change(node, { target: { value: "1" } });
    expect(inner).not.toHaveAttribute("data-lod");
  });

  it("puts the minimap behind its toolbar toggle", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    // Wide windows start with it: jsdom's 1024px counts as wide.
    expect(container.querySelector(".graph-minimap")).not.toBeNull();

    await user.click(screen.getByRole("button", { name: "縮圖" }));
    expect(container.querySelector(".graph-minimap")).toBeNull();
    expect(useApp.getState().preferences.graphMinimap).toBe(false);

    // The preference outlives this test file; put it back.
    act(() => {
      useApp.getState().updateUIPreference("graphMinimap", true);
    });
  });

  it("keeps the display scale off the timeline, which sizes its own cells", () => {
    const { container } = render(<LaneView variant="timeline" {...paneProps} />);
    expect(screen.queryByText(/節點 100%/)).not.toBeInTheDocument();
    const inner = container.querySelector<HTMLElement>(".board-inner")!;
    expect(inner.style.getPropertyValue("--card-font-scale")).toBe("1");
  });

  it("names the timeline column grip as applying to every date column", () => {
    useApp.getState().updateUIPreference("timelineOrientation", "horizontal");
    render(<LaneView variant="timeline" {...paneProps} />);
    const grips = screen.getAllByRole("separator", { name: /全部日期格寬度/ });
    expect(grips.length).toBeGreaterThan(0);
    expect(grips[0]).toHaveAttribute("aria-valuenow");
  });

  it("shows an empty state with a way out when the project has no nodes", () => {
    useApp.setState({ graph: graph([]) });
    render(<LaneView variant="timeline" {...paneProps} />);
    expect(screen.getByText("尚無節點")).toBeInTheDocument();
  });

  // A phone at 100% shows about two cards, so the first sight of a project is
  // fitted to the whole board — once, after which the zoom is the user's own.
  describe("opening fit on a phone", () => {
    // jsdom's matchMedia always reports "no match" and has no listener
    // plumbing, and its elements have no layout, so both are faked: the
    // breakpoint decides whether the fit runs, the measurements decide what
    // zoom it lands on.
    function mockNarrowViewport(narrow: boolean) {
      vi.stubGlobal(
        "matchMedia",
        vi.fn((query: string) => ({
          matches: narrow,
          media: query,
          onchange: null,
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
          addListener: vi.fn(),
          removeListener: vi.fn(),
          dispatchEvent: vi.fn(),
        })),
      );
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

    afterEach(() => {
      vi.unstubAllGlobals();
      Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    });

    it("fits the board once when phone-width", () => {
      mockNarrowViewport(true);
      // One 152×68 card plus the 72px fit padding needs 296×212 board pixels,
      // and 390/296 comes to 1.3 after the fit's own floor-to-tenth.
      mockBoardSize(390, 700);
      const { container, rerender } = render(
        <LaneView variant="graph" {...paneProps} />,
      );
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      expect(inner.style.zoom).toBe("1.3");

      // Once per project: a rerender is not a second first sight.
      rerender(<LaneView variant="graph" {...paneProps} />);
      expect(inner.style.zoom).toBe("1.3");
    });

    it("leaves the opening zoom alone on a desktop", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      expect(inner.style.zoom).toBe("1");
    });
  });
});
