import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import AgentsTab from "./AgentsTab";
import { useExplorer } from "./explorerStore";
import { agentItem } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
});
afterEach(() => vi.restoreAllMocks());

// W-14: running, external and recently finished agents.
describe("AgentsTab", () => {
  it("groups agents and shows a role only for runs", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({
      live: [agentItem({ id: "run:live", runId: "live", state: "running", role: "tester", costEstimated: false, costUsd: 0.4 })],
      external: [
        agentItem({ kind: "session", id: "session:s1", runId: undefined, sessionId: "s1", role: undefined, agent: "claude", state: "online" }),
      ],
      recent: [agentItem()],
    });
    render(<AgentsTab />);
    const running = await screen.findByRole("region", { name: "Running" });
    expect(within(running).getByText("Tester")).toBeInTheDocument();
    expect(within(running).getByText(/\$0\.40/)).toBeInTheDocument();
    const external = screen.getByRole("region", { name: "External sessions" });
    expect(within(external).queryByText(/Executor|Tester|Reviewer/)).not.toBeInTheDocument();
    expect(within(external).getByText(/Online/)).toBeInTheDocument();
    const recent = screen.getByRole("region", { name: "Recently finished" });
    expect(within(recent).getByText("Executor")).toBeInTheDocument();
    expect(within(recent).getByText(/\$0\.09 \(est\.\)/)).toBeInTheDocument();
    expect(within(recent).getByText(/Done/)).toBeInTheDocument();

    await userEvent.click(within(recent).getByText("Login form"));
    expect(useExplorer.getState().view).toEqual({ kind: "agent", id: "run:r1" });
  });

  it("says when there are no agents", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [] });
    render(<AgentsTab />);
    expect(await screen.findByText("No agents yet")).toBeInTheDocument();
  });
});
