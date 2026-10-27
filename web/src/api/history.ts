import { req } from "./http";

export const historyApi = {
  getHistory: (path: string) =>
    // name is opaque: a snapshot file name or a commit ID, depending on the
    // server's history backend. author is only known with git.
    req<{ path: string; versions: { name: string; at: string; size: number; author?: string }[] }>(
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
