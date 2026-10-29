import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    held: [],
    decidingIds: {},
    inboundPendingIds: {},
    toasts: [],
    viewer: null,
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

    expect(answerAsk).toHaveBeenCalledWith("ask-1", { decision: "approve", note: "After backup" });
  });

  it("marks the roster as the connect intro while nobody is connected", () => {
    const { container, rerender } = render(<AgentPanel onClose={vi.fn()} />);
    const roster = () => container.querySelector(".agent-roster-section");
    expect(roster()).not.toHaveClass("is-intro");
    useAgents.setState({ sessions: [] });
    rerender(<AgentPanel onClose={vi.fn()} />);
    expect(roster()).toHaveClass("is-intro");
  });

  it("shows another person's ask without answer controls", () => {
    const answerAsk = vi.fn().mockResolvedValue(undefined);
    useAgents.setState({ answerAsk, viewer: { id: "bob", name: "bob", role: "member" } });

    render(<AgentPanel onClose={vi.fn()} />);

    expect(screen.getByText("Drop legacy column?")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Approve/i })).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("Optional note for the agent…")).not.toBeInTheDocument();
    expect(screen.getByText(/Only the person whose session asked/)).toBeInTheDocument();
  });

  it("shows settled asks with their results alongside pending asks", () => {
    useAgents.setState({
      asks: [
        {
          id: "ask-pending",
          project: "alpha",
          sessionId: "claude:1",
          sessionName: "api-claude",
          actorId: "alice",
          agent: "claude",
          question: "Pending approval?",
          status: "pending",
          createdAt: 1700000000000,
        },
        {
          id: "ask-answered",
          project: "alpha",
          sessionId: "claude:1",
          sessionName: "api-claude",
          actorId: "alice",
          agent: "claude",
          kind: "question",
          question: "Which port?",
          status: "answered",
          answer: "Port 5432",
          createdAt: 1700000001000,
        },
        {
          id: "ask-expired",
          project: "alpha",
          sessionId: "claude:1",
          sessionName: "api-claude",
          actorId: "alice",
          agent: "claude",
          kind: "permission",
          tool: "Bash",
          question: "Run command?",
          status: "expired",
          createdAt: 1700000002000,
        },
      ],
    });

    render(<AgentPanel onClose={vi.fn()} />);

    expect(screen.getByText("Pending approval?")).toBeInTheDocument();
    expect(screen.getByText("Answered")).toBeInTheDocument();
    expect(screen.getByText("Answer: Port 5432")).toBeInTheDocument();
    expect(screen.getByText("Expired")).toBeInTheDocument();
  });
});

