import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
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

  it("lists the new project's agents when the project changes", async () => {
    useApp.setState({ activeProject: "gates2k" });
    const agents = vi
      .spyOn(explorerApi, "agents")
      .mockResolvedValue({ live: [], external: [], recent: [agentItem()] });
    render(<AgentsTab />);
    expect(await screen.findByText("Login form")).toBeInTheDocument();
    agents.mockResolvedValue({ live: [], external: [], recent: [] });
    act(() => useApp.setState({ activeProject: "ai-harness-engineering" }));
    expect(screen.queryByText("Login form")).not.toBeInTheDocument();
    expect(await screen.findByText("No agents yet")).toBeInTheDocument();
    expect(agents).toHaveBeenCalledTimes(2);
  });

  it("says when there are no agents, and what brings one", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [] });
    render(<AgentsTab />);
    expect(await screen.findByText("No agents yet")).toBeInTheDocument();
    expect(
      screen.getByText(/Dispatch a task to an agent, or connect one over MCP, and it shows up here\./),
    ).toBeInTheDocument();
    // A team hub's way there is a runner of one's own, not a button here.
    expect(screen.queryByRole("button", { name: "Open This computer" })).not.toBeInTheDocument();
  });

  // Single-user: a bare "No agents yet" left a new reader with nowhere to go.
  it("leads to This computer in the single-user edition, leaving the workstation as it was", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [] });
    useApp.setState({ preferences: { ...useApp.getState().preferences, agentWorkstation: false, projectMode: "info" } });
    const { container } = render(
      <EditionProvider edition="single">
        <AgentsTab />
      </EditionProvider>,
    );
    expect(await screen.findByText("No agents yet")).toBeInTheDocument();
    expect(
      screen.getByText(/An agent shows up here once you send it a task\. First let agents work on this computer/),
    ).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/runner|MCP/i);

    await userEvent.click(screen.getByRole("button", { name: "Open This computer" }));
    expect(useApp.getState().preferences.settingsSection).toBe("runners");
    expect(useApp.getState().preferences.projectMode).toBe("settings");
    // Opening Settings is not a yes to agents: the switch there is.
    expect(useApp.getState().preferences.agentWorkstation).toBe(false);
  });
});
