import type { Graph, Issue, RunState, Status } from "../types";

/**
 * Hand-written response checks for the handful of payloads the whole app is
 * built on.
 *
 * The Go structs and `types.ts` are kept in step by hand — there is no codegen,
 * and nothing fails a build when they drift. Without a check here a renamed
 * backend field arrives as `undefined` and surfaces as a blank card or a crash
 * three layers into a component, with nothing naming the field that moved. A
 * schema library would do this too, but the shapes worth guarding are these
 * four and a dependency is a worse trade than eighty lines.
 *
 * Deliberately shallow: it asserts the fields the app indexes into, not every
 * optional one. An unknown extra field is fine — the server is allowed to grow.
 */

/** Thrown when a response does not have the shape the client compiles against. */
export class ApiShapeError extends Error {
  endpoint: string;
  field: string;
  constructor(endpoint: string, field: string, expected: string, actual: string) {
    super(`${endpoint} 回應格式不符：${field} 應為 ${expected}，實際為 ${actual}`);
    this.name = "ApiShapeError";
    this.endpoint = endpoint;
    this.field = field;
  }
}

export type Verifier<T> = (value: unknown, endpoint: string) => T;

function describe(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "缺少";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

/** Carries the endpoint so every failure can name it without threading it. */
class Check {
  constructor(private readonly endpoint: string) {}

  fail(field: string, expected: string, value: unknown): never {
    throw new ApiShapeError(this.endpoint, field, expected, describe(value));
  }

  object(value: unknown, field: string): Record<string, unknown> {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      this.fail(field, "object", value);
    }
    return value as Record<string, unknown>;
  }

  string(value: unknown, field: string): string {
    if (typeof value !== "string") this.fail(field, "string", value);
    return value as string;
  }

  number(value: unknown, field: string): number {
    if (typeof value !== "number" || Number.isNaN(value)) {
      this.fail(field, "number", value);
    }
    return value as number;
  }

  array(value: unknown, field: string): unknown[] {
    if (!Array.isArray(value)) this.fail(field, "array", value);
    return value as unknown[];
  }

  /** Go marshals a nil slice as `null`, so an empty list is legitimately absent. */
  nullableArray(value: unknown, field: string): unknown[] {
    if (value === null || value === undefined) return [];
    return this.array(value, field);
  }

  /** Same for a nil map. */
  nullableObject(value: unknown, field: string): Record<string, unknown> {
    if (value === null || value === undefined) return {};
    return this.object(value, field);
  }

  optionalString(value: unknown, field: string): void {
    if (value === undefined || value === null) return;
    this.string(value, field);
  }
}

/** Every value of a `Record<string, Status>` must be a status name. */
function checkStatusMap(check: Check, value: unknown, field: string): void {
  const map = check.nullableObject(value, field);
  for (const [id, status] of Object.entries(map)) {
    check.string(status, `${field}["${id}"]`);
  }
}

function checkGraph(check: Check, value: unknown, field: string): void {
  const graph = check.object(value, field);
  check.number(graph.version, `${field}.version`);
  const nodes = check.nullableArray(graph.nodes, `${field}.nodes`);
  nodes.forEach((entry, index) => {
    const node = check.object(entry, `${field}.nodes[${index}]`);
    check.string(node.id, `${field}.nodes[${index}].id`);
    check.optionalString(node.title, `${field}.nodes[${index}].title`);
  });
  const edges = check.nullableArray(graph.edges, `${field}.edges`);
  edges.forEach((entry, index) => {
    const edge = check.object(entry, `${field}.edges[${index}]`);
    check.string(edge.from, `${field}.edges[${index}].from`);
    check.string(edge.to, `${field}.edges[${index}].to`);
  });
  if (graph.ui !== undefined && graph.ui !== null) {
    check.object(graph.ui, `${field}.ui`);
  }
}

