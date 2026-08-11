import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { analyzeGraph, isSettledStatus } from "./analysis";
import type { Graph, LogicGateOperator, Status, StatusDefinition } from "./types";

interface GateTruthCase {
  name: string;
  operator: LogicGateOperator;
  inputs: string[];
  done: string[];
  satisfied: boolean;
  blockedBy: string[];
  edgeRelation?: "optional" | "deprecated";
}

interface RequiresTruthCase {
  name: string;
  requires: string;
  done: string[];
  graphFlags?: Record<string, unknown>;
  runtimeFlags?: Record<string, unknown>;
  valid?: boolean;
  satisfied: boolean;
  blockedBy: string[];
}

const gateTruthTablePath = [
  resolve(process.cwd(), "testdata/logic_gate_truth_table.json"),
  resolve(process.cwd(), "../testdata/logic_gate_truth_table.json"),
].find(existsSync);
if (!gateTruthTablePath) {
  throw new Error("cannot locate testdata/logic_gate_truth_table.json");
}
const gateTruthTable = JSON.parse(
  readFileSync(gateTruthTablePath, "utf8"),
) as { cases: GateTruthCase[] };

const requiresTruthTablePath = [
  resolve(process.cwd(), "testdata/requires_truth_table.json"),
  resolve(process.cwd(), "../testdata/requires_truth_table.json"),
].find(existsSync);
if (!requiresTruthTablePath) {
  throw new Error("cannot locate testdata/requires_truth_table.json");
}
const requiresTruthTable = JSON.parse(
  readFileSync(requiresTruthTablePath, "utf8"),
) as { cases: RequiresTruthCase[] };

const IDEA: StatusDefinition = {
  id: "custom-status-1",
  label: "概念節點",
  color: "#8b7cf6",
  shape: "circle",
};

function chain(customStatuses: StatusDefinition[] = []): Graph {
  return {
    version: 1,
    nodes: [{ id: "a" }, { id: "b", requires: "a" }],
    edges: [{ from: "a", to: "b" }],
    ui: { customStatuses },
  };
}

function materializedGateRequires(
  operator: LogicGateOperator,
  inputs: string[],
): string {
  if (inputs.length === 0 || operator === "optional" || operator === "deprecated") return "";
  if (operator === "must") return inputs[0] ?? "";
  if (operator === "nand") return `not (${inputs.join(" and ")})`;
  if (operator === "nor") return `not (${inputs.join(" or ")})`;
  return inputs.join(` ${operator} `);
}

function analyze(graph: Graph, statuses: Record<string, Status>) {
  return analyzeGraph(graph, statuses, new Date("2026-01-01T00:00:00"));
}

