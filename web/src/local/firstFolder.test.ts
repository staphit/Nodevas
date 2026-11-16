import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, ConflictError, GlobalRunError, type RepoInspect, type RunnerRepo } from "../api";
import { translate } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { GIT_DOWNLOAD_URL } from "./AddRepoDialog";
import {
  badNameChars,
  createFirstFolder,
  FirstFolderError,
  firstFolderErrorText,
  firstFolderPath,
  PROJECTS_FOLDER,
} from "./firstFolder";
import { localRunner } from "./localFixtures";

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

const HOME = "C:\\Users\\me";
const PROJECTS = `${HOME}\\${PROJECTS_FOLDER}`;
const FOLDER = `${PROJECTS}\\my-project`;
const repo: RunnerRepo = { id: "repo7", name: "my-project", path: FOLDER };

function emptyFolder(fields: Partial<RepoInspect> = {}): RepoInspect {
  return {
    path: FOLDER,
    state: "not_repo",
    init: { files: 0, bytes: 0, ignored: [], writesGitignore: false, tooBig: false, hash: "h0" },
    ...fields,
  };
}

let mkdir: ReturnType<typeof vi.spyOn>;

/** The home folder holds `homeDirs`; mkdir answers each new folder's path. */
function fsWith(homeDirs: string[]) {
  vi.spyOn(api, "listDirs").mockImplementation(async (path = "") => {
    if (path === "") return { path: "", parent: "", home: HOME, dirs: [{ name: "C:", path: "C:\\" }] };
    if (path === HOME) {
      return { path: HOME, parent: "C:\\Users", home: HOME, dirs: homeDirs.map((name) => ({ name, path: `${HOME}\\${name}` })) };
    }
    return { path, parent: HOME, home: HOME, dirs: [] };
  });
  mkdir = vi.spyOn(api, "createDir").mockImplementation(async (parent, name) => ({ ok: true, path: `${parent}\\${name}` }));
}

