// Plain-object fixtures for the world model's tests. Not imported by app
// code.

import type { AgentAsk, AgentMessage, AgentSession } from "../api/agents";
import type { AutopilotView, TaskInstance } from "../api/autopilot";
import type { Runner } from "../api/runners";
import type { PipelineState, Run, RunBudgetStatus } from "../api/runs";

export const NOW = Date.parse("2026-10-26T12:00:00Z");

/** An ISO time `ms` before NOW. */
export function ago(ms: number): string {
  return new Date(NOW - ms).toISOString();
}

let seq = 0;

/** A 24-hex run id, like the hub's, starting with `prefix`. */
export function runId(prefix: string): string {
  return (prefix + "0".repeat(24)).slice(0, 24);
}

export function makeRun(overrides: Partial<Run> = {}): Run {
  seq += 1;
  return {
    id: runId(`f${seq.toString(16).padStart(5, "0")}`),
    project: "main",
    nodeId: "login",
    runnerId: "rn1",
    runnerName: "mac-mini",
    agent: "claude",
    model: "opus",
    repoId: "repo-1",
    repoName: "visual-editor",
    base: "main",
    branch: "",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: "u-bea",
    createdByName: "bea",
    createdAt: ago(60_000 - seq),
    ...overrides,
  };
}

export function makeRunner(overrides: Partial<Runner> = {}): Runner {
  return {
    id: "rn1",
    name: "mac-mini",
    ownerId: "u-bea",
    ownerName: "bea",
    createdAt: ago(86_400_000),
    lastSeenAt: ago(1_000),
    online: true,
    info: {
      version: "1",
      hostname: "bea-mini",
      os: "darwin",
      arch: "arm64",
      agents: [
        { kind: "claude", installed: true, models: [] },
        { kind: "codex", installed: false, models: [] },
      ],
      repos: [],
    },
    ...overrides,
  };
}

export function makeSession(overrides: Partial<AgentSession> = {}): AgentSession {
  return {
    id: "s1",
    project: "/ws/main",
    actorId: "u-bea",
    actorName: "bea",
    name: "laptop-claude",
    agent: "claude",
    state: "idle",
    since: NOW - 60_000,
    lastSeen: NOW - 5_000,
    online: true,
    ...overrides,
  };
}

export function makeAsk(overrides: Partial<AgentAsk> = {}): AgentAsk {
  return {
    id: "ask1",
    project: "/ws/main",
    sessionId: "s1",
    sessionName: "laptop-claude",
    actorId: "u-bea",
    question: "May I drop the table?",
    status: "pending",
    createdAt: NOW - 2_000,
    ...overrides,
  };
}

export function makeMessage(overrides: Partial<AgentMessage> = {}): AgentMessage {
  return {
    id: "m1",
    project: "/ws/main",
    from: "bea/laptop-claude",
    senderActor: "u-bea",
    senderSession: "laptop-claude",
    to: "api-codex",
    body: "The schema is ready.",
    createdAt: NOW - 3_000,
    status: "delivered",
    ...overrides,
  };
}

export function makePipeline(overrides: Partial<PipelineState> = {}): PipelineState {
  return {
    id: "p1",
    nodeId: "login",
    pipeline: { reviewers: [], maxRounds: 1 } as unknown as PipelineState["pipeline"],
    round: 1,
    status: "running",
    runIds: [],
    createdAt: ago(120_000),
    votes: [],
    ...overrides,
  };
}

export function makeTask(overrides: Partial<TaskInstance> = {}): TaskInstance {
  return {
    nodeId: "login",
    title: "Login",
    state: "none",
    stage: "queued",
    try: 0,
    upstream: [],
    votes: [],
    stages: [],
    updatedAt: ago(1_000),
    ...overrides,
  };
}

export function makeAutopilot(
  tasks: TaskInstance[],
  overrides: Partial<AutopilotView["run"]> = {},
): AutopilotView {
  return {
    run: {
      id: "ap1",
      project: "main",
      goalNodeId: "goal",
      status: "running",
      policy: {} as AutopilotView["run"]["policy"],
      counts: {
        total: tasks.length,
        success: tasks.filter((task) => task.state === "success").length,
        running: 0,
        waiting: 0,
        failed: tasks.filter((task) => task.state === "failed").length,
        pending: 0,
        skipped: 0,
      },
      createdBy: "u-bea",
      createdByName: "bea",
      createdAt: ago(600_000),
      updatedAt: ago(1_000),
      ...overrides,
    },
    tasks,
  };
}

export function makeBudget(used: number, dailyTokens: number): RunBudgetStatus {
  return {
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens },
    used: { runs: 3, costUsd: 0, tokens: used },
    exceeded: "",
  };
}
