import type { RunState, Status } from "../types";
import { req } from "./http";
import { verifyStateResponse } from "./verify";

/**
 * Actual lifecycle: every write here appends to `run/journal.jsonl`, and the
 * reply carries the replayed state so a caller never has to re-read it.
 */
export const lifecycleApi = {
  setStatus: (id: string, status: Status, note = "", by = "editor") =>
    req<{ ok: boolean; state: RunState; statuses: Record<string, Status> }>(
      `/api/nodes/${encodeURIComponent(id)}/status`,
      { method: "POST", body: JSON.stringify({ status, by, note }) },
    ),
  moveEvent: (id: string, t: string, note = "") =>
    req<{ ok: boolean; state: RunState; statuses: Record<string, Status> }>(
      "/api/events/move",
      { method: "POST", body: JSON.stringify({ id, t, by: "editor", note }) },
    ),
  getState: () =>
    req<{ state: RunState; statuses: Record<string, Status> }>(
      "/api/state",
      undefined,
      verifyStateResponse,
    ),
};
