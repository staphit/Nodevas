import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useAgents } from "../agents/agentStore";
import type { AgentAsk } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { GameActionsProvider, useGameActions } from "./GameActions";

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
});
