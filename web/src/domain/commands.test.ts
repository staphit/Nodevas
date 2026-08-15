import { describe, expect, it } from "vitest";
import type { Graph } from "../types";
import { applyGraphCommand, describeGraphCommand } from "./commands";
import { CommandError } from "./errors";
import { milestoneTypeUsage } from "./usage";

function graph(): Graph {
  return {
    version: 1,
    users: [{ id: "user-0001", name: "Ana" }],
    nodes: [
      { id: "a", title: "A" },
      { id: "b", title: "B" },
      { id: "c", title: "C" },
    ],
    edges: [],
    ui: {},
  };
}

describe("node commands", () => {
  it("clears optional fields instead of writing empty strings", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "node.updateMetadata",
      nodeId: "a",
      patch: { title: "  ", deadline: "", tags: ["x", " x ", ""] },
    });
    const node = g.nodes![0];
    expect(node.title).toBeUndefined();
    expect(node.deadline).toBeUndefined();
    expect(node.tags).toEqual(["x"]);
  });

  it("stores an entry override and drops it again when set back to automatic", () => {
    const g = graph();
    applyGraphCommand(g, { type: "node.setEntryOverride", nodeId: "a", value: false });
    expect(g.ui!.entryOverrides).toEqual({ a: false });

    applyGraphCommand(g, { type: "node.setEntryOverride", nodeId: "a", value: undefined });
    // Nothing left to say, so nothing is written into graph.yaml.
    expect(g.ui!.entryOverrides).toBeUndefined();
  });

  it("refuses an entry override for a node that is not there", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, { type: "node.setEntryOverride", nodeId: "ghost", value: true }),
    ).toThrow(CommandError);
  });

  it("rejects a malformed deadline", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, {
        type: "node.updateMetadata",
        nodeId: "a",
        patch: { deadline: "2026/07/30" },
      }),
    ).toThrow(CommandError);
  });

  it("reports a missing node instead of silently doing nothing", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, {
        type: "node.updateMetadata",
        nodeId: "ghost",
        patch: { title: "x" },
      }),
    ).toThrow(/ghost/);
  });

  it("creates a project user on first assignment by name", () => {
    const g = graph();
    applyGraphCommand(g, { type: "node.assignByName", nodeId: "a", name: "Bo" });
    expect(g.users).toHaveLength(2);
    expect(g.nodes![0].assignee).toBe("user-0002");

    // Same name again reuses the user rather than duplicating it.
    applyGraphCommand(g, { type: "node.assignByName", nodeId: "b", name: "bo" });
    expect(g.users).toHaveLength(2);
  });

  it("syncs incoming edges with the parsed refs of a requires expression", () => {
    const g = graph();
    g.edges = [{ from: "c", to: "a" }];
    applyGraphCommand(g, {
      type: "node.setRequires",
      nodeId: "a",
      requires: "b",
      refs: ["b"],
    });
    expect(g.nodes![0].requires).toBe("b");
    expect(g.edges).toEqual([{ from: "b", to: "a" }]);
  });

  it("appends a dependency to the requires expression and records the edge", () => {
    const g = graph();
    applyGraphCommand(g, { type: "node.addDependency", sourceId: "b", targetId: "a" });
    applyGraphCommand(g, { type: "node.addDependency", sourceId: "c", targetId: "a" });
    expect(g.nodes![0].requires).toBe("b and c");
    expect(g.edges).toEqual([
      { from: "b", to: "a" },
      { from: "c", to: "a" },
    ]);
  });

  it("parenthesises when the operator changes so precedence cannot shift", () => {
    const g = graph();
    g.nodes![0].requires = "b and c";
    applyGraphCommand(g, {
      type: "node.addDependency",
      sourceId: "c",
      targetId: "a",
      operator: "or",
    });
    expect(g.nodes![0].requires).toBe("(b and c) or c");
  });

  it("marks an optional dependency without blocking the target", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "node.addDependency",
      sourceId: "b",
      targetId: "a",
      relation: "optional",
    });
    expect(g.nodes![0].requires).toBeFalsy();
    expect(g.edges).toEqual([{ from: "b", to: "a", relation: "optional" }]);
  });

  it("preserves relation edges while rebuilding the required projection", () => {
    const g = graph();
    g.edges = [
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "deprecated" },
    ];
    applyGraphCommand(g, {
      type: "node.setRequires",
      nodeId: "a",
      requires: "b",
      refs: ["b"],
    });
    expect(g.nodes![0].requires).toBe("b");
    expect(g.edges).toEqual([
      { from: "b", to: "a" },
      { from: "c", to: "a", relation: "deprecated" },
    ]);
  });

  it("refuses self-links, duplicates and cycles", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, { type: "node.addDependency", sourceId: "a", targetId: "a" }),
    ).toThrow(/自身/);

    applyGraphCommand(g, { type: "node.addDependency", sourceId: "b", targetId: "a" });
    expect(() =>
      applyGraphCommand(g, { type: "node.addDependency", sourceId: "b", targetId: "a" }),
    ).toThrow(/已存在/);
    expect(() =>
      applyGraphCommand(g, { type: "node.addDependency", sourceId: "a", targetId: "b" }),
    ).toThrow(/循環/);
  });

  it("keeps existing edges when the expression could not be parsed", () => {
    const g = graph();
    g.edges = [{ from: "c", to: "a" }];
    applyGraphCommand(g, {
      type: "node.setRequires",
      nodeId: "a",
      requires: "b and",
      refs: null,
    });
    expect(g.edges).toEqual([{ from: "c", to: "a" }]);
  });
});

