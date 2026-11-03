import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Actor, type Run, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import type { Acceptance, Graph, Workflow } from "../types";
import { useBudget } from "./budgetStore";
import { DispatchDialog } from "./DispatchDialog";
import { useRuns } from "./runStore";

const viewer = vi.hoisted(() => ({ current: null as Actor | null }));
vi.mock("../components/SignIn", () => ({ useViewer: () => viewer.current }));

const standard: Workflow = {
  preset: "standard",
  stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
};

function runner(verify: boolean): Runner {
  return {
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
      capabilities: ["headless", "plan", "roles", "verify", "judge"],
      agents: [
        { kind: "claude", installed: true, models: [{ id: "sonnet", default: true }] },
        { kind: "codex", installed: true, models: [{ id: "gpt-5" }] },
      ],
      repos: [{ id: "repo1", name: "nodevas", verify } as never],
    },
  };
}

const created: Run = {
  id: "run1",
  project: "p",
  nodeId: "alpha",
  runnerId: "r1",
  runnerName: "desktop",
  agent: "claude",
  model: "sonnet",
  repoId: "repo1",
  repoName: "nodevas",
  base: "",
  branch: "",
  prompt: "",
  status: "queued",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

function setGraph(workflow: Workflow | null, acceptance?: Acceptance) {
  useApp.setState({
    graph: { version: 1, workflow, nodes: [{ id: "alpha", title: "Login fix", kind: "task", acceptance }] } as Graph,
  });
}

const criteria: Acceptance = { items: [{ id: "a1", text: "logs in on Safari" }] };

beforeEach(() => {
  viewer.current = { id: "u1", name: "alice", role: "admin" };
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useRuns.getState().reset();
  useBudget.getState().reset();
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
});

afterEach(() => vi.restoreAllMocks());

describe("DispatchDialog with workflow stages", () => {
  it("shows the stage chain the run will go through", async () => {
    setGraph(standard, criteria);
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(true)] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    const chain = await screen.findByRole("list", { name: "Stages" });
    expect(within(chain).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "Tester",
      "red",
      "Executor",
      "green",
      "Reviewer",
    ]);
  });

  it("blocks a dispatch without acceptance criteria and offers to write them", async () => {
    setGraph(standard);
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(true)] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/acceptance criteria/i);
    expect(screen.getByRole("button", { name: "Write criteria" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeDisabled();
  });

  it("warns when the repository has no test command", async () => {
    setGraph(standard, criteria);
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(false)] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(await screen.findByText(/nodevas runner repo set .*--test-command/)).toBeInTheDocument();
  });

  it("lets a person override the stages, with a reason", async () => {
    setGraph(standard, criteria);
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(true)] });
    const create = vi.spyOn(api, "createRun").mockResolvedValue({ ...created, override: "prod is down" });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("list", { name: "Stages" });
    fireEvent.click(screen.getByRole("checkbox", { name: /Skip the stages/ }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "prod is down" } });
    fireEvent.click(screen.getByRole("button", { name: "Dispatch" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).toMatchObject({ nodeId: "alpha", override: "prod is down" });
    expect(create.mock.calls[0][0].pipeline).toBeUndefined();
  });

  it("dispatches as before on a legacy project", async () => {
    setGraph(null);
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [runner(true)] });
    render(<DispatchDialog nodeId="alpha" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByRole("combobox", { name: "Runner" });
    expect(screen.queryByRole("list", { name: "Stages" })).toBeNull();
    expect(screen.getByRole("button", { name: "Dispatch" })).toBeEnabled();
  });
});
