import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { Graph, NodeType, NodeTypeDef } from "../../types";
import { applyGraphCommand } from "../commands";
import {
  addNodeType,
  BUILTIN_NODE_TYPES,
  isKnownKind,
  MAX_CUSTOM_NODE_TYPES,
  nextCustomNodeTypeID,
  nodeTypeOf,
  nodeTypesForLayout,
  nodeTypeUsage,
  normalizeKind,
  removeNodeType,
  resolveNodeTypes,
  typeOrderForLayout,
  updateNodeType,
  validateNodeTypeDefs,
} from "./nodeTypes";

const fixturePath = [
  resolve(process.cwd(), "testdata/node_types_builtin.json"),
  resolve(process.cwd(), "../testdata/node_types_builtin.json"),
].find(existsSync);
if (!fixturePath) throw new Error("cannot locate testdata/node_types_builtin.json");
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as { types: NodeType[] };

const FORESHADOW: NodeTypeDef = {
  id: "custom-type-1",
  label: "伏筆",
  shape: "diamond",
  color: "#c9a7ff",
  agentMode: "none",
  completion: "none",
  countsInProgress: false,
};

function graph(nodeTypes?: NodeTypeDef[]): Graph {
  return {
    version: 1,
    nodes: [
      { id: "a", kind: "custom-type-1" },
      { id: "b", kind: "gate" },
      { id: "c" },
      { id: "d", kind: "bug" },
      { id: "e", kind: "custom-type-1" },
    ],
    edges: [],
    ...(nodeTypes ? { nodeTypes } : {}),
  };
}

describe("built-in node types", () => {
  it("match the shared fixture the Go engine reads", () => {
    expect(BUILTIN_NODE_TYPES).toEqual(fixture.types);
  });

  it("list in each layout's order, custom types last", () => {
    expect(typeOrderForLayout("story").slice(0, 2)).toEqual(["scene", "event"]);
    expect(typeOrderForLayout(undefined)[0]).toBe("task");
    const story = nodeTypesForLayout({ ...graph([FORESHADOW]), type: "story" });
    expect(story.map((type) => type.id)).toEqual([
      "scene",
      "event",
      "goal",
      "choice",
      "gate",
      "start",
      "end",
      "note",
      "task",
      "custom-type-1",
    ]);
  });
});

describe("resolveNodeTypes", () => {
  it("applies built-in overrides and keeps custom types in stored order", () => {
    const second: NodeTypeDef = { ...FORESHADOW, id: "custom-type-2", label: "角色" };
    const resolved = resolveNodeTypes(
      graph([second, { id: "gate", shape: "round", color: "#ff9f9f" }, FORESHADOW]),
    );
    const gate = resolved.find((type) => type.id === "gate")!;
    expect(gate).toMatchObject({ shape: "round", color: "#ff9f9f", agentMode: "none", builtin: true });
    expect(resolved.filter((type) => !type.builtin).map((type) => type.label)).toEqual([
      "角色",
      "伏筆",
    ]);
  });

  it("memoizes per nodeTypes array", () => {
    const g = graph([FORESHADOW]);
    expect(resolveNodeTypes(g)).toBe(resolveNodeTypes(g));
  });
});

describe("nodeTypeOf", () => {
  it("reads empty, legacy and undefined custom kinds as Task", () => {
    const g = graph([FORESHADOW]);
    expect(nodeTypeOf(g, { kind: "" }).id).toBe("task");
    expect(nodeTypeOf(g, {}).id).toBe("task");
    expect(nodeTypeOf(g, { kind: "bug" }).id).toBe("task");
    expect(nodeTypeOf(g, { kind: "custom-type-9" }).id).toBe("task");
    expect(nodeTypeOf(g, { kind: "custom-type-1" }).label).toBe("伏筆");
    expect(nodeTypeOf(g, { kind: "review" }).id).toBe("gate");
  });

  it("knows which kinds a project defines", () => {
    const g = graph([FORESHADOW]);
    expect(isKnownKind(g, "")).toBe(true);
    expect(isKnownKind(g, "note")).toBe(true);
    expect(isKnownKind(g, "custom-type-1")).toBe(true);
    expect(isKnownKind(g, "custom-type-2")).toBe(false);
    expect(isKnownKind(g, "bug")).toBe(false);
  });
});

describe("normalizeKind", () => {
  it("trims, lower-cases and maps the aliases", () => {
    expect(normalizeKind(" Review ")).toBe("gate");
    expect(normalizeKind("decision")).toBe("choice");
    expect(normalizeKind("")).toBe("");
    expect(normalizeKind(undefined)).toBe("");
    expect(normalizeKind("custom-type-AB")).toBe("custom-type-AB");
  });
});

describe("validateNodeTypeDefs", () => {
  const cases: [string, NodeTypeDef[]][] = [
    ["bad prefix", [{ ...FORESHADOW, id: "type-1" }]],
    ["built-in carrying agentMode", [{ id: "gate", agentMode: "dispatch" }]],
    ["duplicate label", [FORESHADOW, { ...FORESHADOW, id: "custom-type-2", label: "伏筆" }]],
    ["none with dispatch", [{ ...FORESHADOW, agentMode: "dispatch" }]],
    [
      "autopilot with auto",
      [{ ...FORESHADOW, agentMode: "autopilot", completion: "auto", countsInProgress: true }],
    ],
    ["nil counts", [{ ...FORESHADOW, countsInProgress: undefined }]],
    [
      "65 customs",
      Array.from({ length: MAX_CUSTOM_NODE_TYPES + 1 }, (_, index) => ({
        ...FORESHADOW,
        id: `custom-type-${index + 1}`,
        label: `T${index + 1}`,
      })),
    ],
    ["bad shape", [{ ...FORESHADOW, shape: "star" as never }]],
    ["url( colour", [{ ...FORESHADOW, color: "url(https://x)" }]],
  ];
  for (const [name, defs] of cases) {
    it(`refuses ${name}`, () => {
      expect(() => validateNodeTypeDefs(defs)).toThrow();
    });
  }

  it("accepts a sparse list", () => {
    expect(() =>
      validateNodeTypeDefs([{ id: "gate", shape: "round", color: "#ff9f9f" }, FORESHADOW]),
    ).not.toThrow();
  });
});

