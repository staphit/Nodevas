import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { hasMessage } from "../../i18n";
import { useApp, type AppState } from "../../store";
import { TOUR_CHAPTERS } from "./chapters";
import { OnboardingTour } from "./OnboardingTour";

// The welcome card, every chapter and the tour's own buttons were written in
// Traditional Chinese only, so an English interface replayed the tour in a
// language its reader may not have. They follow the interface language now.

const updateUIPreference = vi.fn();
const added: HTMLElement[] = [];

function setPrefs(language: "en" | "zh-TW", tourPrompt: string) {
  useApp.setState((state) => ({
    preferences: { ...state.preferences, language, tourPrompt, tourChaptersDone: [] } as AppState["preferences"],
    activeTab: null,
    updateUIPreference: updateUIPreference as unknown as AppState["updateUIPreference"],
  }));
}

function addLane() {
  const el = document.createElement("div");
  el.className = "lane-wrap graph";
  el.getBoundingClientRect = () =>
    ({ top: 40, left: 20, width: 240, height: 120, right: 260, bottom: 160, x: 20, y: 40, toJSON: () => ({}) }) as DOMRect;
  document.body.appendChild(el);
  added.push(el);
  return el;
}

beforeEach(() => {
  updateUIPreference.mockReset();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  for (const el of added.splice(0)) el.remove();
});

it("welcomes an English reader in English", () => {
  setPrefs("en", "pending");
  render(<OnboardingTour />);
  act(() => { vi.advanceTimersByTime(800); });
  expect(screen.getByRole("dialog", { name: "Welcome to Nodevas" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Not now" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Start the tour" })).toBeInTheDocument();
  expect(document.body.textContent).not.toMatch(/[一-鿿]/);
});

it("plays a chapter and its buttons in English", () => {
  setPrefs("en", "accepted");
  const lane = addLane();
  render(<OnboardingTour />);
  fireEvent.pointerDown(lane);
  act(() => { vi.advanceTimersByTime(300); });
  expect(screen.getByRole("dialog", { name: "Node cards" })).toBeInTheDocument();
  for (const name of ["Skip", "Skip all", "Next"]) {
    expect(screen.getByRole("button", { name })).toBeInTheDocument();
  }
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("button", { name: "Back" })).toBeInTheDocument();
  expect(document.body.textContent).not.toMatch(/[一-鿿]/);
});

it("keeps Traditional Chinese for a zh-TW reader", () => {
  setPrefs("zh-TW", "pending");
  render(<OnboardingTour />);
  act(() => { vi.advanceTimersByTime(800); });
  expect(screen.getByRole("dialog", { name: "歡迎使用 Nodevas" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "開始導覽" })).toBeInTheDocument();
});

it("has every step in both languages", () => {
  const keys = TOUR_CHAPTERS.flatMap((chapter) => chapter.steps.flatMap((step) => [step.titleKey, step.bodyKey]));
  expect(keys.length).toBeGreaterThan(0);
  for (const key of keys) {
    expect(hasMessage("en", key), key).toBe(true);
    expect(hasMessage("zh-TW", key), key).toBe(true);
  }
});
