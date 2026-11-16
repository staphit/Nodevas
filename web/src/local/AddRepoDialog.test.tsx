import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, GlobalRunError, RunnerRequestError, type RepoInspect, type RunnerRepo } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { AddRepoDialog, GIT_DOWNLOAD_URL, repoAddCommand } from "./AddRepoDialog";
import { LOCAL_CONFIG, localRunner } from "./localFixtures";
import { folderName, inspectName, refusalText, repoErrorText } from "./useAddRepo";

const t = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);

const code: RunnerRepo = { id: "repo9", name: "big", path: "D:\\code\\big" };

function listing(path: string, dirs: string[] = []) {
  return {
    path,
    parent: path ? "D:\\" : "",
    dirs: dirs.map((name) => ({ name, path: `${path}\\${name}` })),
  };
}

function inspected(fields: Partial<RepoInspect>): RepoInspect {
  return { path: "D:\\code\\big", state: "repo", ...fields };
}

function open() {
  const onAdded = vi.fn();
  const onClose = vi.fn();
  render(<AddRepoDialog runnerId="local1" onClose={onClose} onAdded={onAdded} />);
  return { onAdded, onClose };
}

/** Browses into D:\code\big and asks the runner about it. */
async function pickBig() {
  fireEvent.click(await screen.findByRole("button", { name: /code/ }));
  fireEvent.click(await screen.findByRole("button", { name: /big/ }));
  await screen.findByText("D:\\code\\big");
  fireEvent.click(screen.getByRole("button", { name: "Use this folder" }));
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
  vi.spyOn(api, "listDirs").mockImplementation(async (path = "") => {
    if (path === "") return { path: "", parent: "", dirs: [{ name: "code", path: "D:\\code" }] };
    if (path === "D:\\code") return listing("D:\\code", ["big"]);
    return listing(path);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("AddRepoDialog", () => {
  it("adds a Git project the runner says is ready, and hands back the repo", async () => {
    const inspect = vi.spyOn(api, "inspectRepo").mockResolvedValue(inspected({ repo: code }));
    const runner = { ...localRunner(), info: { ...localRunner().info!, repos: [code] } };
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo: code, runner });
    // The list refresh that follows comes back without the repo yet.
    let release: () => void = () => {};
    vi.spyOn(api, "listRunners").mockReturnValue(
      new Promise((resolve) => {
        release = () => resolve({ runners: [runner] });
      }),
    );
    const { onAdded } = open();

    expect(screen.getByRole("dialog", { name: "Add a repo" })).toHaveTextContent(
      "A folder with your code. Agents work in a copy of it, never in your files directly.",
    );
    // Nothing is checked until a folder is picked.
    expect(await screen.findByRole("button", { name: "Use this folder" })).toBeDisabled();
    await pickBig();
    expect(inspect).toHaveBeenCalledWith("local1", "D:\\code\\big");
    expect(await screen.findByText("Ready to add: big")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add" }));

    await waitFor(() => expect(add).toHaveBeenCalledWith("local1", "D:\\code\\big", undefined));
    expect(onAdded).toHaveBeenCalledWith(code, runner);
    // The runner list has the new hello before the list refresh answers:
    // every form sees the repo at once.
    expect(useRunners.getState().runners[0].info?.repos).toEqual([code]);
    release();
  });

  it("adds the whole project for a folder inside one", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({ path: "D:\\code\\big\\web", top: "D:\\code\\big", state: "inside_repo", repo: code }),
    );
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo: code, runner: localRunner() });
    open();
    await pickBig();
    expect(
      await screen.findByText("This folder is inside the Git project D:\\code\\big. The whole project will be added."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add big" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("local1", "D:\\code\\big", undefined));
  });

  it("makes a folder a Git project only after showing what the first version keeps and leaves out", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({
        state: "not_repo",
        repo: code,
        init: {
          files: 312,
          bytes: 1_000_000,
          ignored: ["node_modules/", ".env"],
          writesGitignore: true,
          tooBig: false,
          hash: "h1",
        },
      }),
    );
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo: code, runner: localRunner() });
    open();
    await pickBig();
    expect(await screen.findByText(/isn't a Git project yet/)).toBeInTheDocument();
    expect(screen.queryByText(/312/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Make it a Git project" }));
    const plan = screen.getByRole("group", { name: "Make it a Git project" });
    expect(plan).toHaveTextContent(
      "This adds a hidden .git folder and saves the current 312 files as the first version. Your files stay as they are.",
    );
    expect(plan).toHaveTextContent("Left out: node_modules/, .env");
    expect(plan).toHaveTextContent("A .gitignore file is added listing them.");
    expect(add).not.toHaveBeenCalled();

    // Cancel steps back from the plan, not out of the dialog.
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("group", { name: "Make it a Git project" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Make it a Git project" }));

    fireEvent.click(screen.getByRole("button", { name: "Create and add" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("local1", "D:\\code\\big", "h1"));
  });

  it("refuses a folder too big for a first version, with no button", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({
        state: "not_repo",
        init: { files: 25000, bytes: 0, ignored: [], writesGitignore: false, tooBig: true, hash: "" },
      }),
    );
    open();
    await pickBig();
    expect(
      await screen.findByText(
        "Too many files (25000) to save a first version here. Set it up with a Git tool, then add it.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Make it a Git project" })).not.toBeInTheDocument();
  });

  it.each([
    ["home", "That's your whole user folder. Pick the folder of one project inside it."],
    ["root", "That's a whole drive. Pick a project folder."],
    ["secret", "This folder holds keys, passwords or app data (like .ssh). Agents must never work there."],
    ["system", "That's a system folder. Pick a project folder."],
    ["nodevas", "Nodevas keeps its own data here. Pick a project folder."],
    ["unreadable", "Nodevas can't read this folder. Pick another one."],
  ])("says plainly why a %s folder is refused, with no button", async (refused, text) => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(inspected({ state: "refused", refused }));
    open();
    await pickBig();
    expect(await screen.findByText(text)).toBeInTheDocument();
    const footer = screen.getByRole("dialog").querySelector("footer")!;
    expect(within(footer).getAllByRole("button").map((button) => button.textContent)).toEqual(["Cancel"]);
  });

  it("offers a repo that was added already instead of adding it twice", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(inspected({ already: true, repo: code }));
    const add = vi.spyOn(api, "addRepo");
    const { onAdded } = open();
    await pickBig();
    expect(await screen.findByText("big is already added.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Pick it" }));
    expect(onAdded).toHaveBeenCalledWith(code);
    expect(add).not.toHaveBeenCalled();
  });

  it("says Git is missing, with where to get it", async () => {
    vi.spyOn(api, "inspectRepo").mockRejectedValue(new GlobalRunError(502, "git_missing", "", "", 0, "git not found"));
    open();
    await pickBig();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Git isn't installed on this computer. Install Git, then try again.");
    expect(within(alert).getByRole("link", { name: "Download Git" })).toHaveAttribute("href", GIT_DOWNLOAD_URL);
  });

  it("asks to check again when the folder changed under the plan", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({
        state: "not_repo",
        init: { files: 3, bytes: 10, ignored: [], writesGitignore: true, tooBig: false, hash: "h1" },
      }),
    );
    vi.spyOn(api, "addRepo").mockRejectedValue(new GlobalRunError(409, "plan_changed", "", "", 0, "changed"));
    open();
    await pickBig();
    fireEvent.click(await screen.findByRole("button", { name: "Make it a Git project" }));
    fireEvent.click(screen.getByRole("button", { name: "Create and add" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The folder changed while you were deciding. Check it again.",
    );
    expect(screen.getByRole("button", { name: "Use this folder" })).toBeEnabled();
  });

  it("keeps the command line under a collapsed Advanced", async () => {
    open();
    await screen.findByRole("button", { name: /code/ });
    const advanced = screen.getByText("Advanced: add from the command line").closest("details");
    expect(advanced).not.toHaveAttribute("open");
    expect(advanced).toHaveTextContent(`nodevas runner repo add <path> --config "${LOCAL_CONFIG}"`);
  });

  it("closes on Escape without closing what it sits on", async () => {
    const outer = vi.fn();
    window.addEventListener("keydown", outer);
    const { onClose } = open();
    await screen.findByRole("button", { name: /code/ });
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    expect(outer).not.toHaveBeenCalled();
    window.removeEventListener("keydown", outer);
  });
});