describe("node type reducers", () => {
  it("adds a custom type with every behaviour field, false included", () => {
    const g = graph();
    const id = addNodeType(g, {
      label: "  角色 ",
      shape: "dashed",
      agentMode: "none",
      completion: "none",
      countsInProgress: true,
    });
    expect(id).toBe("custom-type-1");
    // No status means not counted, whatever was asked.
    expect(g.nodeTypes).toEqual([
      {
        id: "custom-type-1",
        label: "角色",
        shape: "dashed",
        agentMode: "none",
        completion: "none",
        countsInProgress: false,
      },
    ]);
    expect(nextCustomNodeTypeID(g.nodeTypes!)).toBe("custom-type-2");
  });

  it("refuses a duplicate name", () => {
    const g = graph([FORESHADOW]);
    expect(() =>
      addNodeType(g, { label: "伏筆", agentMode: "none", completion: "anyone", countsInProgress: true }),
    ).toThrow("伏筆");
  });

  it("lets a built-in change only its shape and colour", () => {
    const g = graph();
    updateNodeType(g, "gate", { shape: "round" });
    expect(g.nodeTypes).toEqual([{ id: "gate", shape: "round" }]);
    expect(() => updateNodeType(g, "gate", { agentMode: "dispatch" })).toThrow();
    expect(() => updateNodeType(g, "task", { label: "Job" })).toThrow();
    // Back to the defaults: the override disappears.
    updateNodeType(g, "gate", { shape: undefined });
    expect(g.nodeTypes).toBeUndefined();
  });

  it("forces a type with no status out of agent work and progress", () => {
    const g = graph([{ ...FORESHADOW, agentMode: "dispatch", completion: "anyone", countsInProgress: true }]);
    updateNodeType(g, "custom-type-1", { completion: "none" });
    expect(g.nodeTypes?.[0]).toMatchObject({
      agentMode: "none",
      completion: "none",
      countsInProgress: false,
    });
  });

  it("takes an automatic type out of Autopilot", () => {
    const g = graph([{ ...FORESHADOW, completion: "anyone", countsInProgress: true }]);
    updateNodeType(g, "custom-type-1", { agentMode: "autopilot" });
    expect(() => updateNodeType(g, "custom-type-1", { completion: "auto" })).not.toThrow();
    // auto forces agentMode none, so the pair stays consistent.
    expect(g.nodeTypes?.[0]).toMatchObject({ agentMode: "none", completion: "auto" });
  });

  it("removes a custom type and turns its nodes into Tasks", () => {
    const g = graph([FORESHADOW]);
    expect(nodeTypeUsage(g, "custom-type-1")).toEqual(["a", "e"]);
    removeNodeType(g, "custom-type-1");
    expect(g.nodeTypes).toBeUndefined();
    expect(g.nodes?.find((node) => node.id === "a")?.kind).toBe("task");
    expect(g.nodes?.find((node) => node.id === "d")?.kind).toBe("bug");
  });

  it("never removes a built-in", () => {
    expect(() => removeNodeType(graph(), "gate")).toThrow();
  });

  it("counts '' as task in usage", () => {
    expect(nodeTypeUsage(graph(), "task")).toEqual(["c"]);
  });

  it("runs through the workflow commands", () => {
    const g = graph();
    applyGraphCommand(g, {
      type: "workflow.addNodeType",
      definition: { label: "角色", agentMode: "none", completion: "none", countsInProgress: false },
    });
    applyGraphCommand(g, {
      type: "workflow.updateNodeType",
      id: "custom-type-1",
      patch: { color: "#c9a7ff" },
    });
    expect(g.nodeTypes?.[0]).toMatchObject({ label: "角色", color: "#c9a7ff" });
    applyGraphCommand(g, { type: "workflow.removeNodeType", id: "custom-type-1" });
    expect(g.nodeTypes).toBeUndefined();
  });
});

describe("node.updateMetadata kind", () => {
  it("stores the canonical id for an alias and refuses an unknown type", () => {
    const g = graph([FORESHADOW]);
    applyGraphCommand(g, { type: "node.updateMetadata", nodeId: "c", patch: { kind: "review" } });
    expect(g.nodes?.find((node) => node.id === "c")?.kind).toBe("gate");
    applyGraphCommand(g, {
      type: "node.updateMetadata",
      nodeId: "c",
      patch: { kind: "custom-type-1" },
    });
    expect(g.nodes?.find((node) => node.id === "c")?.kind).toBe("custom-type-1");
    expect(() =>
      applyGraphCommand(g, {
        type: "node.updateMetadata",
        nodeId: "c",
        patch: { kind: "custom-type-9" },
      }),
    ).toThrow("Unknown node type");
  });

  it("lets a legacy kind be written back unchanged", () => {
    const g = graph();
    expect(() =>
      applyGraphCommand(g, { type: "node.updateMetadata", nodeId: "d", patch: { kind: "bug" } }),
    ).not.toThrow();
  });
});
