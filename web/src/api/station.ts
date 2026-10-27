import { req } from "./http";
import type { MergeStatus, Run } from "./runs";

/**
 * Station's building: GET /api/station/building, one summary per project the
 * viewer may read (its runs, its connected agents, what waits on the viewer
 * there), so the war room can draw every project's office without loading
 * each project. Mirrors internal/httpapi/station/types.go. Arrays are never
 * null on the wire; the client still reads them defensively.
 */

/** The fields of a run "Waiting on you" reads (station/waiting.ts), so a
 * summary from another project counts the same way as a full run here. */
export type WaitingRunLike = Pick<
  Run,
  | "id"
  | "nodeId"
  | "status"
  | "failureKind"
  | "finishedAt"
  | "createdAt"
  | "prUrl"
  | "prState"
  | "ciStatus"
  | "retryOf"
  | "runnerName"
  | "agent"
  | "model"
  | "prNumber"
> & { merge?: { status: MergeStatus | string } | null };

export interface BuildingRun extends WaitingRunLike {
  title: string;
  kind: string;
  runnerId: string;
  pipelineId?: string;
  /** Set while the run waits, queued, for its agent's usage limit. */
  retryAt?: string;
  startedAt?: string;
}

export interface BuildingSession {
  id: string;
  name: string;
  agent: string;
  model: string;
  state: string;
  nodeId: string;
  doing: string;
  runId: string;
  lastSeen: number;
  online: boolean;
}

/** Complete counts of what the viewer can act on in the project. */
export interface BuildingWaiting {
  asks: number;
  held: number;
  reviews: number;
  total: number;
}

export interface BuildingAsk {
  id: string;
  sessionId: string;
  sessionName: string;
  agent: string;
  /** At most 120 characters. */
  question: string;
  nodeId: string;
  createdAt: number;
}

export interface BuildingReview {
  autopilotId: string;
  goalNodeId: string;
  nodeId: string;
  pipelineId: string;
}

export interface BuildingProject {
  name: string;
  label: string;
  /** graph.yaml's type: "workflow", "story" or "". */
  layout: string;
  /** Resolved: the project's choice, or its layout's default. */
  officeStyle: string;
  officeStyleSet: boolean;
  /** The viewer-visible runs: active ones and the last day's, newest first. */
  runs: BuildingRun[];
  runsTruncated: boolean;
  /** Active runs the viewer may not see (someone else's), counted only. */
  othersActive: number;
  /** Connected agent sessions. */
  sessions: BuildingSession[];
  waiting: BuildingWaiting;
  asks: BuildingAsk[];
  reviews: BuildingReview[];
}

export interface BuildingResponse {
  projects: BuildingProject[];
  truncated: boolean;
  total: number;
}

function normalize(response: BuildingResponse | null | undefined): BuildingResponse {
  const projects = (response?.projects ?? []).map((project) => ({
    ...project,
    runs: project.runs ?? [],
    sessions: project.sessions ?? [],
    asks: project.asks ?? [],
    reviews: project.reviews ?? [],
    waiting: project.waiting ?? { asks: 0, held: 0, reviews: 0, total: 0 },
  }));
  return { projects, truncated: Boolean(response?.truncated), total: response?.total ?? projects.length };
}

export const stationApi = {
  getBuilding: async (): Promise<BuildingResponse> =>
    normalize(await req<BuildingResponse>("/api/station/building")),
};
