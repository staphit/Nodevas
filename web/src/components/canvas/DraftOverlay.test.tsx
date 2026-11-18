import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type CopilotDraftData, type ThemePack } from "../../api";
import { currentDraft, entryToItem, useCopilot } from "../../copilot/copilotStore";
import { renameNode } from "../../copilot/draft/draftStore";
import { useDraftUi } from "../../copilot/draft/draftUi";
import { useApp } from "../../store";
import { useThemePacks } from "../../theme-pack/themePackStore";
import { LaneView } from "../LaneView";
import { boardGraph, installPointerShims, resetBoardStore } from "./boardTestKit";
import { COL_W, ROW_H } from "./geometry";

const cardRenders = vi.hoisted(() => new Map<string, number>());
vi.mock("../../runs/RunChanges", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../runs/RunChanges")>();
  return {
    ...actual,
    RunCardBadge: ({ nodeId }: { nodeId: string }) => {
      cardRenders.set(nodeId, (cardRenders.get(nodeId) ?? 0) + 1);
      return null;
    },
  };
});

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
      checkSubgraph: vi.fn(),
    },
  };
});

const paneProps = {
  collapsed: false,
  onToggle: () => undefined,
  selectedNodeId: null,
  onSelectedNodeChange: () => undefined,
  keyboardActive: false,
  onActivate: () => undefined,
};

const draftData: CopilotDraftData = {
  draft: {
    summary: "Launch",
    nodes: [
      { key: "k1", title: "Docs", kind: "task" },
      { key: "k2", title: "Ship", kind: "task", body: "Tag the release and push it.", after: ["k1"] },
    ],
  },
  issues: [],
  provider: "anthropic",
  model: "claude",
  state: "open",
};

const totalCardRenders = () => [...cardRenders.values()].reduce((sum, n) => sum + n, 0);

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.checkSubgraph).mockResolvedValue({
    ok: true,
    ids: {},
    created: [],
    rev: "r",
    issues: [],
    dependencies: 0,
    references: 0,
  });
  resetBoardStore(["a", "b", "c"]);
  const graph = boardGraph(["a", "b", "c"]);
  graph.ui = { positions: { a: { x: 0, y: 0 }, b: { x: 1, y: 0 }, c: { x: 2, y: 0 } } };
  useApp.setState({ graph, activeProject: "main", canvasSelection: { project: "main", ids: [] } });
  useCopilot.setState({ items: [], drafts: {}, activeId: "c1" });
  useDraftUi.setState({ ghost: null, checks: {} });
  useThemePacks.getState().reset();
  cardRenders.clear();
});

/** A theme pack on show, as the board reads it: listed and the default. */
function themeOn() {
  useThemePacks.setState({
    loaded: true,
    packs: [{ id: "pk", name: { en: "Pack" } } as ThemePack],
    defaultId: "pk",
    error: null,
  });
}

function showDraft() {
  act(() => {
    useCopilot.setState({
      items: [
        entryToItem({ id: "d1", seq: 1, kind: "draft", text: "", data: JSON.stringify(draftData), createdAt: 1 }),
      ],
    });
  });
}

