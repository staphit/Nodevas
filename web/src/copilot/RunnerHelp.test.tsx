import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { CopilotRoot } from "./CopilotRoot";
import { useCopilot } from "./copilotStore";
import { runnersChanged } from "./session";

const waiting = {
  enabled: false,
  provider: "anthropic",
  model: "m",
  modes: ["default"],
  allowBypass: false,
  keySource: "runner",
  needsRunner: true,
  reason: "your runner is offline or has no copilot key",
};
const ready = { ...waiting, enabled: true, needsRunner: false, reason: "", runner: "desk PC" };

function stubStatus(statuses: object[]) {
  let call = 0;
  const fetchMock = vi.fn(async (url: string) => {
    if (url === "/api/copilot/status") {
      const body = statuses[Math.min(call, statuses.length - 1)];
      call++;
      return new Response(JSON.stringify(body));
    }
    return new Response(JSON.stringify({ conversations: [] }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" }, activeProject: "main" });
  useCopilot.setState({ serverState: "unknown", status: null, open: false, items: [], pending: [] });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("copilot waiting for the runner", () => {
  it("keeps the button and explains how to start the runner", async () => {
    stubStatus([waiting]);
    render(<CopilotRoot />);
    const fab = await screen.findByRole("button", { name: "Copilot: start your runner" });
    expect(useCopilot.getState().serverState).toBe("needsRunner");
    fireEvent.click(fab);
    expect(await screen.findByRole("dialog", { name: "Copilot needs your runner" })).toBeInTheDocument();
    expect(screen.getByText("nodevas runner copilot-key")).toBeInTheDocument();
    expect(screen.getByText("your runner is offline or has no copilot key")).toBeInTheDocument();
    // No composer: nothing can be sent until the runner answers.
    expect(screen.queryByRole("textbox", { name: "Message to the copilot" })).toBeNull();
  });

  it("turns on when the runner connects", async () => {
    const fetchMock = stubStatus([waiting, ready]);
    render(<CopilotRoot />);
    await screen.findByRole("button", { name: "Copilot: start your runner" });
    await act(async () => {
      runnersChanged();
    });
    expect(await screen.findByRole("button", { name: "Open copilot" })).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([url]) => url === "/api/copilot/status")).toHaveLength(2);
  });

  it("does not refetch on runner changes when the server holds the key", async () => {
    const fetchMock = stubStatus([{ ...ready, keySource: "server", runner: undefined }]);
    render(<CopilotRoot />);
    await screen.findByRole("button", { name: "Open copilot" });
    runnersChanged();
    await act(async () => {});
    expect(fetchMock.mock.calls.filter(([url]) => url === "/api/copilot/status")).toHaveLength(1);
  });
});
