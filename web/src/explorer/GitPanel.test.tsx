import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { explorerApi } from "../api/explorer";
import { makeRun } from "../game/testFixtures";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useExplorer } from "./explorerStore";
import GitPanel from "./GitPanel";

const sha = "c".repeat(40);

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
  vi.spyOn(explorerApi, "workspaceGit").mockResolvedValue({
    backend: "git",
    branch: "main",
    pending: [
      { path: "nodes/login.md", mark: "M" },
      { path: "nodes/schema.md", mark: "A" },
    ],
    commits: [{ sha, at: "2026-10-28T09:00:00Z", author: "run:codex", subject: "login: done" }],
  });
  vi.spyOn(api, "listProjectRuns").mockResolvedValue({
    runs: [
      makeRun({ id: "r-pass", branch: "agent/login-1", ciStatus: "success", verdict: "approve", repoName: "visual-editor" }),
      makeRun({ id: "r-fail", branch: "agent/schema-2", ciStatus: "failure", repoName: "visual-editor" }),
      makeRun({ id: "r-wait", branch: "agent/docs-3", ciStatus: "pending", repoName: "visual-editor" }),
    ],
  });
});
afterEach(() => vi.restoreAllMocks());

// W-12: workspace changes, agent branches, commits.
describe("GitPanel", () => {
  it("opens a repo's uncommitted diff under the repo's name", async () => {
    vi.spyOn(explorerApi, "repos").mockResolvedValue([
      { runnerId: "rn1", runnerName: "mac-mini", online: true, repoId: "r1", name: "api-server", browsable: true },
    ]);
    vi.spyOn(explorerApi, "repoStatus").mockResolvedValue({
      branch: "main", head: sha, entries: [{ path: "src/math.js", mark: "M" }], hidden: 0, truncated: false,
    });
    render(<GitPanel />);
    await userEvent.click(await screen.findByRole("button", { name: /math\.js/ }));
    expect(useExplorer.getState().view).toEqual({
      kind: "diff", source: "repo", runnerId: "rn1", repoId: "r1", repoName: "api-server", ref: "",
    });
  });

  it("shows the workspace's branch and pending changes", async () => {
    render(<GitPanel />);
    const workspace = await screen.findByRole("region", { name: "Workspace · main" });
    expect(within(workspace).getByText("login.md")).toBeInTheDocument();
    expect(within(workspace).getByText("M")).toBeInTheDocument();
    expect(within(workspace).getByText("A")).toBeInTheDocument();
  });

  it("lists agent branches with CI and verdict, and opens a branch's diff", async () => {
    render(<GitPanel />);
    const branches = await screen.findByRole("region", { name: "Agent branches (visual-editor)" });
    expect(within(branches).getByText("agent/login-1")).toBeInTheDocument();
    expect(within(branches).getByText("CI ✓")).toBeInTheDocument();
    expect(within(branches).getByText("CI ✗")).toBeInTheDocument();
    expect(within(branches).getByText("CI …")).toBeInTheDocument();
    await userEvent.click(within(branches).getByText("agent/schema-2"));
    expect(useExplorer.getState().view).toEqual({ kind: "diff", source: "run", runId: "r-fail" });
  });

  it("opens a commit's diff", async () => {
    render(<GitPanel />);
    const commits = await screen.findByRole("region", { name: "Commits" });
    await userEvent.click(within(commits).getByText("login: done"));
    expect(useExplorer.getState().view).toEqual({ kind: "diff", source: "workspace", sha });
  });

  it("explains a workspace that keeps history as file copies", async () => {
    vi.spyOn(explorerApi, "workspaceGit").mockResolvedValue({ backend: "files", branch: "", pending: [], commits: [] });
    render(<GitPanel />);
    expect(
      await screen.findByText("This workspace keeps history as file copies; the commit view needs git history."),
    ).toBeInTheDocument();
  });

  // Switching project from the header left the panel on the previous
  // project's pending changes and commits until the tab was left and reopened.
  it("loads the new project's history when the project changes", async () => {
    useApp.setState({ activeProject: "gates2k" });
    const workspaceGit = vi.mocked(explorerApi.workspaceGit);
    render(<GitPanel />);
    const commits = await screen.findByRole("region", { name: "Commits" });
    expect(within(commits).getByText("login: done")).toBeInTheDocument();

    let answer!: (value: Awaited<ReturnType<typeof explorerApi.workspaceGit>>) => void;
    workspaceGit.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
    const listRuns = vi.mocked(api.listProjectRuns);
    const runCalls = listRuns.mock.calls.length;
    act(() => useApp.setState({ activeProject: "ai-harness-engineering" }));
    // The old project's history is gone at once, not shown under the new one.
    expect(screen.queryByText("login: done")).not.toBeInTheDocument();
    expect(screen.queryByText("login.md")).not.toBeInTheDocument();
    expect(listRuns.mock.calls.length).toBeGreaterThan(runCalls);

    await act(async () =>
      answer({
        backend: "git",
        branch: "main",
        pending: [],
        commits: [{ sha: "e".repeat(40), at: "2026-10-28T10:00:00Z", author: "patrick", subject: "harness: edit" }],
      }),
    );
    expect(await screen.findByText("harness: edit")).toBeInTheDocument();
    expect(screen.getByText("No pending changes")).toBeInTheDocument();
  });
});
