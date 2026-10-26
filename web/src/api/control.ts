import { req } from "./http";

/**
 * Station's control center: POST /api/control/pause-all, the emergency stop.
 * Mirrors internal/httpapi/control. The hub does only what the viewer could
 * do one by one — pause the autopilots they started, cancel the runs they
 * dispatched or that run on their runners — and reports what it left alone.
 */

export interface PauseAllRequest {
  /** Also cancel runs that already started; off, only queued and offered
   * runs are withdrawn. */
  cancelRunning: boolean;
}

export type ControlTargetKind = "autopilot" | "run";

/** Why pause-all left something alone. */
export type ControlSkipReason = "not_permitted" | "running" | "changed" | "failed";

export interface ControlTarget {
  kind: ControlTargetKind | string;
  /** Absent for someone else's run: it is reported by kind and reason only. */
  id?: string;
  nodeId?: string;
  /** What it was before pause-all touched it. */
  status?: string;
  reason?: ControlSkipReason | string;
}

export interface PauseAllResponse {
  paused: ControlTarget[];
  cancelled: ControlTarget[];
  skipped: ControlTarget[];
}

export const controlApi = {
  pauseAll: async (body: PauseAllRequest): Promise<PauseAllResponse> => {
    const response = await req<PauseAllResponse>("/api/control/pause-all", {
      method: "POST",
      body: JSON.stringify(body),
    });
    return {
      paused: response?.paused ?? [],
      cancelled: response?.cancelled ?? [],
      skipped: response?.skipped ?? [],
    };
  },
};
