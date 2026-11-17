import { act, cleanup, fireEvent, render, renderHook, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreference, readPreferences, writePreference } from "../preferences";
import { useApp } from "../store";
import { Composer } from "./Composer";
import { useCopilot } from "./copilotStore";
import { useSpeechRecognition } from "./speech";

const draftApi = vi.hoisted(() => ({ canCreate: vi.fn(() => false), createDraft: vi.fn(async () => true) }));
vi.mock("./draft/draftApi", () => draftApi);

function renderComposer(onSend = vi.fn()) {
  const { result } = renderHook(() => useSpeechRecognition({ lang: "en-US", onFinal: vi.fn() }));
  render(<Composer running={false} speech={result.current} onSend={onSend} onStop={vi.fn()} />);
  return { onSend, box: screen.getByRole("textbox", { name: "Message to the copilot" }) };
}

const openDraftItem = {
  kind: "draft" as const,
  id: "e1",
  text: "s",
  data: { draft: { summary: "s", nodes: [] }, issues: [], provider: "openai", model: "m", state: "open" as const },
};

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "main",
    canvasSelection: { project: "", ids: [] },
  });
  writePreference("copilotIntent", "make");
  useCopilot.setState({ intent: "make", voiceAutoSend: false, items: [], composerFill: null, drafts: {} });
  draftApi.canCreate.mockReturnValue(false);
  draftApi.createDraft.mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("Composer", () => {
  it("defaults to Make nodes and remembers Ask", async () => {
    const user = userEvent.setup();
    const { box } = renderComposer();
    const make = screen.getByRole("button", { name: "Make nodes" });
    expect(make).toHaveAttribute("aria-pressed", "true");
    expect(box).toHaveAttribute("placeholder", "Describe what you want to get done…");
    await user.click(screen.getByRole("button", { name: "Ask" }));
    expect(readPreference("copilotIntent")).toBe("ask");
    expect(useCopilot.getState().intent).toBe("ask");
    expect(box).toHaveAttribute("placeholder", "Ask about this project…");
    expect(box).toHaveFocus();
  });

  it("still switches when storage refuses to save", async () => {
    const user = userEvent.setup();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    renderComposer();
    await user.click(screen.getByRole("button", { name: "Ask" }));
    expect(useCopilot.getState().intent).toBe("ask");
    expect(screen.getByRole("button", { name: "Ask" })).toHaveAttribute("aria-pressed", "true");
  });

  it("says a send goes after the canvas selection, in Make nodes only", () => {
    useApp.setState({ canvasSelection: { project: "main", ids: ["a", "b", "c"] } });
    renderComposer();
    expect(screen.getByText("After 3 selected nodes")).toBeInTheDocument();
    act(() => useCopilot.setState({ intent: "ask" }));
    expect(screen.queryByText("After 3 selected nodes")).toBeNull();
  });

  it("asks to change the open draft, and Enter in the empty box creates it", async () => {
    const user = userEvent.setup();
    useCopilot.setState({ items: [openDraftItem] as never });
    const { box, onSend } = renderComposer();
    expect(box).toHaveAttribute("placeholder", "Change this draft…");
    await user.type(box, "{Enter}");
    expect(draftApi.createDraft).not.toHaveBeenCalled();
    draftApi.canCreate.mockReturnValue(true);
    await user.type(box, "{Enter}");
    expect(draftApi.createDraft).toHaveBeenCalledWith("e1");
    expect(onSend).not.toHaveBeenCalled();
    // With text, Enter sends it (a change to the draft), not Create.
    await user.type(box, "add a review{Enter}");
    expect(onSend).toHaveBeenCalledWith("add a review");
    expect(draftApi.createDraft).toHaveBeenCalledTimes(1);
  });

  it("puts spoken text in the box to check, without sending", () => {
    const { box, onSend } = renderComposer();
    act(() => useCopilot.getState().fillComposer("plan the launch", true));
    expect(box).toHaveValue("plan the launch");
    expect(box).toHaveClass("is-from-voice");
    expect(screen.getByText("Check the text, then send")).toBeInTheDocument();
    expect(onSend).not.toHaveBeenCalled();
    fireEvent.keyDown(box, { key: "Enter" });
    expect(onSend).toHaveBeenCalledWith("plan the launch");
    expect(screen.queryByText("Check the text, then send")).toBeNull();
  });

  it("uses a fill once: closing and reopening the panel does not bring it back", () => {
    const first = renderComposer();
    act(() => useCopilot.getState().fillComposer("Plan a product launch", true));
    expect(first.box).toHaveValue("Plan a product launch");
    fireEvent.keyDown(first.box, { key: "Enter" });
    expect(first.onSend).toHaveBeenCalledWith("Plan a product launch");
    cleanup();
    const again = renderComposer();
    expect(again.box).toHaveValue("");
    expect(again.box).not.toHaveClass("is-from-voice");
    expect(screen.queryByText("Check the text, then send")).toBeNull();
  });

  it("takes a fill that came while the panel was closed, once", () => {
    act(() => useCopilot.getState().fillComposer("What is blocked?"));
    const first = renderComposer();
    expect(first.box).toHaveValue("What is blocked?");
    cleanup();
    expect(renderComposer().box).toHaveValue("");
  });

  it("adds spoken text after what was typed, and an example replaces it", async () => {
    const user = userEvent.setup();
    const { box } = renderComposer();
    await user.type(box, "plan the launch,");
    act(() => useCopilot.getState().fillComposer("then email the team", true));
    expect(box).toHaveValue("plan the launch, then email the team");
    expect(box).toHaveClass("is-from-voice");
    act(() => useCopilot.getState().fillComposer("and a press kit", true));
    expect(box).toHaveValue("plan the launch, then email the team and a press kit");
    act(() => useCopilot.getState().fillComposer("What is blocked?"));
    expect(box).toHaveValue("What is blocked?");
    expect(box).not.toHaveClass("is-from-voice");
  });

  it("joins spoken Chinese to typed Chinese without a space", async () => {
    const user = userEvent.setup();
    const { box } = renderComposer();
    await user.type(box, "幫我規劃發表會");
    act(() => useCopilot.getState().fillComposer("然後寄信給團隊", true));
    expect(box).toHaveValue("幫我規劃發表會然後寄信給團隊");
    // Typed punctuation in full width needs no space either.
    act(() => useCopilot.getState().fillComposer("，也做新聞稿", true));
    expect(box).toHaveValue("幫我規劃發表會然後寄信給團隊，也做新聞稿");
    // A space only where neither side of the join is Chinese.
    act(() => useCopilot.getState().fillComposer("and a press kit", true));
    expect(box).toHaveValue("幫我規劃發表會然後寄信給團隊，也做新聞稿and a press kit");
  });

  it("names one selected node in the singular", () => {
    useApp.setState({ canvasSelection: { project: "main", ids: ["a"] } });
    renderComposer();
    expect(screen.getByText("After 1 selected node")).toBeInTheDocument();
  });

  describe("growing with its text", () => {
    let style: HTMLStyleElement;
    let scrollHeight = 0;
    beforeEach(() => {
      // jsdom lays nothing out: the box's sizes come from this sheet and the
      // scrollHeight below (content plus padding, never the border).
      style = document.createElement("style");
      style.textContent =
        ".copilot-input { box-sizing: border-box; line-height: 20px; padding-top: 8px; padding-bottom: 8px;" +
        " border-top-width: 1px; border-bottom-width: 1px; border-style: solid; }";
      document.head.appendChild(style);
      vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockImplementation(() => scrollHeight);
    });
    afterEach(() => style.remove());

    it("counts the border, so the text fits without a scrollbar", async () => {
      const user = userEvent.setup();
      const { box } = renderComposer();
      scrollHeight = 56; // two lines: 2 x 20 + 16 padding
      await user.type(box, "a{Shift>}{Enter}{/Shift}b");
      expect(box.style.height).toBe("58px");
    });

    it("stops at eight lines, border included", async () => {
      const user = userEvent.setup();
      const { box } = renderComposer();
      scrollHeight = 20 * 30 + 16;
      await user.type(box, "long");
      expect(box.style.height).toBe(`${20 * 8 + 16 + 2}px`);
    });
  });
});
