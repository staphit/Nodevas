import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  RequestError,
  type AutopilotPolicy,
  type AutopilotRun,
  type AutopilotView,
  type Runner,
  type TaskInstance,
} from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { AutopilotCardBadge } from "./AutopilotCardBadge";
import { AutopilotGrid, taskDepths } from "./AutopilotGrid";
import { AutopilotPanel } from "./AutopilotPanel";
import { AUTOPILOT_EVENT_DELAY_MS, deriveIndexes, useAutopilot } from "./autopilotStore";
import { WaitingForYouButton } from "./HumanReview";
import { defaultPolicy, normalizePolicy, policyProblems, quorumNeeded } from "./policy";

const openNodeRuns = vi.fn();
vi.mock("../runs/openRuns", () => ({
  openNodeRuns: (...args: unknown[]) => openNodeRuns(...args),
}));

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
    capabilities: ["headless", "plan"],
    agents: [
      {
        kind: "claude",
        installed: true,
        models: [{ id: "opus", default: true }, { id: "sonnet" }],
      },
      {
        kind: "codex",
        installed: true,
        models: [{ id: "gpt-5.5", default: true }],
      },
    ],
    repos: [{ id: "repo1", name: "app" }],
  },
};

const POLICY: AutopilotPolicy = {
  implement: [],
  review: {
    mode: "agents+human",
    reviewers: [
      { agent: "codex", model: "gpt-5.5" },
      { agent: "claude", model: "sonnet" },
    ],
    quorum: 2,
  },
  maxParallel: 2,
  maxRounds: 2,
  autoMerge: true,
  advisor: "",
};

function run(over: Partial<AutopilotRun> = {}): AutopilotRun {
  return {
    id: "ap1",
    project: "main",
    goalNodeId: "goal",
    status: "running",
    policy: POLICY,
    counts: {
      total: 3,
      success: 1,
      running: 1,
      waiting: 1,
      failed: 0,
      pending: 0,
      skipped: 0,
    },
    createdBy: "u1",
    createdByName: "Ann",
    createdAt: "2026-10-24T08:00:00Z",
    updatedAt: "2026-10-24T08:05:00Z",
    ...over,
  };
}

const TASKS: TaskInstance[] = [
  {
    nodeId: "t1",
    title: "Schema",
    state: "success",
    stage: "done",
    try: 1,
    upstream: [],
    runId: "r1",
    pipelineId: "p1",
    prUrl: "https://github.com/o/r/pull/1",
    ciStatus: "success",
    votes: [
      { agent: "codex", model: "gpt-5.5", verdict: "approve", round: 1 },
      { agent: "claude", model: "sonnet", verdict: "approve", round: 1 },
    ],
    stages: [
      { stage: "queued", status: "success", at: "2026-10-24T08:00:00Z" },
      {
        stage: "implement",
        status: "success",
        at: "2026-10-24T08:01:00Z",
        runId: "r1",
      },
      {
        stage: "review",
        status: "failed",
        at: "2026-10-24T08:02:00Z",
        round: 1,
      },
      {
        stage: "review",
        status: "success",
        at: "2026-10-24T08:03:00Z",
        round: 2,
      },
      { stage: "done", status: "success", at: "2026-10-24T08:04:00Z" },
    ],
    updatedAt: "2026-10-24T08:04:00Z",
  },
  {
    nodeId: "t2",
    title: "API",
    state: "waiting_human",
    stage: "human_review",
    try: 1,
    upstream: ["t1"],
    runId: "r2",
    pipelineId: "p2",
    prUrl: "https://github.com/o/r/pull/2",
    votes: [
      { agent: "codex", model: "gpt-5.5", verdict: "approve", round: 1 },
      { agent: "claude", model: "sonnet", verdict: "changes", round: 1 },
    ],
    stages: [
      {
        stage: "human_review",
        status: "waiting",
        at: "2026-10-24T08:05:00Z",
        round: 1,
      },
    ],
    updatedAt: "2026-10-24T08:05:00Z",
  },
  {
    nodeId: "t3",
    title: "UI",
    state: "failed",
    stage: "implement",
    try: 2,
    upstream: ["t2"],
    stages: [
      {
        stage: "implement",
        status: "failed",
        at: "2026-10-24T08:05:00Z",
        detail: "rate limited",
      },
    ],
    updatedAt: "2026-10-24T08:05:00Z",
  },
];

