import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentAsk } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { AskCard } from "./AskCard";

const baseAsk: AgentAsk = {
  id: "ask-1",
  project: "alpha",
  sessionId: "s1",
  sessionName: "api-claude",
  actorId: "alice",
  agent: "claude",
  question: "Proceed with database migration?",
  status: "pending",
  createdAt: Date.now(),
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("AskCard by kind", () => {
  it("renders approval ask as today with approve and deny buttons and keyboard 1/2", async () => {
    const user = userEvent.setup();
    const onAnswer = vi.fn();
    render(<AskCard ask={{ ...baseAsk, kind: "approval" }} onAnswer={onAnswer} />);

    expect(screen.getByText("Proceed with database migration?")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Approve/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Deny/ })).toBeInTheDocument();

    const noteInput = screen.getByPlaceholderText(/Optional note/);
    await user.type(noteInput, "take backup first");
    await user.click(screen.getByRole("button", { name: /Approve/ }));
    expect(onAnswer).toHaveBeenCalledWith({ decision: "approve", note: "take backup first" });

    const card = screen.getByRole("group", { name: /asks for approval/ });
    card.focus();
    fireEvent.keyDown(card, { key: "2" });
    expect(onAnswer).toHaveBeenCalledWith({ decision: "deny", note: "take backup first" });
  });

  it("renders question ask with textarea, Send and Dismiss buttons", async () => {
    const user = userEvent.setup();
    const onAnswer = vi.fn();
    const questionAsk: AgentAsk = {
      ...baseAsk,
      id: "ask-q",
      kind: "question",
      question: "Which database port should we use?",
    };
    render(<AskCard ask={questionAsk} onAnswer={onAnswer} />);

    expect(screen.getByText("Which database port should we use?")).toBeInTheDocument();
    const sendBtn = screen.getByRole("button", { name: /Send/ });
    const dismissBtn = screen.getByRole("button", { name: /Dismiss/ });
    expect(sendBtn).toBeDisabled();

    const textarea = screen.getByPlaceholderText(/Your answer/);
    await user.type(textarea, "5432");
    expect(sendBtn).not.toBeDisabled();

    await user.click(sendBtn);
    expect(onAnswer).toHaveBeenCalledWith({ decision: "answer", answer: "5432" });

    await user.click(dismissBtn);
    expect(onAnswer).toHaveBeenCalledWith({ decision: "deny" });
  });

  it("renders choice ask with one button per option, optional note, and Dismiss", async () => {
    const user = userEvent.setup();
    const onAnswer = vi.fn();
    const choiceAsk: AgentAsk = {
      ...baseAsk,
      id: "ask-c",
      kind: "choice",
      question: "Pick a target environment:",
      options: ["staging", "production"],
    };
    render(<AskCard ask={choiceAsk} onAnswer={onAnswer} />);

    expect(screen.getByText("Pick a target environment:")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "staging" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "production" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Dismiss/ })).toBeInTheDocument();

    const noteInput = screen.getByPlaceholderText(/Optional note/);
    await user.type(noteInput, "verified credentials");

    await user.click(screen.getByRole("button", { name: "staging" }));
    expect(onAnswer).toHaveBeenCalledWith({
      decision: "answer",
      answer: "staging",
      note: "verified credentials",
    });

    await user.click(screen.getByRole("button", { name: /Dismiss/ }));
    expect(onAnswer).toHaveBeenCalledWith({
      decision: "deny",
      note: "verified credentials",
    });
  });

  it("renders permission ask with tool name, collapsible input, Allow / Allow for this run / Deny, and keys 1/2/3", async () => {
    const user = userEvent.setup();
    const onAnswer = vi.fn();
    const permAsk: AgentAsk = {
      ...baseAsk,
      id: "ask-p",
      kind: "permission",
      question: "Permission to run tool",
      tool: "Bash",
      toolInput: "git status",
    };
    render(<AskCard ask={permAsk} onAnswer={onAnswer} />);

    expect(screen.getByText("Bash")).toBeInTheDocument();
    const details = screen.getByText(/Input/i).closest("details");
    expect(details).toBeInTheDocument();
    expect(details?.querySelector("pre")?.textContent).toBe("git status");

    expect(screen.getByRole("button", { name: /Allow for this run/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Allow for this run/ }));
    expect(onAnswer).toHaveBeenCalledWith({ decision: "approve_run" });

    await user.click(screen.getByRole("button", { name: /^Allow \d/ }));
    expect(onAnswer).toHaveBeenCalledWith({ decision: "approve" });

    await user.click(screen.getByRole("button", { name: /Deny/ }));
    expect(onAnswer).toHaveBeenCalledWith({ decision: "deny" });

    const card = screen.getByRole("group", { name: /tool permission/ });
    card.focus();
    fireEvent.keyDown(card, { key: "1" });
    fireEvent.keyDown(card, { key: "2" });
    fireEvent.keyDown(card, { key: "3" });
    expect(onAnswer).toHaveBeenCalledWith({ decision: "approve" });
    expect(onAnswer).toHaveBeenCalledWith({ decision: "approve_run" });
    expect(onAnswer).toHaveBeenCalledWith({ decision: "deny" });
  });

  it("shows origin run and node and expiry countdown", () => {
    const onSelectNode = vi.fn();
    const ask: AgentAsk = {
      ...baseAsk,
      runId: "run-99",
      nodeId: "node-123",
      createdAt: Date.now() - 60_000,
    };
    render(<AskCard ask={ask} onSelectNode={onSelectNode} onAnswer={vi.fn()} />);

    expect(screen.getByText(/run-99/)).toBeInTheDocument();
    const nodeChip = screen.getByRole("button", { name: "#node-123" });
    expect(nodeChip).toBeInTheDocument();
    fireEvent.click(nodeChip);
    expect(onSelectNode).toHaveBeenCalledWith("node-123");

    // 10 min - 1 min = 9 min remaining
    expect(screen.getByText(/8:5|9:0/)).toBeInTheDocument();
  });

  it("uses ask.expiresAt for countdown when present", () => {
    const ask: AgentAsk = {
      ...baseAsk,
      createdAt: Date.now() - 600_000,
      expiresAt: Date.now() + 120_000,
    };
    render(<AskCard ask={ask} onAnswer={vi.fn()} />);

    expect(screen.getByText(/1:59|2:00/)).toBeInTheDocument();
  });

  it("shows result and hides actions when answered or expired", () => {
    const answeredAsk: AgentAsk = {
      ...baseAsk,
      kind: "question",
      status: "answered",
      answer: "Port 5432",
      decidedBy: "alice",
    };
    const { rerender } = render(<AskCard ask={answeredAsk} onAnswer={vi.fn()} />);

    expect(screen.getByText(/Answered/i)).toBeInTheDocument();
    expect(screen.getByText(/Port 5432/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Send/ })).toBeNull();

    const expiredAsk: AgentAsk = {
      ...baseAsk,
      kind: "permission",
      status: "expired",
      tool: "Bash",
    };
    rerender(<AskCard ask={expiredAsk} onAnswer={vi.fn()} />);
    expect(screen.getByText(/Expired/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Allow/ })).toBeNull();
  });
});
