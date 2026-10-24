import {
  MAX_AUTOPILOT_PARALLEL,
  MAX_FALLBACKS,
  MAX_PIPELINE_ROUNDS,
  MAX_REVIEWERS,
  REVIEW_MODES,
  reviewHasAgents,
  reviewHasHuman,
  type AgentModel,
  type AutopilotPolicy,
} from "../api";
import {
  agentModelProblem,
  catalogDefaultModel,
  trimAgentModel,
  type CatalogAgent,
} from "../runs/AgentModelFields";

/** How the implement step is chosen in the editor: a fixed chain, or left to
 * the advisor (an empty chain on the wire). */
export type ImplementChoice = "advisor" | "chain";

/** A policy the editor starts from: the advisor picks, agents review with a
 * second agent where the catalog has one, a quorum of all, two at a time. */
export function defaultPolicy(catalog: CatalogAgent[]): AutopilotPolicy {
  const first = catalog[0];
  const other = catalog.find((item) => item.kind !== first?.kind) ?? first;
  const reviewers: AgentModel[] = other
    ? [{ agent: other.kind, model: catalogDefaultModel(other) }]
    : [];
  return {
    implement: [],
    review: {
      mode: reviewers.length > 0 ? "agents" : "human",
      reviewers,
      quorum: 0,
    },
    maxParallel: 2,
    maxRounds: 2,
    autoMerge: false,
    advisor: "",
    runnerId: "",
    repoId: "",
  };
}

/** What is wrong with a policy, as i18n keys under "autopilot.problem."; an
 * empty list means it may be sent. */
export function policyProblems(policy: AutopilotPolicy, choice: ImplementChoice): string[] {
  const problems: string[] = [];
  const parallel = policy.maxParallel;
  if (!Number.isInteger(parallel) || parallel < 1 || parallel > MAX_AUTOPILOT_PARALLEL) {
    problems.push("parallel");
  }
  const rounds = policy.maxRounds;
  if (!Number.isInteger(rounds) || rounds < 1 || rounds > MAX_PIPELINE_ROUNDS) {
    problems.push("rounds");
  }
  const implement = policy.implement ?? [];
  if (choice === "chain") {
    if (implement.length === 0) problems.push("implementEmpty");
    else if (implement.length > MAX_FALLBACKS + 1) problems.push("implementLong");
    else if (implement.some((item) => agentModelProblem(item) !== "")) problems.push("implement");
  }
  if (
    policy.advisor !== "" &&
    policy.advisor !== "llm" &&
    policy.advisor !== "systemone" &&
    policy.advisor !== undefined
  ) {
    problems.push("advisor");
  }
  const mode = policy.review.mode;
  if (!REVIEW_MODES.includes(mode as (typeof REVIEW_MODES)[number])) {
    problems.push("reviewMode");
  } else if (reviewHasAgents(mode)) {
    const reviewers = policy.review.reviewers ?? [];
    if (reviewers.length === 0) problems.push("reviewersEmpty");
    else if (reviewers.length > MAX_REVIEWERS) problems.push("reviewersLong");
    else if (reviewers.some((item) => agentModelProblem(item) !== "")) problems.push("reviewers");
    const quorum = policy.review.quorum ?? 0;
    if (!Number.isInteger(quorum) || quorum < 0 || quorum > reviewers.length) {
      problems.push("quorum");
    }
  }
  return problems;
}

/** The policy as it goes on the wire: trimmed, with the parts the chosen
 * modes do not use dropped (no chain under the advisor, no reviewers without
 * agent review). */
export function normalizePolicy(policy: AutopilotPolicy, choice: ImplementChoice): AutopilotPolicy {
  const agents = reviewHasAgents(policy.review.mode);
  return {
    implement: choice === "chain" ? (policy.implement ?? []).map(trimAgentModel) : [],
    review: {
      mode: policy.review.mode,
      reviewers: agents ? (policy.review.reviewers ?? []).map(trimAgentModel) : [],
      quorum: agents ? (policy.review.quorum ?? 0) : 0,
      anyEditor: reviewHasHuman(policy.review.mode) ? Boolean(policy.review.anyEditor) : false,
    },
    maxParallel: policy.maxParallel,
    maxRounds: policy.maxRounds,
    autoMerge: policy.autoMerge,
    advisor: choice === "advisor" ? (policy.advisor ?? "") : "",
    runnerId: policy.runnerId?.trim() ?? "",
    repoId: policy.repoId?.trim() ?? "",
  };
}

/** How many approvals pass a round: the quorum, or every reviewer for 0. */
export function quorumNeeded(policy: AutopilotPolicy): number {
  const reviewers = policy.review.reviewers?.length ?? 0;
  const quorum = policy.review.quorum ?? 0;
  return quorum > 0 ? Math.min(quorum, reviewers || quorum) : reviewers;
}
