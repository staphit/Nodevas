import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { GraphBatchToolbar, type GraphBatchToolbarProps } from "./GraphBatchToolbar";

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    nodeClipboard: null,
  });
});

function toolbar(overrides: Partial<GraphBatchToolbarProps> = {}) {
  const props: GraphBatchToolbarProps = {
    graph: {
      version: 1,
      nodes: [
        { id: "a", title: "Alpha" },
        { id: "b", title: "Beta" },
      ],
      edges: [],
      users: [{ id: "u1", name: "Quinn" }],
      ui: {},
    },
    customStatuses: [],
    selectableStatuses: ["ready", "done"],
    selectedIDs: ["a", "b"],
    shortcutBusy: false,
    setShortcutBusy: vi.fn(),
    applyBatchStatus: vi.fn(),
    applyBatchAssignee: vi.fn(),
    createGroupFromSelection: vi.fn(),
    duplicateNode: vi.fn(async (id: string) => `${id}-copy`),
    deleteNodes: vi.fn(async () => {}),
    setGraphSelection: vi.fn(),
    onSelectedNodeChange: vi.fn(),
    ...overrides,
  };
  render(<GraphBatchToolbar {...props} />);
  return props;
}

describe("GraphBatchToolbar", () => {
  it("keeps the batch actions in the row and the rare ones in ⋯", async () => {
    const user = userEvent.setup();
    toolbar();
    const row = screen.getByRole("toolbar", { name: "2 nodes" });

    expect(within(row).getByRole("combobox", { name: /Mark as/ })).toBeInTheDocument();
    expect(within(row).getByRole("combobox", { name: /Assign to/ })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Delete" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Clear selection" })).toBeInTheDocument();
    for (const name of ["Duplicate", "Create group background", /Copy to project/, /Move to project/]) {
      expect(within(row).queryByRole("button", { name })).not.toBeInTheDocument();
    }

    await user.click(within(row).getByRole("button", { name: "Move or copy to project" }));
    const transfer = screen.getByRole("menu", { name: "Move or copy to project" });
    expect(within(transfer).getByRole("menuitem", { name: "Move to project…" })).toBeInTheDocument();
    expect(within(transfer).getByRole("menuitem", { name: "Copy to project…" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.click(within(row).getByRole("button", { name: "More actions for the selection" }));
    const more = screen.getByRole("menu", { name: "More actions for the selection" });
    expect(within(more).getByRole("menuitem", { name: "Duplicate" })).toBeInTheDocument();
    expect(within(more).getByRole("menuitem", { name: "Create group background" })).toBeInTheDocument();
  });

  it("applies a status or an assignee on choice", async () => {
    const user = userEvent.setup();
    const props = toolbar();

    await user.selectOptions(screen.getByRole("combobox", { name: /Mark as/ }), "done");
    expect(props.applyBatchStatus).toHaveBeenCalledWith("done");

    const assign = screen.getByRole("combobox", { name: /Assign to/ });
    await user.selectOptions(assign, "u1");
    expect(props.applyBatchAssignee).toHaveBeenLastCalledWith("u1");
    // Unassigned is a real choice, not the placeholder.
    await user.selectOptions(assign, "Unassigned");
    expect(props.applyBatchAssignee).toHaveBeenLastCalledWith("");
  });

  it("duplicates every selected node and selects the copies", async () => {
    const user = userEvent.setup();
    const props = toolbar();

    await user.click(screen.getByRole("button", { name: "More actions for the selection" }));
    await user.click(screen.getByRole("menuitem", { name: "Duplicate" }));

    await waitFor(() =>
      expect(props.setGraphSelection).toHaveBeenCalledWith({
        kind: "nodes",
        ids: ["a-copy", "b-copy"],
      }),
    );
    expect(props.duplicateNode).toHaveBeenCalledTimes(2);
  });

  it("clears the selection", async () => {
    const user = userEvent.setup();
    const props = toolbar();
    await user.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(props.setGraphSelection).toHaveBeenCalledWith(null);
    expect(props.onSelectedNodeChange).toHaveBeenCalledWith(null);
  });
});
