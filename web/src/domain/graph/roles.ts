// Agent roles: what an agent run may do on the board. Mirrors
// internal/engine/roles.go; pinned by testdata/agent_roles_builtin.json.
//
// Owns `graph.yaml → agent_roles` (wire `agentRoles`), a sparse list: one entry
// per custom role, plus an entry for a built-in only when its tools or node
// types are narrowed or widened. Everything here is pure.

import type { AgentRole, AgentRoleDef, Capability, Graph } from "../../types";

export const CUSTOM_ROLE_PREFIX = "custom-role-";
export const MAX_CUSTOM_ROLES = 32;
const CUSTOM_ID = /^custom-role-[A-Za-z0-9_-]{1,48}$/;
const MAX_LABEL = 64;
const MAX_INSTRUCTIONS = 2048;

export const CAPABILITIES: readonly Capability[] = Object.freeze([
  "read",
  "claim",
  "status",
  "body",
  "create",
  "link",
  "meta",
  "acceptance",
  "message",
] satisfies Capability[]);

/** Tool → the capabilities it needs. */
export const TOOL_CAPABILITIES: Readonly<Record<string, readonly Capability[]>> = Object.freeze({
  get_node: ["read"],
  get_graph_outline: ["read"],
  search_nodes: ["read"],
  get_ready_tasks: ["read"],
  validate_graph: ["read"],
  list_projects: ["read"],
  search_documents: ["read"],
  index_documents: ["read"],
  claim_task: ["claim"],
  release_task: ["claim"],
  set_node_status: ["status"],
  update_node_body: ["body"],
  create_node: ["create"],
  create_subgraph: ["create", "link"],
  link_nodes: ["link"],
  update_node_meta: ["meta"],
  set_acceptance: ["acceptance"],
  ask_user: ["message"],
  check_inbox: ["message"],
  send_message: ["message"],
  list_team: ["message"],
  save_attachment: ["message"],
} satisfies Record<string, Capability[]>);

/** The tools a role can be given, in catalog order. */
export const ROLE_TOOLS: readonly string[] = Object.freeze(Object.keys(TOOL_CAPABILITIES));

/**
 * Each tool's approximate tools/list cost, in tokens (definition bytes / 4),
 * for the settings hint. The hub pins the real per-role totals in
 * TestRolePayloadBudgets; these only need to rank the tools honestly.
 */
export const TOOL_TOKEN_COST: Readonly<Record<string, number>> = Object.freeze({
  get_node: 300,
  get_graph_outline: 300,
  search_nodes: 200,
  get_ready_tasks: 200,
  validate_graph: 100,
  list_projects: 100,
  search_documents: 300,
  index_documents: 200,
  claim_task: 200,
  release_task: 200,
  set_node_status: 300,
  update_node_body: 300,
  create_node: 400,
  create_subgraph: 500,
  link_nodes: 300,
  update_node_meta: 300,
  set_acceptance: 300,
  ask_user: 300,
  check_inbox: 200,
  send_message: 400,
  list_team: 100,
  save_attachment: 300,
});

/** "~0.5k tok" */
export function toolTokenLabel(tool: string): string {
  const tokens = TOOL_TOKEN_COST[tool] ?? 0;
  return `~${(tokens / 1000).toFixed(1)}k tok`;
}

/** The sum of a toolset's approximate token cost. */
export function toolsetTokens(tools: readonly string[]): number {
  return tools.reduce((sum, tool) => sum + (TOOL_TOKEN_COST[tool] ?? 0), 0);
}

function builtin(
  id: string,
  label: string,
  rank: AgentRole["rank"],
  tools: string[],
  writeTypes: string[],
  acceptanceTypes: string[],
): AgentRole {
  return { id, label, builtin: true, rank, tools, writeTypes, acceptanceTypes, caps: capsForTools(tools) };
}

/** The built-in table, in display order. Equal to the shared fixture. */
export const BUILTIN_AGENT_ROLES: readonly AgentRole[] = Object.freeze([
  builtin(
    "planner",
    "Planner",
    "orchestrator",
    ["get_node", "get_graph_outline", "search_nodes", "create_subgraph", "create_node", "link_nodes", "set_acceptance", "ask_user", "check_inbox", "send_message"],
    ["task", "scene", "event", "note", "gate", "choice"],
    ["task", "scene", "event", "goal"],
  ),
  builtin(
    "qa",
    "QA",
    "orchestrator",
    ["get_node", "get_graph_outline", "set_acceptance", "create_subgraph", "link_nodes", "ask_user", "check_inbox", "send_message"],
    ["task", "scene", "event"],
    ["goal"],
  ),
  builtin("tester", "Tester", "worker", ["get_node", "ask_user", "check_inbox", "send_message"], [], []),
  builtin(
    "executor",
    "Executor",
    "worker",
    ["claim_task", "get_node", "set_node_status", "release_task", "ask_user", "check_inbox", "send_message"],
    ["task", "scene", "event"],
    [],
  ),
  builtin("reviewer", "Reviewer", "worker", ["get_node", "check_inbox", "send_message"], [], []),
  builtin(
    "researcher",
    "Researcher",
    "worker",
    ["get_node", "get_graph_outline", "search_nodes", "search_documents", "create_node", "update_node_body", "link_nodes", "ask_user", "check_inbox", "send_message"],
    ["note"],
    [],
  ),
]);

const BUILTIN_BY_ID = new Map(BUILTIN_AGENT_ROLES.map((role) => [role.id, role]));

