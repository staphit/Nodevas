import { act, fireEvent, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type CopilotDraftData } from "../../api";
import { currentDraft, entryToItem, useCopilot } from "../../copilot/copilotStore";
import { renameNode } from "../../copilot/draft/draftStore";
import { useDraftUi } from "../../copilot/draft/draftUi";
import { useApp } from "../../store";
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
      { key: "k2", title: "Ship", kind: "task", after: ["k1"] },
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
  cardRenders.clear();
});

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
});
