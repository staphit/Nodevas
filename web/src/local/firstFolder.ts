// The first folder an agent works in (single-user edition), for someone who
// has none: <home>/Nodevas-Projects/<name>, made new and empty, then added
// with a history started in it. No second confirmation, because the folder
// is new and holds nothing: the built-in runner's own inspect must say so
// (not a Git project, 0 files) or nothing is added. Every step is an existing
// route (GET /api/fs/dirs, POST /api/fs/mkdir, the repo inspect and add), and
// the runner re-checks everything on add.

import { api, ConflictError, GlobalRunError, type Runner, type RunnerRepo } from "../api";
import { GIT_DOWNLOAD_URL } from "./AddRepoDialog";
import { holdRunner, refusalText, repoErrorText } from "./useAddRepo";
import { useRunners } from "../runners/runnerStore";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** The folder in the person's home that new project folders go in. Not
 * Documents, which OneDrive or iCloud often sync. */
export const PROJECTS_FOLDER = "Nodevas-Projects";
export const DEFAULT_FOLDER_NAME = "my-project";

/** Characters no folder name may hold on Windows (and so here). */
const BAD_CHARS = ['\\', "/", ":", "*", "?", '"', "<", ">", "|"];

/** Why the first folder was not made: a name it can't have, a folder of that
 * name there already, Git missing, or anything else. */
export type FirstFolderProblem = "badName" | "exists" | "gitNeeded" | "failed";

export class FirstFolderError extends Error {
  problem: FirstFolderProblem;
  /** Where to get what is missing (Git), or "". */
  link: string;
  /** badName: the characters it can't have. */
  chars: string[];
  /** failed: the runner refused the folder (a RepoRefusal), or "". */
  refused: string;
  /** failed: what to say after "couldn't be made:", or "". */
  detail: string;
  constructor(
    problem: FirstFolderProblem,
    detail = "",
    extra: { link?: string; chars?: string[]; refused?: string } = {},
  ) {
    super(detail || problem);
    this.name = "FirstFolderError";
    this.problem = problem;
    this.detail = detail;
    this.link = extra.link ?? "";
    this.chars = extra.chars ?? [];
    this.refused = extra.refused ?? "";
  }
}

/** The characters `name` can't have as a folder name, once each, in order;
 * ".." or "." as a whole name is one of them. Empty when it is fine. */
export function badNameChars(name: string): string[] {
  const value = name.trim();
  if (value === "." || value === "..") return [value];
  const found: string[] = [];
  for (const char of value) {
    if ((BAD_CHARS.includes(char) || char < " ") && !found.includes(char)) found.push(char);
  }
  return found;
}

/** Where the folder will be made, with the home folder's own slashes. */
export function firstFolderPath(home: string, name: string): string {
  const slash = home.includes("\\") ? "\\" : "/";
  return [home.replace(/[\\/]+$/, ""), PROJECTS_FOLDER, name.trim()].join(slash);
}

/** Makes the folder and adds it; the repo added and the runner's new hello. */
export async function createFirstFolder(
  runnerId: string,
  name: string,
): Promise<{ repo: RunnerRepo; runner?: Runner }> {
  const folder = name.trim();
  const bad = badNameChars(folder);
  if (!folder || bad.length > 0) throw new FirstFolderError("badName", "", { chars: bad });

  try {
    const top = await api.listDirs("");
    const home = top.home ?? "";
    if (!home) throw new FirstFolderError("failed");
    const inHome = await api.listDirs(home);
    const existing = (inHome.dirs ?? []).find((dir) => dir.name === PROJECTS_FOLDER);
    const projects = existing ? existing.path : (await api.createDir(home, PROJECTS_FOLDER)).path;

    let path: string;
    let reused = false;
    try {
      path = (await api.createDir(projects, folder)).path;
    } catch (error) {
      if (!(error instanceof ConflictError)) throw error;
      // A folder of that name is there. One an earlier try made and left
      // empty (Git was missing then) is used again, if the runner says it
      // still holds nothing; anything else is the person's, and not touched.
      const dirs = (await api.listDirs(projects)).dirs ?? [];
      const found =
        dirs.find((dir) => dir.name === folder) ?? dirs.find((dir) => dir.name.toLowerCase() === folder.toLowerCase());
      if (!found) throw new FirstFolderError("exists");
      path = found.path;
      reused = true;
    }

    // Only a new, empty folder outside any other history is added unasked.
    const inspect = await api.inspectRepo(runnerId, path);
    const plan = inspect.init;
    if (inspect.state !== "not_repo" || !plan || plan.tooBig || plan.files !== 0 || !plan.hash) {
      if (reused) throw new FirstFolderError("exists");
      throw new FirstFolderError("failed", path, { refused: inspect.state === "refused" ? inspect.refused || "unreadable" : "" });
    }
    const res = await api.addRepo(runnerId, path, plan.hash);
    holdRunner(res.runner);
    void useRunners.getState().refreshIfLoaded();
    return { repo: res.repo, runner: res.runner };
  } catch (error) {
    if (error instanceof FirstFolderError) throw error;
    if (error instanceof GlobalRunError && error.code.toLowerCase() === "git_missing") {
      throw new FirstFolderError("gitNeeded", "", { link: GIT_DOWNLOAD_URL });
    }
    throw error;
  }
}

/** Why the first folder was not made, in plain words. */
export function firstFolderErrorText(t: Translate, error: unknown): string {
  if (error instanceof FirstFolderError) {
    switch (error.problem) {
      case "badName":
        return t("local.firstFolder.badName", { chars: error.chars.join(" ") });
      case "exists":
        return t("local.firstFolder.exists");
      case "gitNeeded":
        return t("local.firstFolder.gitNeeded");
      case "failed":
        return t("local.firstFolder.failed", {
          message: error.refused ? refusalText(t, error.refused) : error.detail,
        });
    }
  }
  return t("local.firstFolder.failed", { message: repoErrorText(t, error) });
}