describe("AgentPanel W1.5", () => {
  const heldMessage = {
    id: "h1",
    project: "alpha",
    from: "bob/web-codex",
    senderActor: "bob",
    senderSession: "web-codex",
    senderAgent: "codex",
    to: "api-claude",
    body: "Please run the migration",
    createdAt: 1700000000000,
    status: "held" as const,
    files: [{ name: "migration.sql", size: 2048, sha256: "x" }],
  };

  it("shows held messages with approve and reject", () => {
    const decideHeld = vi.fn().mockResolvedValue(undefined);
    useAgents.setState({ held: [heldMessage], decideHeld, asks: [] });

    render(<AgentPanel onClose={vi.fn()} />);

    const section = screen.getByRole("region", { name: "Held messages (1)" });
    expect(within(section).getByText("Please run the migration")).toBeInTheDocument();
    // Files of a held message are named, not downloadable.
    expect(within(section).getByText("migration.sql")).toBeInTheDocument();
    expect(within(section).queryByRole("link")).toBeNull();

    fireEvent.click(within(section).getByRole("button", { name: "Approve" }));
    expect(decideHeld).toHaveBeenCalledWith("h1", "approve");
    fireEvent.click(within(section).getByRole("button", { name: "Reject" }));
    expect(decideHeld).toHaveBeenCalledWith("h1", "reject");
  });

  it("renders the model badge and an inbound toggle for manageable sessions", () => {
    const setInbound = vi.fn().mockResolvedValue(undefined);
    useAgents.setState((s) => ({
      setInbound,
      viewer: { id: "alice", name: "alice", role: "member" },
      sessions: [
        { ...s.sessions[0], model: "opus-4", inbound: "open" },
        {
          ...s.sessions[0],
          id: "codex:2",
          actorId: "carol",
          actorName: "carol",
          name: "carol-codex",
          inbound: "hold",
        },
      ],
    }));

    render(<AgentPanel onClose={vi.fn()} />);

    expect(screen.getByText("opus-4")).toBeInTheDocument();
    const toggles = screen.getAllByRole("switch");
    // Only alice's own session can be toggled; carol's shows a badge.
    expect(toggles).toHaveLength(1);
    expect(toggles[0]).toHaveAttribute("aria-checked", "false");
    fireEvent.click(toggles[0]);
    expect(setInbound).toHaveBeenCalledWith("claude:1", "hold");
    expect(screen.getByText("Hold")).toBeInTheDocument();
  });

  it("an admin may toggle anyone's session", () => {
    useAgents.setState((s) => ({
      viewer: { id: "root", name: "root", role: "admin" },
      sessions: [{ ...s.sessions[0], inbound: "hold" }],
    }));
    render(<AgentPanel onClose={vi.fn()} />);
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "true");
  });

  it("shows status badges and download chips in the message list", () => {
    useAgents.setState({
      asks: [],
      messages: [
        {
          ...heldMessage,
          id: "d1",
          status: "delivered",
          body: "Here is the file",
          files: [{ name: "notes.md", size: 12, sha256: "y" }],
        },
        { ...heldMessage, id: "r1", status: "rejected", body: "Rejected one", files: undefined },
      ],
    });

    render(<AgentPanel onClose={vi.fn()} />);

    const link = screen.getByRole("link", { name: /notes\.md/ });
    expect(link).toHaveAttribute("href", "/api/agents/messages/d1/files/notes.md");
    expect(link).toHaveAttribute("download", "notes.md");
    expect(screen.getByText("Rejected")).toBeInTheDocument();
  });

  it("sends with notify and attached files, and reports bad files inline", async () => {
    const sendMessage = vi.fn().mockResolvedValue(true);
    useAgents.setState({ sendMessage, asks: [] });

    render(<AgentPanel onClose={vi.fn()} />);

    const bell = screen.getByRole("button", { name: "Notify the recipient" });
    expect(bell).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(bell);
    expect(bell).toHaveAttribute("aria-pressed", "true");

    const input = screen.getByTestId("agent-file-input");
    fireEvent.change(input, {
      target: {
        files: [
          new File(["# plan"], "plan.md", { type: "text/markdown" }),
          new File([new Uint8Array([0, 1, 2])], "blob.bin"),
        ],
      },
    });
    expect(await screen.findByText("plan.md")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("blob.bin: not a UTF-8 text file.");

    fireEvent.change(screen.getByPlaceholderText("Message agent(s)… (Enter to send)"), {
      target: { value: "See attached" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() =>
      expect(sendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          message: "See attached",
          notify: true,
          files: [{ name: "plan.md", content: "# plan" }],
        }),
      ),
    );
    await waitFor(() => expect(screen.queryByText("plan.md")).toBeNull());
  });

  it("asks for notification permission only on click", async () => {
    const requestPermission = vi.fn().mockResolvedValue("granted");
    vi.stubGlobal("Notification", { permission: "default", requestPermission });
    try {
      render(<AgentPanel onClose={vi.fn()} />);

      expect(requestPermission).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Enable desktop notifications" }));
      await waitFor(() => expect(requestPermission).toHaveBeenCalledTimes(1));
      await waitFor(() =>
        expect(
          screen.queryByRole("button", { name: "Enable desktop notifications" }),
        ).toBeNull(),
      );
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// Docked in Station, the panel sits under the card's own "Agent chat · N
// online" toggle; its own large "Agent chat / N online" header said the same
// thing a second time. The heading stays for screen readers (it names the
// region) and the count is left to the card.
describe("AgentPanel docked header", () => {
  it("does not repeat the card's title and online count", () => {
    render(<AgentPanel docked />);
    const heading = screen.getByRole("heading", { name: "Agent chat" });
    expect(heading).toHaveClass("sr-only");
    expect(screen.queryByText("1 online")).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Agent chat" })).toBeInTheDocument();
  });

  it("keeps both when floating over the board", () => {
    render(<AgentPanel onClose={vi.fn()} />);
    expect(screen.getByRole("heading", { name: "Agent chat" })).not.toHaveClass("sr-only");
    expect(screen.getByText("1 online")).toBeInTheDocument();
  });
});
