import { render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type PipelineState, type Run } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { groupRuns, NodeRuns } from "./NodeRuns";
import { resetRunNotify, runNotifyKind, useRunToasts } from "./runNotify";
import { RunToasts } from "./RunToasts";
import { useRuns } from "./runStore";

const systemNotify = vi.hoisted(() => ({ show: vi.fn(() => true) }));
vi.mock("../agents/systemNotify", () => ({
  showSystemNotification: systemNotify.show,
  systemNotifyPermission: () => "unsupported",
  requestSystemNotifications: async () => "unsupported",
}));

function makeRun(overrides: Partial<Run> = {}): Run {
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
    ...overrides,
  };
}

function pipelineState(overrides: Partial<PipelineState> = {}): PipelineState {
  return {
    id: "p1",
    nodeId: "alpha",
    pipeline: {
      implement: { agent: "claude", model: "sonnet" },
      review: { agent: "codex", model: "gpt-5" },
      maxRounds: 3,
    },
    round: 2,
    status: "running",
    runIds: ["run3", "run4", "run5"],
    createdAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

// Newest first, as the hub lists them.
const runs: Run[] = [
  makeRun({
    id: "run5",
    pipelineId: "p1",
    kind: "review",
    verdict: "changes",
    agent: "codex",
    model: "gpt-5",
    createdAt: "2026-10-24T08:05:00Z",
  }),
  makeRun({
    id: "run4",
    pipelineId: "p1",
    retryOf: "run3",
    agent: "claude",
    model: "sonnet",
    createdAt: "2026-10-24T08:04:00Z",
  }),
  makeRun({
    id: "run3",
    pipelineId: "p1",
    status: "failed",
    failureKind: "rate_limit",
    createdAt: "2026-10-24T08:03:00Z",
  }),
  makeRun({ id: "run2", status: "failed", failureKind: "quota", createdAt: "2026-10-24T08:02:00Z" }),
  makeRun({ id: "run1", retryOf: "gone", createdAt: "2026-10-24T08:01:00Z" }),
];

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { nodes: [{ id: "alpha", title: "Design doc" }] } as never,
  });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  resetRunNotify();
  systemNotify.show.mockClear();
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("groupRuns", () => {
  it("gathers a pipeline's runs where its newest run sits", () => {
    const items = groupRuns(runs);
    expect(items.map((item) => (item.type === "run" ? item.run.id : `[${item.id}]`))).toEqual([
      "[p1]",
      "run2",
      "run1",
    ]);
    const group = items[0];
    expect(group.type === "pipeline" && group.entries.map((entry) => entry.index)).toEqual([0, 1, 2]);
  });
});

describe("NodeRuns W5", () => {
  it("shows kinds, verdicts, failure kinds, fallbacks and the pipeline header", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs });
    const getPipeline = vi.spyOn(api, "getPipeline").mockResolvedValue(pipelineState());

    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });

    const header = await within(list).findByRole("group", {
      name: "Pipeline · round 2/3 · implement claude/sonnet → review codex/gpt-5",
    });
    expect(within(header).getByText("Running")).toBeInTheDocument();
    expect(getPipeline).toHaveBeenCalledTimes(1);
    expect(getPipeline).toHaveBeenCalledWith("p1");

    const group = header.closest("li")!;
    const rows = within(group).getAllByRole("button");
    expect(rows).toHaveLength(3);
    expect(within(rows[0]).getByText("Review")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Changes requested")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Fell back from #3 (Rate limited)")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Rate limited")).toBeInTheDocument();

    const all = within(list).getAllByRole("button");
    expect(within(all[3]).getByText("Quota used up")).toBeInTheDocument();
    expect(within(all[4]).getByText("Fell back from an earlier run")).toBeInTheDocument();

    // The selected (newest) run's detail repeats its kind and verdict.
    const detail = screen.getByRole("region", { name: "Run details" });
    expect(within(detail).getByText("Review")).toBeInTheDocument();
    expect(within(detail).getByText("Changes requested")).toBeInTheDocument();
  });

  it("refreshes a running pipeline on runs-changed, and an ended one no more", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs });
    const getPipeline = vi
      .spyOn(api, "getPipeline")
      .mockResolvedValueOnce(pipelineState())
      .mockResolvedValue(pipelineState({ round: 3, status: "approved" }));

    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    await within(list).findByText("Running");

    useRuns.getState().onRunsChanged("alpha");
    const header = await within(list).findByRole("group", {
      name: /^Pipeline · round 3\/3 · /,
    });
    expect(within(header).getByText("Approved")).toBeInTheDocument();
    expect(getPipeline).toHaveBeenCalledTimes(2);

    useRuns.getState().onRunsChanged("alpha");
    await waitFor(() => expect(api.listRuns).toHaveBeenCalledTimes(3));
    expect(getPipeline).toHaveBeenCalledTimes(2);
  });

  it("sets the hub's own notes apart in the timeline", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [runs[3]] });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({
      events: [
        { seq: 1, type: "log", at: "2026-10-24T08:00:00Z", text: "worktree created" },
        {
          seq: 2,
          type: "log",
          at: "2026-10-24T08:00:01Z",
          text: "hub: falling back to codex/gpt-5 (rate_limit) as run run4",
        },
      ],
      more: false,
    });
    render(<NodeRuns id="alpha" />);
    const timeline = await screen.findByRole("list", { name: "Run activity" });
    const note = await within(timeline).findByText(/falling back to codex\/gpt-5/);
    expect(note.closest("li")).toHaveClass("run-ev-hub");
    expect(note.closest("li")).toHaveTextContent("Hub falling back to codex/gpt-5 (rate_limit) as run run4");
    expect(within(timeline).getByText("worktree created").closest("li")).toHaveClass("run-ev-log");
  });

  it("names the steps from the runs while the pipeline cannot be fetched", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: runs.slice(0, 3) });
    vi.spyOn(api, "getPipeline").mockRejectedValue(new Error("404"));
    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    const header = await within(list).findByRole("group", {
      name: "Pipeline · implement claude/sonnet → review codex/gpt-5",
    });
    expect(await within(header).findByText("Status unavailable")).toBeInTheDocument();
  });
});