describe("plan commands", () => {
  it("keeps one milestone per type and sorts by date then time", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "done", date: "2026-08-10" },
    });
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "started", date: "2026-08-01", time: "09:00" },
    });
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "done", date: "2026-08-05", note: " 提前 " },
    });

    expect(g.ui!.plans!.a).toEqual([
      { status: "started", date: "2026-08-01", time: "09:00" },
      { status: "done", date: "2026-08-05", note: "提前" },
    ]);
  });

  it("rejects a malformed date", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, {
        type: "plan.upsert",
        nodeId: "a",
        milestone: { type: "done", date: "8/5" },
      }),
    ).toThrow(CommandError);
  });

  it("moves a milestone while keeping its note", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "done", date: "2026-08-10", note: "交付" },
    });
    applyGraphCommand(g, {
      type: "plan.move",
      nodeId: "a",
      milestoneType: "done",
      date: "2026-08-12",
    });
    expect(g.ui!.plans!.a).toEqual([
      { status: "done", date: "2026-08-12", note: "交付" },
    ]);
  });

  it("drops the node entry once its last milestone is removed", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "done", date: "2026-08-10" },
    });
    applyGraphCommand(g, { type: "plan.remove", nodeId: "a", milestoneType: "done" });
    expect(g.ui!.plans!.a).toBeUndefined();
  });
});

describe("workflow commands", () => {
  it("assigns ids and refuses duplicate labels", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "workflow.addLifecycleStatus",
      definition: { label: "審核中", color: "#888", shape: "circle" },
    });
    expect(g.ui!.customStatuses).toEqual([
      { id: "custom-status-1", label: "審核中", color: "#888", shape: "circle" },
    ]);
    expect(() =>
      applyGraphCommand(g, {
        type: "workflow.addLifecycleStatus",
        definition: { label: "審核中", color: "#111", shape: "square" },
      }),
    ).toThrow(/審核中/);
  });

  it("keeps scheduled milestones when a type is deleted unless asked", () => {
    const g = graph();
    applyGraphCommand(g, { type: "workflow.addMilestoneType", label: "內審" });
    applyGraphCommand(g, {
      type: "plan.upsert",
      nodeId: "a",
      milestone: { type: "custom-1", date: "2026-08-01" },
    });
    expect(milestoneTypeUsage(g, "custom-1")).toEqual({ nodes: ["a"], milestones: 1 });

    applyGraphCommand(g, { type: "workflow.removeMilestoneType", id: "custom-1" });
    expect(g.ui!.planStatuses).toEqual([]);
    expect(g.ui!.plans!.a).toHaveLength(1);

    applyGraphCommand(g, {
      type: "workflow.removeMilestoneType",
      id: "custom-1",
      removeScheduled: true,
    });
    expect(g.ui!.plans!.a).toBeUndefined();
  });
});

