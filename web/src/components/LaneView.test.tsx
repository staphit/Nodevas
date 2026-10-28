import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { readPreferences } from "../preferences";
import { useApp, usePreference } from "../store";
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
// Some tests stub it to see the command a gesture sends; the rest get it back.
const realUpdateCanvasLayout = useApp.getState().updateCanvasLayout;

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
    nodeClipboard: null,
    updateCanvasLayout: realUpdateCanvasLayout,
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

  /** Ids of the cards the board's scroll puts at least partly in view. */
  function cardsInView(container: HTMLElement, width: number, height: number): string[] {
    const board = container.querySelector<HTMLElement>(".board")!;
    const zoom = Number(container.querySelector<HTMLElement>(".board-inner")!.style.zoom || "1");
    const view = {
      left: board.scrollLeft / zoom,
      top: board.scrollTop / zoom,
      right: (board.scrollLeft + width) / zoom,
      bottom: (board.scrollTop + height) / zoom,
    };
    return [...container.querySelectorAll<HTMLElement>(".col-card")]
      .filter((card) => {
        const left = parseFloat(card.style.left);
        const top = parseFloat(card.style.top);
        const right = left + parseFloat(card.style.width);
        const bottom = top + parseFloat(card.style.height);
        return right > view.left && left < view.right && bottom > view.top && top < view.bottom;
      })
      .map((card) => card.getAttribute("data-node-id") ?? card.textContent ?? "");
  }

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
    // The war room is Station's; the graph menu does not open it.
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
    const updateCanvasLayout = vi.fn(async () => ({ ok: true as const }));
    useApp.setState({ updateCanvasLayout } as never);
    render(<LaneView variant="graph" {...paneProps} />);

    await user.click(screen.getByRole("button", { name: "篩選" }));
    let panel = screen.getByRole("dialog", { name: "篩選" });
    // Status comes first, as checkboxes, and takes the keyboard.
    const statusGroup = within(panel).getByRole("group", { name: "狀態篩選" });
    const checkboxes = within(statusGroup).getAllByRole("checkbox");
    expect(checkboxes.length).toBeGreaterThan(1);
    expect(checkboxes[0]).toHaveFocus();
    expect(within(panel).getByRole("button", { name: "清除篩選" })).toBeInTheDocument();
    // The rest waits under More filters.
    const more = within(panel).getByRole("button", { name: /更多篩選/ });
    expect(more).toHaveAttribute("aria-expanded", "false");
    expect(within(panel).queryByRole("textbox", { name: /篩選 ID/ })).not.toBeInTheDocument();

    await user.click(checkboxes[0]);
    await user.click(checkboxes[1]);
    expect(screen.getByRole("button", { name: "篩選（1）" })).toBeInTheDocument();

    await user.click(more);
    expect(more).toHaveAttribute("aria-expanded", "true");
    await user.type(within(panel).getByRole("textbox", { name: /篩選 ID/ }), "設計");
    expect(screen.getByRole("button", { name: "篩選（2）" })).toBeInTheDocument();
    // Saving a view is still part of it, and keeps every checked status.
    await user.type(within(panel).getByRole("textbox", { name: "檢視名稱" }), "Mine");
    await user.click(within(panel).getByRole("button", { name: "儲存檢視" }));
    const saved = updateCanvasLayout.mock.calls.at(-1) as unknown as [
      { type: string; view: { name: string; statuses?: string[] } },
    ];
    expect(saved[0]).toMatchObject({ type: "canvas.saveView", view: { name: "Mine" } });
    expect(saved[0].view.statuses).toHaveLength(2);

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: /篩選/ })).not.toBeInTheDocument();

    // Reopened with a search on, the fold opens by itself to show it.
    await user.click(screen.getByRole("button", { name: "篩選（2）" }));
    panel = screen.getByRole("dialog", { name: "篩選（2）" });
    expect(within(panel).getByRole("button", { name: /更多篩選/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    await user.click(within(panel).getByRole("button", { name: "清除篩選" }));
    expect(screen.getByRole("button", { name: "篩選" })).toBeInTheDocument();
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

    // Picking a problem opens its node, and the drawer takes the side.
    await user.click(within(panel).getByRole("button", { name: /未知的節點/ }));
    expect(openTab).toHaveBeenCalledWith("a");
    expect(screen.queryByRole("complementary", { name: "問題" })).not.toBeInTheDocument();
    expect(problems).toHaveAttribute("aria-expanded", "false");
  });

  it("keeps Problems and the node drawer from sharing the side", async () => {
    const user = userEvent.setup();
    useApp.setState({ activeTab: "a" });
    render(<LaneView variant="graph" {...paneProps} />);

    const problems = screen.getByRole("button", { name: /問題：/ });
    await user.click(problems);
    expect(useApp.getState().activeTab).toBeNull();

    // Closed from inside, by × or by Escape, focus goes back to the button.
    await user.click(screen.getByRole("button", { name: "關閉問題面板" }));
    expect(screen.queryByRole("complementary", { name: "問題" })).not.toBeInTheDocument();
    expect(problems).toHaveFocus();

    await user.click(problems);
    expect(screen.getByRole("complementary", { name: "問題" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("complementary", { name: "問題" })).not.toBeInTheDocument();
    expect(problems).toHaveFocus();

    // A node opened some other way (Enter on a card, the palette) takes the
    // side back from Problems.
    await user.click(problems);
    expect(screen.getByRole("complementary", { name: "問題" })).toBeInTheDocument();
    act(() => useApp.setState({ activeTab: "a" }));
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
    expect(card.title).toContain("標籤：ui、spec");
    expect(card.title).toContain("起始節點");
  });

  it("labels every card with its type and draws the type's shape", () => {
    useApp.setState({
      graph: {
        ...graph([
          { id: "a", title: "設計稿" },
          { id: "b", title: "編輯審稿", kind: "gate" },
          { id: "c", title: "暗線", kind: "custom-type-1" },
          { id: "d", title: "改過形狀", kind: "gate" },
          { id: "e", title: "角色表", kind: "note" },
        ]),
        nodeTypes: [
          {
            id: "custom-type-1",
            label: "伏筆",
            shape: "diamond",
            color: "#c9a7ff",
            agentMode: "none",
            completion: "anyone",
            countsInProgress: true,
          },
        ],
        ui: { nodeStyles: { d: { shape: "pill" } } },
      },
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = (id: string) =>
      container.querySelector<HTMLElement>(`.col-card[data-node-id="${id}"]`)!;

    expect(card("a").querySelector(".col-card-type")).toHaveTextContent("任務");
    expect(card("a")).toHaveClass("shape-round");
    expect(card("b").querySelector(".col-card-type")).toHaveTextContent("審核");
    expect(card("b")).toHaveClass("shape-hexagon");
    expect(card("c").querySelector(".col-card-type")).toHaveTextContent("伏筆");
    expect(card("c")).toHaveClass("shape-diamond");
    // The chip is the meta line's first member.
    expect(card("b").querySelector(".col-card-meta")?.firstElementChild).toHaveClass(
      "col-card-type",
    );
    // The type is in what a screen reader and the tooltip say.
    expect(card("b")).toHaveAccessibleName("編輯審稿 · 審核 · 準備");
    expect(card("b").title).toContain("類型：審核");
    // A per-node shape still wins over the type's.
    expect(card("d")).toHaveClass("shape-pill");
    expect(card("d")).not.toHaveClass("shape-hexagon");
    // A note has no status: no status class and no status marker.
    expect(card("e")).toHaveClass("no-status", "shape-dashed");
    expect(card("e").className).not.toMatch(/status-/);
    expect(card("e").querySelector(".col-card-head svg")).toBeNull();
    expect(card("e")).toHaveAccessibleName("角色表 · 筆記");
  });

  it("creates any type from the New node caret, Task from the button", async () => {
    useApp.setState({
      graph: {
        ...graph([{ id: "a", title: "設計稿" }]),
        nodeTypes: [
          {
            id: "custom-type-1",
            label: "伏筆",
            agentMode: "none",
            completion: "anyone",
            countsInProgress: true,
          },
        ],
      },
    });
    const user = userEvent.setup();
    render(<LaneView variant="graph" {...paneProps} />);

    await user.click(screen.getByRole("button", { name: "選擇新節點的類型" }));
    const menu = screen.getByRole("menu", { name: "選擇新節點的類型" });
    expect(within(menu).getByRole("menuitem", { name: "審核" })).toBeInTheDocument();
    await user.click(within(menu).getByRole("menuitem", { name: "伏筆" }));
    expect(screen.getByRole("combobox", { name: "類型" })).toHaveValue("custom-type-1");
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
    // The dependency form lives in the node's panel, not in the menu.
    expect(within(menu).queryByText("相依關係")).not.toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(container.ownerDocument.querySelector(".lane-context-menu")).toBeNull();
    // The ⋯ lies over the card's corner as its sibling: nothing interactive
    // is nested inside the card's own button.
    const more = screen.getByRole("button", { name: "節點選單：設計稿" });
    expect(card.querySelector("button, [role='button']")).toBeNull();
    expect(card.nextElementSibling).toBe(more);
    await user.click(more);
    expect(
      container.ownerDocument.querySelector(".lane-context-menu.node-actions-menu"),
    ).not.toBeNull();
    expect(screen.getByRole("menu", { name: "「設計稿」的動作" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "建立群組底圖" })).toBeInTheDocument();
  });

  it("draws a group background around one node from its menu", async () => {
    const user = userEvent.setup();
    const updateCanvasLayout = vi.fn(async () => ({ ok: true as const }));
    useApp.setState({ updateCanvasLayout } as never);
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    fireEvent.contextMenu(container.querySelector<HTMLElement>(".col-card")!);
    await user.click(screen.getByRole("menuitem", { name: "建立群組底圖" }));
    expect(updateCanvasLayout).toHaveBeenCalledWith(
      expect.objectContaining({ type: "canvas.upsertGroup" }),
    );
    expect(container.querySelector<HTMLElement>(".col-card")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("shows what the clipboard holds with no selection or one", () => {
    useApp.setState({
      nodeClipboard: { project: "main", ids: ["a"], labels: ["設計稿"], mode: "copy" },
    });
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    expect(container.querySelector(".graph-clipboard-strip")).toHaveTextContent("main");
    const card = container.querySelector<HTMLElement>(".col-card")!;
    act(() => card.focus());
    expect(card).toHaveAttribute("aria-selected", "true");
    expect(container.querySelector(".graph-clipboard-strip")).toHaveTextContent("main");
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

  // The » leaves once the explorer is back, so the keyboard goes to its «.
  it("hands focus to the explorer's fold button once it is back", async () => {
    const user = userEvent.setup();
    act(() => useApp.getState().updateUIPreference("explorerCollapsed", true));
    function Explorer() {
      const collapsed = usePreference("explorerCollapsed");
      return collapsed ? null : (
        <button type="button" className="explorer-collapse">
          «
        </button>
      );
    }
    render(
      <>
        <Explorer />
        <LaneView variant="graph" {...paneProps} />
      </>,
    );
    await user.click(screen.getByRole("button", { name: "展開專案總管" }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "«" })).toHaveFocus());
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

    it("opens an empty board on its empty-state hint", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      useApp.setState({ graph: graph([]) });
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      const board = container.querySelector<HTMLElement>(".board")!;
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      const zoom = Number(inner.style.zoom);
      // What the opening scroll shows, in the board's own units.
      const view = {
        left: board.scrollLeft / zoom,
        top: board.scrollTop / zoom,
        right: (board.scrollLeft + 1280) / zoom,
        bottom: (board.scrollTop + 800) / zoom,
      };
      expect(view.left).toBeGreaterThan(0);

      const hint = screen.getByText("尚無節點").closest<HTMLElement>(".ui-empty-state")!;
      // jsdom has no layout, so the hint's place is read off the inline
      // offsets of everything between it and the board surface.
      let left = 0;
      let top = 0;
      for (let el: HTMLElement | null = hint; el && el !== inner; el = el.parentElement) {
        left += parseFloat(el.style.left || "0");
        top += parseFloat(el.style.top || "0");
      }
      expect(left).toBeGreaterThanOrEqual(view.left);
      expect(left).toBeLessThan(view.right);
      expect(top).toBeGreaterThanOrEqual(view.top);
      expect(top).toBeLessThan(view.bottom);
    });

    // Saved positions need not start at the origin: a board whose cards all
    // sit a few columns right of it used to open on the empty margin.
    it("opens with the cards in view when they sit away from the origin", () => {
      mockNarrowViewport(false);
      mockBoardSize(948, 680);
      useApp.setState({
        graph: {
          version: 1,
          nodes: ["one", "two", "three", "four", "five", "six"].map((id) => ({ id, title: id })),
          edges: [],
          ui: {
            positions: {
              one: { x: 10, y: 0 },
              two: { x: 16, y: 30 },
              three: { x: 21, y: 7 },
              four: { x: 27, y: 28 },
              five: { x: 30, y: 3 },
              six: { x: 32, y: 29 },
            },
          },
        },
      });
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      expect(container.querySelectorAll(".col-card")).toHaveLength(6);
      expect(cardsInView(container, 948, 680)).not.toEqual([]);
    });

    // The fit changes the zoom, and with it where the board centres a small
    // graph; the scroll has to be worked out for the zoom it lands on, and has
    // to land whether the next frame comes before the new zoom is drawn (as a
    // browser may run it) or after.
    it.each(["before", "after"])(
      "fits a phone's board so its cards are in view, frame %s the zoom",
      (order) => {
        mockNarrowViewport(true);
        mockBoardSize(500, 608);
        const frames: FrameRequestCallback[] = [];
        vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
          if (order === "before") callback(0);
          else frames.push(callback);
          return 1;
        });
        useApp.setState({
          graph: {
            version: 1,
            nodes: [
              { id: "outline", title: "寫大綱" },
              { id: "chapter", title: "寫第一章" },
            ],
            edges: [{ from: "outline", to: "chapter" }],
            ui: {
              positions: { outline: { x: 3.348, y: 0 }, chapter: { x: 1.39, y: 0 } },
            },
          },
        });
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        act(() => {
          for (const frame of frames.splice(0)) frame(0);
        });
        expect(container.querySelector<HTMLElement>(".board-inner")!.style.zoom).not.toBe("1");
        expect(cardsInView(container, 500, 608)).toHaveLength(2);
      },
    );

    // A board too big to fit even at the smallest zoom (gates2k: 2000 cards,
    // hundreds of columns, a few tall ones) was centred on the middle of its
    // bounds, which on a wide, mostly shallow board is a stretch with the top
    // card row cut and empty grid below. It opens like the desktop does
    // instead: on the card nearest the content's top-left corner.
    it("opens a phone's board too big to fit on its top-left card, not its empty middle", () => {
      mockNarrowViewport(true);
      mockBoardSize(485, 643);
      const nodes: Graph["nodes"] = [];
      const positions: Record<string, { x: number; y: number }> = {};
      // Two shallow rows across 120 columns...
      for (let col = 0; col < 120; col++) {
        for (let row = 0; row < 2; row++) {
          const id = `w${col}-${row}`;
          nodes.push({ id, title: id });
          positions[id] = { x: col, y: row };
        }
      }
      // ...and one column at the left edge that runs nineteen rows deep.
      for (let row = 2; row < 19; row++) {
        const id = `deep-${row}`;
        nodes.push({ id, title: id });
        positions[id] = { x: 0, y: row };
      }
      useApp.setState({ graph: { version: 1, nodes, edges: [], ui: { positions } } });
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      expect(container.querySelector<HTMLElement>(".board-inner")!.style.zoom).toBe("0.5");

      const board = container.querySelector<HTMLElement>(".board")!;
      const zoom = 0.5;
      const view = {
        left: board.scrollLeft / zoom,
        top: board.scrollTop / zoom,
        right: (board.scrollLeft + 485) / zoom,
        bottom: (board.scrollTop + 643) / zoom,
      };
      const corner = container.querySelector<HTMLElement>('.col-card[data-node-id="w0-0"]');
      expect(corner).not.toBeNull();
      const left = parseFloat(corner!.style.left);
      const top = parseFloat(corner!.style.top);
      // The top-left card is wholly on screen, not cut by the view's top edge.
      expect(left).toBeGreaterThanOrEqual(view.left);
      expect(top).toBeGreaterThanOrEqual(view.top);
      expect(left + parseFloat(corner!.style.width)).toBeLessThanOrEqual(view.right);
      expect(top + parseFloat(corner!.style.height)).toBeLessThanOrEqual(view.bottom);
    });

    it("leaves the opening zoom alone on a desktop", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      expect(inner.style.zoom).toBe("1");
    });
  });

  describe("opening view", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
      Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    });

    // The range starts two weeks before today, so a timeline left at scroll 0
    // showed a fortnight of empty rows and none of the plan.
    it("opens a vertical timeline on today", () => {
      mockNarrowViewport(false);
      mockBoardSize(948, 680);
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      const key = (offset: number) => {
        const date = new Date(today);
        date.setDate(date.getDate() + offset);
        return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
      };
      useApp.getState().updateUIPreference("timelineOrientation", "vertical");
      useApp.setState({
        graph: {
          version: 1,
          nodes: [{ id: "a", title: "設計稿" }],
          edges: [],
          ui: {
            plans: {
              a: [
                { date: key(0), status: "started" },
                { date: key(2), status: "done" },
              ],
            },
          },
        },
      });
      const { container } = render(<LaneView variant="timeline" {...paneProps} />);
      const board = container.querySelector<HTMLElement>(".board")!;
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      const step = parseFloat(inner.style.getPropertyValue("--step"));
      const zoom = Number(inner.style.zoom || "1");
      const rows = [...container.querySelectorAll<HTMLElement>(".board-rows > .board-row")];
      const todayRow = rows.findIndex((row) => row.dataset.date === key(0));
      expect(todayRow).toBeGreaterThan(0);
      expect(board.scrollTop).toBe(todayRow * step * zoom);
    });

    // The minimap floats over the board's bottom-right corner. Daily work
    // schedule in a 773x665 pane (explorer 480) opened centred with 'Weekly
    // review' under it, though its cards had room to sit clear of the map.
    it("opens with the cards clear of the minimap when they have room to be", () => {
      mockNarrowViewport(false);
      mockBoardSize(773, 665);
      // jsdom has no layout: the board fills the pane from 0,0 with 15px
      // scrollbars past its 773x665 client area, and the map sits where the
      // stylesheet puts it, 14px in and 76px up from the pane's corner (the
      // Copilot button is below it), at whatever size it draws itself.
      const realRect = HTMLElement.prototype.getBoundingClientRect;
      vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
        this: HTMLElement,
      ) {
        if (this.classList.contains("board")) return new DOMRect(0, 0, 788, 680);
        if (this.classList.contains("graph-minimap")) {
          const width = parseFloat(this.style.width) || 170;
          const height = parseFloat(this.style.height) || 108;
          return new DOMRect(788 - 14 - width, 680 - 76 - height, width, height);
        }
        return realRect.call(this);
      });
      const at = (x: number, y: number) => ({ x, y });
      useApp.setState({
        graph: {
          version: 1,
          nodes: ["inbox", "focus", "sync", "docs", "client", "review"].map((id) => ({
            id,
            title: id,
          })),
          edges: [],
          ui: {
            positions: {
              inbox: at(0, 0),
              focus: at(0.63, 2.8),
              sync: at(1.55, 1.15),
              docs: at(1.66, 4.9),
              client: at(3.13, 2.88),
              review: at(3.52, 5.62),
            },
          },
        },
      });
      try {
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        const board = container.querySelector<HTMLElement>(".board")!;
        const map = container.querySelector<HTMLElement>(".graph-minimap")!.getBoundingClientRect();
        expect(cardsInView(container, 773, 665)).toHaveLength(6);
        for (const card of container.querySelectorAll<HTMLElement>(".col-card")) {
          const left = parseFloat(card.style.left) - board.scrollLeft;
          const top = parseFloat(card.style.top) - board.scrollTop;
          const right = left + parseFloat(card.style.width);
          const bottom = top + parseFloat(card.style.height);
          const covered =
            right > map.left && left < map.right && bottom > map.top && top < map.bottom;
          expect(covered, card.dataset.nodeId).toBe(false);
        }
      } finally {
        vi.restoreAllMocks();
      }
    });

    // A template with no cards, only its four quadrants, opened on a corner
    // of the first one: the opening framed cards and ignored the groups.
    it("frames a board's groups when it has no cards, with its hint above them", () => {
      mockNarrowViewport(false);
      mockBoardSize(933, 665);
      const quadrant = (id: string, x: number, y: number) => ({
        id,
        title: id,
        x,
        y,
        width: 620,
        height: 380,
      });
      useApp.setState({
        graph: {
          version: 1,
          nodes: [],
          edges: [],
          ui: {
            groups: [
              quadrant("do", 0, 0),
              quadrant("schedule", 640, 0),
              quadrant("delegate", 0, 400),
              quadrant("drop", 640, 400),
            ],
          },
        },
      });
      const { container } = render(<LaneView variant="graph" {...paneProps} />);
      const board = container.querySelector<HTMLElement>(".board")!;
      const inner = container.querySelector<HTMLElement>(".board-inner")!;
      const zoom = Number(inner.style.zoom || "1");
      const view = {
        left: board.scrollLeft / zoom,
        top: board.scrollTop / zoom,
        right: (board.scrollLeft + 933) / zoom,
        bottom: (board.scrollTop + 665) / zoom,
      };
      const groups = [...container.querySelectorAll<HTMLElement>(".canvas-decoration.group")];
      expect(groups).toHaveLength(4);
      for (const group of groups) {
        const left = parseFloat(group.style.left);
        const top = parseFloat(group.style.top);
        // Each quadrant's title bar is on screen.
        expect(left).toBeLessThan(view.right - 40);
        expect(left + parseFloat(group.style.width)).toBeGreaterThan(view.left);
        expect(top).toBeGreaterThanOrEqual(view.top);
        expect(top + 40).toBeLessThan(view.bottom);
      }
      // The empty-state hint sits in the view, above the groups, not over them.
      const hint = container.querySelector<HTMLElement>(".board-empty")!;
      const hintTop = parseFloat(hint.style.top);
      const hintLeft = parseFloat(hint.style.left);
      const groupsTop = Math.min(...groups.map((group) => parseFloat(group.style.top)));
      expect(hintLeft).toBeGreaterThanOrEqual(view.left);
      expect(hintLeft).toBeLessThan(view.right);
      expect(hintTop).toBeGreaterThan(view.top);
      expect(hintTop).toBeLessThanOrEqual(groupsTop);
    });
  });

  // The board unmounts whenever the app shows Station or Settings instead, so
  // coming back must not land on the opening scroll: the pan and the zoom are
  // where the user left them, like the open drawer is.
  describe("leaving the board and coming back", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
      Reflect.deleteProperty(HTMLElement.prototype, "clientWidth");
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    });

    it("comes back panned and zoomed where it was left", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      const first = render(<LaneView variant="graph" {...paneProps} />);
      let board = first.container.querySelector<HTMLElement>(".board")!;
      fireEvent.wheel(board, { altKey: true, deltaY: -100 });
      const zoomed = first.container.querySelector<HTMLElement>(".board-inner")!.style.zoom;
      expect(zoomed).not.toBe("1");
      board.scrollLeft = 1700;
      board.scrollTop = 900;
      fireEvent.scroll(board);
      first.unmount();

      const second = render(<LaneView variant="graph" {...paneProps} />);
      board = second.container.querySelector<HTMLElement>(".board")!;
      expect(second.container.querySelector<HTMLElement>(".board-inner")!.style.zoom).toBe(zoomed);
      expect(board.scrollLeft).toBe(1700);
      expect(board.scrollTop).toBe(900);
    });

    // The project menu swaps the project under a board that stays mounted, so
    // the per-project memory has to apply on the switch too, not only on mount.
    it("switches the zoom with the project when the board stays mounted", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      const project = useApp.getState().activeProject;
      // Each switch lands with its project's graph loaded, as the store's load
      // would; the board waits for that before framing the cards.
      const show = (name: string) =>
        act(() => useApp.setState({ activeProject: name, graphProject: name }));
      act(() => useApp.setState({ activeProject: "in-place-first" }));
      try {
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        const board = container.querySelector<HTMLElement>(".board")!;
        const inner = () => container.querySelector<HTMLElement>(".board-inner")!.style.zoom;
        const opening = { left: board.scrollLeft, top: board.scrollTop };
        fireEvent.wheel(board, { altKey: true, deltaY: 100 });
        const firstZoom = inner();
        expect(firstZoom).not.toBe("1");
        board.scrollLeft = 1700;
        board.scrollTop = 900;
        fireEvent.scroll(board);

        // A project this session has not shown opens at 100% on the opening scroll.
        show("in-place-second");
        expect(inner()).toBe("1");
        expect({ left: board.scrollLeft, top: board.scrollTop }).toEqual(opening);
        fireEvent.wheel(board, { altKey: true, deltaY: -100 });
        const secondZoom = inner();
        expect(secondZoom).not.toBe("1");
        expect(secondZoom).not.toBe(firstZoom);

        // Back to the first: its own zoom and the scroll it was left at.
        show("in-place-first");
        expect(inner()).toBe(firstZoom);
        expect({ left: board.scrollLeft, top: board.scrollTop }).toEqual({
          left: 1700,
          top: 900,
        });

        show("in-place-second");
        expect(inner()).toBe(secondZoom);
      } finally {
        act(() => useApp.setState({ activeProject: project, graphProject: "" }));
      }
    });

    // The project menu changes the project first and loads its graph after;
    // in between the store still holds the old project's graph, which the
    // opening must not frame and then keep.
    it("frames the new project's cards, not the old project's, on an in-place switch", () => {
      mockNarrowViewport(false);
      mockBoardSize(948, 680);
      const project = useApp.getState().activeProject;
      const far = (id: string, x: number, y: number): Graph => ({
        version: 1,
        nodes: [{ id, title: id }],
        edges: [],
        ui: { positions: { [id]: { x, y } } },
      });
      act(() =>
        useApp.setState({
          activeProject: "switch-old",
          graph: far("old-card", 40, 2),
          graphProject: "",
        }),
      );
      try {
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        expect(cardsInView(container, 948, 680)).toHaveLength(1);

        act(() => useApp.setState({ activeProject: "switch-new" }));
        act(() =>
          useApp.setState({ graph: far("new-card", 2, 40), graphProject: "switch-new" }),
        );
        expect(cardsInView(container, 948, 680)).toHaveLength(1);
      } finally {
        act(() => useApp.setState({ activeProject: project, graphProject: "" }));
      }
    });

    // On a reload the graph can load before the workspace names the active
    // project; that graph is the named project's, not a stale one to wait out.
    it("fits a phone's board when the workspace names the project after its graph loaded", () => {
      mockNarrowViewport(true);
      mockBoardSize(390, 700);
      const project = useApp.getState().activeProject;
      act(() => useApp.setState({ activeProject: "", graphProject: "" }));
      try {
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        act(() => useApp.setState({ activeProject: "named-after-load" }));
        expect(container.querySelector<HTMLElement>(".board-inner")!.style.zoom).toBe("1.3");
        expect(cardsInView(container, 390, 700)).toHaveLength(1);
      } finally {
        act(() => useApp.setState({ activeProject: project, graphProject: "" }));
      }
    });

    it("does not refit a phone's board it has already fitted on a project switch", () => {
      mockNarrowViewport(true);
      mockBoardSize(390, 700);
      const project = useApp.getState().activeProject;
      act(() => useApp.setState({ activeProject: "phone-first" }));
      try {
        const { container } = render(<LaneView variant="graph" {...paneProps} />);
        const board = container.querySelector<HTMLElement>(".board")!;
        const inner = () => container.querySelector<HTMLElement>(".board-inner")!.style.zoom;
        fireEvent.wheel(board, { altKey: true, deltaY: 100 });
        fireEvent.wheel(board, { altKey: true, deltaY: 100 });
        const zoomed = inner();

        act(() => useApp.setState({ activeProject: "phone-second" }));
        act(() => useApp.setState({ activeProject: "phone-first" }));
        expect(inner()).toBe(zoomed);
      } finally {
        act(() => useApp.setState({ activeProject: project }));
      }
    });

    it("does not refit a phone's board it has already fitted", () => {
      mockNarrowViewport(true);
      mockBoardSize(390, 700);
      const first = render(<LaneView variant="graph" {...paneProps} />);
      const board = first.container.querySelector<HTMLElement>(".board")!;
      // The fit lands in a frame; the user then zooms back out by hand.
      fireEvent.wheel(board, { altKey: true, deltaY: 100 });
      const zoomed = first.container.querySelector<HTMLElement>(".board-inner")!.style.zoom;
      first.unmount();

      const second = render(<LaneView variant="graph" {...paneProps} />);
      expect(second.container.querySelector<HTMLElement>(".board-inner")!.style.zoom).toBe(zoomed);
    });

    it("opens a project it has not shown yet at the opening scroll", () => {
      mockNarrowViewport(false);
      mockBoardSize(1280, 800);
      const first = render(<LaneView variant="graph" {...paneProps} />);
      const board = first.container.querySelector<HTMLElement>(".board")!;
      const opening = { left: board.scrollLeft, top: board.scrollTop };
      board.scrollLeft = 1700;
      fireEvent.scroll(board);
      first.unmount();

      const project = useApp.getState().activeProject;
      act(() => useApp.setState({ activeProject: "elsewhere" }));
      try {
        const second = render(<LaneView variant="graph" {...paneProps} />);
        const again = second.container.querySelector<HTMLElement>(".board")!;
        expect({ left: again.scrollLeft, top: again.scrollTop }).toEqual(opening);
      } finally {
        act(() => useApp.setState({ activeProject: project }));
      }
    });
  });
});
