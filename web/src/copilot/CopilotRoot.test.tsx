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

  it("shows the mic button and toggles the panel with Ctrl+Shift+Space", async () => {
    stubServer(200);
    render(<CopilotRoot />);
    const fab = await screen.findByRole("button", { name: "Voice Copilot · Open copilot" });
    // Its name is on it, not only in a tooltip.
    expect(fab).toHaveTextContent("Voice Copilot");
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
    await screen.findByRole("button", { name: "Voice Copilot · Open copilot" });
    const other = screen.getByRole("textbox", { name: "other" });
    fireEvent.keyDown(other, { key: " ", code: "Space", altKey: true });
    expect(useCopilot.getState().open).toBe(false);
    fireEvent.keyDown(document.body, { key: " ", code: "Space", altKey: true });
    expect(useCopilot.getState().open).toBe(true);
  });
});