beforeEach(() => {
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("createFirstFolder", () => {
  it("makes Nodevas-Projects when it is missing, then the folder, and adds it with a history", async () => {
    fsWith(["Documents"]);
    const inspect = vi.spyOn(api, "inspectRepo").mockResolvedValue(emptyFolder());
    const runner = { ...localRunner(), info: { ...localRunner().info!, repos: [repo] } };
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo, runner });
    // The list refresh that follows never answers here: the hello alone counts.
    vi.spyOn(api, "listRunners").mockReturnValue(new Promise(() => {}));

    await expect(createFirstFolder("local1", " my-project ")).resolves.toEqual({ repo, runner });
    expect(mkdir.mock.calls).toEqual([
      [HOME, PROJECTS_FOLDER],
      [PROJECTS, "my-project"],
    ]);
    expect(inspect).toHaveBeenCalledWith("local1", FOLDER);
    expect(add).toHaveBeenCalledWith("local1", FOLDER, "h0");
    // Every form sees the new folder at once.
    expect(useRunners.getState().runners[0].info?.repos).toEqual([repo]);
  });

  it("uses Nodevas-Projects when it is there already", async () => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "inspectRepo").mockResolvedValue(emptyFolder());
    vi.spyOn(api, "addRepo").mockResolvedValue({ repo, runner: localRunner() });
    await createFirstFolder("local1", "my-project");
    expect(mkdir.mock.calls).toEqual([[PROJECTS, "my-project"]]);
  });

  it.each([
    ["holds files", emptyFolder({ init: { files: 2, bytes: 9, ignored: [], writesGitignore: false, tooBig: false, hash: "h2" } })],
    ["is refused", emptyFolder({ state: "refused", refused: "secret", init: null })],
    ["is inside another history", emptyFolder({ state: "inside_repo", top: HOME, init: null })],
    ["has no plan", emptyFolder({ init: null })],
  ])("adds nothing and throws when the new folder %s", async (_what, inspected) => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "inspectRepo").mockResolvedValue(inspected);
    const add = vi.spyOn(api, "addRepo");
    await expect(createFirstFolder("local1", "my-project")).rejects.toBeInstanceOf(FirstFolderError);
    expect(add).not.toHaveBeenCalled();
  });

  it("says Git is needed, with where to get it", async () => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "inspectRepo").mockResolvedValue(emptyFolder());
    vi.spyOn(api, "addRepo").mockRejectedValue(new GlobalRunError(502, "git_missing", "", "", 0, "git not found"));
    const error = await createFirstFolder("local1", "my-project").catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(FirstFolderError);
    expect((error as FirstFolderError).problem).toBe("gitNeeded");
    expect((error as FirstFolderError).link).toBe(GIT_DOWNLOAD_URL);
    expect(firstFolderErrorText(t, error)).toBe(t("local.firstFolder.gitNeeded"));
  });

  it("uses the empty folder an earlier try left when Git was missing", async () => {
    fsWith([PROJECTS_FOLDER]);
    // The real runner says Git is missing when it looks, after the folder
    // was made.
    const inspect = vi
      .spyOn(api, "inspectRepo")
      .mockRejectedValueOnce(new GlobalRunError(502, "git_missing", "", "", 0, "git not found"));
    const first = await createFirstFolder("local1", "my-project").catch((caught: unknown) => caught);
    expect((first as FirstFolderError).problem).toBe("gitNeeded");
    expect(firstFolderErrorText(t, first)).toMatch(/open it again/);

    // Git installed, the same name again: the folder is there now.
    vi.spyOn(api, "createDir").mockRejectedValue(new ConflictError("", "", 'folder "my-project" already exists'));
    const listed = vi.mocked(api.listDirs).getMockImplementation()!;
    vi.mocked(api.listDirs).mockImplementation(async (path = "") =>
      path === PROJECTS ? { path, parent: HOME, home: HOME, dirs: [{ name: "my-project", path: FOLDER }] } : listed(path),
    );
    inspect.mockResolvedValue(emptyFolder());
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo, runner: localRunner() });
    await expect(createFirstFolder("local1", "my-project")).resolves.toMatchObject({ repo });
    expect(inspect).toHaveBeenLastCalledWith("local1", FOLDER);
    expect(add).toHaveBeenCalledWith("local1", FOLDER, "h0");
  });

  it("never uses a folder of that name that holds something", async () => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "createDir").mockRejectedValue(new ConflictError("", "", 'folder "my-project" already exists'));
    const listed = vi.mocked(api.listDirs).getMockImplementation()!;
    vi.mocked(api.listDirs).mockImplementation(async (path = "") =>
      path === PROJECTS ? { path, parent: HOME, home: HOME, dirs: [{ name: "my-project", path: FOLDER }] } : listed(path),
    );
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      emptyFolder({ init: { files: 3, bytes: 9, ignored: [], writesGitignore: false, tooBig: false, hash: "h3" } }),
    );
    const add = vi.spyOn(api, "addRepo");
    const error = await createFirstFolder("local1", "my-project").catch((caught: unknown) => caught);
    expect((error as FirstFolderError).problem).toBe("exists");
    expect(add).not.toHaveBeenCalled();
  });

  it("says a folder of that name is there already", async () => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "createDir").mockRejectedValue(new ConflictError("", "", 'folder "my-project" already exists'));
    const inspect = vi.spyOn(api, "inspectRepo");
    const error = await createFirstFolder("local1", "my-project").catch((caught: unknown) => caught);
    expect((error as FirstFolderError).problem).toBe("exists");
    expect(firstFolderErrorText(t, error)).toBe(t("local.firstFolder.exists"));
    expect(inspect).not.toHaveBeenCalled();
  });

  it("refuses a name with characters a folder can't have, before touching anything", async () => {
    fsWith([PROJECTS_FOLDER]);
    const error = await createFirstFolder("local1", "a/b:c").catch((caught: unknown) => caught);
    expect((error as FirstFolderError).problem).toBe("badName");
    expect(firstFolderErrorText(t, error)).toBe("The name can't contain these characters: / :");
    expect(mkdir).not.toHaveBeenCalled();
  });

  it("says any other failure with its reason", async () => {
    fsWith([PROJECTS_FOLDER]);
    vi.spyOn(api, "createDir").mockRejectedValue(new Error("disk full"));
    const error = await createFirstFolder("local1", "my-project").catch((caught: unknown) => caught);
    expect(firstFolderErrorText(t, error)).toBe("The folder couldn't be made: disk full");
  });
});

describe("firstFolder helpers", () => {
  it("lists the characters a name can't have, once each", () => {
    expect(badNameChars("my-project")).toEqual([]);
    expect(badNameChars('a<b>c<"')).toEqual(["<", ">", '"']);
    expect(badNameChars("..")).toEqual([".."]);
    expect(badNameChars("")).toEqual([]);
  });

  it("says where the folder will be made, with the home folder's own slashes", () => {
    expect(firstFolderPath("C:\\Users\\me", "my-project")).toBe("C:\\Users\\me\\Nodevas-Projects\\my-project");
    expect(firstFolderPath("/home/me/", "my-project")).toBe("/home/me/Nodevas-Projects/my-project");
  });
});