describe("dependency blocking", () => {
  it("treats done and skipped as finished, so they never block", () => {
    for (const status of ["done", "skipped"] as Status[]) {
      const analysis = analyze(chain(), { a: status });
      expect(analysis.blocking.has("a")).toBe(false);
      expect(analysis.blocked.has("b")).toBe(false);
    }
  });

  it("blocks on a custom status by default", () => {
    const analysis = analyze(chain([IDEA]), { a: IDEA.id });
    expect(analysis.blocking.get("a")).toEqual(["b"]);
    expect(analysis.blocked.get("b")).toEqual(["a"]);
  });

  it("stops blocking once the custom status is marked settled", () => {
    const analysis = analyze(chain([{ ...IDEA, settled: true }]), { a: IDEA.id });
    expect(analysis.blocking.has("a")).toBe(false);
    expect(analysis.blocked.has("b")).toBe(false);
  });

  it("does not call a node overdue while it sits in a settled state", () => {
    const graph = chain([{ ...IDEA, settled: true }]);
    graph.ui!.plans = { a: [{ status: "done", date: "2025-01-01" }] };
    expect(analyze(graph, { a: IDEA.id }).overdue.has("a")).toBe(false);
    const blocking = chain([IDEA]);
    blocking.ui!.plans = { a: [{ status: "done", date: "2025-01-01" }] };
    expect(analyze(blocking, { a: IDEA.id }).overdue.has("a")).toBe(true);
  });

  it.each(gateTruthTable.cases)(
    "keeps Go and Web aligned for $name",
    ({ operator, inputs, done, satisfied, blockedBy, edgeRelation }) => {
      const graph: Graph = {
        version: 1,
        nodes: [
          ...inputs.map((id) => ({ id })),
          { id: "target", requires: materializedGateRequires(operator, inputs) },
        ],
        edges: inputs.map((from) => ({
          from,
          to: "target",
          ...(edgeRelation ? { relation: edgeRelation } : {}),
        })),
        ui: {
          logicGates: [
            {
              id: "gate-contract",
              operator,
              inputs,
              output: "target",
              x: 0,
              y: 0,
            },
          ],
        },
      };
      const statuses = Object.fromEntries(done.map((id) => [id, "done"])) as Record<
        string,
        Status
      >;
      const analysis = analyze(graph, statuses);
      if (satisfied) {
        expect(analysis.blocked.has("target")).toBe(false);
      } else {
        expect(analysis.blocked.get("target")).toEqual(blockedBy);
      }
    },
  );

  it("reports a completed-but-false gate as a condition failure", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "target", requires: "a xor b" }],
      edges: [
        { from: "a", to: "target" },
        { from: "b", to: "target" },
      ],
      ui: {
        logicGates: [
          {
            id: "gate-xor",
            operator: "xor",
            inputs: ["a", "b"],
            output: "target",
            x: 0,
            y: 0,
          },
        ],
      },
    };
    const analysis = analyze(graph, {
      a: "done",
      b: "done",
      target: "in_progress",
    });
    expect(analysis.blocked.get("target")).toEqual(["a", "b"]);
    expect(analysis.violations[0]?.message).toContain("XOR 條件不成立");
  });

  it("blocks an unwired gate without inventing a node id", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "target" }],
      edges: [],
      ui: {
        logicGates: [
          {
            id: "gate-empty",
            operator: "and",
            inputs: [],
            output: "target",
            x: 0,
            y: 0,
          },
        ],
      },
    };
    const analysis = analyze(graph, { target: "in_progress" });
    expect(analysis.blocked.has("target")).toBe(true);
    expect(analysis.blocked.get("target")).toEqual([]);
    expect(analysis.blocking.size).toBe(0);
    expect(analysis.violations[0]?.sourceIds).toEqual([]);
    expect(analysis.violations[0]?.message).toContain("AND 尚未接妥");
  });

  it.each(requiresTruthTable.cases)(
    "keeps Go and Web requires evaluation aligned for $name",
    ({ requires, done, graphFlags, runtimeFlags, satisfied, blockedBy }) => {
      const graph: Graph = {
        version: 1,
        nodes: [
          { id: "a" },
          { id: "b" },
          { id: "c" },
          { id: "chapter-2" },
          { id: "target", requires },
        ],
        edges: [],
        flags: graphFlags,
      };
      const statuses = Object.fromEntries(done.map((id) => [id, "done"])) as Record<
        string,
        Status
      >;
      const analysis = analyzeGraph(
        graph,
        statuses,
        new Date("2026-01-01T00:00:00"),
        runtimeFlags,
      );
      if (satisfied) {
        expect(analysis.blocked.has("target")).toBe(false);
      } else {
        expect(analysis.blocked.get("target")).toEqual(blockedBy);
      }
    },
  );

  it("uses requires over a stale required-edge projection in both directions", () => {
    const edgeOnly: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "target" }],
      edges: [{ from: "a", to: "target" }],
    };
    expect(analyze(edgeOnly, {}).blocked.has("target")).toBe(false);

    const requiresOnly: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "target", requires: "a" }],
      edges: [],
    };
    expect(analyze(requiresOnly, {}).blocked.get("target")).toEqual(["a"]);
  });
});