describe("canvas commands", () => {
  it("removes an edge together with everything keyed to it", () => {
    const g = graph();
    g.edges = [{ from: "b", to: "a" }];
    g.ui = {
      edgeLabels: { "b->a": { ratio: 0.5 } },
      wireVertices: { "b->a": [{ x: 1, y: 2 }], "gate:a": [{ x: 3, y: 4 }] },
      gates: { a: { x: 1, y: 1 } },
    };
    applyGraphCommand(g, { type: "canvas.removeEdge", from: "b", to: "a" });
    expect(g.edges).toEqual([]);
    expect(g.ui!.edgeLabels).toEqual({});
    expect(g.ui!.wireVertices).toEqual({});
    expect(g.ui!.gates).toEqual({});
  });

  it("wires a logic gate onto the requires of its output node", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "canvas.createLogicGate",
      operator: "and",
      x: 0,
      y: 0,
    });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, {
      type: "canvas.setLogicGateOutput",
      gateId,
      targetId: "a",
    });
    applyGraphCommand(g, {
      type: "canvas.connectLogicGateInput",
      gateId,
      sourceId: "b",
    });
    // One input is not enough for AND: nothing is applied yet.
    expect(g.nodes![0].requires).toBe("");

    applyGraphCommand(g, {
      type: "canvas.connectLogicGateInput",
      gateId,
      sourceId: "c",
    });
    expect(g.nodes![0].requires).toBe("b and c");
    expect(g.edges).toEqual([
      { from: "b", to: "a" },
      { from: "c", to: "a" },
    ]);

    // Removing the gate takes its expression and edges with it.
    applyGraphCommand(g, { type: "canvas.removeLogicGate", gateId });
    expect(g.nodes![0].requires).toBe("");
    expect(g.edges).toEqual([]);
    expect(g.ui!.logicGates).toEqual([]);
  });

  it("requires gate-owned dependencies to be edited through their gate", () => {
    const g = graph();
    applyGraphCommand(g, { type: "canvas.createLogicGate", operator: "must", x: 0, y: 0 });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.setLogicGateOutput", gateId, targetId: "a" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });

    expect(() =>
      applyGraphCommand(g, { type: "canvas.removeEdge", from: "b", to: "a" }),
    ).toThrow(/gate-1/);
    expect(() =>
      applyGraphCommand(g, {
        type: "canvas.setEdgeStyle",
        edges: [{ from: "b", to: "a" }],
        relation: "optional",
      }),
    ).toThrow(/gate-1/);
    expect(() =>
      applyGraphCommand(g, {
        type: "node.setRequires",
        nodeId: "a",
        requires: "c",
        refs: ["c"],
      }),
    ).toThrow(/gate-1/);
    expect(g.nodes![0].requires).toBe("b");
    expect(g.edges).toEqual([{ from: "b", to: "a" }]);
  });

  it("protects optional relation-gate edges from direct deletion", () => {
    const g = graph();
    applyGraphCommand(g, { type: "canvas.createLogicGate", operator: "optional", x: 0, y: 0 });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.setLogicGateOutput", gateId, targetId: "a" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });
    expect(() =>
      applyGraphCommand(g, { type: "canvas.removeEdge", from: "b", to: "a" }),
    ).toThrow(/gate-1/);
    expect(g.nodes![0].requires).toBeFalsy();
    expect(g.edges).toEqual([{ from: "b", to: "a", relation: "optional" }]);
  });

  it("marks its edges instead of writing a condition for an optional gate", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "canvas.createLogicGate",
      operator: "optional",
      x: 0,
      y: 0,
    });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.setLogicGateOutput", gateId, targetId: "a" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });

    // One input is enough for a relation gate, and it leaves `requires` alone.
    expect(g.nodes![0].requires).toBeFalsy();
    expect(g.edges).toEqual([{ from: "b", to: "a", relation: "optional" }]);

    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "c" });
    expect(g.edges).toEqual([
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "optional" },
    ]);

    applyGraphCommand(g, { type: "canvas.disconnectLogicGateInput", gateId, sourceId: "c" });
    expect(g.edges).toEqual([{ from: "b", to: "a", relation: "optional" }]);

    applyGraphCommand(g, { type: "canvas.removeLogicGate", gateId });
    expect(g.edges).toEqual([]);
  });

  it("marks every input towards every output of a relation gate", () => {
    const g = graph();
    g.nodes!.push({ id: "d", title: "D" });
    applyGraphCommand(g, { type: "canvas.createLogicGate", operator: "optional", x: 0, y: 0 });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "a" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });
    applyGraphCommand(g, {
      type: "canvas.toggleLogicGateOutput",
      gateId,
      targetId: "c",
      enabled: true,
    });
    applyGraphCommand(g, {
      type: "canvas.toggleLogicGateOutput",
      gateId,
      targetId: "d",
      enabled: true,
    });
    expect(g.ui!.logicGates![0].outputs).toEqual(["c", "d"]);
    expect(g.edges).toEqual([
      { from: "a", to: "c", relation: "optional" },
      { from: "b", to: "c", relation: "optional" },
      { from: "a", to: "d", relation: "optional" },
      { from: "b", to: "d", relation: "optional" },
    ]);

    // Dropping one output takes only that column of edges with it.
    applyGraphCommand(g, {
      type: "canvas.toggleLogicGateOutput",
      gateId,
      targetId: "c",
      enabled: false,
    });
    expect(g.ui!.logicGates![0].outputs).toEqual(["d"]);
    expect(g.edges).toEqual([
      { from: "a", to: "d", relation: "optional" },
      { from: "b", to: "d", relation: "optional" },
    ]);
  });

  it("turns existing optional edges into the gate that owns them", () => {
    const g = graph();
    g.edges = [
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "optional" },
    ];
    applyGraphCommand(g, {
      type: "canvas.convertEdgesToLogicGate",
      edges: [
        { from: "b", to: "a" },
        { from: "c", to: "a" },
      ],
      x: 10,
      y: 20,
    });
    const gate = g.ui!.logicGates![0];
    expect(gate.operator).toBe("optional");
    expect(gate.inputs).toEqual(["b", "c"]);
    expect(gate.outputs).toEqual(["a"]);
    // The edges are the same ones, restyled in place rather than recreated.
    expect(g.edges).toEqual([
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "optional" },
    ]);
  });

  it("takes in the unselected edges of the same relation instead of dropping them", () => {
    const g = graph();
    g.edges = [
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "optional" },
    ];
    // Only one of the two wires was picked; the other one reaches the same
    // target, so the gate absorbs it rather than deleting it.
    applyGraphCommand(g, {
      type: "canvas.convertEdgesToLogicGate",
      edges: [{ from: "b", to: "a" }],
      x: 0,
      y: 0,
    });
    expect(g.ui!.logicGates![0].inputs).toEqual(["b", "c"]);
    expect(g.edges).toHaveLength(2);
  });

  it("refuses to convert required edges or a node another gate owns", () => {
    const g = graph();
    g.nodes!.push({ id: "d", title: "D" });
    g.edges = [{ from: "b", to: "a" }];
    expect(() =>
      applyGraphCommand(g, {
        type: "canvas.convertEdgesToLogicGate",
        edges: [{ from: "b", to: "a" }],
        x: 0,
        y: 0,
      }),
    ).toThrow(/AND/);

    g.edges = [{ from: "b", to: "d", relation: "optional" }];
    applyGraphCommand(g, {
      type: "canvas.convertEdgesToLogicGate",
      edges: [{ from: "b", to: "d" }],
      x: 0,
      y: 0,
    });
    // A second gate cannot take over a node the first one already drives.
    g.edges!.push({ from: "c", to: "d", relation: "deprecated" });
    expect(() =>
      applyGraphCommand(g, {
        type: "canvas.convertEdgesToLogicGate",
        edges: [{ from: "c", to: "d" }],
        x: 0,
        y: 0,
      }),
    ).toThrow(/gate-1/);
  });

  it("undoes the old shape when a gate switches between operator kinds", () => {
    const g = graph();
    applyGraphCommand(g, { type: "canvas.createLogicGate", operator: "and", x: 0, y: 0 });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.setLogicGateOutput", gateId, targetId: "a" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "c" });
    expect(g.nodes![0].requires).toBe("b and c");

    applyGraphCommand(g, {
      type: "canvas.setLogicGateOperator",
      gateId,
      operator: "optional",
    });
    // The AND expression goes with the AND; only the marked edges remain.
    expect(g.nodes![0].requires).toBe("");
    expect(g.edges).toEqual([
      { from: "b", to: "a", relation: "optional" },
      { from: "c", to: "a", relation: "optional" },
    ]);

    applyGraphCommand(g, { type: "canvas.setLogicGateOperator", gateId, operator: "and" });
    expect(g.nodes![0].requires).toBe("b and c");
    expect(g.edges).toEqual([
      { from: "b", to: "a" },
      { from: "c", to: "a" },
    ]);
  });

  it("refuses a second input on a MUST gate", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "canvas.createLogicGate",
      operator: "must",
      x: 0,
      y: 0,
    });
    const gateId = g.ui!.logicGates![0].id;
    applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "b" });
    expect(() =>
      applyGraphCommand(g, { type: "canvas.connectLogicGateInput", gateId, sourceId: "c" }),
    ).toThrow(/MUST/);
  });
});

