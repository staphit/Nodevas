import { describe, expect, it } from "vitest";
import { ApiShapeError, verifyGraphResponse } from "./verify";

const body = (node: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({
  graph: {
    version: 1,
    nodes: [{ id: "alpha", title: "Alpha", ...node }],
    edges: [],
    ...extra,
  },
  rev: "rev-1",
  statuses: { alpha: "ready" },
  issues: [],
});

describe("verifyGraphResponse with agent roles", () => {
  it("tolerates roles, workflow stages and acceptance", () => {
    const ok = body(
      {
        acceptance: { items: [{ id: "a1", text: "returns 200" }], locked: true, by: "planner (run abc)" },
        workflow: { preset: "full", stages: { planner: true, goalQa: true, tester: true, reviewer: true, ci: true, goalReview: true } },
      },
      {
        agentRoles: [{ id: "executor", tools: ["get_node"] }],
        workflow: { preset: "standard", stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false } },
      },
    );
    expect(verifyGraphResponse(ok, "/api/graph")).toBe(ok);
    expect(() => verifyGraphResponse(body({ acceptance: null }, { workflow: null, agentRoles: null }), "/api/graph")).not.toThrow();
  });

  it("names a malformed acceptance list", () => {
    expect(() => verifyGraphResponse(body({ acceptance: { items: "a1" } }), "/api/graph")).toThrow(ApiShapeError);
    expect(() => verifyGraphResponse(body({ acceptance: { items: [{ id: 1, text: "x" }] } }), "/api/graph")).toThrow(
      /graph\.nodes\[0\]\.acceptance\.items\[0\]\.id/,
    );
  });

  it("names a malformed workflow", () => {
    expect(() => verifyGraphResponse(body({}, { workflow: { preset: 3 } }), "/api/graph")).toThrow(/graph\.workflow\.preset/);
    expect(() => verifyGraphResponse(body({}, { agentRoles: [{ tools: [] }] }), "/api/graph")).toThrow(/graph\.agentRoles\[0\]\.id/);
  });
});
