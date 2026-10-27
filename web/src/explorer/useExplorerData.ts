import type { AgentsResponse, RepoSummary, WorkspaceGit, WorkspaceListing } from "../api/explorer";

export interface ExplorerData {
  files: WorkspaceListing | null;
  repos: RepoSummary[];
  git: WorkspaceGit | null;
  agents: AgentsResponse | null;
  error: string | null;
}

/** Loads what the current tab needs. */
export function useExplorerData(_tab: string): ExplorerData {
  // TODO(green): implement.
  return { files: null, repos: [], git: null, agents: null, error: null };
}