describe("DraftOverlay", () => {
  it("draws ghosts at their cells, right of the content, and publishes them", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const card = container.querySelector<HTMLElement>('.col-card[data-node-id="a"]')!;
    const ghosts = container.querySelectorAll<HTMLElement>(".draft-ghost");
    expect(ghosts).toHaveLength(2);
    expect(useDraftUi.getState().ghost).toEqual({
      entryId: "d1",
      cells: { k1: { col: 4, row: 0 }, k2: { col: 5, row: 0 } },
    });
    const k1 = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    const k2 = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k2"]')!;
    expect(parseFloat(k1.style.left)).toBeCloseTo(parseFloat(card.style.left) + 4 * COL_W);
    expect(parseFloat(k2.style.left)).toBeCloseTo(parseFloat(card.style.left) + 5 * COL_W);
    expect(parseFloat(k1.style.top)).toBeCloseTo(parseFloat(card.style.top));
    expect(container.querySelectorAll(".draft-edges path[data-draft-edge]")).toHaveLength(1);
    // Ghosts are not nodes.
    expect(useApp.getState().graph?.nodes).toHaveLength(3);
  });

  it("redraws on an edit without re-rendering a card", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    cardRenders.clear();
    act(() => {
      useCopilot.getState().editDraft("d1", (draft) => renameNode(draft, "k1", "Write docs"));
    });
    expect(container.querySelector('.draft-ghost[data-draft-key="k1"]')).toHaveTextContent("Write docs");
    expect(totalCardRenders()).toBe(0);
  });

  it("links by dragging a ghost's handle onto a card", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const card = container.querySelector<HTMLElement>('.col-card[data-node-id="b"]')!;
    const scale = Number(
      container.querySelector<HTMLElement>('[style*="--view-scale"]')?.style.getPropertyValue("--view-scale") || 1,
    );
    const handle = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"] .draft-ghost-handle')!;
    act(() => {
      fireEvent.pointerDown(handle, { button: 0, clientX: 5, clientY: 5, pointerType: "mouse" });
    });
    act(() => {
      window.dispatchEvent(
        new PointerEvent("pointerup", {
          clientX: (parseFloat(card.style.left) + 10) * scale,
          clientY: (parseFloat(card.style.top) + 10) * scale,
        }),
      );
    });
    expect(currentDraft(useCopilot.getState(), "d1")?.nodes[0].after).toEqual(["existing:b"]);
    // Linking to a card keeps the draft where it was.
    expect(useDraftUi.getState().ghost?.cells.k1).toEqual({ col: 4, row: 0 });
  });

  it("links one ghost to another", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const scale = Number(
      container.querySelector<HTMLElement>('[style*="--view-scale"]')?.style.getPropertyValue("--view-scale") || 1,
    );
    const target = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k2"]')!;
    const handle = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"] .draft-ghost-handle')!;
    act(() => {
      fireEvent.pointerDown(handle, { button: 0, clientX: 5, clientY: 5, pointerType: "mouse" });
    });
    act(() => {
      window.dispatchEvent(
        new PointerEvent("pointerup", {
          clientX: (parseFloat(target.style.left) + 10) * scale,
          clientY: (parseFloat(target.style.top) + ROW_H / 4) * scale,
        }),
      );
    });
    // k2 waited for k1; k1 now waits for k2: the card shows the cycle.
    expect(currentDraft(useCopilot.getState(), "d1")?.nodes[0].after).toEqual(["k2"]);
  });

  it("shows nothing once the draft is created", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    act(() => {
      useCopilot.getState().setDraftData("d1", { state: "committed" });
    });
    expect(container.querySelector(".draft-ghost")).toBeNull();
    expect(useDraftUi.getState().ghost).toBeNull();
  });
  it("renames a ghost by double-clicking it", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    act(() => {
      fireEvent.doubleClick(ghost);
    });
    const input = ghost.querySelector<HTMLInputElement>("input.draft-ghost-input")!;
    expect(input).not.toBeNull();
    expect(input.value).toBe("Docs");
    act(() => {
      fireEvent.change(input, { target: { value: "Write the docs" } });
      fireEvent.keyDown(input, { key: "Enter" });
    });
    expect(currentDraft(useCopilot.getState(), "d1")?.nodes[0].title).toBe("Write the docs");
    expect(container.querySelector("input.draft-ghost-input")).toBeNull();
    expect(container.querySelector('.draft-ghost[data-draft-key="k1"]')).toHaveTextContent("Write the docs");
  });

  it("takes no rename and offers no link handle while the draft is being refined, and says why", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    act(() => {
      useCopilot.setState({ running: true, refining: "d1" });
    });
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    act(() => {
      fireEvent.doubleClick(ghost);
    });
    expect(container.querySelector("input.draft-ghost-input")).toBeNull();
    expect(ghost.querySelector(".draft-ghost-handle")).toBeNull();
    expect(ghost).toHaveAttribute("title", "Updating this draft…");
    expect(ghost).toHaveClass("is-refining");
    act(() => {
      useCopilot.setState({ running: false, refining: null });
    });
    expect(ghost.querySelector(".draft-ghost-handle")).not.toBeNull();
  });

  it("leaves the title as it was when a ghost's rename is cancelled", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    act(() => {
      fireEvent.doubleClick(ghost);
    });
    const input = ghost.querySelector<HTMLInputElement>("input.draft-ghost-input")!;
    act(() => {
      fireEvent.change(input, { target: { value: "Something else" } });
      fireEvent.keyDown(input, { key: "Escape" });
    });
    expect(currentDraft(useCopilot.getState(), "d1")?.nodes[0].title).toBe("Docs");
    expect(container.querySelector("input.draft-ghost-input")).toBeNull();
  });

  it("starts neither a marquee nor a pan from a ghost", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    const board = container.querySelector<HTMLElement>(".board")!;
    act(() => {
      fireEvent.pointerDown(ghost, { button: 0, ctrlKey: true, clientX: 5, clientY: 5, pointerType: "mouse" });
    });
    expect(container.querySelector(".graph-marquee")).toBeNull();
    act(() => {
      fireEvent.pointerDown(ghost, { button: 0, clientX: 5, clientY: 5, pointerType: "mouse" });
      fireEvent.pointerMove(board, { button: 0, buttons: 1, clientX: 80, clientY: 60, pointerType: "mouse" });
    });
    expect(board).not.toHaveClass("panning");
  });

  it("lets a ghost take the pointer (its double-click and click), unlike the edges", () => {
    // jsdom applies no CSS: the rule itself is the contract.
    const css = readFileSync(resolve(process.cwd(), "src/styles/04-board.css"), "utf8");
    const rule = /\.draft-ghost\s*\{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rule).toMatch(/pointer-events:\s*auto/);
    expect(rule).toMatch(/user-select:\s*none/);
  });

  it("draws plain ghosts without a theme: the title, no picture", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k2"]')!;
    expect(ghost).not.toHaveClass("is-picture");
    expect(ghost.querySelector(".draft-kind-glyph")).toBeNull();
    act(() => {
      fireEvent.click(ghost);
    });
    expect(container.querySelector(".draft-ghost-detail")).toBeNull();
  });

  it("draws picture cards with a theme: a large kind glyph and the title, the rest behind a click", () => {
    themeOn();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k2"]')!;
    expect(ghost).toHaveClass("is-picture");
    const glyph = ghost.querySelector<HTMLElement>(".draft-kind-glyph.is-large");
    expect(glyph).not.toBeNull();
    expect(glyph!.dataset.kind).toBe("task");
    // The title only: the body and what it waits for stay behind a click.
    expect(ghost).toHaveTextContent(/^Ship$/);
    expect(container.querySelector(".draft-ghost-detail")).toBeNull();

    act(() => {
      fireEvent.click(ghost);
    });
    const detail = container.querySelector<HTMLElement>(".draft-ghost-detail")!;
    expect(detail).not.toBeNull();
    expect(detail).toHaveTextContent("Tag the release and push it.");
    expect(detail.querySelector(".draft-chip")).toHaveTextContent("After Docs");

    // A click elsewhere puts it away; so does a second click on the card.
    act(() => {
      fireEvent.pointerDown(document.body);
    });
    expect(container.querySelector(".draft-ghost-detail")).toBeNull();
    act(() => {
      fireEvent.click(ghost);
    });
    expect(container.querySelector(".draft-ghost-detail")).not.toBeNull();
    act(() => {
      fireEvent.keyDown(window, { key: "Escape" });
    });
    expect(container.querySelector(".draft-ghost-detail")).toBeNull();

    // The first step waits for nothing, and has no body yet.
    act(() => {
      fireEvent.click(container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!);
    });
    const first = container.querySelector<HTMLElement>(".draft-ghost-detail")!;
    expect(first).toHaveTextContent("No description yet.");
    expect(first).toHaveTextContent("Waits for nothing.");
  });

  it("renames a picture card by double-clicking it", () => {
    themeOn();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    showDraft();
    const ghost = container.querySelector<HTMLElement>('.draft-ghost[data-draft-key="k1"]')!;
    act(() => {
      fireEvent.click(ghost);
      fireEvent.click(ghost);
      fireEvent.doubleClick(ghost);
    });
    expect(container.querySelector(".draft-ghost-detail")).toBeNull();
    const input = ghost.querySelector<HTMLInputElement>("input.draft-ghost-input")!;
    act(() => {
      fireEvent.change(input, { target: { value: "Docs first" } });
      fireEvent.blur(input);
    });
    expect(currentDraft(useCopilot.getState(), "d1")?.nodes[0].title).toBe("Docs first");
  });
});
