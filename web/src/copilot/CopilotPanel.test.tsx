import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { CopilotPanel } from "./CopilotPanel";
import { useCopilot } from "./copilotStore";
import type { SpeechRecognitionState } from "./speech";

const speech: SpeechRecognitionState = {
  supported: true,
  listening: false,
  interim: "",
  errorKey: null,
  start: vi.fn(),
  stop: vi.fn(),
  abort: vi.fn(),
};

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useCopilot.setState({
    status: {
      enabled: true,
      provider: "anthropic",
      model: "claude-opus-5-5",
      modes: ["plan", "default", "acceptEdits", "bypassPermissions"],
      allowBypass: false,
    },
    serverState: "ready",
    conversations: [],
    activeId: null,
    mode: "default",
    running: false,
    notice: null,
    pending: [],
    items: [
      { kind: "user", id: "u1", text: "Mark login tasks in progress", source: "voice" },
      { kind: "user", id: "u2", text: "typed question", source: "text" },
      {
        kind: "tool",
        id: "t1",
        callId: "c1",
        name: "set_node_status",
        title: "Set node status",
        toolKind: "write",
        input: '{\n  "id": "n1"\n}',
        output: "ok",
        status: "ok",
      },
      { kind: "assistant", id: "a1", text: "Marked **two** tasks.", streaming: true },
      { kind: "notice", id: "n1", text: "Something failed", tone: "error" },
    ],
  });
});

function renderPanel(overrides: Partial<SpeechRecognitionState> = {}) {
  return render(<CopilotPanel speech={{ ...speech, ...overrides }} ttsSupported onClose={vi.fn()} />);
}

describe("CopilotPanel", () => {
  it("renders the transcript entries", () => {
    renderPanel();
    const log = screen.getByRole("log", { name: "Copilot conversation" });
    expect(log).toHaveAttribute("aria-live", "polite");
    expect(within(log).getByText("Mark login tasks in progress").parentElement).toContainElement(
      within(log).getByRole("img", { name: "Spoken" }),
    );
    // Only the spoken bubble carries the microphone.
    expect(within(log).getAllByRole("img", { name: "Spoken" })).toHaveLength(1);
    expect(within(log).getByText("Set node status")).toBeInTheDocument();
    expect(within(log).getByText("write")).toBeInTheDocument();
    expect(within(log).getByText("Done")).toBeInTheDocument();
    // Assistant markdown is rendered, with a caret while it streams.
    expect(within(log).getByText("two").tagName).toBe("STRONG");
    expect(log.querySelector(".copilot-caret")).not.toBeNull();
    expect(within(log).getByRole("alert")).toHaveTextContent("Something failed");
  });

  it("shows the live interim transcript while listening", () => {
    renderPanel({ listening: true, interim: "把登入" });
    expect(screen.getByText("把登入")).toBeInTheDocument();
  });

  it("renders pending permission cards inline", () => {
    useCopilot.setState({
      pending: [
        {
          requestId: "r1",
          callId: "c9",
          turnId: "t",
          name: "update_node_body",
          title: "Replace page",
          kind: "risky",
          input: "{}",
          summary: "Replace n1 body",
        },
      ],
    });
    renderPanel();
    expect(screen.getByRole("group", { name: "Allow Replace page?" })).toBeInTheDocument();
  });

  it("offers bypass only when the server allows it", () => {
    renderPanel();
    const select = screen.getByRole("combobox", { name: "Mode" });
    const options = within(select)
      .getAllByRole("option")
      .map((option) => option.getAttribute("value"));
    expect(options).toEqual(["plan", "default", "acceptEdits"]);
  });

  it("cycles the mode with Shift+Tab from the composer", () => {
    renderPanel();
    const input = screen.getByRole("textbox", { name: "Message to the copilot" });
    fireEvent.keyDown(input, { key: "Tab", shiftKey: true });
    expect(useCopilot.getState().mode).toBe("acceptEdits");
    fireEvent.keyDown(input, { key: "Tab", shiftKey: true });
    expect(useCopilot.getState().mode).toBe("plan");
  });

  it("shows the stop button while a turn runs", () => {
    useCopilot.setState({ running: true });
    renderPanel();
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
  });

  it("shows speech errors", () => {
    renderPanel({ errorKey: "copilot.speech.notAllowed" });
    expect(screen.getByText(/Microphone access is blocked/).closest("[role=status]")).not.toBeNull();
  });

  it("names the browser's model in the header", () => {
    useCopilot.setState({
      status: { enabled: true, provider: "browser", model: "gemini-nano", modes: ["default"], allowBypass: false },
    });
    renderPanel();
    expect(screen.getByText("Browser (Gemini Nano)")).toBeInTheDocument();
  });

  it("names a local model and where it runs", () => {
    useCopilot.setState({
      status: {
        enabled: true,
        provider: "openai",
        model: "qwen3:8b",
        modes: ["default"],
        allowBypass: false,
        localServer: "ollama",
        modelHost: "server",
        serverIsLocal: true,
      },
    });
    renderPanel();
    expect(screen.getByText("Ollama · qwen3:8b on this PC")).toBeInTheDocument();
  });

  it("never offers bypass with the browser's model", () => {
    useCopilot.setState({
      status: {
        enabled: true,
        provider: "browser",
        model: "gemini-nano",
        modes: ["plan", "default", "acceptEdits", "bypassPermissions"],
        allowBypass: true,
      },
    });
    renderPanel();
    const options = within(screen.getByRole("combobox", { name: "Mode" }))
      .getAllByRole("option")
      .map((option) => option.getAttribute("value"));
    expect(options).not.toContain("bypassPermissions");
  });
});
