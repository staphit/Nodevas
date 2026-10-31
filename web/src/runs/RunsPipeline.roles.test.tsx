import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type PipelineState, type Run } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { NodeRuns } from "./NodeRuns";
import { useRuns } from "./runStore";

vi.mock("../agents/systemNotify", () => ({
  showSystemNotification: () => true,
  systemNotifyPermission: () => "unsupported",
  requestSystemNotifications: async () => "unsupported",
}));

function makeRun(overrides: Partial<Run>): Run {
  return {
    id: "run1",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    finishedAt: new Date().toISOString(),
    pipelineId: "p1",
    ...overrides,
  };
}

const head = "a".repeat(40);

// Newest first: review, green verify, executor, red verify, tester.
const runs: Run[] = [
  makeRun({ id: "run5", kind: "review", role: "reviewer", agent: "codex", model: "gpt-5", verdict: "approve", createdAt: "2026-10-24T08:05:00Z" }),
  makeRun({
    id: "run4",
    kind: "verify",
    agent: "",
    model: "",
    verify: { phase: "green", head, exitCode: 0, passed: true, durationMs: 5100, output: "ok  	nodevas/internal/auth" },
    createdAt: "2026-10-24T08:04:00Z",
  }),
  makeRun({ id: "run3", kind: "revise", role: "executor", createdAt: "2026-10-24T08:03:00Z" }),
  makeRun({
    id: "run2",
    kind: "verify",
    agent: "",
    model: "",
    verify: { phase: "red", head, exitCode: 1, passed: true, durationMs: 4200, output: "--- FAIL: TestLogin" },
    createdAt: "2026-10-24T08:02:00Z",
  }),
  makeRun({ id: "run1", kind: "test", role: "tester", model: "sonnet", createdAt: "2026-10-24T08:01:00Z" }),
];

const pipeline: PipelineState = {
  id: "p1",
  nodeId: "alpha",
  pipeline: { implement: { agent: "claude", model: "opus" }, review: { agent: "codex", model: "gpt-5" }, maxRounds: 2 },
  round: 1,
  status: "approved",
  runIds: ["run1", "run2", "run3", "run4", "run5"],
  createdAt: "2026-10-24T08:00:00Z",
  stages: { preset: "standard", stages: { tester: true, reviewer: true } },
  phase: "review",
  testFiles: ["internal/auth/login_test.go"],
  guard: { testFiles: ["internal/auth/login_test.go"], touched: ["internal/auth/login_test.go"], approved: true, approvedBy: "codex/gpt-5" },
};

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { nodes: [{ id: "alpha", title: "Login fix" }] } as never,
  });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "listRuns").mockResolvedValue({ runs });
  vi.spyOn(api, "getPipeline").mockResolvedValue(pipeline);
});

afterEach(() => vi.restoreAllMocks());

describe("staged pipeline in the node's runs", () => {
  it("badges each run with its role", async () => {
    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    const rows = await within(list).findAllByRole("button", { name: /Run #/ });
    const text = rows.map((row) => row.textContent ?? "");
    expect(text.some((row) => /Tester/.test(row))).toBe(true);
    expect(text.some((row) => /Executor/.test(row))).toBe(true);
    expect(text.some((row) => /Reviewer/.test(row))).toBe(true);
  });

  it("shows red and green evidence: exit, duration, head and output", async () => {
    const user = userEvent.setup();
    render(<NodeRuns id="alpha" />);
    const red = await screen.findByRole("button", { name: /Red: tests failed \(exit 1\)/ });
    await user.click(red);
    const evidence = screen.getByRole("dialog", { name: /Evidence/ });
    expect(within(evidence).getByText(/4\.2 s/)).toBeInTheDocument();
    expect(within(evidence).getByText(head.slice(0, 7))).toBeInTheDocument();
    expect(within(evidence).getByText(/--- FAIL: TestLogin/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Green: tests passed \(exit 0\)/ })).toBeInTheDocument();
  });

  it("shows the guard: tester files the executor touched, and who approved it", async () => {
    render(<NodeRuns id="alpha" />);
    const guard = await screen.findByRole("group", { name: /Test files guard/ });
    expect(within(guard).getByText("internal/auth/login_test.go")).toBeInTheDocument();
    expect(within(guard).getByText(/approved by codex\/gpt-5/)).toBeInTheDocument();
  });

  it("lists a judge's checks", async () => {
    vi.mocked(api.listRuns).mockResolvedValue({
      runs: [
        makeRun({
          id: "run9",
          kind: "judge",
          role: "reviewer",
          verdict: "fail",
          checks: [
            { id: "a1", pass: true },
            { id: "a2", pass: false, note: "POV slips to Mei" },
          ],
        }),
      ],
    });
    render(<NodeRuns id="alpha" />);
    const checks = await screen.findByRole("list", { name: "Checks" });
    expect(within(checks).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      expect.stringMatching(/a1.*pass/i),
      expect.stringMatching(/a2.*fail.*POV slips to Mei/i),
    ]);
  });
});
