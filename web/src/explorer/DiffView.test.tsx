import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { explorerApi } from "../api/explorer";
import { makeRun } from "../game/testFixtures";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import DiffView from "./DiffView";
import { useExplorer } from "./explorerStore";

const PATCH = [
  "diff --git a/src/math.js b/src/math.js",
  "--- a/src/math.js",
  "+++ b/src/math.js",
  "@@ -1 +1,2 @@",
  "-export const add = (a, b) => a - b;",
  "+export const add = (a, b) => a + b;",
  "+export const sub = (a, b) => a - b;",
  "",
].join("\n");

const sha = "d".repeat(40);

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
});
afterEach(() => vi.restoreAllMocks());

// W-13: a run's, a workspace commit's and a repo's patch, with the existing
// diff table.
describe("DiffView", () => {
  it("renders a run's patch", async () => {
    vi.spyOn(api, "getRunChanges").mockResolvedValue({
      changes: { commit: sha, base: "main", files: [{ path: "src/math.js", status: "modified", additions: 2, deletions: 1 }], additions: 2, deletions: 1 },
      patch: PATCH,
      patchTruncated: false,
    } as never);
    vi.spyOn(api, "getRun").mockResolvedValue(makeRun({ id: "r1" }));
    render(<DiffView view={{ kind: "diff", source: "run", runId: "r1" }} />);
    expect(await screen.findByText("1 files · +2 −1")).toBeInTheDocument();
    expect(screen.getByRole("table")).toHaveTextContent("export const sub");
  });

  it("renders a workspace commit and finds the agent that made it", async () => {
    vi.spyOn(explorerApi, "workspaceCommit").mockResolvedValue({
      sha,
      at: "2026-10-28T09:00:00Z",
      author: "run:codex",
      subject: "login: done",
      patch: PATCH,
      truncated: false,
    });
    vi.spyOn(explorerApi, "agents").mockResolvedValue({
      live: [],
      external: [],
      recent: [
        {
          kind: "run", id: "run:r1", runId: "r1", agent: "codex", model: "gpt-5.5", role: "executor", state: "succeeded",
          nodeId: "login", nodeTitle: "Login form", tokens: { input: 1, output: 1, cacheRead: 0 }, costUsd: 0.09,
          costEstimated: true, startedAt: "2026-10-28T08:55:00Z", finishedAt: "2026-10-28T09:00:00Z",
          files: [{ path: "src/math.js", mark: "M" }],
        },
      ],
    });
    render(<DiffView view={{ kind: "diff", source: "workspace", sha }} />);
    expect(await screen.findByText("login: done")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Which agent made this" }));
    expect(useExplorer.getState().view).toEqual({ kind: "agent", id: "run:r1" });
  });

  it("says when a person, not an agent, made the change", async () => {
    vi.spyOn(explorerApi, "workspaceCommit").mockResolvedValue({
      sha, at: "2026-10-28T09:00:00Z", author: "bea", subject: "notes", patch: PATCH, truncated: false,
    });
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [] });
    render(<DiffView view={{ kind: "diff", source: "workspace", sha }} />);
    await userEvent.click(await screen.findByRole("button", { name: "Which agent made this" }));
    expect(await screen.findByText("This change was made by a person, not an agent.")).toBeInTheDocument();
  });

  it("renders a repo's uncommitted diff", async () => {
    vi.spyOn(explorerApi, "repoDiff").mockResolvedValue({ ref: "", patch: PATCH, truncated: false, hidden: 1 });
    render(<DiffView view={{ kind: "diff", source: "repo", runnerId: "rn1", repoId: "r1", ref: "" }} />);
    expect(await screen.findByText("Workspace · uncommitted changes")).toBeInTheDocument();
    expect(screen.getByRole("table")).toHaveTextContent("export const add");
  });
});
