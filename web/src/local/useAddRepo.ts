// "Add repo" without a command line (single-user edition): browse to a folder
// on this computer, have the built-in runner say what it is (a Git project, a
// folder inside one, not one yet, or one it refuses), then add it, after
// making it a Git project when the person agrees to the plan the runner
// showed. The runner re-checks everything on add; this only walks the steps.

import { useCallback, useEffect, useRef, useState } from "react";

import {
  api,
  GlobalRunError,
  RunnerRequestError,
  type RepoInspect,
  type Runner,
  type RunnerRepo,
} from "../api";
import type { FolderListing } from "../components/FolderBrowser";
import { translate } from "../i18n";
import { useRunners } from "../runners/runnerStore";

type Translate = (key: string, values?: Record<string, string | number>) => string;

const REFUSALS = new Set(["home", "root", "secret", "system", "nodevas", "unreadable"]);

/** The last part of a path, either slash. */
export function folderName(path: string): string {
  const parts = path.replace(/[\\/]+$/, "").split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

/** The name the repo is (or would be) added under. */
export function inspectName(inspect: RepoInspect): string {
  return inspect.repo?.name || folderName(inspect.top || inspect.path);
}

/** Why the runner refused a folder, in plain words. */
export function refusalText(t: Translate, refused: string | undefined): string {
  return t(`repoAdd.refused.${REFUSALS.has(refused ?? "") ? refused : "unreadable"}`);
}

/** A refused repo change, in plain words. `count` is the plan's file count
 * for too_big; `machine` names a team runner for add_on_runner. */
export function repoErrorText(
  t: Translate,
  error: unknown,
  context: { name?: string; count?: number; machine?: string } = {},
): string {
  if (error instanceof GlobalRunError) {
    if (error.blocked === "add_on_runner") return t("repoAdd.onRunner", { machine: context.machine ?? "" });
    switch (error.code.toLowerCase()) {
      case "git_missing":
        return t("repoAdd.gitMissing");
      case "plan_changed":
        return t("repoAdd.planChanged");
      case "too_big":
        return t("repoAdd.tooBig", { count: context.count ?? 0 });
      case "already":
        return t("repoAdd.already", { name: context.name ?? "" });
      case "sensitive":
        return refusalText(t, error instanceof RunnerRequestError && error.refused ? error.refused : "secret");
      case "not_found":
      case "not_dir":
      case "not_repo":
      case "too_many":
      case "busy":
      case "refused":
        return t(`repoAdd.error.${error.code.toLowerCase()}`);
      case "git_failed":
        return t("repoAdd.error.git_failed", { message: error.message });
      case "off":
      case "runner_offline":
        return t("local.unavailable");
    }
    if (error.status === 503) return t("local.unavailable");
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

/** Puts the runner's new hello in the runner list, so every form reading it
 * sees the repo at once. */
export function holdRunner(runner: Runner | undefined): void {
  if (!runner?.id) return;
  useRunners.setState((state) => ({
    runners: state.runners.some((item) => item.id === runner.id)
      ? state.runners.map((item) => (item.id === runner.id ? runner : item))
      : [...state.runners, runner],
  }));
}

export interface AddRepoState {
  listing: FolderListing | null;
  /** The folder being looked at: the pick. */
  path: string;
  /** Why browsing failed, or why the last step was refused. */
  error: string;
  /** The refusal's runner code (git_missing shows a download link). */
  errorCode: string;
  checking: boolean;
  /** The runner's word on the picked folder; null until asked. */
  inspect: RepoInspect | null;
  /** not_repo: the person asked to make it a Git project, the plan shows. */
  confirmInit: boolean;
  adding: boolean;
}

export function useAddRepo(runnerId: string, onAdded: (repo: RunnerRepo, runner: Runner | undefined) => void) {
  const t: Translate = useCallback((key, values) => translate(key, undefined, values), []);
  const [state, setState] = useState<AddRepoState>({
    listing: null,
    path: "",
    error: "",
    errorCode: "",
    checking: false,
    inspect: null,
    confirmInit: false,
    adding: false,
  });
  // A newer browse or check wins: an older answer arriving late is dropped.
  const turn = useRef(0);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  const patch = useCallback((next: Partial<AddRepoState>) => {
    if (live.current) setState((current) => ({ ...current, ...next }));
  }, []);

  const browse = useCallback(async (path: string) => {
    const mine = ++turn.current;
    try {
      const listing = await api.listDirs(path);
      if (mine !== turn.current) return;
      patch({ listing, path: listing.path, inspect: null, confirmInit: false, error: "", errorCode: "" });
    } catch (error) {
      if (mine !== turn.current) return;
      patch({ error: (error as Error).message || t("explorer.readFolderFailed"), errorCode: "" });
    }
  }, [patch, t]);

  const check = async () => {
    const path = state.path;
    if (!path || state.checking || state.adding) return;
    const mine = ++turn.current;
    patch({ checking: true, inspect: null, confirmInit: false, error: "", errorCode: "" });
    try {
      const inspect = await api.inspectRepo(runnerId, path);
      if (mine !== turn.current) return;
      patch({ inspect, checking: false });
    } catch (error) {
      if (mine !== turn.current) return;
      patch({
        checking: false,
        error: repoErrorText(t, error, { name: folderName(path) }),
        errorCode: error instanceof GlobalRunError ? error.code.toLowerCase() : "",
      });
    }
  };

  const add = async () => {
    const inspect = state.inspect;
    if (!inspect || state.adding) return;
    const init = inspect.state === "not_repo";
    if (init && (!state.confirmInit || !inspect.init?.hash || inspect.init.tooBig)) return;
    const path = inspect.state === "inside_repo" && inspect.top ? inspect.top : inspect.path;
    patch({ adding: true, error: "", errorCode: "" });
    try {
      const res = await api.addRepo(runnerId, path, init ? inspect.init?.hash : undefined);
      holdRunner(res.runner);
      void useRunners.getState().refreshIfLoaded();
      patch({ adding: false });
      onAdded(res.repo, res.runner);
    } catch (error) {
      const code = error instanceof GlobalRunError ? error.code.toLowerCase() : "";
      patch({
        adding: false,
        error: repoErrorText(t, error, { name: inspectName(inspect), count: inspect.init?.files }),
        errorCode: code,
        // The folder changed under the plan: the person checks it again.
        ...(code === "plan_changed" ? { inspect: null, confirmInit: false } : {}),
      });
    }
  };

  return {
    state,
    browse,
    check,
    add,
    startInit: () => patch({ confirmInit: true, error: "", errorCode: "" }),
    cancelInit: () => patch({ confirmInit: false }),
    setError: (error: string | null) => patch({ error: error ?? "", errorCode: "" }),
  };
}