function view(over: Partial<AutopilotRun> = {}, tasks = TASKS): AutopilotView {
  return { run: run(over), tasks };
}

beforeEach(() => {
  vi.restoreAllMocks();
  openNodeRuns.mockReset();
  useApp.setState({
    activeProject: "main",
    preferences: { ...readPreferences(), language: "en" },
  });
  useApp.getState().updateUIPreference("language", "en");
  useAutopilot.getState().reset();
  useRunners.setState({
    runners: [RUNNER],
    loaded: true,
    loading: false,
    error: null,
  });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [RUNNER] });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("policy", () => {
  it("starts from the advisor, an agent reviewer of another kind, and a quorum of all", () => {
    const catalog = [
      { kind: "claude", models: [{ id: "opus", default: true }] },
      { kind: "codex", models: [{ id: "gpt-5.5" }] },
    ];
    const policy = defaultPolicy(catalog);
    expect(policy.implement).toEqual([]);
    expect(policy.review).toEqual({
      mode: "agents",
      reviewers: [{ agent: "codex", model: "gpt-5.5" }],
      quorum: 0,
    });
    expect(policyProblems(policy, "advisor")).toEqual([]);
    // No agents at all: a person reviews.
    expect(defaultPolicy([]).review.mode).toBe("human");
  });

  it("finds what is wrong before sending", () => {
    const bad: AutopilotPolicy = {
      implement: [{ agent: "claude", model: "" }],
      review: { mode: "agents", reviewers: [], quorum: 3 },
      maxParallel: 9,
      maxRounds: 0,
      autoMerge: false,
    };
    expect(policyProblems(bad, "chain")).toEqual(
      expect.arrayContaining(["parallel", "rounds", "implement", "reviewersEmpty", "quorum"]),
    );
    expect(policyProblems({ ...bad, implement: [] }, "chain")).toContain("implementEmpty");
    expect(policyProblems({ ...POLICY, review: { mode: "bogus" } }, "advisor")).toContain(
      "reviewMode",
    );
  });

  it("drops what the chosen modes do not use", () => {
    const sent = normalizePolicy(
      {
        ...POLICY,
        implement: [{ agent: " claude ", model: "opus" }],
        review: {
          mode: "human",
          reviewers: [{ agent: "codex", model: "x" }],
          quorum: 1,
        },
        advisor: "llm",
      },
      "advisor",
    );
    expect(sent.implement).toEqual([]);
    expect(sent.advisor).toBe("llm");
    expect(sent.review).toEqual({ mode: "human", reviewers: [], quorum: 0 });
    const chain = normalizePolicy(
      {
        ...POLICY,
        implement: [{ agent: " claude ", model: "opus" }],
        advisor: "llm",
      },
      "chain",
    );
    expect(chain.implement).toEqual([{ agent: "claude", model: "opus" }]);
    expect(chain.advisor).toBe("");
  });

  it("counts the approvals a round needs", () => {
    expect(quorumNeeded(POLICY)).toBe(2);
    expect(quorumNeeded({ ...POLICY, review: { ...POLICY.review, quorum: 0 } })).toBe(2);
    expect(quorumNeeded({ ...POLICY, review: { ...POLICY.review, quorum: 1 } })).toBe(1);
  });
});

