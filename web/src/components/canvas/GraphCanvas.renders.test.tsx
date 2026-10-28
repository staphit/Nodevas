import { act, fireEvent, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useApp } from "../../store";
import type { CanvasCommand } from "../../store";
import { LaneView } from "../LaneView";
import { installPointerShims, manualFrames, resetBoardStore } from "./boardTestKit";

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

const realUpdateCanvasLayout = useApp.getState().updateCanvasLayout;

beforeEach(() => {
  installPointerShims();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  resetBoardStore(["a", "b", "c"]);
  useApp.setState({ updateCanvasLayout: realUpdateCanvasLayout });
  cardRenders.clear();
});

function stubLayout() {
  const commands: CanvasCommand[] = [];
  useApp.setState({
    updateCanvasLayout: vi.fn(async (command: CanvasCommand) => {
      commands.push(command);
      return { ok: true as const };
    }) as never,
  });
  return commands;
}

describe("graph cards", () => {
  it("re-render only the card whose selection changed", () => {
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    cardRenders.clear();

    act(() => {
      container.querySelector<HTMLElement>('.col-card[data-node-id="b"]')!.focus();
    });

    expect(container.querySelector('.col-card[data-node-id="b"]')).toHaveClass("selected");
    expect(cardRenders.get("b") ?? 0).toBeGreaterThan(0);
    expect(cardRenders.get("a") ?? 0).toBe(0);
    expect(cardRenders.get("c") ?? 0).toBe(0);
  });

  it("follows the pointer by frame during a drag and commits once on pointerup", () => {
    const frames = manualFrames();
    const commands = stubLayout();
    const { container } = render(<LaneView variant="graph" {...paneProps} />);
    const card = container.querySelector<HTMLElement>('.col-card[data-node-id="a"]')!;
    const surface = container.querySelector<HTMLElement>(".board-inner")!;

    act(() => {
      fireEvent.pointerDown(card, { button: 0, pointerId: 1, clientX: 0, clientY: 0 });
      fireEvent.pointerMove(card, { pointerId: 1, clientX: 12, clientY: 0 });
    });
    expect(card).toHaveClass("lifting");
    expect(surface).toHaveClass("dragging");
    cardRenders.clear();

    for (let step = 2; step <= 20; step++) {
      act(() => {
        fireEvent.pointerMove(card, { pointerId: 1, clientX: step * 12, clientY: step * 5 });
      });
    }
    // Nineteen moves and no card re-rendered: the move rides a transform.
    expect(cardRenders.get("a") ?? 0).toBe(0);
    expect(cardRenders.get("b") ?? 0).toBe(0);
    expect(cardRenders.get("c") ?? 0).toBe(0);

    act(() => frames.flush());
    expect(surface.style.getPropertyValue("--drag-dx")).toBe("240px");
    expect(surface.style.getPropertyValue("--drag-dy")).toBe("100px");
    expect(card.style.transform).toContain("var(--drag-dx");
    expect(commands).toHaveLength(0);

    act(() => {
      fireEvent.pointerUp(card, { pointerId: 1, clientX: 240, clientY: 100 });
    });
    const moves = commands.filter((command) => command.type === "canvas.moveNodes");
    expect(moves).toHaveLength(1);
    expect(card).not.toHaveClass("lifting");
    expect(card.style.transform).toBe("");
  });
});