describe("AddRepoDialog plain", () => {
  function openPlain() {
    const onAdded = vi.fn();
    render(<AddRepoDialog runnerId="local1" plain onClose={vi.fn()} onAdded={onAdded} />);
    return { onAdded };
  }

  it("speaks of a folder for the agent, with no command line", async () => {
    openPlain();
    const dialog = screen.getByRole("dialog", { name: "Choose a folder for the agent" });
    expect(dialog).toHaveTextContent("The agent works in a copy of it, never in your files directly.");
    await screen.findByRole("button", { name: /code/ });
    expect(screen.queryByText("Advanced: add from the command line")).not.toBeInTheDocument();
    expect(dialog.querySelector("details")).toBeNull();
  });

  it("still asks before it starts a history in a folder that has none", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({
        state: "not_repo",
        repo: code,
        init: { files: 12, bytes: 100, ignored: [], writesGitignore: false, tooBig: false, hash: "h1" },
      }),
    );
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo: code, runner: localRunner() });
    const { onAdded } = openPlain();
    await pickBig();
    expect(
      await screen.findByText(
        "Nodevas doesn't keep a history of this folder yet. It needs one to show you what the agent changed.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Git/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Start keeping a history" }));
    expect(
      screen.getByText("This saves the 12 files in it as the starting point. Your files stay as they are."),
    ).toBeInTheDocument();
    expect(add).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Start and use this folder" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("local1", "D:\\code\\big", "h1"));
    expect(onAdded).toHaveBeenCalledWith(code, localRunner());
  });

  const DEV_WORDS = /\bgit\b|git project|git tool|\brepos?\b/i;

  it("says a folder inside another history without Git words", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({ state: "inside_repo", path: "D:\\code\\big\\web", top: "D:\\code\\big" }),
    );
    openPlain();
    await pickBig();
    const dialog = screen.getByRole("dialog");
    await within(dialog).findByText(/D:\\code\\big/, { selector: ".repo-add-result" });
    expect(dialog.querySelector(".repo-add-result")?.textContent).not.toMatch(DEV_WORDS);
  });

  it("says a folder too big for a history, pointing to a smaller or a new one", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue(
      inspected({
        state: "not_repo",
        init: { files: 25000, bytes: 0, ignored: [], writesGitignore: false, tooBig: true, hash: "" },
      }),
    );
    openPlain();
    await pickBig();
    const said = await screen.findByText(t("local.pickFolder.tooBig", { count: 25000 }));
    expect(said.textContent).not.toMatch(DEV_WORDS);
    expect(said.textContent).toMatch(/smaller folder|new one/);
  });

  it.each(["not_repo", "too_many", "refused", "git_failed"])("says a refused add (%s) without Git words", async (code) => {
    vi.spyOn(api, "inspectRepo").mockRejectedValue(new GlobalRunError(409, code, "", "", 0, "git says no"));
    openPlain();
    await pickBig();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent?.replace("git says no", "")).not.toMatch(DEV_WORDS);
  });
});

