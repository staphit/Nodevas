import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunEvent } from "../api";
import { ConfirmDialogHost } from "../components/ConfirmDialog";
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
    prompt: "Keep the diff small.",
    status: "running",
    exitCode: 0,
    usage: { inputTokens: 12000, outputTokens: 345, costUsd: 0.042 },
    createdBy: "u1",
    createdByName: "alice",
    createdAt: "2026-10-24T08:00:00Z",
    ...overrides,
  };
}

const at = "2026-10-24T08:00:01.000Z";
const events: RunEvent[] = [
  { seq: 1, type: "log", at, text: "worktree created" },
  { seq: 2, type: "thinking", at, text: "Need to read the router first." },
  { seq: 3, type: "text", at, text: "First paragraph.\n\nSecond paragraph." },
  { seq: 4, type: "tool_call", at, tool: "Read", toolId: "t1", input: '{"file_path":"main.go"}' },
  { seq: 5, type: "tool_result", at, toolId: "t1", text: "package main" },
  { seq: 6, type: "tool_call", at, tool: "Bash", toolId: "t2", input: '{"command":"go test"}' },
  { seq: 7, type: "tool_result", at, toolId: "t2", text: "FAIL", isError: true },
  {
    seq: 8,
    type: "usage",
    at,
    usage: { inputTokens: 1200, outputTokens: 34, cacheReadTokens: 800, costUsd: 0.0123 },
  },
  { seq: 9, type: "error", at, text: "rate limited" },
];

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRuns.getState().reset();
  useRuns.setState({ watched: {} });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("NodeRuns", () => {
  it("lists a node's runs with status, agent, runner, repo, branch and usage", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({
      runs: [makeRun(), makeRun({ id: "run0", status: "succeeded", agent: "codex", model: "gpt-5", usage: undefined })],
    });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });

    render(<NodeRuns id="alpha" />);

    const list = await screen.findByRole("list", { name: "Runs" });
    const rows = within(list).getAllByRole("button");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveAttribute("aria-pressed", "true");
    expect(within(rows[0]).getByText("Running")).toBeInTheDocument();
    expect(within(rows[0]).getByText("claude · opus")).toBeInTheDocument();
    expect(within(rows[0]).getByText("desktop")).toBeInTheDocument();
    expect(within(rows[0]).getByText("nodevas")).toBeInTheDocument();
    expect(within(rows[0]).getByText("agent/alpha-run1")).toBeInTheDocument();
    expect(within(rows[0]).getByText(/12\.3K tokens · \$0\.042/)).toBeInTheDocument();
    expect(within(rows[1]).getByText("Succeeded")).toBeInTheDocument();
    expect(within(rows[1]).getByText("codex · gpt-5")).toBeInTheDocument();
  });

  it("shows an empty state and opens the dispatch dialog", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: null });
    render(<NodeRuns id="alpha" />);
    expect(await screen.findByText(/No runs for this node yet/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dispatch to agent…" }));
    expect(useRuns.getState().dispatchNodeId).toBe("alpha");
  });

  it("renders every event type of the selected run", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun()] });
    const getEvents = vi.spyOn(api, "getRunEvents").mockResolvedValue({ events, more: false });

    render(<NodeRuns id="alpha" />);

    const timeline = await screen.findByRole("list", { name: "Run activity" });
    await within(timeline).findByText("worktree created");
    expect(getEvents).toHaveBeenCalledWith("run1", 0);
    expect(useRuns.getState().watched.run1).toBe(1);

    // text: one paragraph per blank-line-separated block
    expect(within(timeline).getByText("First paragraph.").tagName).toBe("P");
    expect(within(timeline).getByText("Second paragraph.").tagName).toBe("P");

    // thinking: collapsed by default
    const thinking = within(timeline).getByText("Thinking").closest("details")!;
    expect(thinking).not.toHaveAttribute("open");
    expect(thinking.closest("li")).toHaveClass("run-ev-thinking");

    // tool_call: name and a compact input preview, full input on expand
    const read = within(timeline).getByText("Read").closest("details")!;
    expect(within(read).getByText('{"file_path":"main.go"}')).toBeInTheDocument();
    expect(read.querySelector("pre")?.textContent).toBe('{\n  "file_path": "main.go"\n}');

    // tool_result: says which tool it answers; errors are marked
    const ok = within(timeline).getByText("Result · Read").closest("li")!;
    expect(ok).not.toHaveClass("is-error");
    const failed = within(timeline).getByText("Error result · Bash").closest("li")!;
    expect(failed).toHaveClass("is-error");

    // usage
    expect(within(timeline).getByText(/1,200 in · 34 out · 800 cache read · \$0\.012$/)).toBeInTheDocument();

    // error
    const error = within(timeline).getByText("rate limited").closest("li")!;
    expect(error).toHaveClass("run-ev-error");
    expect(error).toHaveAttribute("role", "alert");

    // log
    expect(within(timeline).getByText("worktree created")).toHaveClass("run-ev-log");

    // the dispatcher's instructions are kept with the run
    expect(screen.getByText("Keep the diff small.")).toBeInTheDocument();
  });

  it("appends pushed events to the open run", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun()] });
    const getEvents = vi
      .spyOn(api, "getRunEvents")
      .mockResolvedValueOnce({ events: [events[0]], more: false })
      .mockResolvedValue({ events: [events[0], { seq: 2, type: "text", at, text: "Done reading." }], more: false });

    render(<NodeRuns id="alpha" />);
    const timeline = await screen.findByRole("list", { name: "Run activity" });
    await within(timeline).findByText("worktree created");

    useRuns.getState().onRunEvents("run1");
    expect(await within(timeline).findByText("Done reading.")).toBeInTheDocument();
    expect(getEvents).toHaveBeenLastCalledWith("run1", 1);
    expect(within(timeline).getAllByText("worktree created")).toHaveLength(1);
  });

  it("explains an offered run and cancels it after confirmation", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun({ status: "offered", usage: undefined })] });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
    const cancel = vi.spyOn(api, "cancelRun").mockResolvedValue(makeRun({ status: "cancelled" }));

    render(
      <>
        <NodeRuns id="alpha" />
        <ConfirmDialogHost />
      </>,
    );

    expect(await screen.findByText("Waiting for acceptance at the runner.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(cancel).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel run" }));

    await waitFor(() => expect(cancel).toHaveBeenCalledWith("run1"));
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument(),
    );
    expect(screen.getAllByText("Cancelled").length).toBeGreaterThan(0);
  });

  it("says the stop is on its way while a cancelled run is still running", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [makeRun()] });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });
    vi.spyOn(api, "cancelRun").mockResolvedValue(makeRun());

    render(
      <>
        <NodeRuns id="alpha" />
        <ConfirmDialogHost />
      </>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Cancel run" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/stop the agent/);
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel run" }));

    expect(await screen.findByText(/Stop requested/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument();
  });

  it("shows the summary of a finished run and no cancel button", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({
      runs: [makeRun({ status: "succeeded", summary: "Added the route.\n\nTests pass." })],
    });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });

    render(<NodeRuns id="alpha" />);
    expect(await screen.findByText("Added the route.")).toBeInTheDocument();
    expect(screen.getByText("Tests pass.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument();
  });

  it("shows the error and exit code of a failed run", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({
      runs: [makeRun({ status: "failed", error: "claude exited", exitCode: 2 })],
    });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });

    render(<NodeRuns id="alpha" />);
    expect(await screen.findByText("claude exited")).toBeInTheDocument();
    expect(screen.getByText("Exit code 2")).toBeInTheDocument();
  });

  it("switches the live view to another run", async () => {
    vi.spyOn(api, "listRuns").mockResolvedValue({
      runs: [makeRun(), makeRun({ id: "run0", status: "succeeded", summary: "Earlier work." })],
    });
    vi.spyOn(api, "getRunEvents").mockResolvedValue({ events: [], more: false });

    render(<NodeRuns id="alpha" />);
    const list = await screen.findByRole("list", { name: "Runs" });
    fireEvent.click(within(list).getAllByRole("button")[1]);

    expect(await screen.findByText("Earlier work.")).toBeInTheDocument();
    expect(useRuns.getState().selectedRun.alpha).toBe("run0");
    await waitFor(() => expect(useRuns.getState().watched.run1).toBeUndefined());
    expect(useRuns.getState().watched.run0).toBe(1);
  });
});