export function isBuiltinRole(id: string): boolean {
  return BUILTIN_BY_ID.has(id);
}

export function isCustomRoleId(id: string): boolean {
  return CUSTOM_ID.test(id);
}

/** The union of the capabilities tools need, in CAPABILITIES order. */
export function capsForTools(tools: readonly string[]): Capability[] {
  const needed = new Set<Capability>();
  for (const tool of tools) for (const cap of TOOL_CAPABILITIES[tool] ?? []) needed.add(cap);
  return CAPABILITIES.filter((cap) => needed.has(cap));
}

function copyRole(role: AgentRole): AgentRole {
  return {
    ...role,
    tools: [...role.tools],
    writeTypes: [...role.writeTypes],
    acceptanceTypes: [...role.acceptanceTypes],
    caps: [...role.caps],
  };
}

const EMPTY_DEFS: readonly AgentRoleDef[] = Object.freeze([]);
const resolvedCache = new WeakMap<readonly AgentRoleDef[], AgentRole[]>();

function resolveDefs(defs: readonly AgentRoleDef[]): AgentRole[] {
  const byId = new Map(defs.map((def) => [def.id, def]));
  const roles = BUILTIN_AGENT_ROLES.map((base) => {
    const role = copyRole(base);
    const def = byId.get(base.id);
    if (def?.tools) role.tools = [...def.tools];
    if (def?.writeTypes) role.writeTypes = [...def.writeTypes];
    if (def?.acceptanceTypes) role.acceptanceTypes = [...def.acceptanceTypes];
    role.caps = capsForTools(role.tools);
    return role;
  });
  for (const def of defs) {
    if (isBuiltinRole(def.id)) continue;
    const base = BUILTIN_BY_ID.get(def.basedOn ?? "") ?? BUILTIN_BY_ID.get("executor")!;
    const tools = [...(def.tools ?? base.tools)];
    const role: AgentRole = {
      id: def.id,
      label: def.label || def.id,
      builtin: false,
      basedOn: def.basedOn ?? base.id,
      rank: def.rank ?? "worker",
      tools,
      writeTypes: [...(def.writeTypes ?? base.writeTypes)],
      acceptanceTypes: [...(def.acceptanceTypes ?? base.acceptanceTypes)],
      caps: capsForTools(tools),
    };
    if (def.instructions) role.instructions = def.instructions;
    roles.push(role);
  }
  return roles;
}

/** Built-ins with the project's overrides, then its custom roles. */
export function resolveAgentRoles(graph: Graph | null | undefined): AgentRole[] {
  const defs = graph?.agentRoles ?? EMPTY_DEFS;
  const cached = resolvedCache.get(defs);
  if (cached) return cached;
  const resolved = resolveDefs(defs);
  resolvedCache.set(defs, resolved);
  return resolved;
}

export function roleOf(graph: Graph | null | undefined, id: string): AgentRole | undefined {
  return resolveAgentRoles(graph).find((role) => role.id === id);
}

/** "orchestrator" for the planner and QA (and custom roles ranked so), "worker" otherwise. */
export function roleClass(graph: Graph | null | undefined, id: string): "" | "worker" | "orchestrator" {
  return roleOf(graph, id)?.rank ?? "";
}

/** Throws with every problem in a stored agent_roles list. */
export function validateAgentRoleDefs(defs: readonly AgentRoleDef[]): void {
  const problems: string[] = [];
  const seen = new Set<string>();
  let custom = 0;
  for (const def of defs) {
    const id = def.id ?? "";
    if (seen.has(id)) problems.push(`role ${id} is listed twice`);
    seen.add(id);
    for (const tool of def.tools ?? []) {
      if (!(tool in TOOL_CAPABILITIES)) problems.push(`role ${id}: unknown tool ${tool}`);
    }
    if (isBuiltinRole(id)) {
      if (def.label !== undefined || def.basedOn !== undefined || def.rank !== undefined || def.instructions !== undefined) {
        problems.push(`built-in role ${id}: a project may change only tools and node types`);
      }
      continue;
    }
    custom++;
    if (!CUSTOM_ID.test(id)) {
      problems.push(`${JSON.stringify(id)} is not a custom role id (custom-role-…)`);
      continue;
    }
    const label = (def.label ?? "").trim();
    if (!label || [...label].length > MAX_LABEL) problems.push(`role ${id}: a name of 1 to ${MAX_LABEL} characters`);
    if (!isBuiltinRole(def.basedOn ?? "")) problems.push(`role ${id}: must be based on a built-in role`);
    if (def.rank !== undefined && def.rank !== "worker" && def.rank !== "orchestrator") {
      problems.push(`role ${id}: rank is worker or orchestrator`);
    }
    if ((def.instructions ?? "").length > MAX_INSTRUCTIONS) {
      problems.push(`role ${id}: instructions are at most 2 KB`);
    }
  }
  if (custom > MAX_CUSTOM_ROLES) problems.push(`at most ${MAX_CUSTOM_ROLES} custom roles`);
  if (problems.length) throw new Error(problems.join("; "));
}

/** The next free custom-role id. */
export function nextCustomRoleID(defs: readonly AgentRoleDef[]): string {
  let max = 0;
  for (const def of defs) {
    const match = /^custom-role-(\d+)$/.exec(def.id);
    if (match) max = Math.max(max, Number(match[1]));
  }
  return `${CUSTOM_ROLE_PREFIX}${max + 1}`;
}
