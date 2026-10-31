import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { useApp } from "../../store";
import type { Graph, GraphEdge, GraphNode, LogicGate } from "../../types";
import { EdgeMenu } from "./EdgeMenu";
import { GraphMenu } from "./GraphMenu";
import { NodeMenu, type NodeMenuProps } from "./NodeMenu";

const titles: Record<string, string> = {
  design: "Design",
  review: "Review",
  ship: "Ship",
  other: "Other",
};
const nodeTitle = (id: string | undefined) => (id ? titles[id] ?? id : "");

function nodeMenu(
  options: {
    requires?: string;
    incoming?: GraphEdge[];
    controllingLogicGate?: LogicGate;
    conditionOperator?: string;
    edges?: GraphEdge[];
  } = {},
) {
  const incoming = options.incoming ?? [
    { from: "design", to: "ship" },
    { from: "review", to: "ship" },
  ];
  const target: GraphNode = {
    id: "ship",
    title: "Ship",
    ...(options.requires !== undefined ? { requires: options.requires } : {}),
  };
  const addDependency = vi.fn();
  const props: NodeMenuProps = {
    contextMenu: { kind: "node", nodeId: "ship", x: 0, y: 0 },
    nodeTitle,
    edges: options.edges ?? incoming,
    incomingConditions: incoming,
    availableConditionSources: [{ id: "other", title: "Other" }],
    conditionSource: "other",
    setConditionSource: vi.fn(),
    conditionOperator: options.conditionOperator ?? "and",
    changeGateOperator: vi.fn(),
    conditionRelation: "",
    setConditionRelation: vi.fn(),
    hasEditableGateOperator: incoming.length > 1,
    conditionTarget: target,
    addDependency,
    setDependencyRelation: vi.fn(),
    controllingLogicGate: options.controllingLogicGate,
    assignableLogicGates: options.controllingLogicGate ? [options.controllingLogicGate] : [],
    assignExistingLogicGateToNode: vi.fn(),
    setContextMenu: vi.fn(),
  };
  const view = render(<NodeMenu {...props} />);
  return { ...view, addDependency };
}

const advancedDisclosure = (container: HTMLElement) =>
  container.querySelector("details.dependency-advanced") as HTMLDetailsElement;

describe("NodeMenu dependencies", () => {
  // UX sweep: a prerequisite the expression names without a wire was still
  // offered as a new one.
  it("does not offer a prerequisite the expression already names", () => {
    nodeMenu({ requires: "design and review and other" });
    const picker = screen.getByRole("combobox", { name: "Add a dependency" });
    expect(within(picker).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "No nodes available to add",
    ]);
    expect(picker).toBeDisabled();
  });

  // Other already waits on Ship (through Review here), so Ship waiting for
  // Other would close a loop that Add then refuses.
  it("does not offer a node that already waits on this one", () => {
    nodeMenu({
      requires: "design",
      incoming: [{ from: "design", to: "ship" }],
      edges: [
        { from: "design", to: "ship" },
        { from: "ship", to: "review" },
        { from: "review", to: "other" },
      ],
    });
    const picker = screen.getByRole("combobox", { name: "Add a dependency" });
    expect(within(picker).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "No nodes available to add",
    ]);
  });

  it("shows the default all-of condition as a plain depends-on list", async () => {
    const { container, addDependency } = nodeMenu({ requires: "design and review" });
    const list = container.querySelector(".dependency-list") as HTMLElement;
    expect(within(list).getByText("Depends on")).toBeTruthy();
    expect(within(list).getByText("Design")).toBeTruthy();
    expect(within(list).getByText("Review")).toBeTruthy();
    expect(list.querySelector(".dependency-badge")).toBeNull();
    expect(screen.getByText("This node will wait for all of them")).toBeTruthy();
    const advanced = advancedDisclosure(container);
    expect(advanced.open).toBe(false);
    expect(advanced.querySelector("summary .dependency-badge")).toBeNull();
    // The operator choice lives inside the closed disclosure.
    expect(advanced.contains(screen.getByText("Decision mode for multiple conditions"))).toBe(true);

    await userEvent.click(screen.getByRole("menuitem", { name: /Add dependency/ }));
    expect(addDependency).toHaveBeenCalledWith("ship");
  });

  it("opens advanced conditions and names the operator when the node uses OR", () => {
    const { container } = nodeMenu({ requires: "design or review", conditionOperator: "or" });
    const advanced = advancedDisclosure(container);
    expect(advanced.open).toBe(true);
    expect(advanced.querySelector("summary .dependency-badge")?.textContent).toBe("Any · OR");
    expect(screen.getByText("design or review")).toBeTruthy();
    expect(screen.getByText(/Evaluated with At least one required/)).toBeTruthy();
  });

  it("opens advanced conditions for an optional prerequisite and badges the row", () => {
    const { container } = nodeMenu({
      requires: "design",
      incoming: [
        { from: "design", to: "ship" },
        { from: "review", to: "ship", relation: "optional" },
      ],
    });
    expect(advancedDisclosure(container).open).toBe(true);
    expect(
      container.querySelector(".dependency-list .dependency-badge")?.textContent,
    ).toBe("Optional");
  });

  it("opens advanced conditions for a flag or nested expression", () => {
    const { container } = nodeMenu({ requires: "design and flag(approved)" });
    const advanced = advancedDisclosure(container);
    expect(advanced.open).toBe(true);
    expect(advanced.querySelector("summary .dependency-badge")?.textContent).toBe(
      "Custom condition",
    );
  });

  it("keeps a standalone gate that manages the node in plain sight", () => {
    const { container } = nodeMenu({
      requires: "design nor review",
      controllingLogicGate: {
        id: "gate-1",
        operator: "nor",
        x: 0,
        y: 0,
        inputs: ["design", "review"],
        output: "ship",
      },
    });
    expect(screen.getByText("Prerequisites managed by a standalone logic gate")).toBeTruthy();
    expect(advancedDisclosure(container).open).toBe(true);
  });

  it("states the first dependency simply", () => {
    nodeMenu({ incoming: [] });
    expect(screen.getByText(/Nothing yet/)).toBeTruthy();
    expect(screen.getByText("This node will wait until it is done")).toBeTruthy();
  });
});

