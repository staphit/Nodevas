/**
 * The notice that stands in for the board while graph.yaml cannot be read
 * was ~312px wide at 1280×800 with the node drawer open, and with 150% text
 * the Reload button took a third of that: the parser's words wrapped a word
 * to a line. It is laid out in the part of the board the reader can see
 * (boardVisibleArea), as wide as that allows up to a reading width that grows
 * with the text, and below a width the text needs it puts Reload under the
 * words instead of beside them.
 *
 * On a 500px phone the node drawer is a sheet over the bottom 76% of the
 * window: ~110px of board showed above it, too little for the card, so it lay
 * under the sheet. Where the visible board cannot hold the card it goes full
 * width across the foot of the board, over the drawer, clear of the drawer's
 * head and its close button.
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { insetsWithin } from "./boardVisibleArea";
import { noticeAboveDrawer, noticeLayout, UnreadableGraphState } from "./UnreadableGraphState";

const MESSAGE =
  "graph.yaml: line 3 has an error. Fix the file, then reload. (yaml: line 3: did not find expected node content)";

describe("insetsWithin", () => {
  it("is how far the visible part sits inside the whole on each side", () => {
    expect(
      insetsWithin({ left: 340, top: 82, right: 1280, bottom: 800 }, { left: 340, top: 82, right: 700, bottom: 800 }),
    ).toEqual({ left: 0, top: 0, right: 580, bottom: 0 });
    expect(
      insetsWithin({ left: 0, top: 46, right: 500, bottom: 800 }, { left: 0, top: 46, right: 500, bottom: 420 }),
    ).toEqual({ left: 0, top: 0, right: 0, bottom: 380 });
  });

  it("is never negative", () => {
    expect(
      insetsWithin({ left: 10, top: 10, right: 100, bottom: 100 }, { left: 0, top: 0, right: 200, bottom: 200 }),
    ).toEqual({ left: 0, top: 0, right: 0, bottom: 0 });
  });
});

describe("noticeLayout", () => {
  it("keeps Reload beside the words where the visible board is wide", () => {
    expect(noticeLayout(940, 1)).toBe("row");
    expect(noticeLayout(940, 1.5)).toBe("row");
  });

  it("puts Reload under the words beside an open drawer", () => {
    // 1280 wide, 340px sidebar, 580px drawer: 360px of board shows.
    expect(noticeLayout(360, 1)).toBe("stacked");
    expect(noticeLayout(360, 1.5)).toBe("stacked");
  });

  it("needs more room before going side by side when the text is larger", () => {
    expect(noticeLayout(600, 1)).toBe("row");
    expect(noticeLayout(600, 1.5)).toBe("stacked");
  });
});

describe("noticeAboveDrawer", () => {
  const pane = { left: 340, top: 82, right: 1280, bottom: 800 };

  it("stays in the board where the drawer leaves room for the card", () => {
    // 1280×800, 580px drawer: 360px of board shows, at 100% and 150% text.
    const beside = { ...pane, right: 700 };
    expect(noticeAboveDrawer(pane, beside, 1)).toBe(false);
    expect(noticeAboveDrawer(pane, beside, 1.5)).toBe(false);
  });

  it("goes over the drawer when what shows is too short for the card", () => {
    // 500×800 phone, 76vh sheet: 110px of board shows above it.
    const phone = { left: 0, top: 82, right: 500, bottom: 800 };
    expect(noticeAboveDrawer(phone, { ...phone, bottom: 192 }, 1)).toBe(true);
    // The sheet dragged down to 300px leaves room.
    expect(noticeAboveDrawer(phone, { ...phone, bottom: 500 }, 1)).toBe(false);
  });

  it("goes over the drawer when what shows is too narrow, sooner with larger text", () => {
    const strip = { ...pane, right: 600 };
    expect(noticeAboveDrawer(pane, strip, 1)).toBe(false);
    expect(noticeAboveDrawer(pane, strip, 1.5)).toBe(true);
    expect(noticeAboveDrawer(pane, { ...pane, right: 440 }, 1)).toBe(true);
  });

  it("never goes over anything when nothing covers the board", () => {
    const small = { left: 0, top: 0, right: 180, bottom: 120 };
    expect(noticeAboveDrawer(small, small, 1)).toBe(false);
  });
});

describe("the notice in the visible part of the board", () => {
  let paneRect = new DOMRect(340, 82, 940, 718);
  let drawerRect = new DOMRect(700, 46, 580, 754);
  beforeEach(() => {
    paneRect = new DOMRect(340, 82, 940, 718);
    drawerRect = new DOMRect(700, 46, 580, 754);
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const realRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
      this: HTMLElement,
    ) {
      // The notice fills the pane only while it is in it; over the drawer it
      // is a band at the foot, so the pane is what is measured.
      if (this.classList.contains("lane-body")) return paneRect;
      if (this.classList.contains("drawer")) return drawerRect;
      return realRect.call(this);
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.documentElement.style.removeProperty("--font-scale");
    document.querySelectorAll("main[data-test-stage]").forEach((stage) => stage.remove());
  });

  function mount() {
    const stage = document.createElement("main");
    stage.className = "stage";
    stage.dataset.testStage = "";
    document.body.appendChild(stage);
    const view = render(
      <section className="lane-wrap">
        <div className="lane-body">
          <UnreadableGraphState message={MESSAGE} />
        </div>
      </section>,
      { container: stage },
    );
    const notice = () => stage.querySelector<HTMLElement>(".board-unreadable")!;
    const openDrawer = () => {
      const drawer = document.createElement("div");
      drawer.className = "drawer";
      stage.appendChild(drawer);
      return drawer;
    };
    return { ...view, notice, openDrawer };
  }

  it("lays out beside the words in a pane with no drawer", () => {
    const { notice, unmount } = mount();
    expect(notice().style.getPropertyValue("--notice-right")).toBe("0px");
    expect(notice().dataset.layout).toBe("row");
    expect(notice().hasAttribute("data-above-drawer")).toBe(false);
    unmount();
  });

  it("keeps clear of an open drawer and stacks, and widens again when it closes", async () => {
    const { notice, openDrawer, unmount } = mount();
    let drawer!: HTMLElement;
    await act(async () => {
      drawer = openDrawer();
    });
    expect(notice().style.getPropertyValue("--notice-right")).toBe("580px");
    expect(notice().style.getPropertyValue("--notice-left")).toBe("0px");
    expect(notice().dataset.layout).toBe("stacked");
    expect(notice().hasAttribute("data-above-drawer")).toBe(false);
    await act(async () => {
      drawer.remove();
    });
    expect(notice().style.getPropertyValue("--notice-right")).toBe("0px");
    expect(notice().dataset.layout).toBe("row");
    unmount();
  });

  it("stacks in a narrow pane once the text is 150%", async () => {
    paneRect = new DOMRect(340, 82, 600, 718);
    const { notice, unmount } = mount();
    expect(notice().dataset.layout).toBe("row");
    await act(async () => {
      document.documentElement.style.setProperty("--font-scale", "1.5");
    });
    expect(notice().dataset.layout).toBe("stacked");
    unmount();
  });

  it("stays beside an open drawer at 1280 wide with 150% text", async () => {
    document.documentElement.style.setProperty("--font-scale", "1.5");
    const { notice, openDrawer, unmount } = mount();
    await act(async () => {
      openDrawer();
    });
    expect(notice().style.getPropertyValue("--notice-right")).toBe("580px");
    expect(notice().hasAttribute("data-above-drawer")).toBe(false);
    unmount();
  });

  it("goes full width across the foot of the board, over a phone's sheet", async () => {
    // 500×800 phone; the sheet is 76vh (608px): 110px of board shows above it.
    paneRect = new DOMRect(0, 82, 500, 718);
    drawerRect = new DOMRect(0, 192, 500, 608);
    const { notice, openDrawer, unmount } = mount();
    let drawer!: HTMLElement;
    await act(async () => {
      drawer = openDrawer();
    });
    expect(notice().hasAttribute("data-above-drawer")).toBe(true);
    for (const side of ["top", "right", "bottom", "left"]) {
      expect(notice().style.getPropertyValue(`--notice-${side}`)).toBe("0px");
    }
    expect(notice().dataset.layout).toBe("stacked");
    expect(notice().querySelector("button")?.textContent).toBe("Reload");

    // Dragged down to 300px, the sheet leaves room: the card sits above it.
    drawerRect = new DOMRect(0, 500, 500, 300);
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });
    expect(notice().hasAttribute("data-above-drawer")).toBe(false);
    expect(notice().style.getPropertyValue("--notice-bottom")).toBe("300px");

    drawerRect = new DOMRect(0, 192, 500, 608);
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });
    expect(notice().hasAttribute("data-above-drawer")).toBe(true);
    await act(async () => {
      drawer.remove();
    });
    expect(notice().hasAttribute("data-above-drawer")).toBe(false);
    unmount();
  });

  it("goes over a drawer dragged so wide that the strip beside it is too narrow", async () => {
    drawerRect = new DOMRect(500, 46, 780, 754);
    const { notice, openDrawer, unmount } = mount();
    await act(async () => {
      openDrawer();
    });
    expect(notice().hasAttribute("data-above-drawer")).toBe(true);
    expect(notice().style.getPropertyValue("--notice-right")).toBe("0px");
    unmount();
  });
});

describe("the notice's stylesheet contract", () => {
  // jsdom applies no CSS, so the rules are read from the stylesheet.
  const css = readFileSync(resolve(process.cwd(), "src/styles/10d-canvas-chrome.css"), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );
  const rule = (selector: RegExp) => new RegExp(`${selector.source}\\s*\\{([^}]*)\\}`).exec(css)?.[1] ?? "";

  it("pads the overlay down to the visible part of the board", () => {
    const body = rule(/\n\.board-unreadable/);
    expect(body).toMatch(/padding:\s*var\(--notice-top[^;]*var\(--notice-right[^;]*var\(--notice-bottom[^;]*var\(--notice-left/);
  });

  it("sizes the card from the visible width up to a reading width that follows the text size", () => {
    const body = rule(/\n\.board-unreadable-state/);
    expect(body).toMatch(/width:\s*min\(calc\(100% - \d+px\),\s*calc\(\d+px \* var\(--font-scale, 1\)\)\)/);
    expect(body).toMatch(/min-width:\s*min\(/);
  });

  it("puts Reload under the words when stacked", () => {
    expect(rule(/\.board-unreadable\[data-layout="stacked"\] \.board-unreadable-state/)).toMatch(
      /grid-template-columns:/,
    );
    expect(rule(/\.board-unreadable\[data-layout="stacked"\] \.ui-empty-state-action/)).toMatch(/grid-column:\s*2/);
  });

  it("scrolls a long parser message inside the card so Reload stays in view", () => {
    const body = rule(/\.board-unreadable-detail/);
    expect(body).toMatch(/max-height:/);
    expect(body).toMatch(/overflow:\s*auto/);
  });

  it("over the drawer, is a band across the foot of the board that scrolls when taller", () => {
    const band = rule(/\.board-unreadable\[data-above-drawer\]/);
    expect(band).toMatch(/z-index:\s*calc\(var\(--z-drawer,\s*40\)\s*\+\s*1\)/);
    expect(band).toMatch(/top:\s*auto/);
    expect(band).toMatch(/bottom:\s*0/);
    expect(band).toMatch(/max-height:\s*100%/);
    const card = rule(/\.board-unreadable\[data-above-drawer\] \.board-unreadable-state/);
    expect(card).toMatch(/width:\s*min\(calc\(100% - \d+px\)/);
  });
});
