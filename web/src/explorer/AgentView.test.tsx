import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { explorerApi } from "../api/explorer";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import AgentView from "./AgentView";
import { useExplorer } from "./explorerStore";
import { agentItem, repo } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useExplorer.setState({ view: null });
  vi.spyOn(api, "getRunEvents").mockResolvedValue({
    events: [
      { seq: 1, type: "text", at: "2026-10-28T09:00:05Z", text: "Reading the tests." },
      { seq: 2, type: "tool_call", at: "2026-10-28T09:01:00Z", tool: "Edit", toolId: "t1", input: "{}" },
    ],
    more: false,
  });
  vi.spyOn(api, "listProjectRuns").mockResolvedValue({ runs: [] });
});
afterEach(() => vi.restoreAllMocks());

// W-15: one agent's cards, files and timeline.
describe("AgentView", () => {
  it("shows tokens, cache, cost with its estimate flag, model, time, runner and verdict", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({
      live: [],
      external: [],
      recent: [
        agentItem({
          role: "reviewer",
          verdict: { outcome: "fail", severity: "high", reasons: ["tests_weak", "regression"] },
        }),
      ],
    });
    vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    render(<AgentView id="run:r1" />);
    expect(await screen.findByText("Tokens (in / out)")).toBeInTheDocument();
    expect(screen.getByText("52K / 3.4K")).toBeInTheDocument();
    expect(screen.getByText("cache read 12K")).toBeInTheDocument();
    expect(screen.getByText("$0.09 (est.)")).toBeInTheDocument();
    expect(screen.getByText("gpt-5.5")).toBeInTheDocument();
    expect(screen.getByText("mac-mini")).toBeInTheDocument();
    const verdict = screen.getByText("Verdict").parentElement as HTMLElement;
    expect(within(verdict).getByText("Fail")).toBeInTheDocument();
    expect(within(verdict).getByText("High")).toBeInTheDocument();
    expect(within(verdict).getByText("Weak tests")).toBeInTheDocument();
    expect(within(verdict).getByText("Regression")).toBeInTheDocument();

    const timeline = screen.getByRole("region", { name: "Event log" });
    expect(within(timeline).getByText("Reading the tests.")).toBeInTheDocument();
  });

  it("opens a touched file, unless its runner is offline", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [agentItem()] });
    const repos = vi.spyOn(explorerApi, "repos").mockResolvedValue([repo()]);
    const { unmount } = render(<AgentView id="run:r1" />);
    const files = await screen.findByRole("region", { name: "Files touched" });
    await userEvent.click(within(files).getByText("src/math.js"));
    expect(useExplorer.getState().view).toEqual({
      kind: "file", source: "repo", runnerId: "rn1", runnerName: "mac-mini", repoId: "r1", path: "src/math.js",
    });
    unmount();

    useExplorer.setState({ view: null });
    repos.mockResolvedValue([repo({ online: false })]);
    render(<AgentView id="run:r1" />);
    const again = await screen.findByRole("region", { name: "Files touched" });
    await userEvent.click(within(again).getByText("src/math.js"));
    expect(useExplorer.getState().view).toBeNull();
    expect(await screen.findByText("The runner is offline; this repo file can't open.")).toBeInTheDocument();
  });

  it("shows red and green verify dots on a run's timeline", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [agentItem({ testState: "green" })] });
    vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
    render(<AgentView id="run:r1" />);
    const timeline = await screen.findByRole("region", { name: "Event log" });
    expect(timeline.querySelector(".ev .dot.is-green")).not.toBeNull();
  });

  it("builds an external session's timeline from its actions", async () => {
    vi.spyOn(explorerApi, "agents").mockResolvedValue({
      live: [],
      external: [agentItem({ kind: "session", id: "session:s1", sessionId: "s1", runId: undefined, role: undefined, state: "online" })],
      recent: [],
    });
    vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
    vi.spyOn(explorerApi, "sessionActions").mockResolvedValue([
      { at: "2026-10-28T09:00:00Z", action: "joined", nodeId: "", detail: "" },
      { at: "2026-10-28T09:01:00Z", action: "doing", nodeId: "login", detail: "writing tests" },
      { at: "2026-10-28T09:02:00Z", action: "message", nodeId: "", detail: "other-claude" },
      { at: "2026-10-28T09:03:00Z", action: "changes", nodeId: "login", detail: "1" },
      { at: "2026-10-28T09:04:00Z", action: "changes", nodeId: "login", detail: "3" },
    ]);
    render(<AgentView id="session:s1" />);
    const timeline = await screen.findByRole("region", { name: "Event log" });
    expect(within(timeline).getByText("Joined")).toBeInTheDocument();
    expect(within(timeline).getByText("Now: writing tests")).toBeInTheDocument();
    expect(within(timeline).getByText("Sent a message to other-claude")).toBeInTheDocument();
    expect(within(timeline).getByText("Reported changes to 1 file")).toBeInTheDocument();
    expect(within(timeline).getByText("Reported changes to 3 files")).toBeInTheDocument();
    expect(api.getRunEvents).not.toHaveBeenCalled();
  });
});
