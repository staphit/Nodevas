import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useAgents } from "../agents/agentStore";
import type { AgentAsk } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useWorkspace } from "../runs/workspaceStore";
import { api } from "../api";
import { useRuns } from "../runs/runStore";
import { useGlobalAgents } from "../station/globalAgentsStore";
import { useStation } from "../station/stationStore";
import { GameActionsProvider, GameDialog, useGameActions } from "./GameActions";
import type { ClickTarget } from "./world";

function Trigger({ askId }: { askId: string }) {
  const { dispatch } = useGameActions();
  return <button type="button" onClick={() => dispatch({ kind: "ask", askId })}>Open</button>;
}

describe("Station ask dialog title per kind", () => {
  const setupAsk = (kind: AgentAsk["kind"]) => {
    const ask: AgentAsk = {
      id: `ask-${kind}`,
      project: "alpha",
      sessionId: "s1",
      sessionName: "agent-1",
      actorId: "u1",
      kind,
      question: "Do something?",
      status: "pending",
      createdAt: 1,
    };
    useAgents.setState({ asks: [ask] });
    return ask;
  };

  it("renders Approve / 核准 for approval", () => {
    setupAsk("approval");
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const { rerender } = render(
      <GameActionsProvider>
        <Trigger askId="ask-approval" />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Approve");

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    rerender(
      <GameActionsProvider>
        <Trigger askId="ask-approval" />
      </GameActionsProvider>,
    );
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("核准");
  });

  it("renders Review permission / 審核權限 for permission", () => {
    setupAsk("permission");
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const { rerender } = render(
      <GameActionsProvider>
        <Trigger askId="ask-permission" />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Review permission");

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    rerender(
      <GameActionsProvider>
        <Trigger askId="ask-permission" />
      </GameActionsProvider>,
    );
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("審核權限");
  });

  it("renders Choose / 選擇 for choice", () => {
    setupAsk("choice");
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const { rerender } = render(
      <GameActionsProvider>
        <Trigger askId="ask-choice" />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Choose");

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    rerender(
      <GameActionsProvider>
        <Trigger askId="ask-choice" />
      </GameActionsProvider>,
    );
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("選擇");
  });

  it("renders Answer question / 回答問題 for question", () => {
    setupAsk("question");
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const { rerender } = render(
      <GameActionsProvider>
        <Trigger askId="ask-question" />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Answer question");

    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    rerender(
      <GameActionsProvider>
        <Trigger askId="ask-question" />
      </GameActionsProvider>,
    );
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("回答問題");
  });
});

describe("Station ask dialog answering", () => {
  const permission: AgentAsk = {
    id: "ask-perm",
    project: "alpha",
    sessionId: "s1",
    sessionName: "agent-1",
    actorId: "u1",
    kind: "permission",
    tool: "Bash",
    toolInput: '{"command":"npm test"}',
    runId: "run-1",
    question: "agent-1 wants to run Bash",
    status: "pending",
    createdAt: Date.now(),
  };

  beforeEach(() => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  });

  const open = (askId: string) => {
    render(
      <GameActionsProvider>
        <Trigger askId={askId} />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
  };

  it("opens with focus in the ask card, so its number keys answer", () => {
    const answerAsk = vi.fn(async () => true);
    useAgents.setState({ asks: [permission], answerAsk, viewer: null, error: null });
    open("ask-perm");
    const card = screen.getByRole("group", { name: /tool permission/ });
    expect(card.contains(document.activeElement)).toBe(true);
    fireEvent.keyDown(document.activeElement!, { key: "2" });
    expect(answerAsk).toHaveBeenCalledWith("ask-perm", { decision: "approve_run" });
  });

  it("stays open and shows why when the answer fails, and closes once it is taken", async () => {
    let ok = false;
    const answerAsk = vi.fn(async () => {
      if (!ok) useAgents.setState({ error: "answer exceeds 4000 characters" });
      return ok;
    });
    useAgents.setState({ asks: [permission], answerAsk, viewer: null, error: null });
    open("ask-perm");
    fireEvent.click(screen.getByRole("button", { name: /Deny/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("answer exceeds 4000 characters");
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    ok = true;
    fireEvent.click(screen.getByRole("button", { name: /Deny/ }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("shows another person's ask without answer controls to a member", () => {
    const answerAsk = vi.fn(async () => true);
    useAgents.setState({
      asks: [permission],
      answerAsk,
      viewer: { id: "bob", name: "bob", role: "member" },
      error: null,
    });
    open("ask-perm");
    expect(screen.getByText(/Only the runner's owner/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Allow|Deny/ })).toBeNull();
    fireEvent.keyDown(screen.getByRole("group", { name: /tool permission/ }), { key: "1" });
    expect(answerAsk).not.toHaveBeenCalled();
  });

  it("follows the hub's canAnswer: an admin may not answer another member's permission prompt", () => {
    const answerAsk = vi.fn(async () => true);
    useAgents.setState({
      asks: [{ ...permission, canAnswer: false }],
      answerAsk,
      viewer: { id: "root", name: "root", role: "admin" },
      error: null,
    });
    open("ask-perm");
    expect(screen.getByText(/Only the runner's owner/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Allow|Deny/ })).toBeNull();
    fireEvent.keyDown(screen.getByRole("group", { name: /tool permission/ }), { key: "3" });
    expect(answerAsk).not.toHaveBeenCalled();
  });

  it("follows the hub's canAnswer when it lets the viewer answer", () => {
    useAgents.setState({
      asks: [{ ...permission, canAnswer: true }],
      answerAsk: vi.fn(async () => true),
      viewer: { id: "bob", name: "bob", role: "member" },
      error: null,
    });
    open("ask-perm");
    expect(screen.getByRole("button", { name: /Deny/ })).toBeInTheDocument();
  });
});

describe("run-workspace", () => {
  function Dispatch({ target }: { target: ClickTarget }) {
    const { dispatch } = useGameActions();
    return (
      <button type="button" onClick={() => dispatch(target)}>
        Go
      </button>
    );
  }

  it("opens the run's workspace through the app's one host, not a dialog of its own", () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const openWorkspace = vi.fn();
    useWorkspace.setState({ openWorkspace });
    render(
      <GameActionsProvider>
        <Dispatch target={{ kind: "run-workspace", runId: "run7", nodeId: "alpha" }} />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(openWorkspace).toHaveBeenCalledWith("run7", undefined);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("global agents", () => {
  function Dispatch({ target }: { target: ClickTarget }) {
    const { dispatch } = useGameActions();
    return (
      <button type="button" onClick={() => dispatch(target)}>
        Go
      </button>
    );
  }

  beforeEach(() => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en", projectMode: "info" }, activeProject: "p" });
    useStation.setState({ tab: "project" });
    useGlobalAgents.setState({ focus: null });
  });

  it("opens Station on its Global agents tab with the run highlighted", () => {
    render(
      <GameActionsProvider>
        <Dispatch target={{ kind: "global-agents", runId: "g1" }} />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(useApp.getState().preferences.projectMode).toBe("station");
    expect(useStation.getState().tab).toBe("global");
    expect(useGlobalAgents.getState().focus).toBe("g1");
  });

  it("opens a global run's workspace without switching the board's project", () => {
    const openWorkspace = vi.fn();
    const switchProject = vi.fn(async () => undefined);
    useWorkspace.setState({ openWorkspace });
    useApp.setState({ switchProject } as never);
    render(
      <GameActionsProvider>
        <Dispatch target={{ kind: "run-workspace", runId: "g1", nodeId: "" }} />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(openWorkspace).toHaveBeenCalledWith("g1", undefined);
    expect(switchProject).not.toHaveBeenCalled();
    expect(useApp.getState().activeProject).toBe("p");
  });

  it("finds a global run's diff in the run store", async () => {
    const run = {
      id: "g2",
      project: "",
      nodeId: "",
      global: true,
      title: "Bump the deps",
      runnerId: "r1",
      runnerName: "desktop",
      agent: "claude",
      model: "opus",
      repoId: "repo1",
      repoName: "nodevas",
      base: "main",
      branch: "agent/bump-the-deps-g2",
      prompt: "",
      status: "succeeded",
      exitCode: 0,
      commit: "abc",
      createdBy: "u1",
      createdByName: "alice",
      createdAt: "2026-11-12T08:00:00Z",
    };
    useRuns.setState({ globalById: { g2: run } });
    vi.spyOn(api, "getRunChanges").mockResolvedValue({ changes: null });
    render(
      <GameActionsProvider>
        <Dispatch target={{ kind: "run-diff", runId: "g2", nodeId: "" }} />
      </GameActionsProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    await waitFor(() => expect(api.getRunChanges).toHaveBeenCalledWith("g2"));
    expect(screen.queryByText("This run is no longer loaded.")).not.toBeInTheDocument();
    useRuns.setState({ globalById: {} });
  });
});

describe("GameDialog Escape inside xterm", () => {
  it("does not swallow Escape when focus is inside .xterm, but still closes outside .xterm", () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const onClose = vi.fn();
    const xtermKeyListener = vi.fn();

    render(
      <GameDialog title="Expanded session" onClose={onClose}>
        <div className="xterm" onKeyDown={xtermKeyListener}>
          <textarea aria-label="terminal input" />
        </div>
        <button type="button">Outside terminal</button>
      </GameDialog>,
    );

    const xtermTextarea = screen.getByLabelText("terminal input");
    fireEvent.keyDown(xtermTextarea, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
    expect(xtermKeyListener).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(screen.getByRole("button", { name: "Outside terminal" }), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
