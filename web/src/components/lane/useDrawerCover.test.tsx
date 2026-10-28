/**
 * The node drawer is laid over the panes' right side, toolbars included. At
 * 1280×800 the default 580px drawer sat on the graph toolbar's Problems and
 * ⋯ and on the timeline toolbar's orientation switch, ? and ⋯: they could not
 * be clicked without closing the drawer. A pane measures how far the drawer
 * reaches into its toolbar and the toolbar keeps its controls left of it.
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { drawerCover, useDrawerCover } from "./useDrawerCover";

const bar = { left: 340, top: 46, right: 1280, bottom: 82 };

describe("drawerCover", () => {
  it("is how far a side drawer reaches into the toolbar", () => {
    expect(drawerCover(bar, [{ left: 700, top: 46, right: 1280, bottom: 800 }])).toBe(580);
  });

  it("is the collapsed rail's width when only the rail is out", () => {
    expect(drawerCover(bar, [{ left: 1250, top: 46, right: 1280, bottom: 800 }])).toBe(30);
  });

  it("is nothing when the drawer is not beside the toolbar", () => {
    // A phone's sheet over the bottom half, below the upper pane's toolbar.
    expect(drawerCover(bar, [{ left: 0, top: 400, right: 1280, bottom: 800 }])).toBe(0);
    expect(drawerCover(bar, [])).toBe(0);
  });

  it("is nothing when the drawer covers the whole toolbar: no padding would bring it back", () => {
    expect(drawerCover(bar, [{ left: 0, top: 0, right: 1280, bottom: 800 }])).toBe(0);
  });
});

function Pane() {
  const ref = useRef<HTMLElement>(null);
  const cover = useDrawerCover(ref);
  return (
    <section ref={ref} className="lane-wrap" data-cover={cover}>
      <div className="board-title" />
    </section>
  );
}

describe("useDrawerCover", () => {
  let drawerLeft = 700;
  beforeEach(() => {
    drawerLeft = 700;
    const realRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
      this: HTMLElement,
    ) {
      if (this.classList.contains("board-title")) return new DOMRect(340, 46, 940, 36);
      if (this.classList.contains("drawer")) return new DOMRect(drawerLeft, 46, 1280 - drawerLeft, 754);
      return realRect.call(this);
    });
  });
  afterEach(() => vi.restoreAllMocks());

  function mount() {
    const stage = document.createElement("main");
    stage.className = "stage";
    document.body.appendChild(stage);
    const view = render(<Pane />, { container: stage });
    const pane = () => stage.querySelector<HTMLElement>(".lane-wrap")!;
    const openDrawer = () => {
      const drawer = document.createElement("div");
      drawer.className = "drawer";
      stage.appendChild(drawer);
      return drawer;
    };
    return { ...view, stage, pane, openDrawer };
  }

  it("follows the drawer opening and closing beside the pane", async () => {
    const { pane, openDrawer, unmount } = mount();
    expect(pane().dataset.cover).toBe("0");
    let drawer!: HTMLElement;
    await act(async () => {
      drawer = openDrawer();
    });
    expect(pane().dataset.cover).toBe("580");
    await act(async () => {
      drawer.remove();
    });
    expect(pane().dataset.cover).toBe("0");
    unmount();
  });

  it("measures a drawer already open when the pane mounts", () => {
    const stage = document.createElement("main");
    document.body.appendChild(stage);
    const drawer = document.createElement("div");
    drawer.className = "drawer";
    stage.appendChild(drawer);
    render(<Pane />, { container: stage });
    expect(stage.querySelector<HTMLElement>(".lane-wrap")!.dataset.cover).toBe("580");
  });
});

describe("toolbar beside an open drawer", () => {
  const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );

  it("pads the pane's toolbar by the drawer's reach on a desktop", () => {
    const rule = /\.lane-wrap\[data-drawer-cover\]\s*>\s*\.board-title\s*\{([^}]*)\}/.exec(css);
    expect(rule).not.toBeNull();
    expect(rule![1]).toMatch(/padding-right:\s*calc\([^;]*var\(--drawer-cover\)/);
  });
});