describe("pipeline run-notify", () => {
  const last = makeRun({
    id: "run5",
    pipelineId: "p1",
    kind: "review",
    verdict: "approve",
    prUrl: "https://github.com/example/nodevas/pull/42",
    prNumber: 42,
    prState: "open",
  });

  it("tells a pipeline's end instead of its last run's", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(last);
    vi.spyOn(api, "getPipeline").mockResolvedValue(pipelineState({ status: "approved" }));
    render(<RunToasts />);
    await useRunToasts.getState().handleRunNotify("run5");

    const toast = await screen.findByText("Pipeline approved: Design doc");
    expect(toast.closest("[data-kind]")).toHaveAttribute("data-kind", "pipeline-approved");
    expect(screen.getByText("round 2/3 · PR #42")).toBeInTheDocument();
    // The fetched state also feeds the run view.
    expect(useRuns.getState().pipelines.p1?.status).toBe("approved");

    // Another step of the same pipeline does not tell its end twice.
    vi.spyOn(api, "getRun").mockResolvedValue({ ...last, id: "run4", kind: "" });
    await useRunToasts.getState().handleRunNotify("run4");
    expect(useRunToasts.getState().toasts).toHaveLength(1);
  });

  it.each([
    ["changes_left", "Pipeline ended with changes requested: Design doc"],
    ["failed", "Pipeline failed: Design doc"],
    ["cancelled", "Pipeline cancelled: Design doc"],
  ])("toasts a pipeline that ended %s", async (status, title) => {
    vi.spyOn(api, "getRun").mockResolvedValue(last);
    vi.spyOn(api, "getPipeline").mockResolvedValue(pipelineState({ status }));
    render(<RunToasts />);
    await useRunToasts.getState().handleRunNotify("run5");
    expect(await screen.findByText(title)).toBeInTheDocument();
  });

  it("tells a step's own end while the pipeline runs on", async () => {
    vi.spyOn(api, "getRun").mockResolvedValue(last);
    vi.spyOn(api, "getPipeline").mockResolvedValue(pipelineState());
    render(<RunToasts />);
    await useRunToasts.getState().handleRunNotify("run5");
    expect(await screen.findByText("Run succeeded: Design doc")).toBeInTheDocument();
  });

  it("puts a red CI before the pipeline's end", () => {
    const ended = pipelineState({ status: "approved" });
    expect(runNotifyKind({ ...last, ciStatus: "failure", prHeadSha: "abc" }, Date.now(), ended)?.kind).toBe(
      "ci-failed",
    );
    expect(runNotifyKind(last, Date.now(), ended)).toEqual({
      kind: "pipeline-approved",
      key: "pipeline:p1:approved",
    });
    // A state for another pipeline says nothing about this run.
    expect(runNotifyKind(last, Date.now(), { ...ended, id: "p2" })?.kind).toBe("succeeded");
  });
});