function edgeMenu(edges: GraphEdge[]) {
  const graph = { nodes: [], edges } as unknown as Graph;
  return render(
    <EdgeMenu
      contextMenu={{ kind: "edge", edges: edges.map(({ from, to }) => ({ from, to })), x: 0, y: 0 }}
      graph={graph}
      nodeTitle={nodeTitle}
      setEdgeStyles={vi.fn(async () => {})}
      convertEdgesToLogicGate={vi.fn()}
      setContextMenu={vi.fn()}
    />,
  );
}

describe("EdgeMenu dependencies", () => {
  it("reads a required wire as a plain dependency", () => {
    const { container } = edgeMenu([{ from: "design", to: "ship" }]);
    expect(screen.getByText("Ship depends on Design")).toBeTruthy();
    expect(screen.getByRole("menuitemradio", { name: /Depends on/ }).getAttribute("aria-checked")).toBe("true");
    expect(advancedDisclosure(container).open).toBe(false);
  });

  it("opens advanced conditions for an optional wire", () => {
    const { container } = edgeMenu([{ from: "design", to: "ship", relation: "optional" }]);
    const advanced = advancedDisclosure(container);
    expect(advanced.open).toBe(true);
    expect(advanced.querySelector("summary .dependency-badge")?.textContent).toBe("Optional");
    expect(screen.getByText("Convert to logic gate")).toBeTruthy();
  });

  it("reports a failing style update through the app error store", async () => {
    useApp.setState({ error: null });
    const globalReportError = vi.fn();
    vi.stubGlobal("reportError", globalReportError);
    try {
      const setEdgeStyles = vi.fn(async () => {
        throw new Error("wire save failed");
      });
      const setContextMenu = vi.fn();
      const graph = { nodes: [], edges: [{ from: "design", to: "ship" }] } as unknown as Graph;
      render(
        <EdgeMenu
          contextMenu={{ kind: "edge", edges: [{ from: "design", to: "ship" }], x: 0, y: 0 }}
          graph={graph}
          nodeTitle={nodeTitle}
          setEdgeStyles={setEdgeStyles}
          convertEdgesToLogicGate={vi.fn()}
          setContextMenu={setContextMenu}
        />,
      );

      await userEvent.click(screen.getByRole("menuitemradio", { name: "Dashed" }));
      expect(useApp.getState().error).toBe("wire save failed");
      expect(globalReportError).not.toHaveBeenCalled();
      expect(setContextMenu).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("GraphMenu", () => {
  it("keeps standalone gate types behind the advanced disclosure", () => {
    const { container } = render(
      <GraphMenu
        contextMenu={{ kind: "graph", x: 0, y: 0, col: 0, row: 0 }}
        openNodeCreationMenu={vi.fn()}
        createCanvasGroup={vi.fn()}
        createCanvasAnnotation={vi.fn()}
        createStandaloneLogicGate={vi.fn()}
      />,
    );
    const advanced = advancedDisclosure(container);
    expect(advanced.open).toBe(false);
    expect(advanced.querySelector(".logic-gate-create-grid")).not.toBeNull();
  });
});
