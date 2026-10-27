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
    if (url.startsWith("/api/copilot/status")) {
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
    expect(fetchMock.mock.calls.filter(([url]) => url.startsWith("/api/copilot/status"))).toHaveLength(2);
  });

  it("does not refetch on runner changes when the server holds the key", async () => {
    const fetchMock = stubStatus([{ ...ready, keySource: "server", runner: undefined }]);
    render(<CopilotRoot />);
    await screen.findByRole("button", { name: "Open copilot" });
    runnersChanged();
    await act(async () => {});
    expect(fetchMock.mock.calls.filter(([url]) => url.startsWith("/api/copilot/status"))).toHaveLength(1);
  });

  it("explains the free options when automatic mode has no model", async () => {
    stubStatus([
      { ...waiting, keySource: "runner", needsRunner: false, needsSetup: true, reasonCode: "no_model", automatic: true },
    ]);
    render(<CopilotRoot />);
    fireEvent.click(await screen.findByRole("button", { name: "Copilot: set up a model" }));
    expect(await screen.findByRole("dialog", { name: "Copilot needs a model" })).toBeInTheDocument();
    expect(screen.getByText(/Chrome 148 or later/)).toBeInTheDocument();
    expect(screen.getByText("ollama pull qwen3:8b")).toBeInTheDocument();
    expect(screen.getByText("nodevas runner copilot-key")).toBeInTheDocument();
  });

  it("downloads the on-device model with progress", async () => {
    let finish: () => void = () => undefined;
    let progress: (loaded: number) => void = () => undefined;
    vi.stubGlobal("LanguageModel", {
      availability: vi.fn(async () => "downloadable"),
      create: vi.fn(
        (options: { monitor?: (m: { addEventListener: (t: string, l: (e: { loaded: number }) => void) => void }) => void }) =>
          new Promise((resolve) => {
            options.monitor?.({ addEventListener: (_t, listener) => (progress = (loaded) => listener({ loaded })) });
            finish = () => resolve({ prompt: vi.fn(), destroy: vi.fn() });
          }),
      ),
    });
    const downloading = { ...waiting, provider: "browser", model: "gemini-nano", needsRunner: false, needsDownload: true };
    const fetchMock = stubStatus([downloading, { ...downloading, enabled: true, needsDownload: false }]);
    render(<CopilotRoot />);
    fireEvent.click(await screen.findByRole("button", { name: "Copilot: download the on-device model" }));
    fireEvent.click(await screen.findByRole("button", { name: "Download the on-device model" }));
    await act(async () => {
      progress(0.4);
    });
    expect(screen.getByRole("progressbar", { name: "Download progress" })).toHaveAttribute("value", "40");
    expect(screen.getByText("40%")).toBeInTheDocument();
    await act(async () => {
      finish();
    });
    // Still open, now as the copilot itself, on the browser's model.
    expect(await screen.findByRole("dialog", { name: "Copilot" })).toBeInTheDocument();
    expect(await screen.findByText("Browser (Gemini Nano)")).toBeInTheDocument();
    // The status said which model this browser has.
    expect(fetchMock.mock.calls.some(([url]) => url === "/api/copilot/status?browserModel=downloadable")).toBe(true);
  });
});
