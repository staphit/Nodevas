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

  // 上線 waits for 設計, so 設計 waiting for 上線 (or for anything that
  // waits on 上線) would close a loop: the picker used to offer it first and
  // the Add button then failed with a circular-dependency error.
  it("never offers a node that already waits on this one, however far down", () => {
    const g = graph();
    g.nodes!.push({ id: "launch", title: "發表" });
    g.edges!.push({ from: "ship", to: "launch" });
    useApp.setState({ graph: g });
    render(<NodeDependencies id="design" />);
    const picker = screen.getByLabelText("選擇前置節點");
    const offered = within(picker).getAllByRole("option").map((option) => option.textContent);
    expect(offered).toEqual(["審查"]);
    expect(picker).toHaveValue("review");
  });

  it("adds the prerequisite picked from the nodes not yet connected", async () => {
    const user = userEvent.setup();
    render(<NodeDependencies id="design" />);
    const picker = screen.getByLabelText("選擇前置節點");
    // Neither the node itself, a node it already waits for, nor one that
    // waits on it (上線) is offered.
    const offered = within(picker).getAllByRole("option").map((option) => option.textContent);
    expect(offered).toEqual(["審查"]);

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

  // UX sweep: the picker still offered prerequisites the node already had —
  // the one just added, until the server answered, and any the expression
  // names without a wire (a requires saved while the server could not check
  // it).
  it("stops offering a prerequisite the moment it is added", async () => {
    const user = userEvent.setup();
    updateNode.mockImplementationOnce(() => new Promise(() => {}));
    const g = graph();
    g.nodes!.push({ id: "docs", title: "文件" });
    useApp.setState({ graph: g });
    render(<NodeDependencies id="design" />);
    const picker = screen.getByLabelText("選擇前置節點");
    await user.selectOptions(picker, "review");
    await user.click(screen.getByRole("button", { name: "新增" }));
    const offered = within(screen.getByLabelText("選擇前置節點")).getAllByRole("option").map((option) => option.textContent);
    expect(offered).toEqual(["文件"]);
  });

  it("does not offer a prerequisite the expression already names", () => {
    const g = graph("plan and review");
    g.nodes!.push({ id: "docs", title: "文件" });
    useApp.setState({ graph: g });
    render(<NodeDependencies id="design" />);
    const offered = within(screen.getByLabelText("選擇前置節點")).getAllByRole("option").map((option) => option.textContent);
    expect(offered).toEqual(["文件"]);
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

describe("NodeDependencies references", () => {
  function withNotes(): Graph {
    const g = graph();
    g.nodes = [
      ...g.nodes!,
      { id: "world", title: "世界設定", kind: "note" },
      { id: "hero", title: "角色卡：林曉", kind: "note" },
    ];
    g.nodes.find((node) => node.id === "design")!.refs = ["hero", "gone"];
    return g;
  }

  beforeEach(() => {
    useApp.setState({ graph: withNotes() });
  });

  it("lists the references apart from the dependencies, missing ones included", () => {
    render(<NodeDependencies id="design" />);
    const list = screen.getByRole("list", { name: "參考" });
    expect(within(list).getByRole("button", { name: "角色卡：林曉" })).toBeInTheDocument();
    expect(within(list).getByText("筆記")).toBeInTheDocument();
    expect(within(list).getByText("找不到節點 gone")).toBeInTheDocument();
    // The dependency list is unchanged: one prerequisite.
    expect(within(screen.getAllByRole("list")[0]).getAllByRole("listitem")).toHaveLength(1);
  });

  it("removes a reference from its own button", async () => {
    const user = userEvent.setup();
    render(<NodeDependencies id="design" />);
    await user.click(screen.getByRole("button", { name: "移除對「角色卡：林曉」的參考" }));
    expect(updateNode).toHaveBeenCalledWith({ type: "node.removeRef", nodeId: "design", ref: "hero" });
    await user.click(screen.getByRole("button", { name: "移除對「gone」的參考" }));
    expect(updateNode).toHaveBeenCalledWith({ type: "node.removeRef", nodeId: "design", ref: "gone" });
  });

  it("offers Notes first, then the other nodes, and adds the pick", async () => {
    const user = userEvent.setup();
    render(<NodeDependencies id="design" />);
    const picker = screen.getByLabelText("選擇參考節點");
    const groups = within(picker).getAllByRole("group");
    expect(groups.map((group) => group.getAttribute("label"))).toEqual(["筆記", "其他節點"]);
    // Neither the node itself nor what it already references is offered.
    expect(within(groups[0]).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "世界設定",
    ]);
    expect(within(groups[1]).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "企劃",
      "審查",
      "上線",
    ]);
    expect(picker).toHaveValue("world");

    await user.click(screen.getByRole("button", { name: "加入參考" }));
    expect(updateNode).toHaveBeenCalledWith({ type: "node.addRef", nodeId: "design", ref: "world" });
  });
});