describe("grid", () => {
  it("indents by depth in the DAG", () => {
    expect(taskDepths(TASKS)).toEqual({ t1: 0, t2: 1, t3: 2 });
    // An upstream task outside the list, or a cycle, does not hang it.
    expect(taskDepths([{ ...TASKS[0], upstream: ["elsewhere"] }])).toEqual({
      t1: 0,
    });
  });

  it("draws one row per task, each stage's latest status, votes and actions", () => {
    const onOpen = vi.fn();
    const onTaskAction = vi.fn();
    render(
      <AutopilotGrid
        tasks={TASKS}
        policy={POLICY}
        active
        busy={{}}
        onOpen={onOpen}
        onTaskAction={onTaskAction}
      />,
    );
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    const first = within(rows[0]);
    // The second review round won; both rounds are in the tooltip.
    const review = first.getByRole("img", { name: /^Agent review:/ });
    expect(review.className).toContain("ap-cell-success");
    expect(review.getAttribute("aria-label")).toContain("round 1");
    expect(review.getAttribute("aria-label")).toContain("round 2");
    expect(first.getByText("Done")).toBeTruthy();
    expect(first.getByLabelText("2 of 2 approvals needed, round 1")).toBeTruthy();
    expect(first.queryByRole("button", { name: "Retry" })).toBeNull();

    const second = within(rows[1]);
    expect(second.getByText("after Schema")).toBeTruthy();
    expect(second.getByText("Needs you")).toBeTruthy();
    expect(second.getByLabelText("1 of 2 approvals needed, round 1")).toBeTruthy();
    expect(second.getByRole("img", { name: /^Human review: Waiting/ }).className).toContain(
      "ap-cell-waiting",
    );

    const third = within(rows[2]);
    expect(third.getByText("try 2")).toBeTruthy();
    expect(
      third.getByRole("img", { name: /^Implement: Failed/ }).getAttribute("aria-label"),
    ).toContain("rate limited");
    fireEvent.click(third.getByRole("button", { name: "Retry" }));
    expect(onTaskAction).toHaveBeenCalledWith(TASKS[2], "retry");
    fireEvent.click(third.getByRole("button", { name: "Skip" }));
    expect(onTaskAction).toHaveBeenCalledWith(TASKS[2], "skip");

    fireEvent.click(screen.getByRole("button", { name: /^Schema/ }));
    expect(onOpen).toHaveBeenCalledWith(TASKS[0]);
  });

  it("offers no retry or skip once the run ended", () => {
    render(
      <AutopilotGrid
        tasks={TASKS}
        policy={POLICY}
        active={false}
        busy={{}}
        onOpen={vi.fn()}
        onTaskAction={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Skip" })).toBeNull();
  });
});

describe("store", () => {
  it("marks the tasks of active runs and lists those waiting for a person", () => {
    const { marks, waiting } = deriveIndexes({
      ap1: view(),
      ap0: view({ id: "ap0", status: "succeeded" }),
    });
    expect(marks.t2).toEqual({
      autopilotId: "ap1",
      goalNodeId: "goal",
      state: "waiting_human",
      stage: "human_review",
    });
    expect(waiting.map((item) => item.task.nodeId)).toEqual(["t2"]);
    // An ended run marks nothing.
    expect(deriveIndexes({ ap0: view({ id: "ap0", status: "cancelled" }) }).marks).toEqual({});
  });

  it("refetches a run once per burst of autopilot-changed", async () => {
    vi.useFakeTimers();
    const get = vi.spyOn(api, "getAutopilot").mockResolvedValue(view());
    useAutopilot.getState().onChanged("ap1");
    useAutopilot.getState().onChanged("ap1");
    useAutopilot.getState().onChanged("ap1");
    await act(async () => {
      vi.advanceTimersByTime(AUTOPILOT_EVENT_DELAY_MS);
    });
    expect(get).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
    await waitFor(() => expect(useAutopilot.getState().views.ap1).toBeTruthy());
    expect(useAutopilot.getState().byGoal.goal).toBe("ap1");
    expect(useAutopilot.getState().marks.t3.state).toBe("failed");
  });

  it("drops what it held when the project changes", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view());
    await useAutopilot.getState().loadForGoal("goal");
    expect(useAutopilot.getState().views.ap1).toBeTruthy();
    useAutopilot.getState().reset();
    expect(useAutopilot.getState().views).toEqual({});
    expect(useAutopilot.getState().marks).toEqual({});
  });
});

