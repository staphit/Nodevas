// Seeds the app's stores with one small scene for the game views' component
// tests, and stubs every loader and action the views call, so nothing goes
// to the network and every click can be asserted. Not imported by app code.

import { vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useSessions } from "../runs/sessionStore";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import type { Graph } from "../types";
import {
  makeAsk,
  makeAutopilot,
  makeBudget,
  makeMessage,
  makePipeline,
  makeRun,
  makeRunner,
  makeSession,
  makeTask,
  NOW,
  runId,
} from "./testFixtures";

/** schema (done) → api → login; docs needs api; an AND gate (api + docs) in
 * front of deploy. */
export function sceneGraph(): Graph {
  return {
    version: 1,
    nodes: [
      { id: "schema", title: "DB schema", kind: "task" },
      { id: "api", title: "Auth API", kind: "task", requires: "schema", priority: "high" },
      { id: "login", title: "Login form", kind: "task", requires: "api" },
      { id: "docs", title: "API docs", kind: "task", requires: "api" },
      { id: "deploy", title: "Deploy", kind: "task", requires: "api and docs", priority: "urgent" },
    ],
    edges: [
      { from: "schema", to: "api" },
      { from: "api", to: "login" },
      { from: "api", to: "docs" },
      { from: "api", to: "deploy" },
      { from: "docs", to: "deploy" },
    ],
    ui: {
      logicGates: [{ id: "g1", operator: "and", inputs: ["api", "docs"], output: "deploy", x: 0, y: 0 }],
    },
  };
}

export const RUN_WORKING = runId("a1");
export const RUN_OFFERED = runId("a2");
export const RUN_DONE = runId("a3");

export interface SceneSpies {
  openTab: ReturnType<typeof vi.fn>;
  showRuns: ReturnType<typeof vi.fn>;
  openDispatch: ReturnType<typeof vi.fn>;
  showAutopilot: ReturnType<typeof vi.fn>;
  decideHeld: ReturnType<typeof vi.fn>;
  answerAsk: ReturnType<typeof vi.fn>;
  loadSessions: ReturnType<typeof vi.fn>;
  loadChanges: ReturnType<typeof vi.fn>;
  setLifecycleStatus: ReturnType<typeof vi.fn>;
}

/** Stubs every loader the views and useWorld call on mount. */
export function stubLoaders() {
  const loaders = {
    refreshRunners: vi.fn(async () => undefined),
    loadBudget: vi.fn(async () => undefined),
    loadActiveRuns: vi.fn(async () => {
      useRuns.setState({ activeRunsLoaded: true });
    }),
    loadProjectRuns: vi.fn(async () => undefined),
    loadRuns: vi.fn(async () => undefined),
    loadPipeline: vi.fn(async () => undefined),
    loadActive: vi.fn(async () => {
      useAutopilot.setState({ activeLoaded: true });
    }),
  };
  useRunners.setState({ refresh: loaders.refreshRunners });
  useBudget.setState({ load: loaders.loadBudget });
  useRuns.setState({
    loadActiveRuns: loaders.loadActiveRuns,
    loadProjectRuns: loaders.loadProjectRuns,
    loadRuns: loaders.loadRuns,
    loadPipeline: loaders.loadPipeline,
  });
  useAutopilot.setState({ loadActive: loaders.loadActive });
  return loaders;
}

/** The whole scene: two runners, three runs, an external session asking,
 * a held message, a review meeting, an autopilot, a budget. */
export function seedScene(now = NOW): SceneSpies {
  vi.setSystemTime(now);
  stubLoaders();
  const spies: SceneSpies = {
    openTab: vi.fn(async () => undefined),
    showRuns: vi.fn(),
    openDispatch: vi.fn(),
    showAutopilot: vi.fn(),
    decideHeld: vi.fn(async () => undefined),
    answerAsk: vi.fn(async () => undefined),
    loadSessions: vi.fn(async () => undefined),
    loadChanges: vi.fn(async () => undefined),
    setLifecycleStatus: vi.fn(async () => ({ ok: true })),
  };
  const graph = sceneGraph();
  useApp.setState({
    graph,
    statuses: { schema: "done", api: "started", login: "ready", docs: "ready", deploy: "ready" },
    activeProject: "main",
    runState: { history: [{ id: "schema", t: "2026-10-26T10:00:00Z", from: "started", to: "done", by: "editor" }] } as never,
    openTab: spies.openTab as never,
    setLifecycleStatus: spies.setLifecycleStatus as never,
  });
  useRunners.setState({
    loaded: true,
    runners: [
      makeRunner(),
      makeRunner({ id: "rn2", name: "build-box", online: false, info: { ...makeRunner().info!, hostname: "box" } }),
    ],
  });
  const working = makeRun({
    id: RUN_WORKING,
    nodeId: "api",
    status: "running",
    usage: { inputTokens: 40_000, outputTokens: 10_000 },
    commit: "abc123",
    filesChanged: 2,
  });
  const offered = makeRun({ id: RUN_OFFERED, nodeId: "login", agent: "codex", model: "gpt-5", status: "offered" });
  const merged = makeRun({
    id: RUN_DONE,
    nodeId: "schema",
    status: "succeeded",
    prUrl: "https://github.com/example/repo/pull/3",
    prState: "merged",
    finishedAt: new Date(now - 60_000).toISOString(),
  });
  useRuns.setState({
    activeRuns: [working, offered],
    activeRunsLoaded: true,
    latestByNode: { schema: merged },
    runsByNode: {},
    pipelines: { p1: makePipeline() },
    showRuns: spies.showRuns as never,
    openDispatch: spies.openDispatch as never,
    loadChanges: spies.loadChanges as never,
  });
  useAgents.setState({
    sessions: [
      makeSession({ id: "s-run", name: "visual-editor-claude-opus-x", runId: RUN_WORKING, nodeId: "api", lastSeen: now - 2_000 }),
      makeSession({ id: "s1", name: "laptop-claude", nodeId: "deploy", state: "busy", lastSeen: now - 5_000 }),
    ],
    asks: [makeAsk({ id: "ask1", sessionId: "s1", question: "May I drop the table?" })],
    held: [makeMessage({ id: "held1", to: "laptop-claude", status: "held", body: "Rebase first." })],
    messages: [makeMessage({ id: "m1", senderSession: "visual-editor-claude-opus-x", body: "Schema is ready.", createdAt: now - 1_000 })],
    decideHeld: spies.decideHeld as never,
    answerAsk: spies.answerAsk as never,
    submittingAskIds: {},
    decidingIds: {},
  });
  useAutopilot.setState({
    views: {
      ap1: makeAutopilot(
        [
          makeTask({ nodeId: "schema", state: "success" }),
          makeTask({ nodeId: "api", state: "running" }),
          makeTask({ nodeId: "deploy", state: "none" }),
        ],
        { goalNodeId: "deploy" },
      ),
    },
    activeLoaded: true,
    showAutopilot: spies.showAutopilot as never,
  });
  useBudget.setState({ status: makeBudget(250_000, 1_000_000) });
  useSessions.setState({ byNode: {}, load: spies.loadSessions as never });
  return spies;
}
