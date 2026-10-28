/**
 * Both panes keep their toolbar's right-hand controls (Problems, the timeline
 * orientation switch, ?, ⋯) clear of an open node drawer: the pane is told
 * how far the drawer reaches into its toolbar.
 */
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
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
