import { req } from "./http";

/** One recorded write, returned by the database-backed audit API. */
export interface AuditEntry {
  at: string;
  actor: string;
  name?: string;
  op: string;
  target?: string;
}

export const historyApi = {
  getAudit: () => req<{ entries: AuditEntry[] }>("/api/audit"),

  getHistory: (path: string) =>
    req<{ path: string; versions: { name: string; at: string; size: number }[] }>(
      `/api/history?path=${encodeURIComponent(path)}`,
    ),
  getHistoryVersion: (path: string, version: string) =>
    req<{ path: string; version: string; content: string }>(
      `/api/history/version?path=${encodeURIComponent(path)}&version=${encodeURIComponent(version)}`,
    ),
  restoreHistory: (path: string, version: string) =>
    req<{ ok: boolean }>("/api/history/restore", {
      method: "POST",
      body: JSON.stringify({ path, version }),
    }),
};
