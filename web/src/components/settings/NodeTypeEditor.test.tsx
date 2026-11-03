import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { applyGraphCommand, type WorkflowCommand } from "../../domain/commands";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { ConfirmDialogHost } from "../ConfirmDialog";
import { NodeTypeEditor } from "./NodeTypeEditor";

/** Applies the command the way the store would, so the list re-renders. */
const updateWorkflowDefinition = vi.fn(async (command: WorkflowCommand) => {
  const graph = structuredClone(useApp.getState().graph!) as Graph;
  try {
    applyGraphCommand(graph, command);
  } catch (error) {
    return { ok: false as const, message: (error as Error).message };
  }
  useApp.setState({ graph });
  return { ok: true as const };
});

function open() {
  useApp.setState({
    graph: {
      version: 1,
      nodes: [
        { id: "a", title: "暗線一", kind: "custom-type-1" },
        { id: "b", title: "暗線二", kind: "custom-type-1" },
        { id: "c", title: "第一章" },
      ],
      edges: [],
      nodeTypes: [
        {
          id: "custom-type-1",
          label: "伏筆",
          shape: "diamond",
          agentMode: "none",
          completion: "anyone",
          countsInProgress: true,
        },
      ],
    },
    updateWorkflowDefinition: updateWorkflowDefinition as never,
  });
  const notify = { onError: vi.fn(), onNotice: vi.fn() };
  render(
    <>
      <NodeTypeEditor notify={notify} />
      <ConfirmDialogHost />
    </>,
  );
  return { notify, user: userEvent.setup() };
}

function row(id: string): HTMLElement {
  return document.querySelector<HTMLElement>(`.node-type-row[data-node-type="${id}"]`)!;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
  useApp.getState().updateUIPreference("language", "zh-TW");
  updateWorkflowDefinition.mockClear();
});

describe("NodeTypeEditor", () => {
  it("lists the nine built-ins and the project's own types", () => {
    open();
    const rows = document.querySelectorAll(".node-type-row");
    expect(rows).toHaveLength(10);
    for (const label of ["任務", "目標", "場景", "事件", "審核", "決策", "開始", "結束", "筆記"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
    expect(screen.getByRole("textbox", { name: "伏筆 的名稱" })).toHaveValue("伏筆");
  });

  it("lets a built-in change only its shape and colour", async () => {
    const { user } = open();
    const gate = row("gate");
    expect(within(gate).queryByRole("button", { name: /刪除/ })).toBeNull();
    expect(within(gate).queryByRole("combobox", { name: /agent 模式/ })).toBeNull();
    expect(within(gate).queryByRole("combobox", { name: /誰能完成/ })).toBeNull();
    // Its behaviour is described instead.
    expect(gate).toHaveTextContent("不派 agent · 只有人");
    // Goal's people-only rule reads as what it means.
    expect(row("goal")).toHaveTextContent("Autopilot 完成所有子任務時，或由人標記");

    await user.selectOptions(within(gate).getByRole("combobox", { name: "審核 的形狀" }), "round");
    expect(updateWorkflowDefinition).toHaveBeenCalledWith({
      type: "workflow.updateNodeType",
      id: "gate",
      patch: { shape: "round" },
    });
    expect(useApp.getState().graph?.nodeTypes).toContainEqual({ id: "gate", shape: "round" });
  });

  it("adds a custom type from the name typed", async () => {
    const { user } = open();
    await user.type(screen.getByRole("textbox", { name: "新節點類型名稱" }), "角色");
    await user.click(screen.getByRole("button", { name: "新增參考類型（沒有狀態）" }));

    expect(updateWorkflowDefinition).toHaveBeenCalledWith({
      type: "workflow.addNodeType",
      definition: {
        label: "角色",
        shape: "dashed",
        agentMode: "none",
        completion: "none",
        countsInProgress: false,
      },
    });
    await waitFor(() => expect(row("custom-type-2")).toBeTruthy());
  });

  it("makes a type with no status stop counting as work", async () => {
    const { user } = open();
    const custom = row("custom-type-1");
    const counts = within(custom).getByRole("checkbox", { name: "計入進度" });
    expect(counts).toBeChecked();
    expect(counts).toBeEnabled();

    await user.selectOptions(
      within(custom).getByRole("combobox", { name: "誰能完成 伏筆" }),
      "none",
    );
    await waitFor(() =>
      expect(within(row("custom-type-1")).getByRole("checkbox", { name: "計入進度" })).toBeDisabled(),
    );
    expect(within(row("custom-type-1")).getByRole("checkbox", { name: "計入進度" })).not.toBeChecked();
    expect(within(row("custom-type-1")).getByRole("combobox", { name: "伏筆 的 agent 模式" })).toBeDisabled();
  });

  it("asks before deleting a type in use, and its nodes become Tasks", async () => {
    const { user } = open();
    await user.click(within(row("custom-type-1")).getByRole("button", { name: "刪除 伏筆" }));

    expect(await screen.findByText("有 2 個節點使用這個類型，刪除後會變回任務。")).toBeInTheDocument();
    expect(updateWorkflowDefinition).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "刪除類型" }));

    await waitFor(() =>
      expect(updateWorkflowDefinition).toHaveBeenCalledWith({
        type: "workflow.removeNodeType",
        id: "custom-type-1",
      }),
    );
    const nodes = useApp.getState().graph?.nodes ?? [];
    expect(nodes.find((node) => node.id === "a")?.kind).toBe("task");
    expect(nodes.find((node) => node.id === "b")?.kind).toBe("task");
    expect(useApp.getState().graph?.nodeTypes).toBeUndefined();
  });
});
