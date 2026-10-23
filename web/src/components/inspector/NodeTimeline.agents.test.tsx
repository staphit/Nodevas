import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { useAgents } from "../../agents/agentStore";
import { useApp } from "../../store";
import { NodeTimeline } from "./NodeTimeline";

vi.mock("../../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api")>();
  return {
    ...actual,
    api: {
      getAgentMessages: vi.fn(),
    },
  };
});

const message = (id: string, body: string) => ({
  id,
  project: "p",
  from: "alice/api-claude",
  senderActor: "alice",
  senderAgent: "claude",
  to: "web-codex",
  nodeId: "a",
  body,
  createdAt: Date.UTC(2026, 9, 1, 8, 0),
});

beforeEach(() => {
  vi.mocked(api.getAgentMessages).mockReset();
  useAgents.setState({ messages: [] });
  useApp.setState({
    graph: { version: 1, nodes: [{ id: "a", title: "設計稿" }], edges: [], ui: {} },
    runState: { nodes: {}, history: [] },
    operations: {},
    preferences: { ...useApp.getState().preferences, language: "en" },
  });
});

describe("NodeTimeline agent messages", () => {
  it("lists messages bound to the node and refetches when the agent store refreshes", async () => {
    vi.mocked(api.getAgentMessages).mockResolvedValueOnce({
      messages: [message("m1", "schema ready?")],
    });
    render(<NodeTimeline id="a" />);

    expect(await screen.findByText("schema ready?")).toBeTruthy();
    expect(screen.getByText("Agent discussion")).toBeTruthy();
    expect(screen.getByText("alice/api-claude → web-codex")).toBeTruthy();
    expect(api.getAgentMessages).toHaveBeenCalledWith("a");

    vi.mocked(api.getAgentMessages).mockResolvedValueOnce({
      messages: [message("m1", "schema ready?"), message("m2", "yes, merged")],
    });
    act(() => useAgents.setState({ messages: [] }));
    expect(await screen.findByText("yes, merged")).toBeTruthy();
  });

  it("shows nothing when the read is refused", async () => {
    vi.mocked(api.getAgentMessages).mockRejectedValue(new Error("403"));
    render(<NodeTimeline id="a" />);
    await waitFor(() => expect(api.getAgentMessages).toHaveBeenCalled());
    expect(screen.queryByText("Agent discussion")).toBeNull();
  });
});