describe("useAddRepo helpers", () => {
  it("names folders and repos by their last part", () => {
    expect(folderName("D:\\code\\big\\")).toBe("big");
    expect(folderName("/home/me/app")).toBe("app");
    expect(inspectName(inspected({ path: "D:\\code\\big\\web", top: "D:\\code\\big" }))).toBe("big");
  });

  it("puts each refusal in plain words", () => {
    expect(refusalText(t, "nope")).toBe(t("repoAdd.refused.unreadable"));
    expect(repoErrorText(t, new GlobalRunError(400, "too_many", "", "", 0, "x"))).toBe(t("repoAdd.error.too_many"));
    expect(repoErrorText(t, new GlobalRunError(400, "already", "", "", 0, "x"), { name: "big" })).toBe(
      "big is already added.",
    );
    expect(
      repoErrorText(
        t,
        new RunnerRequestError(new GlobalRunError(400, "sensitive", "", "", 0, "x"), "nodevas"),
      ),
    ).toBe(t("repoAdd.refused.nodevas"));
    expect(repoErrorText(t, new GlobalRunError(400, "sensitive", "", "", 0, "x"))).toBe(t("repoAdd.refused.secret"));
    expect(
      repoErrorText(t, new GlobalRunError(409, "FAILED_PRECONDITION", "add_on_runner", "", 0, "x"), {
        machine: "Alice's PC",
      }),
    ).toBe("Add it in Nodevas Runner on Alice's PC: right-click the tray icon → Add repo.");
    expect(repoErrorText(t, new GlobalRunError(503, "RUNNER_OFFLINE", "", "", 0, "x"))).toBe(t("local.unavailable"));
    expect(repoErrorText(t, new Error("boom"))).toBe("boom");
  });

  it("writes the command line with the settings file when known", () => {
    expect(repoAddCommand("D:\\code\\big", undefined)).toBe('nodevas runner repo add "D:\\code\\big"');
    expect(repoAddCommand("", "C:\\x.json")).toBe('nodevas runner repo add <path> --config "C:\\x.json"');
  });
});
