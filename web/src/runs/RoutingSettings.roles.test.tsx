import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ROUTE_MATCHES, type Actor, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { RoutingSettings } from "./RoutingSettings";
import { useRouting } from "./routingStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

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
    capabilities: ["headless"],
    agents: [
      { kind: "claude", installed: true, models: [{ id: "opus" }, { id: "sonnet", default: true }] },
      { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
    ],
    repos: [],
  },
};

beforeEach(() => {
  viewer.current = { id: "u1", name: "alice", role: "admin" };
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "proj",
    graph: { version: 1, nodes: [] } as never,
  });
  useRunners.setState({ loaded: true, loading: false, runners: [desktop], error: null, creating: false, revoking: {} });
  useRouting.getState().reset();
});

afterEach(() => vi.restoreAllMocks());

describe("RoutingSettings by role", () => {
  it("offers a role match next to tag, kind and project", () => {
    expect(ROUTE_MATCHES).toContain("role");
  });

  it("routes a role to an agent", async () => {
    vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
    const put = vi.spyOn(api, "putRouting").mockImplementation(async (rules) => ({ rules }));
    render(<RoutingSettings />);
    await screen.findByText(/No rules yet/);
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    const item = screen.getByRole("listitem", { name: "Rule 1" });
    const match = within(item).getByRole("combobox", { name: "Match" }) as HTMLSelectElement;
    expect(within(match).getAllByRole("option").map((option) => option.textContent)).toContain("Role");
    fireEvent.change(match, { target: { value: "role" } });
    const role = within(item).getByRole("combobox", { name: "Role" }) as HTMLSelectElement;
    expect(within(role).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual(
      expect.arrayContaining(["planner", "qa", "tester", "executor", "reviewer", "researcher"]),
    );
    fireEvent.change(role, { target: { value: "reviewer" } });
    fireEvent.change(within(item).getByRole("combobox", { name: "Target agent" }), { target: { value: "codex" } });
    expect(within(item).getByText("Role = Reviewer → codex/gpt-5")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][0]).toEqual([
      expect.objectContaining({ match: "role", value: "reviewer", target: { agent: "codex", model: "gpt-5" } }),
    ]);
  });
});
