import { req } from "./http";
import {
  rememberGlobalRuns,
  runStartReq,
  type FileChange,
  type OnBusy,
  type Run,
  type RunStatus,
} from "./runs";

// Coordination layer 3: deliberate agent contests (比稿).
// Mirrors internal/runnerwire/coordination.go and §C.6.

/** Maximum entries in one contest (runnerwire.MaxContestEntries). */
export const MAX_CONTEST_ENTRIES = 3;

export type ContestState = "running" | "judging" | "ready" | "decided" | "cancelled";
export type ContestLabel = "A" | "B" | "C" | "J";
export type ContestApply = "local" | "pr" | "keep";

export interface ContestEntrySpec {
  runnerId: string;
  agent: string;
  model: string;
}

export interface GlobalContestEntrySpec {
  agent: string;
  model: string;
}

export interface ContestReviewer {
  runnerId?: string;
  agent: string;
  model: string;
}

export interface Contest {
  id: string;
  title: string;
  nodeId?: string;
  project?: string;
  global: boolean;
  state: ContestState | string;
  entryCount: number;
  reviewer?: ContestReviewer | null;
  judgeRunId?: string;
  winnerRunId?: string;
  createdBy: string;
  createdByName?: string;
  createdAt: string;
  decidedAt?: string;
}

export interface CreateContestRequest {
  nodeId: string;
  repoId: string;
  base?: string;
  prompt?: string;
  simple?: boolean;
  entries: ContestEntrySpec[];
  reviewer?: ContestEntrySpec;
  onBusy?: OnBusy;
  holderIds?: string[];
}

export interface CreateGlobalContestRequest {
  runnerId: string;
  repoId: string;
  base?: string;
  task: string;
  slug?: string;
  simple?: boolean;
  entries: GlobalContestEntrySpec[];
  reviewer?: GlobalContestEntrySpec;
  onBusy?: OnBusy;
  holderIds?: string[];
}

export interface ContestScore {
  entry: string;
  score: number;
  why: string;
}

export interface ContestScores {
  scores: ContestScore[];
  pick: string;
}

export interface ContestCompareEntry {
  label: ContestLabel | string;
  runId: string;
  agent: string;
  model: string;
  status: RunStatus | string;
  files: FileChange[] | null;
  additions: number;
  deletions: number;
  summary?: string;
}

export interface ContestCompare {
  entries: ContestCompareEntry[] | null;
  scores: ContestScores | null;
}

export interface ContestResponse {
  contest: Contest;
  runs: Run[] | null;
}

export interface ContestDetailResponse {
  contest: Contest;
  runs: Run[] | null;
  compare: ContestCompare;
}

export interface JudgeContestRequest {
  runnerId?: string;
  agent: string;
  model: string;
}

export interface JudgeContestResponse {
  contest: Contest;
}

export interface PickContestRequest {
  runId: string;
  apply: ContestApply;
  discardOthers: boolean;
}

export interface PickActionResult {
  runId: string;
  action: string;
  status: string;
  code?: string;
  reason?: string;
}

export interface PickContestResponse {
  contest: Contest;
  actions: PickActionResult[] | null;
}

export const contestsApi = {
  /** Starts a node contest (2..3 headless entries, optional reviewer). */
  createContest: async (body: CreateContestRequest): Promise<ContestResponse> => {
    const res = await runStartReq<ContestResponse>("/api/runs/contests", {
      method: "POST",
      body: JSON.stringify(body),
    });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
  /** Starts a global contest on one runner and repo (2..3 entries, optional reviewer). */
  createGlobalContest: async (body: CreateGlobalContestRequest): Promise<ContestResponse> => {
    const res = await runStartReq<ContestResponse>("/api/global-runs/contests", {
      method: "POST",
      body: JSON.stringify(body),
    });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
  /** Fetches a contest, its runs, and the comparison view (entries + reviewer scores). */
  getContest: async (id: string): Promise<ContestDetailResponse> => {
    const res = await req<ContestDetailResponse>(`/api/contests/${encodeURIComponent(id)}`, {
      cache: "no-store",
    });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
  /** Starts a reviewer (`compare`) run for a ready contest. */
  judgeContest: (id: string, body: JudgeContestRequest): Promise<JudgeContestResponse> =>
    req<JudgeContestResponse>(`/api/contests/${encodeURIComponent(id)}/judge`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  /** Picks a winning entry, applies it (local / pr / keep), and optionally discards others. */
  pickContest: (id: string, body: PickContestRequest): Promise<PickContestResponse> =>
    req<PickContestResponse>(`/api/contests/${encodeURIComponent(id)}/pick`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  /** Cancels an undecided contest and its unfinished runs. */
  cancelContest: async (id: string): Promise<ContestResponse> => {
    const res = await req<ContestResponse>(`/api/contests/${encodeURIComponent(id)}/cancel`, {
      method: "POST",
      body: "{}",
    });
    rememberGlobalRuns(res.runs ?? []);
    return res;
  },
};
