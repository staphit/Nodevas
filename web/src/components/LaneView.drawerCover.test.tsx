/**
 * Both panes keep their toolbar's right-hand controls (Problems, the timeline
 * orientation switch, ?, ⋯) clear of an open node drawer: the pane is told
 * how far the drawer reaches into its toolbar.
 */
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import { installPointerShims, resetBoardStore } from "./canvas/boardTestKit";
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
  resetBoardStore(["a"]);
  const realRect = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
    this: HTMLElement,
  ) {
    if (this.classList.contains("board-title")) return new DOMRect(340, 46, 940, 36);
    if (this.classList.contains("drawer")) return new DOMRect(700, 46, 580, 754);
    if (this.classList.contains("board")) return new DOMRect(340, 82, 940, 718);
    return realRect.call(this);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  document.querySelectorAll(".stage").forEach((stage) => stage.remove());
});

describe.each(["graph", "timeline"] as const)("%s pane beside an open drawer", (variant) => {
  it("pads its toolbar by the drawer's reach, and drops it when the drawer closes", async () => {
    const stage = document.createElement("main");
    stage.className = "stage";
    document.body.appendChild(stage);
    const { unmount } = render(<LaneView variant={variant} {...paneProps} />, { container: stage });
    const pane = stage.querySelector<HTMLElement>(".lane-wrap")!;
    expect(pane.hasAttribute("data-drawer-cover")).toBe(false);

    const drawer = document.createElement("div");
    drawer.className = "drawer";
    await act(async () => {
      stage.appendChild(drawer);
    });
    expect(pane.hasAttribute("data-drawer-cover")).toBe(true);
    expect(pane.style.getPropertyValue("--drawer-cover")).toBe("580px");

    await act(async () => {
      drawer.remove();
    });
    expect(pane.hasAttribute("data-drawer-cover")).toBe(false);
    expect(pane.style.getPropertyValue("--drawer-cover")).toBe("");
    unmount();
  });
});

describe("vertical timeline's date ruler beside an open drawer", () => {
  beforeEach(() => {
    // The board's scrollport, inside its 10px scrollbar: 340…1270.
    Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => 930 });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => 700 });
  });
  afterEach(() => {
    Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
    Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
  });

  // The ruler sticks to the board's right edge, which an open 580px drawer
  // covers at 1280px: every date, today's and the current time with them,
  // was under the drawer. It sticks to the drawer's edge instead.
  it("sticks the ruler left of the drawer, and back to the edge when it closes", async () => {
    const stage = document.createElement("main");
    stage.className = "stage";
    document.body.appendChild(stage);
    const { unmount } = render(<LaneView variant="timeline" {...paneProps} />, { container: stage });
    const rulers = () => [
      ...stage.querySelectorAll<HTMLElement>(".board-date-r, .lane-head-row > .lane-head-cell.ruler"),
    ];
    expect(stage.querySelector(".board-date-r.today")).not.toBeNull();
    for (const ruler of rulers()) expect(ruler.style.right).toBe("");

    const drawer = document.createElement("div");
    drawer.className = "drawer";
    await act(async () => {
      stage.appendChild(drawer);
    });
    // The scrollport's right edge (1270) less the drawer's left (700).
    expect(rulers().length).toBeGreaterThan(1);
    for (const ruler of rulers()) expect(ruler.style.right).toBe("570px");

    await act(async () => {
      drawer.remove();
    });
    for (const ruler of rulers()) expect(ruler.style.right).toBe("");
    unmount();
  });
});

describe("horizontal timeline's node column beside an open drawer", () => {
  beforeEach(() => {
    // The board's scrollport, inside its 10px scrollbar: 340…1270.
    Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => 930 });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => 700 });
  });
  afterEach(() => {
    Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
    Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
  });

  const renderHorizontal = (nodeAxis: "left" | "right") => {
    useApp.setState({
      preferences: {
        ...useApp.getState().preferences,
        timelineOrientation: "horizontal",
        timelineNodeAxis: nodeAxis,
      },
    });
    const stage = document.createElement("main");
    stage.className = "stage";
    document.body.appendChild(stage);
    const view = render(<LaneView variant="timeline" {...paneProps} />, { container: stage });
    const column = () => [
      ...stage.querySelectorAll<HTMLElement>(
        ".horizontal-corner, .horizontal-date-lead, .horizontal-node-head",
      ),
    ];
    const openDrawer = async () => {
      const drawer = document.createElement("div");
      drawer.className = "drawer";
      await act(async () => {
        stage.appendChild(drawer);
      });
      return drawer;
    };
    return { stage, view, column, openDrawer };
  };

  // The sticky node column is a whole date cell wide (200–360px). Beside a
  // 580px drawer at 1280px the board shows 360px, so the column left a sliver
  // of the dates beside it. It narrows to a third of what the drawer leaves,
  // names cut short with the full name on hover, and is a cell wide again
  // once the drawer closes.
  it("narrows to a third of the uncovered board, and widens again when it closes", async () => {
    const { column, openDrawer, view } = renderHorizontal("left");
    expect(column().length).toBe(3);
    const full = column()[0].style.width;
    expect(parseFloat(full)).toBeGreaterThanOrEqual(200);
    for (const cell of column()) expect(cell.style.width).toBe(full);

    const drawer = await openDrawer();
    // The scrollport (930) less the drawer's reach into it (570), over three.
    for (const cell of column()) expect(cell.style.width).toBe("120px");
    const head = column().find((cell) => cell.classList.contains("horizontal-node-head"))!;
    expect(head).toHaveAttribute("title", expect.stringContaining("a"));
    expect(head.closest(".horizontal-timeline")).toHaveClass("node-column-compact");

    await act(async () => {
      drawer.remove();
    });
    for (const cell of column()) expect(cell.style.width).toBe(full);
    expect(head.closest(".horizontal-timeline")).not.toHaveClass("node-column-compact");
    view.unmount();
  });

  // On the right the column sticks to the board's right edge: all of it was
  // under the drawer. It sticks beside the drawer instead, like the vertical
  // timeline's date ruler.
  it("sticks a right-hand node column beside the drawer", async () => {
    const { stage, openDrawer, view } = renderHorizontal("right");
    const sticky = () => [
      ...stage.querySelectorAll<HTMLElement>(".horizontal-corner, .horizontal-node-head"),
    ];
    for (const cell of sticky()) expect(cell.style.right).toBe("");
    const drawer = await openDrawer();
    expect(sticky().length).toBe(2);
    for (const cell of sticky()) expect(cell.style.right).toBe("570px");
    await act(async () => {
      drawer.remove();
    });
    for (const cell of sticky()) expect(cell.style.right).toBe("");
    view.unmount();
  });
});
