import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences/storage";
import { useApp } from "../../store";
import type { GraphNode } from "../../types";
import { NodeFolderTree, type NodeRowProps } from "./NodeFolderTree";

// A 2000-node project mounted every row of the explorer's node list — about
// 25k DOM nodes — whether or not anyone could see them. Only the rows in or
// near view are mounted now; the list still says how long it is, the arrow
// keys still walk all of it, and the open node is still brought into view.

const NODES: GraphNode[] = Array.from({ length: 2000 }, (_, index) => ({
  id: `node-${String(index + 1).padStart(4, "0")}`,
  title: `Card ${index + 1}`,
}));

function renderRow(node: GraphNode, row?: NodeRowProps) {
  return (
    <li key={node.id} className="test-row" {...row}>
      <button type="button">{node.title}</button>
    </li>
  );
}

function setup(overrides: Partial<Parameters<typeof NodeFolderTree>[0]> = {}) {
  return render(
    <NodeFolderTree
      folders={[]}
      folderOf={{}}
      visibleNodes={NODES}
      searching={false}
      renderNode={renderRow}
      createFolder={vi.fn()}
      renameFolder={vi.fn()}
      deleteFolder={vi.fn()}
      moveFolder={vi.fn()}
      moveNodes={vi.fn()}
      {...overrides}
    />,
  );
}

const mountedRows = (container: HTMLElement) => container.querySelectorAll(".test-row");

describe("NodeFolderTree with a long node list", () => {
  beforeEach(() => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  });
  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it("mounts only the rows near the view", () => {
    const { container } = setup();
    const rows = mountedRows(container);
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.length).toBeLessThan(100);
  });

  it("does the same for a flat search result", () => {
    const { container } = setup({ searching: true });
    expect(mountedRows(container).length).toBeLessThan(100);
  });

  it("tells assistive tech where each row sits in the whole list", () => {
    const { container } = setup();
    const first = mountedRows(container)[0];
    expect(first).toHaveAttribute("aria-setsize", "2000");
    expect(first).toHaveAttribute("aria-posinset", "1");
  });

  it("walks every row with the arrow keys, mounting the ones it reaches", () => {
    setup();
    const first = screen.getByRole("button", { name: "Card 1" });
    first.focus();
    fireEvent.keyDown(first, { key: "ArrowDown" });
    expect(screen.getByRole("button", { name: "Card 2" })).toHaveFocus();

    fireEvent.keyDown(document.activeElement!, { key: "End" });
    const last = screen.getByRole("button", { name: "Card 2000" });
    expect(last).toHaveFocus();
    expect(last.closest("li")).toHaveAttribute("aria-posinset", "2000");

    fireEvent.keyDown(last, { key: "ArrowUp" });
    expect(screen.getByRole("button", { name: "Card 1999" })).toHaveFocus();

    fireEvent.keyDown(document.activeElement!, { key: "Home" });
    expect(screen.getByRole("button", { name: "Card 1" })).toHaveFocus();
  });

  it("keeps the focused row mounted however far the list scrolls", () => {
    const { container } = setup();
    const first = screen.getByRole("button", { name: "Card 1" });
    first.focus();
    fireEvent.keyDown(first, { key: "End" });
    act(() => {
      window.dispatchEvent(new Event("scroll"));
    });
    expect(screen.getByRole("button", { name: "Card 2000" })).toHaveFocus();
    expect(mountedRows(container).length).toBeLessThan(100);
  });

  it("brings the open node into view", () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(Element.prototype, "scrollIntoView", {
      configurable: true,
      writable: true,
      value: scrollIntoView,
    });
    const { rerender } = setup();
    expect(screen.queryByRole("button", { name: "Card 1500" })).toBeNull();
    rerender(
      <NodeFolderTree
        folders={[]}
        folderOf={{}}
        visibleNodes={NODES}
        searching={false}
        renderNode={renderRow}
        activeNodeId="node-1500"
        createFolder={vi.fn()}
        renameFolder={vi.fn()}
        deleteFolder={vi.fn()}
        moveFolder={vi.fn()}
        moveNodes={vi.fn()}
      />,
    );
    const row = screen.getByRole("button", { name: "Card 1500" }).closest("li");
    expect(scrollIntoView).toHaveBeenCalled();
    expect(scrollIntoView.mock.contexts).toContain(row);
  });
});
