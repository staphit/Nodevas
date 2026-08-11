import { describe, expect, it } from "vitest";
import {
  ApiShapeError,
  verifyGraphResponse,
  verifyNodeResponse,
  verifyProjectsResponse,
  verifyStateResponse,
} from "./verify";

const graphBody = () => ({
  graph: {
    version: 1,
    nodes: [{ id: "alpha", title: "Alpha" }],
    edges: [{ from: "alpha", to: "beta" }],
    ui: { positions: { alpha: { x: 0, y: 0 } } },
  },
  rev: "rev-1",
  statuses: { alpha: "ready" },
  issues: [{ severity: "warning", msg: "beta 不存在" }],
});

const stateBody = () => ({
  state: {
    nodes: { alpha: { status: "done", at: "2026-01-01T00:00:00Z" } },
    history: [{ t: "2026-01-01T00:00:00Z", event: "status" }],
  },
  statuses: { alpha: "done" },
});

const projectsBody = () => ({
  workspace: "/w",
  workspaces: [{ path: "/w", label: "w", active: true, projects: 2 }],
  active: "alpha",
  projects: [
    { name: "alpha", label: "Alpha", depth: 0, path: "/w/alpha", nodes: 3 },
  ],
});

describe("verifyGraphResponse", () => {
  it("passes a well-formed response through unchanged", () => {
    const body = graphBody();
    expect(verifyGraphResponse(body, "/api/graph")).toBe(body);
  });

  it("accepts the nulls Go writes for an empty slice or map", () => {
    const body = { ...graphBody(), statuses: null, issues: null };
    body.graph.nodes = null as never;
    body.graph.edges = null as never;
    expect(() => verifyGraphResponse(body, "/api/graph")).not.toThrow();
  });

  it("names the endpoint and the field when the graph loses a key", () => {
    const body = graphBody();
    delete (body.graph as { version?: number }).version;
    expect(() => verifyGraphResponse(body, "/api/graph")).toThrow(ApiShapeError);
    expect(() => verifyGraphResponse(body, "/api/graph")).toThrow(
      /\/api\/graph.*graph\.version/s,
    );
  });

  it("points at the node that drifted, not just the array", () => {
    const body = graphBody();
    (body.graph.nodes as unknown[])[0] = { nodeId: "alpha" };
    let caught: unknown;
    try {
      verifyGraphResponse(body, "/api/graph");
    } catch (error) {
      caught = error;
    }
    expect((caught as ApiShapeError).field).toBe("graph.nodes[0].id");
    expect((caught as ApiShapeError).endpoint).toBe("/api/graph");
  });

  it("rejects a status map whose values stopped being status names", () => {
    const body = { ...graphBody(), statuses: { alpha: { status: "ready" } } };
    expect(() => verifyGraphResponse(body, "/api/graph")).toThrow(
      /statuses\["alpha"\]/,
    );
  });

  it("rejects a body that is not an object at all", () => {
    expect(() => verifyGraphResponse("nope", "/api/graph")).toThrow(ApiShapeError);
    expect(() => verifyGraphResponse(null, "/api/graph")).toThrow(ApiShapeError);
  });
});

describe("verifyStateResponse", () => {
  it("passes a well-formed run state through", () => {
    const body = stateBody();
    expect(verifyStateResponse(body, "/api/state")).toBe(body);
  });

  it("accepts a run that has no history yet", () => {
    const body = stateBody();
    body.state.history = null as never;
    body.state.nodes = null as never;
    expect(() => verifyStateResponse(body, "/api/state")).not.toThrow();
  });

  it("names the node whose status is missing", () => {
    const body = stateBody();
    body.state.nodes.alpha = {} as never;
    expect(() => verifyStateResponse(body, "/api/state")).toThrow(
      /state\.nodes\["alpha"\]\.status/,
    );
  });

  it("names the history entry whose timestamp changed type", () => {
    const body = stateBody();
    (body.state.history[0] as { t: unknown }).t = 1735689600;
    expect(() => verifyStateResponse(body, "/api/state")).toThrow(
      /state\.history\[0\]\.t/,
    );
  });
});

describe("verifyProjectsResponse", () => {
  it("passes a well-formed project list through", () => {
    const body = projectsBody();
    expect(verifyProjectsResponse(body, "/api/projects")).toBe(body);
  });

  it("names a renamed project field", () => {
    const body = projectsBody();
    const project = body.projects[0] as Record<string, unknown>;
    project.nodeCount = project.nodes;
    delete project.nodes;
    expect(() => verifyProjectsResponse(body, "/api/projects")).toThrow(
      /projects\[0\]\.nodes/,
    );
  });

  it("rejects a missing active project name", () => {
    const body = projectsBody();
    delete (body as { active?: string }).active;
    expect(() => verifyProjectsResponse(body, "/api/projects")).toThrow(/active/);
  });
});

describe("verifyNodeResponse", () => {
  it("passes a well-formed node through", () => {
    const body = { id: "alpha", content: "# Alpha", rev: "rev-1" };
    expect(verifyNodeResponse(body, "/api/nodes/alpha")).toBe(body);
  });

  it("rejects an empty document body that arrived as null", () => {
    expect(() =>
      verifyNodeResponse({ id: "alpha", content: null, rev: "r" }, "/api/nodes/alpha"),
    ).toThrow(/\/api\/nodes\/alpha.*content/s);
  });

  it("rejects a node with no rev, which would break the next save's lock", () => {
    expect(() =>
      verifyNodeResponse({ id: "alpha", content: "" }, "/api/nodes/alpha"),
    ).toThrow(/rev/);
  });
});
