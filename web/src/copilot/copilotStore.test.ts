import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CopilotEntry } from "../api";
import { readCopilotPrefs, reduceTurnEvent, useCopilot, type TurnState } from "./copilotStore";

const empty: TurnState = { items: [], pending: [], running: true, turnId: null, lastReply: null };

function apply(state: TurnState, ...events: Parameters<typeof reduceTurnEvent>[1][]): TurnState {
  return events.reduce((current, event) => ({ ...current, ...reduceTurnEvent(current, event) }), state);
}

const userLive: TurnState = {
  ...empty,
  items: [{ kind: "user", id: "live-user-x", text: "hi", source: "voice", live: true }],
};

describe("reduceTurnEvent", () => {
  it("turn_started records the turn and adopts the stored user entry id", () => {
    const next = apply(userLive, { type: "turn_started", turnId: "t1", entryId: "e1" });
    expect(next.turnId).toBe("t1");
    expect(next.items[0]).toMatchObject({ kind: "user", id: "e1", live: false });
  });

  it("ping and unknown events change nothing", () => {
    expect(reduceTurnEvent(userLive, { type: "ping" })).toEqual({});
    const unknown = { type: "from_a_newer_server" } as unknown as Parameters<typeof reduceTurnEvent>[1];
    expect(reduceTurnEvent(userLive, unknown)).toEqual({});
  });

  it("text_delta appends to the streaming bubble", () => {
    const next = apply(
      empty,
      { type: "text_delta", text: "Hel" },
      { type: "text_delta", text: "lo" },
    );
    expect(next.items).toHaveLength(1);
    expect(next.items[0]).toMatchObject({ kind: "assistant", text: "Hello", streaming: true });
  });

  it("tool_call closes the streaming text and adds a running tool card", () => {
    const next = apply(
      empty,
      { type: "text_delta", text: "Let me look." },
      { type: "tool_call", callId: "c1", name: "get_node", title: "Read node", kind: "read", input: { id: "n1" } },
    );
    expect(next.items[0]).toMatchObject({ kind: "assistant", streaming: false });
    expect(next.items[1]).toMatchObject({
      kind: "tool",
      callId: "c1",
      title: "Read node",
      toolKind: "read",
      status: "running",
      input: JSON.stringify({ id: "n1" }, null, 2),
    });
    // Text after the tool starts a new bubble.
    const after = apply(next, { type: "text_delta", text: "Done." });
    expect(after.items[2]).toMatchObject({ kind: "assistant", text: "Done.", streaming: true });
  });

  it("permission_request queues a request and marks the card awaiting", () => {
    const next = apply(
      { ...empty, turnId: "t1" },
      { type: "tool_call", callId: "c1", name: "set_node_status", title: "Set status", kind: "write", input: {} },
      {
        type: "permission_request",
        requestId: "r1",
        callId: "c1",
        name: "set_node_status",
        kind: "write",
        input: { id: "n1", status: "done" },
        summary: "Mark n1 done",
      },
    );
    expect(next.pending).toEqual([
      expect.objectContaining({ requestId: "r1", turnId: "t1", title: "Set status", summary: "Mark n1 done" }),
    ]);
    expect(next.items[0]).toMatchObject({ status: "awaiting" });
  });

  it("permission_resolved removes the request; deny marks the card denied", () => {
    const waiting = apply(
      { ...empty, turnId: "t1" },
      { type: "tool_call", callId: "c1", name: "w", title: "W", kind: "write", input: {} },
      { type: "permission_request", requestId: "r1", callId: "c1", name: "w", kind: "write", input: {}, summary: "" },
    );
    const allowed = apply(waiting, { type: "permission_resolved", requestId: "r1", decision: "allow" });
    expect(allowed.pending).toEqual([]);
    expect(allowed.items[0]).toMatchObject({ status: "running" });
    const denied = apply(waiting, { type: "permission_resolved", requestId: "r1", decision: "deny" });
    expect(denied.items[0]).toMatchObject({ status: "denied" });
    // A result after a denial does not resurrect the card.
    const after = apply(denied, { type: "tool_result", callId: "c1", output: "refused", isError: true });
    expect(after.items[0]).toMatchObject({ status: "denied", output: "refused" });
  });

  it("client_tool_call adds a UI tool card", () => {
    const next = apply(empty, { type: "client_tool_call", callId: "u1", name: "ui_open_node", input: { id: "n1" } });
    expect(next.items[0]).toMatchObject({ kind: "tool", client: true, toolKind: "read", status: "running" });
  });

  it("tool_result fills output and status", () => {
    const base = apply(empty, { type: "tool_call", callId: "c1", name: "x", title: "X", kind: "read", input: {} });
    expect(apply(base, { type: "tool_result", callId: "c1", output: "ok!", isError: false }).items[0]).toMatchObject({
      status: "ok",
      output: "ok!",
    });
    expect(apply(base, { type: "tool_result", callId: "c1", output: "boom", isError: true }).items[0]).toMatchObject({
      status: "error",
    });
  });

  it("entry adopts the live bubble instead of duplicating it", () => {
    const streamed = apply(empty, { type: "text_delta", text: "Hi there" });
    const entry: CopilotEntry = { id: "e2", seq: 2, kind: "assistant", text: "Hi there", createdAt: 1 };
    const next = apply(streamed, { type: "entry", entry });
    expect(next.items).toEqual([{ kind: "assistant", id: "e2", text: "Hi there" }]);
    // The same entry again is a no-op.
    expect(apply(next, { type: "entry", entry }).items).toHaveLength(1);
    // An entry nothing on screen matches is appended.
    const notice: CopilotEntry = { id: "e3", seq: 3, kind: "notice", text: "Mode changed", createdAt: 1 };
    expect(apply(next, { type: "entry", entry: notice }).items[1]).toMatchObject({ kind: "notice", text: "Mode changed" });
  });

  it("entry for a tool keeps the live card's title and call id", () => {
    const live = apply(empty, { type: "tool_call", callId: "c1", name: "get_node", title: "Read node", kind: "read", input: {} });
    const entry: CopilotEntry = {
      id: "e4",
      seq: 4,
      kind: "tool",
      text: "",
      toolName: "get_node",
      toolOutput: "{}",
      toolStatus: "ok",
      createdAt: 1,
    };
    expect(apply(live, { type: "entry", entry }).items[0]).toMatchObject({
      id: "e4",
      callId: "c1",
      title: "Read node",
      status: "ok",
      output: "{}",
    });
  });

  it("error adds an error notice", () => {
    const next = apply(empty, { type: "error", message: "provider down", code: "provider" });
    expect(next.items[0]).toMatchObject({ kind: "notice", tone: "error", text: "provider down" });
  });

  it("turn_finished stops the turn and keeps the reply to speak", () => {
    const next = apply(
      { ...userLive, turnId: "t9" },
      { type: "text_delta", text: "First." },
      { type: "tool_call", callId: "c", name: "x", title: "X", kind: "read", input: {} },
      { type: "text_delta", text: "Second." },
      { type: "turn_finished", stop: "end_turn" },
    );
    expect(next.running).toBe(false);
    expect(next.turnId).toBeNull();
    expect(next.pending).toEqual([]);
    expect(next.lastReply).toEqual({ turnId: "t9", text: "First.\n\nSecond." });
    expect(next.items.some((item) => item.kind === "assistant" && item.streaming)).toBe(false);
  });
});

