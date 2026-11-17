import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, ConflictError, GlobalRunError, type RunnerRepo } from "../api";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { GIT_DOWNLOAD_URL } from "./AddRepoDialog";
import { FirstFolderStep } from "./FirstFolderStep";
import { localRunner } from "./localFixtures";

const HOME = "C:\\Users\\me";
const PROJECTS = `${HOME}\\Nodevas-Projects`;
const repo: RunnerRepo = { id: "repo7", name: "bakery", path: `${PROJECTS}\\bakery` };

function renderStep(edition: "single" | "team" = "single") {
  const onAdded = vi.fn();
  const view = render(
    <EditionProvider edition={edition}>
      <FirstFolderStep runnerId="local1" onAdded={onAdded} />
    </EditionProvider>,
  );
  return { onAdded, ...view };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
  vi.spyOn(api, "listDirs").mockImplementation(async (path = "") => {
    if (path === "") return { path: "", parent: "", home: HOME, dirs: [{ name: "C:", path: "C:\\" }] };
    if (path === HOME) {
      return { path: HOME, parent: "C:\\Users", home: HOME, dirs: [{ name: "Nodevas-Projects", path: PROJECTS }] };
    }
    return { path, parent: HOME, home: HOME, dirs: [] };
  });
  vi.spyOn(api, "createDir").mockImplementation(async (parent, name) => ({ ok: true, path: `${parent}\\${name}` }));
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("FirstFolderStep", () => {
  it("makes a new folder for the agent under the given name and hands it back", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue({
      path: `${PROJECTS}\\bakery`,
      state: "not_repo",
      init: { files: 0, bytes: 0, ignored: [], writesGitignore: false, tooBig: false, hash: "h0" },
    });
    const add = vi.spyOn(api, "addRepo").mockResolvedValue({ repo, runner: localRunner() });
    const { onAdded } = renderStep();

    expect(screen.getByRole("heading", { name: "Where should the agent work?" })).toBeInTheDocument();
    const name = screen.getByRole("textbox", { name: "Folder name" });
    expect(name).toHaveValue("my-project");
    expect(await screen.findByText(`It will be made here: ${PROJECTS}\\my-project`)).toBeInTheDocument();

    fireEvent.change(name, { target: { value: "bakery" } });
    expect(screen.getByText(`It will be made here: ${PROJECTS}\\bakery`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Create the folder" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("local1", `${PROJECTS}\\bakery`, "h0"));
    expect(onAdded).toHaveBeenCalledWith(repo);
  });

  it("says a folder of that name is there already", async () => {
    vi.spyOn(api, "createDir").mockRejectedValue(new ConflictError("", "", "exists"));
    renderStep();
    fireEvent.click(screen.getByRole("button", { name: "Create the folder" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A folder with that name is already there. Choose another name.",
    );
  });

  it("says Git is needed, with where to get it", async () => {
    vi.spyOn(api, "inspectRepo").mockResolvedValue({
      path: `${PROJECTS}\\my-project`,
      state: "not_repo",
      init: { files: 0, bytes: 0, ignored: [], writesGitignore: false, tooBig: false, hash: "h0" },
    });
    vi.spyOn(api, "addRepo").mockRejectedValue(new GlobalRunError(502, "git_missing", "", "", 0, "git not found"));
    renderStep();
    fireEvent.click(screen.getByRole("button", { name: "Create the folder" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("One more free program is needed to keep that history: Git.");
    expect(within(alert).getByRole("link")).toHaveAttribute("href", GIT_DOWNLOAD_URL);
  });

  it("refuses a blank name or one with characters a folder can't have", async () => {
    const mkdir = vi.spyOn(api, "createDir");
    renderStep();
    const name = screen.getByRole("textbox", { name: "Folder name" });
    fireEvent.change(name, { target: { value: "a?b" } });
    expect(screen.getByRole("alert")).toHaveTextContent("The name can't contain these characters: ?");
    expect(screen.getByRole("button", { name: "Create the folder" })).toBeDisabled();
    fireEvent.change(name, { target: { value: "  " } });
    expect(screen.getByRole("button", { name: "Create the folder" })).toBeDisabled();
    expect(mkdir).not.toHaveBeenCalled();
  });

  it("lets the person use a folder they already have, in plain words", async () => {
    renderStep();
    fireEvent.click(screen.getByRole("button", { name: "Use a folder I already have" }));
    expect(await screen.findByRole("dialog", { name: "Choose a folder for the agent" })).toBeInTheDocument();
  });

  it("renders nothing on a team hub", () => {
    const { container } = renderStep("team");
    expect(container).toBeEmptyDOMElement();
  });
});