describe("panel", () => {
  it("offers the breakdown and the policy when the goal has no run, and starts one", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValueOnce(null).mockResolvedValue(view());
    const plan = vi.spyOn(api, "createPlanRun").mockResolvedValue({ id: "plan1" } as never);
    const create = vi.spyOn(api, "createAutopilot").mockResolvedValue(run());
    vi.spyOn(api, "getAutopilot").mockResolvedValue(view());
    render(<AutopilotPanel id="goal" />);

    await screen.findByRole("heading", { name: "1. Break down" });
    fireEvent.click(screen.getByRole("button", { name: "Break down" }));
    await waitFor(() =>
      expect(plan).toHaveBeenCalledWith({
        nodeId: "goal",
        runnerId: "rn1",
        agent: "claude",
        model: "opus",
        repoId: "repo1",
      }),
    );
    expect(await screen.findByText("Breakdown started.")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Who approves the PR"), {
      target: { value: "agents+human" },
    });
    fireEvent.change(screen.getByLabelText("Tasks at once"), {
      target: { value: "3" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: /Merge approved PRs/ }));
    fireEvent.click(screen.getByRole("button", { name: "Start autopilot" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0]).toEqual({
      goalNodeId: "goal",
      policy: {
        implement: [],
        review: {
          mode: "agents+human",
          reviewers: [{ agent: "codex", model: "gpt-5.5" }],
          quorum: 0,
        },
        maxParallel: 3,
        maxRounds: 2,
        autoMerge: true,
        advisor: "",
        runnerId: "",
        repoId: "",
      },
    });
    await waitFor(() =>
      expect(document.querySelector(".ap-run-status-running")?.textContent).toBe("Running"),
    );
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });

  it("will not start a fixed chain without an implementer", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(null);
    const create = vi.spyOn(api, "createAutopilot");
    render(<AutopilotPanel id="goal" />);
    await screen.findByRole("heading", { name: "2. Run the autopilot" });
    fireEvent.click(screen.getByRole("radio", { name: "Fixed agent and fallbacks" }));
    // Picking the chain seeds an implementer; removing it leaves none.
    fireEvent.click(screen.getByRole("button", { name: "Remove Implementer" }));
    fireEvent.click(screen.getByRole("button", { name: "Start autopilot" }));
    expect(
      (
        await screen.findAllByText(
          "Add at least one agent to implement, or let the autopilot choose per task.",
        )
      ).length,
    ).toBeGreaterThan(0);
    expect(create).not.toHaveBeenCalled();
  });

  it("pauses, cancels after confirming, retries a task and reviews one", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view());
    vi.spyOn(api, "getAutopilot").mockResolvedValue(view());
    const action = vi.spyOn(api, "autopilotAction").mockResolvedValue({});
    const taskAction = vi.spyOn(api, "autopilotTaskAction").mockResolvedValue({});
    const review = vi.spyOn(api, "reviewPipeline").mockResolvedValue({} as never);
    render(
      <>
        <ConfirmDialogHost />
        <AutopilotPanel id="goal" />
      </>,
    );
    await screen.findByText("Waiting for your review (1)");
    expect(screen.getByText(/1 of 3 done/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    await waitFor(() => expect(action).toHaveBeenCalledWith("ap1", "pause"));

    fireEvent.click(screen.getByRole("button", { name: "Cancel autopilot" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel autopilot" }));
    await waitFor(() => expect(action).toHaveBeenCalledWith("ap1", "cancel"));

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(taskAction).toHaveBeenCalledWith("ap1", "t3", "retry"));

    // Changes need a comment: the hub refuses them without one.
    fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
    expect(
      await screen.findByText(
        "Say what should change: the implementer revises against your comment.",
      ),
    ).toBeTruthy();
    expect(review).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Comment"), {
      target: { value: "  name the endpoint  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Request changes" }));
    await waitFor(() =>
      expect(review).toHaveBeenCalledWith("p2", {
        verdict: "changes",
        comment: "name the endpoint",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(review).toHaveBeenLastCalledWith("p2", {
        verdict: "approve",
        comment: undefined,
      }),
    );
  });

  it("opens a task's run from its row", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view());
    render(<AutopilotPanel id="goal" />);
    fireEvent.click(await screen.findByRole("button", { name: /^API/ }));
    expect(openNodeRuns).toHaveBeenCalledWith("t2", "r2");
  });

  it("shows the error the hub gave", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockRejectedValue(new Error("boom"));
    render(<AutopilotPanel id="goal" />);
    expect((await screen.findByRole("alert")).textContent).toBe("boom");
  });
});

