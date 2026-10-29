import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
