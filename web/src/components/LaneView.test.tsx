import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
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
    preferences: { ...readPreferences(), language: "zh-TW" },
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
  useApp.getState().updateUIPreference("language", "zh-TW");
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

    // A setting, not a first-day control: it lives in the header's ⋯ menu.
    await user.click(screen.getByRole("button", { name: "更多關係圖工具" }));
    const toggle = screen.getByRole("menuitemcheckbox", { name: /對齊輔助/ });
    // On by default: lining a board up by hand is the tedious part.
    expect(toggle).toHaveAttribute("aria-checked", "true");

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(useApp.getState().preferences.graphSnapGuides).toBe(false);
    // The preference outlives this test file; put it back.
    act(() => {
      useApp.getState().updateUIPreference("graphSnapGuides", true);
    });
  });

  // The node scale rides the board's CSS zoom, and the text divides it back
  // out — which is what lets the cards grow while the words stay put.
  it("scales the cards and the card text separately", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const inner = container.querySelector<HTMLElement>(".board-inner")!;

    await user.click(screen.getByRole("button", { name: "更多關係圖工具" }));
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

    await user.click(screen.getByRole("button", { name: "更多關係圖工具" }));
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

    await user.click(screen.getByRole("button", { name: "更多關係圖工具" }));
    await user.click(screen.getByRole("menuitemcheckbox", { name: "縮圖" }));
    expect(container.querySelector(".graph-minimap")).toBeNull();
    expect(useApp.getState().preferences.graphMinimap).toBe(false);

    // The preference outlives this test file; put it back.
    act(() => {
      useApp.getState().updateUIPreference("graphMinimap", true);
    });
  });

  // The header keeps what a first board needs — create, Filter, Problems —
  // and the rest is one press away in ⋯, still all there.
  it("keeps the graph header short and the rest in its ⋯ menu", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);

    expect(screen.getByRole("button", { name: /新增節點/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "篩選" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "問題：0 項" })).toBeInTheDocument();
    for (const name of ["全部置中", "選取置中", "貼齊格線", "全部對齊", "縮圖", "操作提示", /分析/]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }

    await user.click(screen.getByRole("button", { name: "更多關係圖工具" }));
    const menu = screen.getByRole("menu", { name: "更多關係圖工具" });
    for (const name of ["全部置中", "選取置中", /對齊輔助/, "貼齊格線", "全部對齊", "縮圖", "時間軸", "操作說明"]) {
      expect(
        within(menu).queryByRole("menuitem", { name }) ??
          within(menu).getByRole("menuitemcheckbox", { name }),
      ).toBeInTheDocument();
    }
    // The game views are Station's, opened from its own switch.
    expect(within(menu).queryByRole("menuitem", { name: /工作站/ })).not.toBeInTheDocument();
    // Nothing selected, so there is nothing to fit to.
    expect(within(menu).getByRole("menuitem", { name: "選取置中" })).toBeDisabled();

    // Help shows the gesture sentence the ? button used to.
    const help = within(menu).getByRole("menuitemcheckbox", { name: "操作說明" });
    await user.click(help);
    expect(help).toHaveAttribute("aria-checked", "true");
    expect(container.querySelector(".board-title-hint.open")).not.toBeNull();
  });

  it("filters from a popover and counts the filters that are on", async () => {
    const user = userEvent.setup();
    render(<LaneView variant="graph" {...paneProps} />);

    await user.click(screen.getByRole("button", { name: "篩選" }));
    const panel = screen.getByRole("dialog", { name: "篩選" });
    // The first field takes the keyboard.
    expect(within(panel).getByRole("textbox", { name: /篩選 ID/ })).toHaveFocus();
    await user.type(within(panel).getByRole("textbox", { name: /篩選 ID/ }), "設計");
    expect(screen.getByRole("button", { name: "篩選（1）" })).toBeInTheDocument();
    // Saving a view is still part of it.
    expect(within(panel).getByRole("button", { name: "儲存檢視" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: /篩選/ })).not.toBeInTheDocument();
  });

  it("gathers validation warnings and analysis in the Problems panel", async () => {
    const user = userEvent.setup();
    const openTab = vi.fn(async () => {});
    useApp.setState({
      openTab,
      issues: [{ severity: "error", nodeId: "a", field: "requires", msg: "未知的節點" }],
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);

    const problems = screen.getByRole("button", { name: "問題：1 項" });
    await user.click(problems);
    expect(problems).toHaveAttribute("aria-expanded", "true");
    const panel = screen.getByRole("complementary", { name: "問題" });
    expect(within(panel).getByText("驗證警告")).toBeInTheDocument();
    expect(within(panel).getByText("排程分析")).toBeInTheDocument();
    // Beside the board, not on top of it.
    expect(container.querySelector(".lane-body > .problems-panel")).not.toBeNull();

    await user.click(within(panel).getByRole("button", { name: /未知的節點/ }));
    expect(openTab).toHaveBeenCalledWith("a");

    await user.click(within(panel).getByRole("button", { name: "關閉問題面板" }));
    expect(screen.queryByRole("complementary", { name: "問題" })).not.toBeInTheDocument();
  });

  it("draws a card as status, title and assignee with one marker", () => {
    useApp.setState({
      graph: graph([{ id: "a", title: "設計稿", tags: ["ui", "spec"], priority: "high" }]),
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = container.querySelector<HTMLElement>(".col-card")!;

    // Nothing points at it, so it is a start node: that is its one marker.
    expect(card.querySelectorAll(".col-card-marker")).toHaveLength(1);
    expect(card.querySelector(".col-card-marker")).toHaveTextContent("起點");
    // Tags, priority and the status word are in the tooltip, not on the face.
    expect(card).not.toHaveTextContent("ui");
    expect(card.querySelector(".col-card-st, .col-card-tags, .entry-flag")).toBeNull();
    expect(card.title).toContain("標籤：ui, spec");
    expect(card.title).toContain("起始節點");
  });

  it("opens a short node menu from a right click or the card's ⋯", async () => {
    const user = userEvent.setup();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = container.querySelector<HTMLElement>(".col-card")!;

    fireEvent.contextMenu(card);
    const menu = container.ownerDocument.querySelector<HTMLElement>(".lane-context-menu")!;
    for (const name of ["開啟", "新增相依…", "建立副本", "移到或複製到專案", "移到垃圾桶"]) {
      expect(within(menu).getByRole("menuitem", { name })).toBeInTheDocument();
    }
    // The dependency form is one step further in, not the menu itself.
    expect(within(menu).queryByText("相依關係")).not.toBeInTheDocument();
    await user.click(within(menu).getByRole("menuitem", { name: "新增相依…" }));
    expect(
      container.ownerDocument.querySelector(".lane-context-menu.condition-menu"),
    ).not.toBeNull();

    await user.keyboard("{Escape}");
    expect(container.ownerDocument.querySelector(".lane-context-menu")).toBeNull();
    await user.click(screen.getByRole("button", { name: "節點選單：設計稿" }));
    expect(
      container.ownerDocument.querySelector(".lane-context-menu.node-actions-menu"),
    ).not.toBeNull();
  });

  it("keeps the batch toolbar for two or more nodes", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = container.querySelector<HTMLElement>(".col-card")!;
    act(() => card.focus());
    expect(card).toHaveAttribute("aria-selected", "true");
    expect(container.querySelector(".graph-batch-toolbar")).toBeNull();
  });

  it("brings a folded explorer back from the graph header", async () => {
    const user = userEvent.setup();
    act(() => useApp.getState().updateUIPreference("explorerCollapsed", true));
    render(<LaneView variant="graph" {...paneProps} />);

    await user.click(screen.getByRole("button", { name: "展開專案總管" }));
    expect(useApp.getState().preferences.explorerCollapsed).toBe(false);
    expect(screen.queryByRole("button", { name: "展開專案總管" })).not.toBeInTheDocument();
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
