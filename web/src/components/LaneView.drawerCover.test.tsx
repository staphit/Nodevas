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