describe("isSettledStatus", () => {
  it("is true for the built-in finished states whatever the project defines", () => {
    expect(isSettledStatus("done")).toBe(true);
    expect(isSettledStatus("skipped")).toBe(true);
  });

  it("is false for states that are still in play", () => {
    expect(isSettledStatus("ready")).toBe(false);
    expect(isSettledStatus("in_progress")).toBe(false);
    expect(isSettledStatus("failed")).toBe(false);
  });

  it("follows the definition for a custom status", () => {
    expect(isSettledStatus(IDEA.id, [IDEA])).toBe(false);
    expect(isSettledStatus(IDEA.id, [{ ...IDEA, settled: true }])).toBe(true);
    // An id with no definition cannot claim to be finished.
    expect(isSettledStatus(IDEA.id, [])).toBe(false);
  });
});

describe("entry nodes", () => {
  it("counts every relation as incoming, deprecated included", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }],
      edges: [
        { from: "a", to: "b" },
        { from: "a", to: "c", relation: "optional" },
        { from: "a", to: "d", relation: "deprecated" },
      ],
    };
    const analysis = analyzeGraph(graph, {});
    // Only `a` is a starting point: the others all have a wire pointing at
    // them, whatever it means.
    expect([...analysis.entryNodeIds].sort()).toEqual(["a"]);
  });

  it("lets a manual override decide, in both directions", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "lonely" }, { id: "a" }, { id: "b" }],
      edges: [{ from: "a", to: "b" }],
      // An isolated node the author says is not a beginning, and a wired one
      // they say is.
      ui: { entryOverrides: { lonely: false, b: true } },
    };
    expect([...analyzeGraph(graph, {}).entryNodeIds].sort()).toEqual(["a", "b"]);
  });

  it("still ignores a deprecated edge when deciding what blocks", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }],
      edges: [{ from: "a", to: "b", relation: "deprecated" }],
    };
    expect(analyzeGraph(graph, {}).blocked.has("b")).toBe(false);
  });
});

describe("deprecated nodes", () => {
  it("fades a node whose only way in is deprecated, and its chain", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }],
      edges: [
        { from: "a", to: "b", relation: "deprecated" },
        { from: "b", to: "c" },
        { from: "a", to: "d" },
      ],
    };
    const analysis = analyzeGraph(graph, {});
    expect([...analysis.deprecatedNodeIds].sort()).toEqual(["b", "c"]);
  });

  it("keeps a node that still has one live way in", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }],
      edges: [
        { from: "a", to: "c", relation: "deprecated" },
        { from: "b", to: "c", relation: "optional" },
      ],
    };
    expect(analyzeGraph(graph, {}).deprecatedNodeIds.size).toBe(0);
  });
});

describe("deprecated status spreads", () => {
  it("fades what only a deprecated node leads to", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }],
      edges: [
        { from: "a", to: "b" },
        { from: "b", to: "c" },
      ],
    };
    const analysis = analyzeGraph(graph, { a: "deprecated" });
    expect([...analysis.deprecatedNodeIds].sort()).toEqual(["a", "b", "c"]);
  });

  it("keeps a node alive when another live parent still feeds it", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }],
      edges: [
        { from: "a", to: "c" },
        { from: "b", to: "c" },
      ],
    };
    const analysis = analyzeGraph(graph, { a: "deprecated" });
    expect(analysis.deprecatedNodeIds.has("c")).toBe(false);
  });

  it("settles on a cycle instead of spinning, and leaves a live way in alone", () => {
    const graph: Graph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }, { id: "e" }],
      edges: [
        // `b` and `c` point at each other, and `a` still feeds the loop.
        { from: "a", to: "b" },
        { from: "b", to: "c" },
        { from: "c", to: "b" },
        // `d` and `e` are the same loop, reachable only through a dead wire.
        { from: "a", to: "d", relation: "deprecated" },
        { from: "d", to: "e" },
        { from: "e", to: "d" },
      ],
    };
    const analysis = analyzeGraph(graph, {});
    expect(analysis.deprecatedNodeIds.has("b")).toBe(false);
    expect(analysis.deprecatedNodeIds.has("c")).toBe(false);
    // The dead loop keeps itself alive by pointing at itself, which is what
    // the previous fixpoint concluded too: neither node ever loses every way in.
    expect(analysis.deprecatedNodeIds.has("d")).toBe(false);
    expect(analysis.deprecatedNodeIds.has("e")).toBe(false);
  });
});
