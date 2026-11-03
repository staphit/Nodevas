import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmDialogHost } from "../ConfirmDialog";
import type { GraphSelection } from "../LaneView";
import { useBoardShortcuts } from "./useBoardShortcuts";
import type { Graph } from "../../types";

const graph: Graph = {
  version: 1,
  nodes: [{ id: "a", title: "丁節點" }],
  edges: [],
  ui: {},
};

function Board({
  selection,
  openTab,
  deleteNode,
  setGraphSelection = () => undefined,
}: {
  selection: GraphSelection;
  openTab: (id: string) => Promise<unknown>;
  deleteNode: (id: string) => Promise<unknown>;
  setGraphSelection?: (selection: GraphSelection) => void;
}) {
  useBoardShortcuts({
    isGraph: true,
    collapsed: false,
    keyboardActive: true,
    graph,
    graphSelection: selection,
    setGraphSelection,
    selectedEdgeEndpoints: null,
    marqueeHandlers: { cancel: () => false },
    openTab,
    deleteNode,
    deleteNodes: async () => undefined,
    updateCanvasLayout: async () => ({ ok: true }) as never,
    toggleEdgeStyle: async () => undefined,
    shortcutBusy: false,
    setShortcutBusy: () => undefined,
  });
  return (
    <button type="button" onClick={() => toolbarClicks.push("clicked")}>
      toolbar
    </button>
  );
}

let toolbarClicks: string[] = [];

describe("useBoardShortcuts", () => {
  beforeEach(() => {
    toolbarClicks = [];
  });

  it("lets Enter confirm the delete dialog instead of opening the node behind it", async () => {
    const user = userEvent.setup();
    const openTab = vi.fn(async () => undefined);
    const deleteNode = vi.fn(async () => undefined);
    render(
      <>
        <Board selection={{ kind: "node", id: "a" }} openTab={openTab} deleteNode={deleteNode} />
        <ConfirmDialogHost />
      </>,
    );

    await user.keyboard("{Delete}");
    const confirm = await screen.findByRole("button", { name: "Move to trash" });
    expect(document.activeElement).toBe(confirm);

    await user.keyboard("{Enter}");

    expect(openTab).not.toHaveBeenCalled();
    expect(deleteNode).toHaveBeenCalledWith("a");
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("leaves Enter on a focused button to the button", async () => {
    const user = userEvent.setup();
    const openTab = vi.fn(async () => undefined);
    render(
      <Board
        selection={{ kind: "node", id: "a" }}
        openTab={openTab}
        deleteNode={async () => undefined}
      />,
    );

    screen.getByRole("button", { name: "toolbar" }).focus();
    await user.keyboard("{Enter}");

    expect(toolbarClicks).toEqual(["clicked"]);
    expect(openTab).not.toHaveBeenCalled();
  });

  it("leaves Escape in an open modal to the modal, not the selection", async () => {
    const user = userEvent.setup();
    const deleteNode = vi.fn(async () => undefined);
    const setGraphSelection = vi.fn();
    render(
      <>
        <Board
          selection={{ kind: "node", id: "a" }}
          openTab={async () => undefined}
          deleteNode={deleteNode}
          setGraphSelection={setGraphSelection}
        />
        <ConfirmDialogHost />
      </>,
    );

    await user.keyboard("{Delete}");
    await screen.findByRole("alertdialog");
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(deleteNode).not.toHaveBeenCalled();
    // Cancelling the dialog keeps the card selected for another try.
    expect(setGraphSelection).not.toHaveBeenCalled();
  });
});
