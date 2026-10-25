import { req, RequestError } from "./http";
import type { AgentModel, PipelineState, ReviewVerdict, Run } from "./runs";

/**
 * Autopilot (W7): a goal node's tasks dispatched as a DAG. The board is the
 * graph — a task's requires edges decide when it is ready — and an autopilot
 * run walks it, sending every ready task through implement → review → revise
 * → merge and recording each task's stages the way an Airflow DAG run records
 * its task instances. Mirrors internal/runnerwire/autopilot.go.
 */

export type ReviewMode = "none" | "agents" | "human" | "agents+human";
export const REVIEW_MODES: readonly ReviewMode[] = ["none", "agents", "human", "agents+human"];
/** Whether a review mode has agent reviewers. */
export function reviewHasAgents(mode: string): boolean {
  return mode === "agents" || mode === "agents+human";
}
/** Whether a review mode waits for a person. */
export function reviewHasHuman(mode: string): boolean {
  return mode === "human" || mode === "agents+human";
}

export const MAX_REVIEWERS = 5;
export const MAX_AUTOPILOT_PARALLEL = 8;

export interface ReviewPolicy {
  mode: ReviewMode | string;
  reviewers?: AgentModel[];
  /** How many reviewers must approve; 0 means all of them. */
  quorum?: number;
  /** Anyone who may change the project gives the person's review, not only
   * the creator, the runner's owner or an administrator. */
  anyEditor?: boolean;
}

/** "" picks by the implement chain, else the owner's routing rules; "llm"
 * lets a fast model on the owner's runner pick per task; "systemone" asks a
 * System One decision model there (local Laya, or TypeSafe Jev). */
export type AutopilotAdvisor = "" | "llm" | "systemone";

export interface AutopilotPolicy {
  /** The implement step's chain (first, then fallbacks); empty leaves the
   * choice to the advisor. */
  implement?: AgentModel[];
  review: ReviewPolicy;
  maxParallel: number;
  maxRounds: number;
  autoMerge: boolean;
  /** Lets autoMerge merge what no person reviewed (review none or agents).
   * The hub refuses autoMerge without a person's review unless this is set. */
  allowAgentOnlyMerge?: boolean;
  advisor?: AutopilotAdvisor | string;
  runnerId?: string;
  repoId?: string;
}

export type AutopilotStatus = "running" | "paused" | "succeeded" | "failed" | "cancelled";

/** Whether an autopilot run will not change any more. */
export function autopilotEnded(status: string | undefined): boolean {
  return status === "succeeded" || status === "failed" || status === "cancelled";
}

export interface TaskCounts {
  total: number;
  success: number;
  running: number;
  waiting: number;
  failed: number;
  pending: number;
  skipped: number;
}

export interface AutopilotRun {
  id: string;
  project: string;
  goalNodeId: string;
  status: AutopilotStatus | string;
  policy: AutopilotPolicy;
  reason?: string;
  counts: TaskCounts;
  createdBy: string;
  createdByName: string;
  createdAt: string;
  updatedAt: string;
  finishedAt?: string;
}

/** A task instance's state, as Airflow names them. */
export type TaskState =
  | "none"
  | "scheduled"
  | "queued"
  | "running"
  | "waiting_review"
  | "waiting_human"
  | "success"
  | "failed"
  | "upstream_failed"
  | "skipped";

export const TASK_STATES: readonly TaskState[] = [
  "none",
  "scheduled",
  "queued",
  "running",
  "waiting_review",
  "waiting_human",
  "success",
  "failed",
  "upstream_failed",
  "skipped",
];

/** A task's stages, in the order a task reaches them. */
export type TaskStage =
  | "queued"
  | "implement"
  | "commit"
  | "push"
  | "pr"
  | "ci"
  | "review"
  | "human_review"
  | "revise"
  | "approved"
  | "merge"
  | "done";

export const TASK_STAGES: readonly TaskStage[] = [
  "queued",
  "implement",
  "commit",
  "push",
  "pr",
  "ci",
  "review",
  "human_review",
  "revise",
  "approved",
  "merge",
  "done",
];

export type StageStatus = "pending" | "running" | "success" | "failed" | "skipped" | "waiting";
export const STAGE_STATUSES: readonly StageStatus[] = [
  "pending",
  "running",
  "success",
  "failed",
  "skipped",
  "waiting",
];

export interface StageRecord {
  stage: TaskStage | string;
  status: StageStatus | string;
  at: string;
  runId?: string;
  /** The review round (1-based) for review, revise and human_review. */
  round?: number;
  detail?: string;
}

export interface ReviewVote {
  agent: string;
  model: string;
  /** "approve" | "changes" | "abstain" (the review failed or gave no
   * verdict), or "" while it is still running. A person's vote has agent
   * "human" and their name as the model. */
  verdict: ReviewVerdict | "abstain" | "" | string;
  runId?: string;
  round: number;
}

