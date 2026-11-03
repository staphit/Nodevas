import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import { LaneView } from "../LaneView";
import { resetBoardStore } from "./boardTestKit";

// Every card draws one run badge, so the badge's renders count the card's.
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

const totalCardRenders = () => [...cardRenders.values()].reduce((sum, n) => sum + n, 0);

beforeEach(() => {
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore(["a", "b", "c"]);
  cardRenders.clear();
});

function renderBoardWithPeer() {
  const view = render(<LaneView variant="graph" {...paneProps} />);
  act(() => {
    useApp.getState().setPeers([
      { id: "p1", actor: { name: "Robin" }, color: "#e11d48" } as never,
    ]);
  });
  cardRenders.clear();
  return view;
}

describe("PeerOverlay", () => {
  it("draws a remote pointer without re-rendering a single card", () => {
    const { container } = renderBoardWithPeer();

    for (let step = 0; step < 5; step++) {
      act(() => {
        useApp.getState().setPresence("p1", { pointer: { x: 40 + step, y: 20 } });
      });
    }

    const pointer = container.querySelector<HTMLElement>(".peer-pointer");
    expect(pointer).not.toBeNull();
    expect(pointer).toHaveTextContent("Robin");
    expect(pointer!.style.getPropertyValue("--peer-color")).toBe("#e11d48");
    expect(totalCardRenders()).toBe(0);

    act(() => {
      useApp.getState().setPresence("p1", null);
    });
    expect(container.querySelector(".peer-pointer")).toBeNull();
    expect(totalCardRenders()).toBe(0);
  });

  it("draws a remote drag ghost over the card it moves, cards untouched", () => {
    const { container } = renderBoardWithPeer();
    const card = container.querySelector<HTMLElement>('.col-card[data-node-id="b"]')!;
    const left = parseFloat(card.style.left);
    const top = parseFloat(card.style.top);

    act(() => {
      useApp.getState().setGhost("p1", { ids: ["b"], dx: 30, dy: -12, active: true });
    });

    const ghost = container.querySelector<HTMLElement>(".peer-ghost");
    expect(ghost).not.toBeNull();
    expect(ghost).toHaveAttribute("title", "Robin");
    expect(parseFloat(ghost!.style.left)).toBeCloseTo(left + 30);
    expect(parseFloat(ghost!.style.top)).toBeCloseTo(top - 12);
    expect(ghost!.style.width).toBe(card.style.width);
    expect(totalCardRenders()).toBe(0);

    act(() => {
      useApp.getState().setGhost("p1", { ids: [], dx: 0, dy: 0, active: false });
    });
    expect(container.querySelector(".peer-ghost")).toBeNull();
    expect(totalCardRenders()).toBe(0);
  });
});
