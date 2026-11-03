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

/**
 * Runs load once per key and again on reload; a stale answer is dropped. What
 * was loaded under another key (another project, another file) is never
 * handed back: a reload keeps the last answer up while it runs, a new key
 * starts empty.
 */
export function useLoad<T>(load: () => Promise<T>, key: string): Loaded<T> {
  const [state, setState] = useState<{ key: string; data: T | null; error: unknown; loading: boolean }>({
    key,
    data: null,
    error: null,
    loading: true,
  });
  const [nonce, setNonce] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    let live = true;
    setState((current) =>
      current.key === key ? { ...current, loading: true } : { key, data: null, error: null, loading: true },
    );
    loadRef
      .current()
      .then((data) => live && setState({ key, data, error: null, loading: false }))
      .catch(
        (error: unknown) =>
          live && setState((current) => ({ key, data: current.key === key ? current.data : null, error, loading: false })),
      );
    return () => {
      live = false;
    };
  }, [key, nonce]);
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  if (state.key !== key) return { data: null, error: null, loading: true, reload };
  return { data: state.data, error: state.error, loading: state.loading, reload };
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