export interface TaskInstance {
  nodeId: string;
  title: string;
  state: TaskState | string;
  stage: TaskStage | string;
  try: number;
  upstream?: string[] | null;
  runId?: string;
  pipelineId?: string;
  prUrl?: string;
  ciStatus?: string;
  votes?: ReviewVote[] | null;
  humanVerdict?: string;
  stages: StageRecord[] | null;
  reason?: string;
  /** Why this try's implementer was picked (the advisor's reason). */
  advisor?: string;
  /** Why the task waits for a person outside its review (state waiting_human):
   * its node changed or left the goal after the start (retry or skip it), or
   * its PR touches CI, manifests or scripts (approve the merge). */
  hold?: TaskHold | string;
  updatedAt: string;
}

export type TaskHold = "changed" | "removed" | "sensitive";

export interface AutopilotView {
  run: AutopilotRun;
  /** In topological order: upstream before downstream, then by title. */
  tasks: TaskInstance[] | null;
}

export interface CreateAutopilotRequest {
  goalNodeId: string;
  policy: AutopilotPolicy;
}

export interface HumanReviewRequest {
  verdict: ReviewVerdict;
  comment?: string;
}

const ZERO_COUNTS: TaskCounts = {
  total: 0,
  success: 0,
  running: 0,
  waiting: 0,
  failed: 0,
  pending: 0,
  skipped: 0,
};

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

/** A run with every field the UI reads present: Go sends nil slices as null
 * and may leave counts out of an answer that has none. */
export function normalizeAutopilotRun(raw: AutopilotRun): AutopilotRun {
  const counts = { ...ZERO_COUNTS };
  for (const key of Object.keys(ZERO_COUNTS) as (keyof TaskCounts)[]) {
    counts[key] = num(raw.counts?.[key]);
  }
  const policy = raw.policy ?? ({} as AutopilotPolicy);
  return {
    ...raw,
    counts,
    policy: {
      ...policy,
      implement: policy.implement ?? [],
      review: {
        mode: policy.review?.mode ?? "none",
        reviewers: policy.review?.reviewers ?? [],
        quorum: num(policy.review?.quorum),
      },
      maxParallel: num(policy.maxParallel) || 1,
      maxRounds: num(policy.maxRounds) || 1,
      autoMerge: Boolean(policy.autoMerge),
      allowAgentOnlyMerge: Boolean(policy.allowAgentOnlyMerge),
    },
  };
}

export function normalizeAutopilotView(raw: AutopilotView): AutopilotView {
  return {
    run: normalizeAutopilotRun(raw.run),
    tasks: (raw.tasks ?? []).map((task) => ({
      ...task,
      upstream: task.upstream ?? [],
      votes: task.votes ?? [],
      stages: task.stages ?? [],
      try: num(task.try),
    })),
  };
}

export type AutopilotAction = "pause" | "resume" | "cancel";
export type AutopilotTaskAction = "retry" | "skip";

const autopilotPath = (id: string) => `/api/autopilot/${encodeURIComponent(id)}`;

export const autopilotApi = {
  createAutopilot: async (body: CreateAutopilotRequest) =>
    normalizeAutopilotRun(
      await req<AutopilotRun>("/api/autopilot", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    ),
  /** The goal's latest autopilot run, or null when it never had one. */
  getAutopilotForGoal: async (goalNodeId: string): Promise<AutopilotView | null> => {
    try {
      return normalizeAutopilotView(
        await req<AutopilotView>(`/api/autopilot?goal=${encodeURIComponent(goalNodeId)}`),
      );
    } catch (error) {
      if (error instanceof RequestError && error.status === 404) return null;
      throw error;
    }
  },
  getAutopilot: async (id: string) =>
    normalizeAutopilotView(await req<AutopilotView>(autopilotPath(id))),
  autopilotAction: (id: string, action: AutopilotAction) =>
    req<unknown>(`${autopilotPath(id)}/${action}`, { method: "POST" }),
  autopilotTaskAction: (id: string, nodeId: string, action: AutopilotTaskAction) =>
    req<unknown>(`${autopilotPath(id)}/tasks/${encodeURIComponent(nodeId)}/${action}`, {
      method: "POST",
    }),
  /** A person's decision on a task the autopilot holds for its sensitive
   * paths: approve lets it merge, changes start a new try. */
  reviewAutopilotTask: (id: string, nodeId: string, body: HumanReviewRequest) =>
    req<unknown>(`${autopilotPath(id)}/tasks/${encodeURIComponent(nodeId)}/review`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  /** A person's decision on a pipeline's PR (W7 human review). */
  /** 400 for "changes" without a comment, 409 when the pipeline is not
   * waiting or the round already has a person's vote. */
  reviewPipeline: (pipelineId: string, body: HumanReviewRequest) =>
    req<PipelineState>(`/api/runs/pipelines/${encodeURIComponent(pipelineId)}/review`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
};

/** A plan run's request: POST /api/runs with kind "plan". */
export interface PlanRunRequest {
  nodeId: string;
  runnerId: string;
  agent: string;
  model: string;
  repoId: string;
}

export const planApi = {
  /** Breaks a goal into task nodes: a read-only run that only writes the
   * board (create_node, link_nodes). */
  createPlanRun: (body: PlanRunRequest) =>
    req<Run>("/api/runs", {
      method: "POST",
      body: JSON.stringify({ ...body, kind: "plan" }),
    }),
};
