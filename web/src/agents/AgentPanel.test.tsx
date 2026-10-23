import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { AgentPanel } from "./AgentPanel";
import { useAgents } from "./agentStore";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useAgents.setState({
    open: true,
    showOffline: false,
    selectedRecipient: "api-claude",
    replyToId: "",
    filterNodeId: "",
    sessions: [
      {
        id: "claude:1",
        project: "alpha",
        actorId: "alice",
        actorName: "alice",
        name: "api-claude",
        agent: "claude",
        role: "worker",
        repo: "nodevas",
        branch: "main",
        state: "busy",
        since: 1700000000000,
        doing: "Wire auth middleware",
        nodeId: "task-1",
        lastSeen: 1700000000000,
        online: true,
      },
    ],
    users: [],
    messages: [
      {
        id: "m1",
        project: "alpha",
        from: "bob/web-codex",
        senderActor: "bob",
        senderSession: "web-codex",
        senderAgent: "codex",
        to: "api-claude",
        nodeId: "task-1",
        body: "Schema ready for review",
        createdAt: 1700000000000,
      },
    ],
    asks: [
      {
        id: "ask-1",
        project: "alpha",
        sessionId: "claude:1",
        sessionName: "api-claude",
        actorId: "alice",
        agent: "claude",
        question: "Drop legacy column?",
        nodeId: "task-1",
        status: "pending",
        createdAt: 1700000000000,
      },
    ],
    submittingAskIds: {},
    sending: false,
    error: null,
  });
});

describe("AgentPanel", () => {
  it("renders roster, messages, and pending ask_user card", () => {
    const answerAsk = vi.fn().mockResolvedValue(undefined);
    useAgents.setState({ answerAsk });

    render(<AgentPanel onClose={vi.fn()} />);

    expect(screen.getByText("alice/api-claude")).toBeInTheDocument();
    expect(screen.getByText("nodevas@main")).toBeInTheDocument();
    expect(screen.getByText("Wire auth middleware")).toBeInTheDocument();
    expect(screen.getByText("Schema ready for review")).toBeInTheDocument();
    expect(screen.getByText("Drop legacy column?")).toBeInTheDocument();

    const noteInput = screen.getByPlaceholderText("Optional note for the agent…");
    fireEvent.change(noteInput, { target: { value: "After backup" } });
    fireEvent.click(screen.getByRole("button", { name: /Approve/i }));

    expect(answerAsk).toHaveBeenCalledWith("ask-1", "approved", "After backup");
  });
});
