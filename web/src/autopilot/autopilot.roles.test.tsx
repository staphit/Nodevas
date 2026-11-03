import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AutopilotPolicy, type AutopilotRun, type AutopilotView, type Runner } from "../api";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import type { Graph, Workflow } from "../types";
import { AutopilotPanel } from "./AutopilotPanel";
import { useAutopilot } from "./autopilotStore";

vi.mock("../runs/openRuns", () => ({ openNodeRuns: () => {} }));

const RUNNER: Runner = {
  id: "rn1",
  name: "pc",
  ownerId: "u1",
  ownerName: "Ann",
  createdAt: "2026-10-01T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "dev",
    hostname: "pc",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless", "plan", "roles", "verify", "judge"],
    agents: [
      { kind: "claude", installed: true, models: [{ id: "opus", default: true }] },
      { kind: "codex", installed: true, models: [{ id: "gpt-5.5", default: true }] },
    ],
    repos: [{ id: "repo1", name: "app" }],
  },
};

const POLICY: AutopilotPolicy = {
  implement: [],
  review: { mode: "agents", reviewers: [{ agent: "codex", model: "gpt-5.5" }] },
  maxParallel: 2,
  maxRounds: 2,
  autoMerge: false,
  advisor: "",
};

const full: Workflow = {
  preset: "full",
  stages: { planner: true, goalQa: true, tester: true, reviewer: true, ci: true, goalReview: true },
  maxQaRounds: 2,
};

function run(over: Partial<AutopilotRun>): AutopilotRun {
  return {
    id: "ap1",
    project: "main",
    goalNodeId: "goal",
    status: "running",
    policy: POLICY,
    counts: { total: 1, success: 1, running: 0, waiting: 0, failed: 0, pending: 0, skipped: 0 },
    createdBy: "u1",
    createdByName: "Ann",
    createdAt: "2026-10-24T08:00:00Z",
    updatedAt: "2026-10-24T08:05:00Z",
    ...over,
  };
}

function setGraph(workflow: Workflow | null) {
  useApp.setState({ graph: { version: 1, workflow, nodes: [{ id: "goal", title: "Checkout", kind: "goal" }] } as Graph });
}

beforeEach(() => {
  vi.restoreAllMocks();
  useApp.setState({ activeProject: "main", preferences: { ...readPreferences(), language: "en" } });
  useAutopilot.getState().reset();
  useRunners.setState({ runners: [RUNNER], loaded: true, loading: false, error: null });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [RUNNER] });
  useRuns.getState().reset();
  vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [] });
});

afterEach(() => vi.useRealTimers());

describe("autopilot with workflow stages", () => {
  it("shows the goal's QA phase and round", async () => {
    setGraph(full);
    const view: AutopilotView = {
      run: run({ goalPhase: "qa", qaRound: 1, stages: { preset: "full", stages: { goalQa: true }, maxQaRounds: 2 } }),
      tasks: [],
    };
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view);
    vi.spyOn(api, "getAutopilot").mockResolvedValue(view);
    render(<AutopilotPanel id="goal" />);
    expect(await screen.findByText(/QA verification · round 1\/2/)).toBeInTheDocument();
  });

  it("shows the goal waiting for a person's review after QA passed", async () => {
    setGraph(full);
    const view: AutopilotView = { run: run({ goalPhase: "human", stages: { preset: "full", stages: { goalReview: true } } }), tasks: [] };
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view);
    vi.spyOn(api, "getAutopilot").mockResolvedValue(view);
    render(<AutopilotPanel id="goal" />);
    expect(await screen.findByText(/Waiting for your review of the goal/)).toBeInTheDocument();
  });

  it("replaces the review picker with the project's stages when it has them", async () => {
    setGraph(full);
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(null);
    render(<AutopilotPanel id="goal" />);
    await screen.findByRole("heading", { name: "2. Run the autopilot" });
    expect(screen.queryByLabelText("Who approves the PR")).toBeNull();
    expect(screen.getByText(/Stages: Full/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Change in Settings/ })).toBeInTheDocument();
  });

  it("keeps the review picker on a legacy project", async () => {
    setGraph(null);
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(null);
    render(<AutopilotPanel id="goal" />);
    await screen.findByRole("heading", { name: "2. Run the autopilot" });
    expect(screen.getByLabelText("Who approves the PR")).toBeInTheDocument();
  });
});
