import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { NodeActionsMenu, type NodeActionsMenuProps } from "./NodeActionsMenu";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});
afterEach(() => {
  act(() => useApp.getState().updateUIPreference("agentWorkstation", false));
});

function menu(overrides: Partial<NodeActionsMenuProps> = {}) {
  const props: NodeActionsMenuProps = {
    contextMenu: { kind: "node-actions", nodeId: "a", x: 10, y: 20 },
    graph: { version: 1, nodes: [{ id: "a", title: "Alpha" }], edges: [], ui: {} },
    nodeTitle: (id) => (id === "a" ? "Alpha" : id ?? ""),
    openTab: vi.fn(async () => {}),
    selectNode: vi.fn(),
    duplicateNode: vi.fn(async () => "a2"),
    deleteNode: vi.fn(async () => {}),
    setContextMenu: vi.fn(),
    ...overrides,
  };
  render(
    <div role="menu">
      <NodeActionsMenu {...props} />
    </div>,
  );
  return props;
}

describe("NodeActionsMenu", () => {
  it("lists the everyday actions and focuses the first", () => {
    menu();
    const names = screen.getAllByRole("menuitem").map((item) => item.textContent);
    expect(names).toEqual([
      "Open",
      "Add dependency…",
      "Duplicate",
      "Move or copy to project▸",
      "Move to trash",
    ]);
    expect(screen.getByRole("menuitem", { name: "Open" })).toHaveFocus();
  });

  it("opens the node and closes", async () => {
    const user = userEvent.setup();
    const props = menu();
    await user.click(screen.getByRole("menuitem", { name: "Open" }));
    expect(props.setContextMenu).toHaveBeenCalledWith(null);
    expect(props.selectNode).toHaveBeenCalledWith("a");
    expect(props.openTab).toHaveBeenCalledWith("a");
  });

  it("swaps itself for the dependency editor at the same spot", async () => {
    const user = userEvent.setup();
    const props = menu();
    await user.click(screen.getByRole("menuitem", { name: "Add dependency…" }));
    expect(props.setContextMenu).toHaveBeenCalledWith({ kind: "node", nodeId: "a", x: 10, y: 20 });
  });

  it("keeps runs and autopilot under Dispatch to agent", async () => {
    act(() => useApp.getState().updateUIPreference("agentWorkstation", true));
    const user = userEvent.setup();
    menu();

    const agent = screen.getByRole("menuitem", { name: "Dispatch to agent" });
    expect(agent).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("menuitem", { name: "Runs" })).not.toBeInTheDocument();

    agent.focus();
    await user.keyboard("{ArrowRight}");
    const submenu = screen.getByRole("menu", { name: "Dispatch to agent" });
    for (const name of ["Dispatch to agent…", "Runs", "Autopilot"]) {
      expect(within(submenu).getByRole("menuitem", { name })).toBeInTheDocument();
    }
    await vi.waitFor(() =>
      expect(within(submenu).getByRole("menuitem", { name: "Dispatch to agent…" })).toHaveFocus(),
    );
    await user.keyboard("{ArrowLeft}");
    expect(screen.queryByRole("menu", { name: "Dispatch to agent" })).not.toBeInTheDocument();
    expect(agent).toHaveFocus();
  });

  it("moves with the arrow keys", async () => {
    const user = userEvent.setup();
    menu();
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("menuitem", { name: "Add dependency…" })).toHaveFocus();
    await user.keyboard("{ArrowUp}{ArrowUp}");
    expect(screen.getByRole("menuitem", { name: "Move to trash" })).toHaveFocus();
  });
});
