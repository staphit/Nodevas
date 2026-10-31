import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { AssigneeField, NodeMetaForm } from "./NodeMetaForm";

const updateNode = vi.fn(async () => ({ ok: true }));

beforeEach(() => {
  updateNode.mockClear();
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: {
      version: 1,
      nodes: [{ id: "a", title: "A", assignee: "u1" }],
      edges: [],
      users: [{ id: "u1", name: "Ann" }],
      ui: {},
    } as unknown as Graph,
    updateNode: updateNode as never,
  });
});

function field() {
  render(<AssigneeField id="a" />);
  const input = screen.getByRole("combobox") as HTMLInputElement;
  input.focus();
  return input;
}

describe("AssigneeField", () => {
  it("commits a typed name on Enter", () => {
    const input = field();
    fireEvent.change(input, { target: { value: "Bob" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(updateNode).toHaveBeenCalledWith({ type: "node.assignByName", nodeId: "a", name: "Bob" });
  });

  // Escape reverts: the blur it causes must not commit what was typed.
  it("reverts on Escape without committing", () => {
    const input = field();
    fireEvent.change(input, { target: { value: "Bob" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(updateNode).not.toHaveBeenCalled();
    expect(input.value).toBe("Ann");
    // The next edit commits as usual.
    input.focus();
    fireEvent.change(input, { target: { value: "Cid" } });
    input.blur();
    expect(updateNode).toHaveBeenCalledWith({ type: "node.assignByName", nodeId: "a", name: "Cid" });
  });
});

describe("NodeMetaForm type", () => {
  const updateNodeMetadata = vi.fn(async () => ({ ok: true as const }));

  function form(kind: string) {
    useApp.setState({
      graph: {
        version: 1,
        nodes: [{ id: "a", title: "A", kind }],
        edges: [],
        nodeTypes: [
          {
            id: "custom-type-1",
            label: "Foreshadowing",
            agentMode: "none",
            completion: "none",
            countsInProgress: false,
          },
        ],
      } as Graph,
      updateNodeMetadata: updateNodeMetadata as never,
    });
    render(<NodeMetaForm id="a" />);
    return screen.getByRole("combobox", { name: "Type" }) as HTMLSelectElement;
  }

  it("lists built-in and custom types, and commits the id", () => {
    const select = form("gate");
    expect(select.value).toBe("gate");
    expect(within(select).getByRole("group", { name: "Built-in" })).toBeInTheDocument();
    expect(within(select).getByRole("group", { name: "Custom" })).toBeInTheDocument();
    fireEvent.change(select, { target: { value: "custom-type-1" } });
    expect(updateNodeMetadata).toHaveBeenCalledWith("a", { kind: "custom-type-1" });
  });

  it("shows a legacy free-form kind as written, without offering it", () => {
    const select = form("bug");
    expect(select.value).toBe("bug");
    expect(within(select).getByRole("option", { name: "bug" })).toBeDisabled();
  });
});
