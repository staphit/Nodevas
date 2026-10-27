import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Actor, type RouteRule, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useRouting } from "../runs/routingStore";
import { useApp } from "../store";
import type { AgentRoleDef, Graph } from "../types";
import { RolesSettings } from "./RolesSettings";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current, useTeamServer: () => false }));

const desktop: Runner = {
  id: "r1",
  name: "desktop",
  ownerId: "u1",
  ownerName: "alice",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.4.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless", "roles", "verify", "judge"],
    agents: [
      { kind: "claude", installed: true, models: [{ id: "opus" }, { id: "sonnet", default: true }] },
      { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
    ],
    repos: [],
  },
};

function roleRule(role: string, agent: string, model: string): RouteRule {
  return { id: `rule-${role}`, project: "", match: "role", value: role, target: { agent, model } };
}

function setGraph(agentRoles?: AgentRoleDef[]) {
  useApp.setState({ activeProject: "demo", graph: { version: 1, nodes: [], agentRoles } as Graph });
}

function lastRolesOp(spy: ReturnType<typeof vi.spyOn>) {
  const calls = spy.mock.calls as unknown as [Array<{ kind: string; agentRoles?: AgentRoleDef[] }>][];
  return calls.flatMap((call) => call[0]).filter((op) => op.kind === "agent-roles").at(-1);
}

let graphOps: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  viewer.current = { id: "u1", name: "alice", role: "admin" };
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: true, loading: false, runners: [desktop], error: null, creating: false, revoking: {} });
  useRouting.getState().reset();
  graphOps = vi.spyOn(api, "graphOps").mockResolvedValue({} as never);
  setGraph();
});

afterEach(() => vi.restoreAllMocks());

describe("RolesSettings", () => {
  it("lists the six built-in roles", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    render(<RolesSettings />);
    const list = await screen.findByRole("list", { name: "Roles" });
    expect(within(list).getAllByRole("listitem").map((item) => item.getAttribute("aria-label"))).toEqual([
      "Planner",
      "QA",
      "Tester",
      "Executor",
      "Reviewer",
      "Researcher",
    ]);
  });

  it("saves who runs a role as a role routing rule", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const put = vi.spyOn(api, "putRouting").mockImplementation(async (rules) => ({ rules }));
    const user = userEvent.setup();
    render(<RolesSettings />);
    const reviewer = await screen.findByRole("listitem", { name: "Reviewer" });
    await user.click(within(reviewer).getByRole("button", { name: /Reviewer/ }));
    await user.selectOptions(within(reviewer).getByRole("combobox", { name: "Agent" }), "codex");
    await user.click(within(reviewer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls.at(-1)![0]).toContainEqual(
      expect.objectContaining({ match: "role", value: "reviewer", target: { agent: "codex", model: "gpt-5" } }),
    );
  });

  it("warns when the reviewer or QA uses the executor's vendor", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({
      rules: [roleRule("executor", "claude", "opus"), roleRule("reviewer", "claude", "sonnet")],
    });
    render(<RolesSettings />);
    const reviewer = await screen.findByRole("listitem", { name: "Reviewer" });
    expect(within(reviewer).getByText(/different vendor/)).toBeInTheDocument();
    expect(within(screen.getByRole("listitem", { name: "Executor" })).queryByText(/different vendor/)).toBeNull();
  });

  it("edits what a role may do in this project, with each tool's token cost", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const user = userEvent.setup();
    render(<RolesSettings />);
    const planner = await screen.findByRole("listitem", { name: "Planner" });
    await user.click(within(planner).getByRole("button", { name: /Planner/ }));
    const subgraph = within(planner).getByRole("checkbox", { name: /create_subgraph/ });
    expect(subgraph).toBeChecked();
    expect(within(planner).getByText(/create_subgraph.*k tok|k tok.*create_subgraph/)).toBeInTheDocument();
    await user.click(subgraph);
    await waitFor(() => expect(lastRolesOp(graphOps)).toBeTruthy());
    const def = lastRolesOp(graphOps)!.agentRoles!.find((role) => role.id === "planner");
    expect(def?.tools).not.toContain("create_subgraph");
    expect(def?.tools).toContain("get_node");
  });

  it("adds a custom role based on a built-in", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const user = userEvent.setup();
    render(<RolesSettings />);
    await user.click(await screen.findByRole("button", { name: "Add role" }));
    await user.type(screen.getByRole("textbox", { name: "Role name" }), "Lore keeper");
    await user.selectOptions(screen.getByRole("combobox", { name: "Based on" }), "researcher");
    await user.click(screen.getByRole("button", { name: "Create role" }));
    await waitFor(() => expect(lastRolesOp(graphOps)).toBeTruthy());
    expect(lastRolesOp(graphOps)!.agentRoles).toContainEqual(
      expect.objectContaining({ id: "custom-role-1", label: "Lore keeper", basedOn: "researcher", rank: "worker" }),
    );
  });
});