describe("useCopilot", () => {
  beforeEach(() => {
    useCopilot.setState({ items: [], pending: [], running: false, turnId: null });
  });

  it("beginTurn adds the optimistic user bubble; endTurn closes open cards", () => {
    useCopilot.getState().beginTurn("hello", "voice");
    useCopilot.getState().applyEvent({ type: "tool_call", callId: "c", name: "x", title: "X", kind: "read", input: {} });
    expect(useCopilot.getState().running).toBe(true);
    expect(useCopilot.getState().lastSource).toBe("voice");
    useCopilot.getState().endTurn("Stopped.");
    const state = useCopilot.getState();
    expect(state.running).toBe(false);
    expect(state.items[1]).toMatchObject({ kind: "tool", status: "error" });
    expect(state.items[2]).toMatchObject({ kind: "notice", text: "Stopped.", tone: "error" });
  });

  it("persists UI preferences", () => {
    useCopilot.getState().setSpeakReplies(true);
    useCopilot.getState().setMode("acceptEdits");
    expect(readCopilotPrefs()).toEqual({ speakReplies: true, defaultMode: "acceptEdits" });
  });

  it("survives storage that throws", () => {
    vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readCopilotPrefs()).toEqual({ speakReplies: false, defaultMode: "default" });
    expect(() => useCopilot.getState().setSpeakReplies(true)).not.toThrow();
  });

  it("showConversation renders stored entries", () => {
    useCopilot.getState().showConversation(
      { id: "c1", project: "p", title: "T", mode: "plan", createdAt: 1, updatedAt: 1 },
      [
        { id: "1", seq: 1, kind: "user", source: "voice", text: "hi", createdAt: 1 },
        { id: "2", seq: 2, kind: "tool", text: "", toolName: "ui_open_node", toolInput: '{"id":"n"}', toolStatus: "denied", createdAt: 1 },
      ],
    );
    const state = useCopilot.getState();
    expect(state.activeId).toBe("c1");
    expect(state.mode).toBe("plan");
    expect(state.items[0]).toMatchObject({ kind: "user", source: "voice" });
    expect(state.items[1]).toMatchObject({ kind: "tool", client: true, status: "denied", input: '{\n  "id": "n"\n}' });
  });
});