describe("bulk import", () => {
  it("writes placement, metadata and dependencies in one command", () => {
    const g = graph();
    g.users = [{ id: "user-0001", name: "Ana" }];
    applyGraphCommand(g, {
      type: "graph.applyImport",
      payload: {
        nodes: [
          {
            id: "a",
            position: { x: 2, y: 1 },
            style: { width: 240, color: "#123456" },
            priority: "high",
            assignee: "user-0001",
            tags: ["匯入"],
          },
          { id: "b", position: { x: -5, y: 3 }, assignee: "ghost" },
        ],
        edges: [
          { from: "a", to: "b" },
          { from: "c", to: "b", relation: "optional" },
        ],
      },
    });

    expect(g.ui!.positions).toEqual({ a: { x: 2, y: 1 }, b: { x: 0, y: 3 } });
    expect(g.ui!.nodeStyles).toEqual({ a: { width: 240, color: "#123456" } });
    expect(g.nodes![0]).toMatchObject({ priority: "high", assignee: "user-0001" });
    // An assignee the project does not know is dropped rather than dangling.
    expect(g.nodes![1].assignee).toBeUndefined();
    // Required edges become the condition; optional ones stay decorative.
    expect(g.nodes![1].requires).toBe("a");
    expect(g.edges).toEqual([
      { from: "a", to: "b" },
      { from: "c", to: "b", relation: "optional" },
    ]);
  });

  it("refuses an import larger than the limit", () => {
    const g = graph();
    const nodes = Array.from({ length: 1001 }, (_, index) => ({
      id: `n${index}`,
      position: { x: 0, y: index },
    }));
    expect(() =>
      applyGraphCommand(g, { type: "graph.applyImport", payload: { nodes, edges: [] } }),
    ).toThrow(/1000/);
  });
});

describe("labels", () => {
  it("names every command for the undo stack", () => {
    expect(describeGraphCommand({ type: "plan.remove", nodeId: "a", milestoneType: "done" })).toBe(
      "刪除預期里程碑",
    );
  });
});

describe("wire decoration cleanup", () => {
  it("drops bend points and labels of edges a rewrite removed", () => {
    const g = graph();
    applyGraphCommand(g, { type: "node.addDependency", sourceId: "b", targetId: "a" });
    applyGraphCommand(g, {
      type: "canvas.setWireVertices",
      wireKey: "b->a",
      vertices: [{ x: 10, y: 20 }],
    });
    expect(g.ui!.wireVertices!["b->a"]).toHaveLength(1);

    // Clearing the expression drops the edge; its placements go with it.
    applyGraphCommand(g, {
      type: "node.setRequires",
      nodeId: "a",
      requires: "",
      refs: [],
    });
    expect(g.edges ?? []).toEqual([]);
    expect(g.ui!.wireVertices?.["b->a"]).toBeUndefined();
  });
});
