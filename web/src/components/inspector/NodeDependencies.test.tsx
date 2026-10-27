import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { NodeDependencies } from "./NodeDependencies";

const ok = { ok: true } as never;

function graph(requires?: string): Graph {
  return {
    version: 1,
    nodes: [
      { id: "plan", title: "企劃" },
      { id: "design", title: "設計", ...(requires ? { requires } : {}) },
      { id: "review", title: "審查" },
      { id: "ship", title: "上線" },
    ],
    edges: [
      { from: "plan", to: "design" },
      { from: "design", to: "ship" },
    ],
    ui: {},
  } as unknown as Graph;
}

const updateNode = vi.fn(async () => ok);
const updateCanvasLayout = vi.fn(async () => ok);

beforeEach(() => {
  updateNode.mockClear();
  updateCanvasLayout.mockClear();
  vi.spyOn(api, "getNodeLinks").mockResolvedValue({ backlinks: [], outgoing: [] });
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "demo",
    graph: graph(),
    updateNode,
    updateCanvasLayout,
  });
});

describe("NodeDependencies", () => {
  it("lists what the node waits for and what it blocks", async () => {
    render(<NodeDependencies id="design" />);
    const list = screen.getAllByRole("list")[0];
    expect(within(list).getByRole("button", { name: "企劃" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "上線" })).toBeInTheDocument();
    // Document links are references, listed beside the dependencies.
    expect(await screen.findByText(/還沒有文件連結/)).toBeInTheDocument();
  });

  it("removes a prerequisite from its own button", async () => {
    const user = userEvent.setup();
    render(<NodeDependencies id="design" />);
    await user.click(screen.getByRole("button", { name: "移除對「企劃」的相依" }));
    expect(updateCanvasLayout).toHaveBeenCalledWith({
      type: "canvas.removeEdge",
      from: "plan",
      to: "design",
    });
  });

  it("adds the prerequisite picked from the nodes not yet connected", async () => {
    const user = userEvent.setup();
    render(<NodeDependencies id="design" />);
    const picker = screen.getByLabelText("選擇前置節點");
    // Neither the node itself nor a node it already waits for is offered.
    const offered = within(picker).getAllByRole("option").map((option) => option.textContent);
    expect(offered).toEqual(["審查", "上線"]);

    await user.selectOptions(picker, "review");
    await user.click(screen.getByRole("button", { name: "新增" }));
    expect(updateNode).toHaveBeenCalledWith({
      type: "node.addDependency",
      sourceId: "review",
      targetId: "design",
      operator: "and",
      relation: "",
    });
  });

  it("keeps advanced conditions folded unless the node already uses one", () => {
    const { unmount } = render(<NodeDependencies id="design" />);
    expect(screen.getByText("進階條件").closest("details")).not.toHaveAttribute("open");
    unmount();

    useApp.setState({ graph: graph("plan or review") });
    render(<NodeDependencies id="design" />);
    expect(screen.getByText("進階條件").closest("details")).toHaveAttribute("open");
  });
});
