import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, WorkspaceActionError, type Run, type RunGroup } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { makeView } from "../runs/workspaceFixtures";
import { useWorkspace } from "../runs/workspaceStore";
import { useApp } from "../store";
import { GlobalAgentGroup } from "./GlobalAgentGroup";
import { useGlobalAgents } from "./globalAgentsStore";

function makeMember(overrides: Partial<Run> = {}): Run {
  return {
    id: "m1",
    project: "",
    nodeId: "",
    global: true,
    groupId: "grp1",
    title: "Upgrade React",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "api",
    base: "main",
    branch: "agent/upgrade-react-m1000000",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    commit: "abc1234",
    noPR: true,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-11-20T08:00:00Z",
    startedAt: "2026-11-20T08:00:05Z",
    finishedAt: "2026-11-20T08:05:00Z",
    ...overrides,
  };
}

function makeGroup(overrides: Partial<RunGroup> = {}): RunGroup {
  return {
    id: "grp1",
    runnerId: "r1",
    createdBy: "u1",
    title: "Upgrade React",
    agent: "claude",
    model: "opus",
    noPR: true,
    repoCount: 3,
    createdAt: "2026-11-20T08:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useGlobalAgents.getState().reset();
  useRuns.getState().reset();
  useRuns.setState({ globalById: {}, cancelling: {}, cancelErrors: {}, stopRequested: {} });
  useWorkspace.getState().reset();
  useRunners.setState({ runners: [], loaded: false });
  vi.spyOn(api, "getRunWorkspace").mockResolvedValue(makeView());
  vi.spyOn(api, "getGlobalRunThread").mockResolvedValue({ runs: [] });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "getRunAsks").mockResolvedValue({ asks: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GlobalAgentGroup", () => {
  it("shows group counts, per-repo pips, waiting count, Paused until with Resume, and budget stop", async () => {
    const future = new Date(Date.now() + 30 * 60_000).toISOString();
    const group = makeGroup({ repoCount: 4, heldUntil: future });
    const members = [
      makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" }),
      makeMember({ id: "m2", repoId: "r-web", repoName: "web", status: "succeeded", commit: undefined }),
      makeMember({ id: "m3", repoId: "r-worker", repoName: "worker", status: "queued", commit: undefined, finishedAt: undefined }),
      makeMember({ id: "m4", repoId: "r-billing", repoName: "billing", status: "cancelled", failureCode: "budget", commit: undefined }),
    ];
    useGlobalAgents.setState({ groups: { grp1: group }, groupRuns: { grp1: members } });
    const resume = vi.spyOn(api, "resumeGlobalGroup").mockResolvedValue({ group: { ...group, heldUntil: "" } });

    render(<GlobalAgentGroup groupId="grp1" runs={members} expanded own />);

    expect(screen.getByText("4 folders")).toBeInTheDocument();
    expect(screen.getByText("3 of 4 done")).toBeInTheDocument();
    expect(screen.getByText("1 waiting their turn")).toBeInTheDocument();
    expect(screen.getByText(/Paused until .*: usage limit/)).toBeInTheDocument();
    expect(screen.getByText("Stopped: today's cost or token limit was reached")).toBeInTheDocument();
    expect(screen.getByText("Nothing to change here")).toBeInTheDocument();

    const pips = screen.getByRole("list", { name: /4 folders/i });
    expect(within(pips).getAllByRole("listitem")).toHaveLength(4);

    fireEvent.click(screen.getByRole("button", { name: "Resume now" }));
    await waitFor(() => expect(resume).toHaveBeenCalledWith("grp1"));
  });

  it("applies all finished members sequentially, skipping ineligible ones and reporting conflicts", async () => {
    const m1 = makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" });
    const m2 = makeMember({ id: "m2", repoId: "r-web", repoName: "web", status: "succeeded", commit: "bbb2222" });
    const m3 = makeMember({ id: "m3", repoId: "r-cli", repoName: "cli", status: "failed", commit: undefined });
    const group = makeGroup({ repoCount: 3 });
    useGlobalAgents.setState({ groups: { grp1: group }, groupRuns: { grp1: [m1, m2, m3] } });

    const merge = vi
      .spyOn(api, "mergeRunLocal")
      .mockResolvedValueOnce({ ...m1, workspace: "merged_local" })
      .mockRejectedValueOnce(new WorkspaceActionError(409, "FAILED_PRECONDITION", "conflict", "CONFLICT in src/index.ts"));
    vi.spyOn(api, "getRun").mockResolvedValue(m2);

    render(
      <>
        <GlobalAgentGroup groupId="grp1" runs={[m1, m2, m3]} expanded own />
        <ConfirmDialogHost />
      </>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Apply all finished" }));
    await waitFor(() => expect(merge).toHaveBeenCalledTimes(2));
    expect(merge).toHaveBeenNthCalledWith(1, "m1", "aaa1111");
    expect(merge).toHaveBeenNthCalledWith(2, "m2", "bbb2222");

    const report = await screen.findByTestId("group-apply-report");
    expect(report).toHaveTextContent("api");
    expect(report).toHaveTextContent("web");
    expect(report).toHaveTextContent(/conflict/i);
  });

  it("sends tell-failed via group follow-up API", async () => {
    const m1 = makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" });
    const m2 = makeMember({ id: "m2", repoId: "r-web", repoName: "web", status: "failed", commit: undefined });
    const group = makeGroup({ repoCount: 2 });
    useGlobalAgents.setState({ groups: { grp1: group }, groupRuns: { grp1: [m1, m2] } });

    const fu = vi.spyOn(api, "followUpGlobalGroup").mockResolvedValue({
      started: [makeMember({ id: "m2b", continueOf: "m2", workRoot: "m2", repoId: "r-web", repoName: "web", status: "queued" })],
      skipped: [],
    });

    render(<GlobalAgentGroup groupId="grp1" runs={[m1, m2]} expanded own />);

    fireEvent.click(screen.getByRole("button", { name: "Tell failed" }));
    const box = screen.getByRole("textbox");
    fireEvent.change(box, { target: { value: "Fix the failing lint rule" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() =>
      expect(fu).toHaveBeenCalledWith("grp1", { message: "Fix the failing lint rule", only: "failed" }),
    );
  });

  it("says how many were skipped and why, and closes the tell box once some started", async () => {
    const m1 = makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" });
    const m2 = makeMember({ id: "m2", repoId: "r-web", repoName: "web", status: "succeeded", commit: "bbb2222" });
    const m3 = makeMember({ id: "m3", repoId: "r-db", repoName: "db", status: "succeeded", commit: "ccc3333" });
    useGlobalAgents.setState({ groups: { grp1: makeGroup({ repoCount: 3 }) }, groupRuns: { grp1: [m1, m2, m3] } });
    vi.spyOn(api, "followUpGlobalGroup").mockResolvedValue({
      started: [makeMember({ id: "m1b", continueOf: "m1", workRoot: "m1", repoId: "r-api", status: "queued" })],
      skipped: [
        { runId: "m2", code: "WORK_GONE" },
        { runId: "m3", code: "BUDGET_EXCEEDED" },
      ],
    });

    render(<GlobalAgentGroup groupId="grp1" runs={[m1, m2, m3]} expanded own />);
    fireEvent.click(screen.getByRole("button", { name: "Tell all" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Bump the version" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    const report = await screen.findByTestId("group-tell-report");
    expect(report).toHaveTextContent("2 skipped");
    expect(report).toHaveTextContent("web: This work's folder is gone");
    expect(report).toHaveTextContent("db: today's limit was reached");
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  // The hub's generic skip codes (groupFollowUpSkipCode) read as plain
  // sentences in Simple: never a raw code.
  it("words every skip in Simple without a raw code", async () => {
    const codes = ["FAILED_PRECONDITION", "NOT_FOUND", "INVALID_ARGUMENT", "BUDGET_EXCEEDED", "SOMETHING_NEW"];
    const members = codes.map((_, index) =>
      makeMember({ id: `m${index}`, repoId: `r${index}`, repoName: `folder${index}`, status: "succeeded", commit: "aaa1111" }),
    );
    useGlobalAgents.setState({ groups: { grp1: makeGroup({ repoCount: members.length }) }, groupRuns: { grp1: members } });
    vi.spyOn(api, "followUpGlobalGroup").mockResolvedValue({
      started: [],
      skipped: codes.map((code, index) => ({ runId: `m${index}`, code })),
    });

    render(<GlobalAgentGroup groupId="grp1" runs={members} expanded own simple />);
    fireEvent.click(screen.getByRole("button", { name: "Tell all" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Bump the version" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    const report = await screen.findByTestId("group-tell-report");
    for (const code of codes) expect(report.textContent).not.toContain(code);
    expect(report).toHaveTextContent("folder0: it can't go on from where it is right now");
    expect(report).toHaveTextContent("folder1: this work isn't here anymore");
    expect(report).toHaveTextContent("folder2: this one can't take another message");
    expect(report).toHaveTextContent("folder3: today's limit was reached");
    expect(report).toHaveTextContent("folder4: it didn't go on this time");
  });

  it("keeps the text when nothing started", async () => {
    const m1 = makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" });
    useGlobalAgents.setState({ groups: { grp1: makeGroup({ repoCount: 1 }) }, groupRuns: { grp1: [m1] } });
    vi.spyOn(api, "followUpGlobalGroup").mockResolvedValue({
      started: [],
      skipped: [{ runId: "m1", code: "IN_USE" }],
    });

    render(<GlobalAgentGroup groupId="grp1" runs={[m1]} expanded own />);
    fireEvent.click(screen.getByRole("button", { name: "Tell all" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Bump the version" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    const report = await screen.findByTestId("group-tell-report");
    expect(report).toHaveTextContent("Nothing started; your text is kept");
    expect(report).toHaveTextContent("1 skipped");
    expect(report).toHaveTextContent("api: Another turn of this work is still running");
    expect(screen.getByRole("textbox")).toHaveValue("Bump the version");
  });

  it("opens a member's dialog when its folder card is clicked", () => {
    const m1 = makeMember({ id: "m1", repoId: "r-api", repoName: "api", status: "succeeded", commit: "aaa1111" });
    const m2 = makeMember({ id: "m2", repoId: "r-web", repoName: "web", status: "running", commit: undefined, finishedAt: undefined });
    useRuns.getState().putRuns([m1, m2]);
    useGlobalAgents.setState({ groups: { grp1: makeGroup({ repoCount: 2 }) }, groupRuns: { grp1: [m1, m2] } });

    render(<GlobalAgentGroup groupId="grp1" runs={[m1, m2]} expanded own />);
    fireEvent.click(screen.getByRole("button", { name: /web/i }));
    expect(screen.getByRole("region", { name: /Claude opus's session/i })).toBeInTheDocument();
  });
});
