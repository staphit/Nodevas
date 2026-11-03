import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useApp, type AppState } from "../../store";
import { OnboardingTour } from "./OnboardingTour";

// A first launch with Ctrl+K pressed inside the welcome delay: the invitation
// used to open on top of the command palette, two modals at once, and 開始導覽
// then spotlit the explorer under a palette that was still open. The
// invitation waits for the other dialog to close.

const updateUIPreference = vi.fn();
let palette: HTMLElement | null = null;

beforeEach(() => {
  updateUIPreference.mockReset();
  useApp.setState((state) => ({
    preferences: {
      ...state.preferences,
      language: "zh-TW",
      tourPrompt: "pending",
      tourChaptersDone: [],
    } as AppState["preferences"],
    activeTab: null,
    updateUIPreference: updateUIPreference as unknown as AppState["updateUIPreference"],
  }));
});

afterEach(() => {
  vi.useRealTimers();
  palette?.remove();
  palette = null;
});

function openPalette() {
  palette = document.createElement("section");
  palette.className = "command-palette";
  palette.setAttribute("role", "dialog");
  palette.setAttribute("aria-modal", "true");
  palette.setAttribute("aria-label", "搜尋或命令");
  const input = document.createElement("input");
  palette.appendChild(input);
  document.body.appendChild(palette);
  input.focus();
}

it("waits for an open command palette to close before inviting", () => {
  vi.useFakeTimers();
  render(<OnboardingTour />);
  openPalette();
  act(() => {
    vi.advanceTimersByTime(3000);
  });
  expect(screen.queryByText("歡迎使用 Nodevas")).not.toBeInTheDocument();

  act(() => {
    palette?.remove();
    palette = null;
  });
  act(() => {
    vi.advanceTimersByTime(1000);
  });
  expect(screen.getByText("歡迎使用 Nodevas")).toBeInTheDocument();
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
});