function checkIssues(check: Check, value: unknown, field: string): void {
  const issues = check.nullableArray(value, field);
  issues.forEach((entry, index) => {
    const issue = check.object(entry, `${field}[${index}]`);
    check.string(issue.severity, `${field}[${index}].severity`);
    check.string(issue.msg, `${field}[${index}].msg`);
  });
}

function checkRunState(check: Check, value: unknown, field: string): void {
  const state = check.object(value, field);
  const nodes = check.nullableObject(state.nodes, `${field}.nodes`);
  for (const [id, entry] of Object.entries(nodes)) {
    const node = check.object(entry, `${field}.nodes["${id}"]`);
    check.string(node.status, `${field}.nodes["${id}"].status`);
  }
  const history = check.nullableArray(state.history, `${field}.history`);
  history.forEach((entry, index) => {
    const event = check.object(entry, `${field}.history[${index}]`);
    check.string(event.t, `${field}.history[${index}].t`);
    check.string(event.event, `${field}.history[${index}].event`);
  });
}

/** GET /api/graph — the board, and the rev every later save locks against. */
export const verifyGraphResponse: Verifier<{
  graph: Graph;
  rev: string;
  statuses: Record<string, Status>;
  issues: Issue[] | null;
}> = (value, endpoint) => {
  const check = new Check(endpoint);
  const body = check.object(value, "回應");
  checkGraph(check, body.graph, "graph");
  check.string(body.rev, "rev");
  checkStatusMap(check, body.statuses, "statuses");
  checkIssues(check, body.issues, "issues");
  return value as {
    graph: Graph;
    rev: string;
    statuses: Record<string, Status>;
    issues: Issue[] | null;
  };
};

/** GET /api/state — the journal replay every lifecycle view reads. */
export const verifyStateResponse: Verifier<{
  state: RunState;
  statuses: Record<string, Status>;
}> = (value, endpoint) => {
  const check = new Check(endpoint);
  const body = check.object(value, "回應");
  checkRunState(check, body.state, "state");
  checkStatusMap(check, body.statuses, "statuses");
  return value as { state: RunState; statuses: Record<string, Status> };
};

export interface ProjectsResponse {
  workspace: string;
  workspaces: {
    path: string;
    label: string;
    active: boolean;
    projects: number;
  }[];
  active: string;
  projects: {
    name: string;
    label: string;
    parent?: string;
    depth: number;
    path: string;
    nodes: number;
    isFolder?: boolean;
  }[];
}

/** GET /api/projects — the explorer tree and the active-project name. */
export const verifyProjectsResponse: Verifier<ProjectsResponse> = (
  value,
  endpoint,
) => {
  const check = new Check(endpoint);
  const body = check.object(value, "回應");
  check.string(body.workspace, "workspace");
  check.string(body.active, "active");
  check.nullableArray(body.workspaces, "workspaces").forEach((entry, index) => {
    const workspace = check.object(entry, `workspaces[${index}]`);
    check.string(workspace.path, `workspaces[${index}].path`);
    check.string(workspace.label, `workspaces[${index}].label`);
  });
  check.nullableArray(body.projects, "projects").forEach((entry, index) => {
    const project = check.object(entry, `projects[${index}]`);
    check.string(project.name, `projects[${index}].name`);
    check.string(project.label, `projects[${index}].label`);
    check.string(project.path, `projects[${index}].path`);
    check.number(project.depth, `projects[${index}].depth`);
    check.number(project.nodes, `projects[${index}].nodes`);
  });
  return value as ProjectsResponse;
};

/** GET /api/nodes/{id} — the document body and the rev its saves lock against. */
export const verifyNodeResponse: Verifier<{
  id: string;
  content: string;
  rev: string;
}> = (value, endpoint) => {
  const check = new Check(endpoint);
  const body = check.object(value, "回應");
  check.string(body.id, "id");
  check.string(body.content, "content");
  check.string(body.rev, "rev");
  return value as { id: string; content: string; rev: string };
};
