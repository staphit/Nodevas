import { create } from "zustand";

export type ExplorerTab = "nodes" | "files" | "git" | "agent";

/** What the main area shows instead of the board. */
export type ExplorerView =
  | { kind: "file"; source: "workspace"; path: string }
  | { kind: "file"; source: "repo"; runnerId: string; runnerName: string; repoId: string; path: string }
  | { kind: "diff"; source: "run"; runId: string }
  | { kind: "diff"; source: "workspace"; sha: string }
  | { kind: "diff"; source: "repo"; runnerId: string; repoId: string; ref: string }
  | { kind: "agent"; id: string };

export interface ExplorerState {
  view: ExplorerView | null;
  open: (view: ExplorerView) => void;
  close: () => void;
}

export const useExplorer = create<ExplorerState>((set) => ({
  view: null,
  open: (view) => set({ view }),
  close: () => set({ view: null }),
}));
