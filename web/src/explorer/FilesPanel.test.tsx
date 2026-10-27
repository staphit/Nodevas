import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useExplorer } from "./explorerStore";
import FilesPanel from "./FilesPanel";
import { repo, workspaceListing } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
  vi.spyOn(explorerApi, "workspaceFiles").mockResolvedValue(workspaceListing);
  vi.spyOn(explorerApi, "repoTree").mockResolvedValue({
    path: "",
    entries: [
      { name: "src", type: "dir", size: 0 },
      { name: "README.md", type: "file", size: 20 },
    ],
    hidden: 2,
    truncated: false,
  });
});
afterEach(() => vi.restoreAllMocks());

// W-10: workspace files and the runners' repos.
describe("FilesPanel", () => {
  it("shows the workspace tree, folding and filtering it", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
    render(<FilesPanel />);
    const workspace = await screen.findByRole("region", { name: /^Workspace/ });
    expect(within(workspace).getByText("login.md")).toBeInTheDocument();
    await userEvent.click(within(workspace).getByRole("button", { name: /docs/ }));
    expect(within(workspace).queryByText("notes.txt")).not.toBeInTheDocument();
    await userEvent.click(within(workspace).getByRole("button", { name: /docs/ }));
    expect(within(workspace).getByText("notes.txt")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("searchbox", { name: "Search file names…" }), "graph");
    expect(within(workspace).getByText("graph.yaml")).toBeInTheDocument();
    expect(within(workspace).queryByText("login.md")).not.toBeInTheDocument();

    await userEvent.clear(screen.getByRole("searchbox", { name: "Search file names…" }));
    await userEvent.click(within(workspace).getByText("login.md"));
    expect(useExplorer.getState().view).toEqual({ kind: "file", source: "workspace", path: "nodes/login.md" });
    expect(screen.getByText("No repos: add one on your runner with nodevas runner repo add.")).toBeInTheDocument();
  });

  it("browses an online repo read-only and says what is hidden", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    render(<FilesPanel />);
    const section = await screen.findByRole("region", { name: "Repo · visual-editor" });
    expect(await within(section).findByText("README.md")).toBeInTheDocument();
    expect(within(section).getByText("Read-only, served by the runner. 2 sensitive files hidden.")).toBeInTheDocument();
    expect(explorerApi.repoTree).toHaveBeenCalledWith("rn1", "r1", "");
  });

  it("explains an offline runner, a runner with browsing off, and a cut-short list", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([
      repo({ repoId: "r2", name: "game", online: false, runnerName: "laptop" }),
      repo({ repoId: "r3", name: "site", browsable: false, reason: "REPO_BROWSE_OFF" }),
    ]);
    vi.spyOn(explorerApi, "workspaceFiles").mockResolvedValue({ ...workspaceListing, truncated: true });
    render(<FilesPanel />);
    const offline = await screen.findByRole("region", { name: "Repo · game" });
    expect(within(offline).getByText("offline")).toBeInTheDocument();
    expect(
      within(offline).getByText("Runner “laptop” is offline, so this repo can't be browsed right now."),
    ).toBeInTheDocument();
    const off = screen.getByRole("region", { name: "Repo · site" });
    expect(
      within(off).getByText("Repo browsing is off on this runner (nodevas runner set repoBrowse on)."),
    ).toBeInTheDocument();
    expect(screen.getByText("Showing the first 5 entries.")).toBeInTheDocument();
    expect(explorerApi.repoTree).not.toHaveBeenCalled();
  });
});
