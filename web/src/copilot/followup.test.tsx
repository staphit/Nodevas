import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useCopilot } from "./copilotStore";
import { CopilotRoot } from "./CopilotRoot";
import { followupArrived } from "./session";

const status = {
  enabled: true,
  provider: "anthropic",
  model: "m",
  modes: ["plan", "default", "acceptEdits"],
  allowBypass: false,
};

const conversation = { id: "c1", project: "main", title: "Login page", mode: "default", createdAt: 1, updatedAt: 2 };

const entries = [
  { id: "e1", seq: 1, kind: "user", source: "voice", text: "build the login page", createdAt: 1 },
  { id: "e2", seq: 2, kind: "event", text: "Breakdown finished: 2 tasks", createdAt: 2 },
  {
    id: "e3",
    seq: 3,
    kind: "assistant",
    text: "The breakdown of “Login page” is done: 2 tasks.\n1. Users table\n2. Login form (after 1)\n\nShall I start the autopilot?",
    createdAt: 2,
  },
];

/** The server: the conversation list says c1 is unread until c1 is fetched. */
function stubServer() {
  let unread = true;
  const fetchMock = vi.fn(async (url: string) => {
    if (url.startsWith("/api/copilot/status")) return new Response(JSON.stringify(status));
    if (url.startsWith("/api/copilot/conversations?project=")) {
      return new Response(JSON.stringify({ conversations: [{ ...conversation, unread }] }));
    }
    if (url === "/api/copilot/conversations/c1") {
      unread = false;
      return new Response(JSON.stringify({ conversation, entries }));
    }
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
  });
  useCopilot.setState({
    serverState: "unknown",
    status: null,
    open: false,
    activeId: null,
    conversations: [],
    items: [],
    pending: [],
    running: false,
    turnId: null,
    lastReply: null,
    notice: null,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("copilot follow-up", () => {
  it("shows the follow-up at once, and queues it to be spoken, when its conversation is open", async () => {
    const fetchMock = stubServer();
    useCopilot.setState({ open: true, activeId: "c1", conversations: [{ ...conversation, mode: "default" }] });

    await followupArrived("c1");

    expect(fetchMock).toHaveBeenCalledWith("/api/copilot/conversations/c1", expect.anything());
    const items = useCopilot.getState().items;
    expect(items.map((item) => item.kind)).toEqual(["user", "notice", "assistant"]);
    expect(items[1]).toMatchObject({ kind: "notice", text: "Breakdown finished: 2 tasks", tone: "info" });
    expect(useCopilot.getState().lastReply).toEqual({
      turnId: "followup-e3",
      text: expect.stringContaining("Shall I start the autopilot?"),
    });
  });

  it("waits for a running turn in the same conversation", async () => {
    const fetchMock = stubServer();
    useCopilot.setState({ open: true, activeId: "c1", running: true });
    await followupArrived("c1");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(useCopilot.getState().items).toEqual([]);
  });

  it("waits as unread with the panel closed; the mic button opens it", async () => {
    stubServer();
    render(<CopilotRoot />);
    await screen.findByRole("button", { name: "Open copilot · New follow-up from the copilot" });
    // The push arrives too: still unread, nothing opens by itself.
    await act(async () => {
      await followupArrived("c1");
    });
    expect(useCopilot.getState().open).toBe(false);
    expect(useCopilot.getState().conversations[0].unread).toBe(true);
    expect(screen.getByTestId("copilot-unread")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open copilot · New follow-up from the copilot" }));
    expect(await screen.findByRole("dialog", { name: "Copilot" })).toBeInTheDocument();
    await vi.waitFor(() => expect(useCopilot.getState().activeId).toBe("c1"));
    expect(await screen.findByText("Breakdown finished: 2 tasks")).toBeInTheDocument();
    expect(screen.getByText(/Shall I start the autopilot\?/)).toBeInTheDocument();
    expect(useCopilot.getState().conversations[0].unread).toBe(false);
    expect(useCopilot.getState().lastReply?.turnId).toBe("followup-e3");
  });

  it("tells an open panel showing another conversation where the follow-up is", async () => {
    stubServer();
    useCopilot.setState({ open: true, activeId: "c9" });
    await followupArrived("c1");
    expect(useCopilot.getState().notice).toBe("The copilot followed up in “Login page”.");
    expect(useCopilot.getState().activeId).toBe("c9");
    expect(useCopilot.getState().conversations[0].unread).toBe(true);
  });
});
