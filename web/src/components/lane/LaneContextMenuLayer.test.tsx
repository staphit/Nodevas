import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import {
  LaneContextMenuLayer,
  useLaneContextMenu,
  type LaneContextMenuLayerProps,
} from "./LaneContextMenuLayer";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

const createGroupFromSelection = vi.fn();
const selectNode = vi.fn();

/** A card that opens the node menu, as GraphCanvas's do. */
function Board() {
  const { contextMenu, setContextMenu } = useLaneContextMenu();
  return (
    <>
      <div
        role="button"
        tabIndex={0}
        onClick={() => setContextMenu({ kind: "node-actions", nodeId: "a", x: 10, y: 20 })}
      >
        Alpha card
      </div>
      {contextMenu && (
        <LaneContextMenuLayer
          {...({
            contextMenu,
            setContextMenu,
            graph: { version: 1, nodes: [{ id: "a", title: "Alpha" }], edges: [], ui: {} },
            nodeTitle: (id: string | undefined) => (id === "a" ? "Alpha" : id ?? ""),
            logicGates: [],
            planStatusDefinitions: [],
            openTab: vi.fn(async () => {}),
            selectNode,
            duplicateNode: vi.fn(async () => "a2"),
            deleteNode: vi.fn(async () => {}),
            createGroupFromSelection,
          } as unknown as LaneContextMenuLayerProps)}
        />
      )}
    </>
  );
}

describe("LaneContextMenuLayer", () => {
  const drawerEscape = vi.fn();
  beforeEach(() => window.addEventListener("keydown", drawerEscape));
  afterEach(() => {
    window.removeEventListener("keydown", drawerEscape);
    vi.clearAllMocks();
  });

  it("names the node menu after its node", async () => {
    const user = userEvent.setup();
    render(<Board />);
    await user.click(screen.getByRole("button", { name: "Alpha card" }));
    expect(screen.getByRole("menu", { name: "Actions for Alpha" })).toBeInTheDocument();
  });

  it("takes Escape for itself, so the drawer below stays open, and hands focus back", async () => {
    const user = userEvent.setup();
    render(<Board />);
    const card = screen.getByRole("button", { name: "Alpha card" });
    act(() => card.focus());
    await user.click(card);
    expect(screen.getByRole("menuitem", { name: "Open" })).toHaveFocus();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(drawerEscape).not.toHaveBeenCalled();
    expect(card).toHaveFocus();

    // With the menu gone, Escape is the drawer's again.
    await user.keyboard("{Escape}");
    expect(drawerEscape).toHaveBeenCalledTimes(1);
  });

  it("draws a group background around the node from its menu", async () => {
    const user = userEvent.setup();
    render(<Board />);
    await user.click(screen.getByRole("button", { name: "Alpha card" }));
    await user.click(screen.getByRole("menuitem", { name: "Create group background" }));
    expect(selectNode).toHaveBeenCalledWith("a");
    expect(createGroupFromSelection).toHaveBeenCalledWith(["a"]);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});