describe("breakdown", () => {
  it("says to update a runner that cannot plan, and the hub's 409 too", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(null);
    const old = { ...RUNNER, info: { ...RUNNER.info!, capabilities: ["headless"] } };
    useRunners.setState({ runners: [old] });
    vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [old] });
    render(<AutopilotPanel id="goal" />);
    const hint =
      "This runner cannot run a breakdown yet: update nodevas on that PC and restart the runner.";
    expect((await screen.findAllByText(hint)).length).toBeGreaterThan(0);
    expect((screen.getByRole("button", { name: "Break down" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("explains a 409 from the hub", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(null);
    vi.spyOn(api, "createPlanRun").mockRejectedValue(
      new RequestError(409, "runner lacks plan", "FAILED_PRECONDITION"),
    );
    render(<AutopilotPanel id="goal" />);
    fireEvent.click(await screen.findByRole("button", { name: "Break down" }));
    expect((await screen.findByRole("alert")).textContent).toContain("update nodevas on that PC");
  });
});

describe("votes", () => {
  it("draws abstentions and a person's vote apart from the quorum", () => {
    const task: TaskInstance = {
      ...TASKS[1],
      votes: [
        { agent: "codex", model: "gpt-5.5", verdict: "approve", round: 2 },
        { agent: "claude", model: "sonnet", verdict: "abstain", round: 2 },
        { agent: "human", model: "patrick", verdict: "changes", round: 2 },
      ],
    };
    render(
      <AutopilotGrid
        tasks={[task]}
        policy={POLICY}
        active
        busy={{}}
        onOpen={vi.fn()}
        onTaskAction={vi.fn()}
      />,
    );
    const votes = screen.getByLabelText("1 of 2 approvals needed, round 2");
    expect(votes.getAttribute("title")).toBe(
      "codex/gpt-5.5: Approved\nclaude/sonnet: No verdict\npatrick (person): Changes requested",
    );
    expect(votes.querySelectorAll(".ap-vote-person")).toHaveLength(1);
  });
});

describe("badges", () => {
  it("badges a task card and counts what waits for a person", async () => {
    vi.spyOn(api, "getAutopilotForGoal").mockResolvedValue(view());
    await useAutopilot.getState().loadForGoal("goal");
    render(
      <>
        <AutopilotCardBadge nodeId="t2" />
        <AutopilotCardBadge nodeId="elsewhere" />
        <WaitingForYouButton />
      </>,
    );
    expect(screen.getByLabelText("Autopilot: Needs you · Human review")).toBeTruthy();
    expect(screen.getByRole("button", { name: "1 waiting for you" })).toBeTruthy();
  });
});

describe("autopilot strings", () => {
  // zh-TW falls back to English silently, so a missing key would never fail
  // a render test.
  it("has every key in English and Traditional Chinese", () => {
    const source = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
    const keys = [
      ...source.matchAll(
        /^\s+"((?:autopilot\.|drawer\.autopilot|nodeMenu\.autopilot|explorer\.autopilot)[\w.+]*)":/gm,
      ),
    ].map((m) => m[1]);
    expect(keys.length).toBeGreaterThan(250);
    const counts = new Map<string, number>();
    for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1);
    expect([...counts].filter(([, n]) => n !== 2).map(([key]) => key)).toEqual([]);
  });
});
