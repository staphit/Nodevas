import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences/storage";
import { useApp } from "../../store";
import type { GraphNode } from "../../types";
import { FOLDER_DRAG_TYPE, NODE_DRAG_TYPE, NodeFolderTree } from "./NodeFolderTree";

const NODES: GraphNode[] = [
  { id: "alpha", title: "設計稿" },
  { id: "beta", title: "實作" },
  { id: "gamma", title: "驗收" },
];

function setup(overrides: Partial<Parameters<typeof NodeFolderTree>[0]> = {}) {
  const actions = {
    createFolder: vi.fn(),
    renameFolder: vi.fn(),
    deleteFolder: vi.fn(),
    moveFolder: vi.fn(),
    moveNodes: vi.fn(),
  };
  render(
    <NodeFolderTree
      folders={["章節一", "章節一/場景"]}
      folderOf={{ alpha: "章節一", beta: "章節一/場景" }}
      visibleNodes={NODES}
      searching={false}
      renderNode={(node) => <li key={node.id}>{node.title}</li>}
      {...actions}
      {...overrides}
    />,
  );
  return actions;
}

/** Minimal stand-in for the drag payload; jsdom has no DataTransfer. */
function transfer(type: string, data: string) {
  return {
    types: [type],
    getData: (requested: string) => (requested === type ? data : ""),
    dropEffect: "",
  };
}

describe("NodeFolderTree", () => {
  beforeEach(() => {
    useApp.setState({
      preferences: { ...readPreferences(), language: "zh-TW" },
    });
  });

  it("files each node under the folder holding it", () => {
    setup();
    // Nested folders and their contents are shown, and a node with no folder
    // stays at the root.
    expect(screen.getByText("章節一")).toBeInTheDocument();
    expect(screen.getByText("場景")).toBeInTheDocument();
    expect(screen.getByText("設計稿")).toBeInTheDocument();
    expect(screen.getByText("驗收")).toBeInTheDocument();
  });

  it("moves the dropped nodes into the folder they land on", () => {
    const actions = setup();
    const row = screen.getByTitle(/^章節一（/);
    fireEvent.drop(row, {
      dataTransfer: transfer(NODE_DRAG_TYPE, JSON.stringify(["gamma"])),
    });
    expect(actions.moveNodes).toHaveBeenCalledWith(["gamma"], "章節一");
  });

  it("refuses to drop a folder inside its own subtree", () => {
    const actions = setup();
    const inner = screen.getByTitle(/^章節一\/場景（/);
    fireEvent.drop(inner, {
      dataTransfer: transfer(FOLDER_DRAG_TYPE, "章節一"),
    });
    expect(actions.moveFolder).not.toHaveBeenCalled();
  });

  it("creates a folder from the name typed into the new row", async () => {
    const user = userEvent.setup();
    const actions = setup();
    await user.click(screen.getByRole("button", { name: /新增資料夾/ }));
    const input = screen.getByLabelText("新資料夾名稱");
    await user.type(input, "章節二{Enter}");
    expect(actions.createFolder).toHaveBeenCalledWith("章節二");
  });

  it("lists every hit flat while a search is running", () => {
    setup({ searching: true });
    expect(screen.queryByText("章節一")).not.toBeInTheDocument();
    expect(screen.getByText("設計稿")).toBeInTheDocument();
    expect(screen.getByText("實作")).toBeInTheDocument();
  });
});
