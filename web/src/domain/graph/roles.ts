// Agent roles: what an agent run may do on the board. Mirrors
// internal/engine/roles.go; pinned by testdata/agent_roles_builtin.json.
//
// RED PHASE STUB: declarations only, no behavior.

import type { AgentRole, AgentRoleDef, Capability, Graph } from "../../types";

export const CUSTOM_ROLE_PREFIX = "custom-role-";
export const MAX_CUSTOM_ROLES = 32;

export const CAPABILITIES: readonly Capability[] = [];

/** Tool → the capabilities it needs. */
export const TOOL_CAPABILITIES: Readonly<Record<string, readonly Capability[]>> = {};

export const BUILTIN_AGENT_ROLES: readonly AgentRole[] = [];

export function isBuiltinRole(_id: string): boolean {
  return false;
}

/** The union of the capabilities tools need, in CAPABILITIES order. */
export function capsForTools(_tools: readonly string[]): Capability[] {
  return [];
}

/** Built-ins with the project's overrides, then its custom roles. */
export function resolveAgentRoles(_graph: Graph | null | undefined): AgentRole[] {
  return [];
}

export function roleOf(_graph: Graph | null | undefined, _id: string): AgentRole | undefined {
  return undefined;
}

/** "orchestrator" for the planner and QA (and custom roles ranked so), "worker" otherwise. */
export function roleClass(_graph: Graph | null | undefined, _id: string): "" | "worker" | "orchestrator" {
  return "";
}

/** Throws with every problem in a stored agent_roles list. */
export function validateAgentRoleDefs(_defs: readonly AgentRoleDef[]): void {}

/** The next free custom-role id. */
export function nextCustomRoleID(_defs: readonly AgentRoleDef[]): string {
  return "";
}

/** Each tool's measured tools/list cost, in tokens (for the settings hint). */
export const TOOL_TOKEN_COST: Readonly<Record<string, number>> = {};
