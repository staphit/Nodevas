import { useCallback, useEffect, useRef, useState } from "react";
import { explorerApi, type AgentsResponse, type RepoSummary, type WorkspaceGit, type WorkspaceListing } from "../api/explorer";

export interface ExplorerData {
  files: WorkspaceListing | null;
  repos: RepoSummary[];
  git: WorkspaceGit | null;
  agents: AgentsResponse | null;
  error: string | null;
}

const message = (error: unknown) => (error instanceof Error ? error.message : String(error));

export interface Loaded<T> {
  data: T | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

/** Runs load once per key and again on reload; a stale answer is dropped. */
export function useLoad<T>(load: () => Promise<T>, key: string): Loaded<T> {
  const [state, setState] = useState<{ data: T | null; error: unknown; loading: boolean }>({
    data: null,
    error: null,
    loading: true,
  });
  const [nonce, setNonce] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    let live = true;
    setState((current) => ({ ...current, loading: true }));
    loadRef
      .current()
      .then((data) => live && setState({ data, error: null, loading: false }))
      .catch((error: unknown) => live && setState((current) => ({ data: current.data, error, loading: false })));
    return () => {
      live = false;
    };
  }, [key, nonce]);
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { ...state, reload };
}

const none = <T,>(): Promise<T | null> => Promise.resolve(null);

/** Loads what the current tab needs. */
export function useExplorerData(tab: string): ExplorerData {
  const files = useLoad(tab === "files" ? explorerApi.workspaceFiles : none<WorkspaceListing>, `files:${tab}`);
  const repos = useLoad(tab === "files" || tab === "git" ? explorerApi.repos : () => Promise.resolve([]), `repos:${tab}`);
  const git = useLoad(tab === "git" ? explorerApi.workspaceGit : none<WorkspaceGit>, `git:${tab}`);
  const agents = useLoad(tab === "agent" ? explorerApi.agents : none<AgentsResponse>, `agents:${tab}`);
  const failed = [files, repos, git, agents].find((r) => r.error);
  return {
    files: files.data,
    repos: repos.data ?? [],
    git: git.data,
    agents: agents.data,
    error: failed ? message(failed.error) : null,
  };
}
