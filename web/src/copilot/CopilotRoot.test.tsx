import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useCopilot } from "./copilotStore";
import { CopilotRoot } from "./CopilotRoot";

const status = {
  enabled: true,
  provider: "anthropic",
  model: "m",
  modes: ["plan", "default", "acceptEdits"],
  allowBypass: false,
};

function stubServer(statusCode: number) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url === "/api/copilot/status") {
        return statusCode === 200
          ? new Response(JSON.stringify(status))
          : new Response(JSON.stringify({ error: "not found" }), { status: statusCode });
      }
      return new Response(JSON.stringify({ conversations: [] }));
    }),
  );
}

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
  });
  useCopilot.setState({ serverState: "unknown", status: null, open: false, items: [], pending: [] });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("CopilotRoot", () => {
  it("renders nothing when the server has no copilot", async () => {
    stubServer(404);
    const { container } = render(<CopilotRoot />);
    await act(async () => {});
    expect(useCopilot.getState().serverState).toBe("disabled");
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the Copilot button and toggles the panel with Ctrl+Shift+Space", async () => {
    stubServer(200);
    render(<CopilotRoot />);
    const fab = await screen.findByRole("button", { name: "Copilot · Open copilot" });
    // Its name is on it, not only in a tooltip; typing first, so no mic.
    expect(fab).toHaveTextContent("Copilot");
    expect(fab.querySelector('[data-icon="copilot"]')).not.toBeNull();
    fireEvent.keyDown(window, { key: " ", code: "Space", ctrlKey: true, shiftKey: true });
    expect(await screen.findByRole("dialog", { name: "Copilot" })).toBeInTheDocument();
    await vi.waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Message to the copilot" })).toHaveFocus(),
    );
    fireEvent.keyDown(window, { key: " ", code: "Space", ctrlKey: true, shiftKey: true });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("leaves Alt+Space alone inside other text fields", async () => {
    stubServer(200);
    render(
      <>
        <input aria-label="other" />
        <CopilotRoot />
      </>,
    );
    await screen.findByRole("button", { name: "Copilot · Open copilot" });
    const other = screen.getByRole("textbox", { name: "other" });
    fireEvent.keyDown(other, { key: " ", code: "Space", altKey: true });
    expect(useCopilot.getState().open).toBe(false);
    fireEvent.keyDown(document.body, { key: " ", code: "Space", altKey: true });
    expect(useCopilot.getState().open).toBe(true);
  });

  describe("holding Alt+Space", () => {
    class Recognition {
      static last: Recognition | null = null;
      lang = "";
      interimResults = false;
      continuous = false;
      maxAlternatives = 0;
      onresult: ((event: unknown) => void) | null = null;
      onerror: ((event: unknown) => void) | null = null;
      onend: (() => void) | null = null;
      onstart: (() => void) | null = null;
      constructor() {
        Recognition.last = this;
      }
      start() {}
      stop() {
        this.onend?.();
      }
      abort() {
        this.onend?.();
      }
      say(text: string) {
        this.onresult?.({ resultIndex: 0, results: [Object.assign([{ transcript: text }], { isFinal: true })] });
      }
    }

    afterEach(() => {
      delete window.webkitSpeechRecognition;
    });

    async function dictate(text: string) {
      window.webkitSpeechRecognition = Recognition as never;
      render(<CopilotRoot />);
      await screen.findByRole("button", { name: "Copilot · Open copilot" });
      fireEvent.keyDown(window, { key: " ", code: "Space", altKey: true });
      const recognition = Recognition.last!;
      expect(recognition.continuous).toBe(true);
      act(() => recognition.say(text));
      fireEvent.keyUp(window, { key: " ", code: "Space", altKey: true });
    }

    function turnPosts() {
      const calls = (fetch as unknown as { mock: { calls: [string, RequestInit?][] } }).mock.calls;
      return calls.filter(([url, init]) => init?.method === "POST" && String(url).startsWith("/api/copilot/conversations"));
    }

    it("puts what was said in the box to check, unsent", async () => {
      stubServer(200);
      useCopilot.setState({ voiceAutoSend: false, intent: "make" });
      await dictate("plan the launch");
      const box = await screen.findByRole("textbox", { name: "Message to the copilot" });
      await vi.waitFor(() => expect(box).toHaveValue("plan the launch"));
      expect(screen.getByText("Check the text, then send")).toBeInTheDocument();
      expect(turnPosts()).toHaveLength(0);
    });

    it("sends it when the person chose to send when they stop talking", async () => {
      stubServer(200);
      useCopilot.setState({ voiceAutoSend: true, intent: "make" });
      await dictate("plan the launch");
      await vi.waitFor(() => expect(turnPosts().length).toBeGreaterThan(0));
    });
  });
});
