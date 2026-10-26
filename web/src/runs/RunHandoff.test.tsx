import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
import { formatLocalizedTime } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { NodeRuns } from "./NodeRuns";
import { useRuns } from "./runStore";

function makeRun(overrides: Partial<Run> = {}): Run {
  return {
    id: "run1",
    project: "p",
    nodeId: "alpha",
    runnerId: "r1",
    runnerName: "desktop",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/alpha-run1",
    prompt: "",
    status: "failed",
    exitCode: 1,
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

const retryAt = "2026-10-24T15:01:00.000000Z";

// The first run hit its quota and kept its work; the second waits for the
// limit to reset, continuing from it.
const failed = makeRun({
  failureKind: "quota",
  error: "Claude AI usage limit reached",
  finishedAt: "2026-10-24T08:30:00Z",
  handoffBranch: "nodevas/handoff/run1",
  handoffNote: "Run run1 stopped: quota.\n## Its plan\n- [pending] Write the tests",
});
const waiting = makeRun({
  id: "run2",
  status: "queued",
  exitCode: 0,
  branch: "",
  retryOf: "run1",
  continuesFrom: "nodevas/handoff/run1",
  retryAt,
  createdAt: "2026-10-24T08:30:01Z",
});

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
  vi.spyOn(api, "getSessionChanges").mockResolvedValue({ changes: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("run handoff", () => {
  it("shows a run waiting for its usage limit to reset, and retries it now", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [waiting, failed] });
    const retry = vi
      .spyOn(api, "retryRunNow")
      .mockResolvedValue({ ...waiting, status: "offered", retryAt: undefined });

    render(<NodeRuns id="alpha" />);

    const label = `Waiting for limit reset · retry at ${formatLocalizedTime(retryAt, "en")}`;
    expect((await screen.findAllByText(label)).length).toBeGreaterThanOrEqual(2);
    const panel = screen.getByRole("status");
    expect(within(panel).getByText(/offered again at/)).toBeInTheDocument();
    // The wait's own cancel lives in the panel; the header has none.
    expect(screen.getAllByRole("button", { name: "Cancel run" })).toHaveLength(1);
    // It continues the first run's work, whose note it shows.
    expect(screen.getAllByText("Continues #1's work").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "#1 · claude/opus" })).toBeInTheDocument();
    expect(screen.getByText("Handoff note")).toBeInTheDocument();
    expect(screen.getByText(/Write the tests/)).toBeInTheDocument();

    fireEvent.click(within(panel).getByRole("button", { name: "Retry now" }));
    await waitFor(() => expect(retry).toHaveBeenCalledWith("run2"));
    await waitFor(() => expect(screen.queryByText(label)).not.toBeInTheDocument());
    expect(screen.getAllByText("Awaiting acceptance").length).toBeGreaterThan(0);
  });

  it("cancels a wait after confirmation", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [waiting, failed] });
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue({ ...waiting, status: "cancelled" });

    render(
      <>
        <NodeRuns id="alpha" />
        <ConfirmDialogHost />
      </>,
    );
    const panel = await screen.findByRole("status");
    fireEvent.click(within(panel).getByRole("button", { name: "Cancel run" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel run" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledWith("run2"));
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });

  it("says who a run handed its work off to, and links there", async () => {
    const fallback = makeRun({
      id: "run2",
      agent: "codex",
      model: "gpt-5",
      status: "running",
      exitCode: 0,
      retryOf: "run1",
      continuesFrom: "nodevas/handoff/run1",
      createdAt: "2026-10-24T08:30:01Z",
    });
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [fallback, failed] });

    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    fireEvent.click(within(list).getAllByRole("button")[1]);

    expect((await screen.findAllByText("Handed off to codex/gpt-5")).length).toBeGreaterThan(0);
    expect(screen.getByText(/Partial work kept on nodevas\/handoff\/run1/)).toBeInTheDocument();
    expect(screen.getByText(/Write the tests/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "#2 · codex/gpt-5" }));
    await waitFor(() => expect(useRuns.getState().selectedRun.alpha).toBe("run2"));
  });
});
