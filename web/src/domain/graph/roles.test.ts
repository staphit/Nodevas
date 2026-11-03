import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { AgentRole, AgentRoleDef, Capability, Graph } from "../../types";
import {
  BUILTIN_AGENT_ROLES,
  CAPABILITIES,
  capsForTools,
  isBuiltinRole,
  nextCustomRoleID,
  resolveAgentRoles,
  roleClass,
  roleOf,
  TOOL_CAPABILITIES,
  validateAgentRoleDefs,
} from "./roles";

const fixturePath = [
  resolve(process.cwd(), "testdata/agent_roles_builtin.json"),
  resolve(process.cwd(), "../testdata/agent_roles_builtin.json"),
].find(existsSync);
if (!fixturePath) throw new Error("cannot locate testdata/agent_roles_builtin.json");
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as {
  capabilities: Capability[];
  toolCapabilities: Record<string, Capability[]>;
  roles: AgentRole[];
};

function graph(agentRoles?: AgentRoleDef[]): Graph {
  return { version: 1, nodes: [], agentRoles } as Graph;
}

describe("built-in roles", () => {
  it("match the shared fixture", () => {
    expect([...CAPABILITIES]).toEqual(fixture.capabilities);
    expect(TOOL_CAPABILITIES).toEqual(fixture.toolCapabilities);
    expect(BUILTIN_AGENT_ROLES).toEqual(fixture.roles);
    for (const role of fixture.roles) expect(isBuiltinRole(role.id)).toBe(true);
    expect(isBuiltinRole("boss")).toBe(false);
  });

  it("rank the planner and QA as orchestrators, the rest as workers", () => {
    const g = graph([{ id: "custom-role-lead", label: "Lead", basedOn: "planner", rank: "orchestrator", tools: ["get_node"] }]);
    expect(roleClass(g, "planner")).toBe("orchestrator");
    expect(roleClass(g, "qa")).toBe("orchestrator");
    for (const id of ["tester", "executor", "reviewer", "researcher"]) expect(roleClass(g, id)).toBe("worker");
    expect(roleClass(g, "custom-role-lead")).toBe("orchestrator");
    expect(roleClass(g, "custom-role-ghost")).toBe("");
  });

  it("derive capabilities from tools", () => {
    expect(capsForTools(["send_message", "get_node"])).toEqual(["read", "message"]);
    expect(capsForTools(["create_subgraph"])).toEqual(["create", "link"]);
    const narrowed = roleOf(graph([{ id: "executor", tools: ["get_node", "claim_task"] }]), "executor");
    expect(narrowed?.caps).toEqual(["read", "claim"]);
    expect(narrowed?.label).toBe("Executor");
  });
});

describe("project roles", () => {
  it("append custom roles after the built-ins", () => {
    const roles = resolveAgentRoles(
      graph([{ id: "custom-role-lore", label: "設定管理", basedOn: "researcher", rank: "worker", tools: ["get_node"], writeTypes: ["note"] }]),
    );
    expect(roles.map((role) => role.id)).toEqual(["planner", "qa", "tester", "executor", "reviewer", "researcher", "custom-role-lore"]);
    expect(roles[6]).toMatchObject({ builtin: false, basedOn: "researcher", caps: ["read"] });
    expect(nextCustomRoleID([{ id: "custom-role-1" }, { id: "custom-role-3" }])).toBe("custom-role-4");
  });

  it("validate like the server", () => {
    expect(() => validateAgentRoleDefs([{ id: "reviewer", tools: ["get_node"] }])).not.toThrow();
    expect(() => validateAgentRoleDefs([{ id: "reviewer", label: "Boss" }])).toThrow(/only tools/);
    expect(() => validateAgentRoleDefs([{ id: "role-1", label: "x", basedOn: "executor", rank: "worker" }])).toThrow(/custom role id/);
    expect(() => validateAgentRoleDefs([{ id: "custom-role-1", label: "x", basedOn: "boss", rank: "worker" }])).toThrow(/based on/);
    expect(() =>
      validateAgentRoleDefs([{ id: "custom-role-1", label: "x", basedOn: "executor", rank: "worker", tools: ["exec_shell"] }]),
    ).toThrow(/unknown tool/);
  });
});
