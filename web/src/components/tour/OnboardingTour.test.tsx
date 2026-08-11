import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp, type AppState } from "../../store";
import { OnboardingTour } from "./OnboardingTour";

// The real `updateUIPreference` writes through the preference schema; the tour
// only needs to hand it the right key/value, so a spy keeps the test focused
// (and independent of when the `tourPrompt`/`tourChaptersDone` schema entries
// land).
const updateUIPreference = vi.fn();

function setTourPrefs(prompt: string, chaptersDone: string[] = []) {
  useApp.setState((state) => ({
    preferences: {
      ...state.preferences,
      tourPrompt: prompt,
      tourChaptersDone: chaptersDone,
    } as AppState["preferences"],
  }));
}

const extraElements: HTMLElement[] = [];

/** jsdom rects are all-zero; the tour skips zero-size targets, so stub one. */
function addTarget(create: (el: HTMLElement) => void): HTMLElement {
  const el = document.createElement("div");
  create(el);
  el.getBoundingClientRect = () =>
    ({
      top: 40,
      left: 20,
      width: 240,
      height: 120,
      right: 260,
      bottom: 160,
      x: 20,
      y: 40,
      toJSON: () => ({}),
    }) as DOMRect;
  document.body.appendChild(el);
  extraElements.push(el);
  return el;
}

function addGraphLane(): HTMLElement {
  return addTarget((el) => {
    el.className = "lane-wrap graph";
  });
}

beforeEach(() => {
  updateUIPreference.mockReset();
  useApp.setState({
    activeTab: null,
    updateUIPreference:
      updateUIPreference as unknown as AppState["updateUIPreference"],
  });
});

afterEach(() => {
  vi.useRealTimers();
  for (const el of extraElements.splice(0)) el.remove();
});

describe("OnboardingTour welcome dialog", () => {
  it("appears after a short delay while the choice is pending", () => {
    vi.useFakeTimers();
    setTourPrefs("pending");
    render(<OnboardingTour />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(800);
    });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("歡迎使用 Nodevas")).toBeInTheDocument();
  });

  it.each(["declined", "accepted"])("stays hidden when the choice is %s", (prompt) => {
    vi.useFakeTimers();
    setTourPrefs(prompt);
    render(<OnboardingTour />);
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("開始導覽 records acceptance and plays the shell chapter", () => {
    vi.useFakeTimers();
    addTarget((el) => {
      el.id = "project-explorer";
    });
    setTourPrefs("pending");
    render(<OnboardingTour />);
    act(() => {
      vi.advanceTimersByTime(800);
    });

    fireEvent.click(screen.getByText("開始導覽"));

    expect(updateUIPreference).toHaveBeenCalledWith("tourPrompt", "accepted");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("專案總管")).toBeInTheDocument();
  });

  it("先不用 records the decline and keeps contextual triggers off", () => {
    vi.useFakeTimers();
    const lane = addGraphLane();
    setTourPrefs("pending");
    render(<OnboardingTour />);
    act(() => {
      vi.advanceTimersByTime(800);
    });

    fireEvent.click(screen.getByText("先不用"));
    expect(updateUIPreference).toHaveBeenCalledWith("tourPrompt", "declined");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // The stubbed store still says "pending"; mirror the write like the app
    // would, then verify a click in the graph never starts a chapter.
    act(() => {
      setTourPrefs("declined");
    });
    fireEvent.pointerDown(lane);
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("OnboardingTour contextual chapters", () => {
  it("starts the graph chapter on pointerdown and records it when finished", () => {
    vi.useFakeTimers();
    const lane = addGraphLane();
    setTourPrefs("accepted");
    render(<OnboardingTour />);

    fireEvent.pointerDown(lane);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(300);
    });

    // `.board-primary-action` does not exist here, so the chapter opens on
    // its first usable step and skips the missing target.
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("節點卡片")).toBeInTheDocument();

    fireEvent.click(screen.getByText("下一步"));
    expect(screen.getByText("建立依賴")).toBeInTheDocument();
    fireEvent.click(screen.getByText("下一步"));
    expect(screen.getByText("批次操作")).toBeInTheDocument();

    fireEvent.click(screen.getByText("完成"));
    expect(updateUIPreference).toHaveBeenCalledWith("tourChaptersDone", ["graph"]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("Escape closes the chapter and still marks it done", () => {
    vi.useFakeTimers();
    const lane = addGraphLane();
    setTourPrefs("accepted");
    render(<OnboardingTour />);

    fireEvent.pointerDown(lane);
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(updateUIPreference).toHaveBeenCalledWith("tourChaptersDone", ["graph"]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("全部略過 turns the tour off for good", () => {
    vi.useFakeTimers();
    const lane = addGraphLane();
    addTarget((el) => {
      el.className = "drawer";
    });
    setTourPrefs("accepted");
    render(<OnboardingTour />);

    fireEvent.pointerDown(lane);
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByText("全部略過"));

    // Both halves are written: the flag is what silences the triggers, the
    // list is what a half-updated read would otherwise disagree with.
    expect(updateUIPreference).toHaveBeenCalledWith("tourPrompt", "declined");
    expect(updateUIPreference).toHaveBeenCalledWith(
      "tourChaptersDone",
      ["shell", "graph", "timeline", "drawer", "explorer"],
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // Mirror the write the store would have made, then confirm neither trigger
    // kind brings the tour back.
    act(() => {
      setTourPrefs("declined", ["shell", "graph", "timeline", "drawer", "explorer"]);
    });
    fireEvent.pointerDown(lane);
    act(() => {
      useApp.setState({ activeTab: "node-1" as AppState["activeTab"] });
    });
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not retrigger a chapter that is already done", () => {
    vi.useFakeTimers();
    const lane = addGraphLane();
    setTourPrefs("accepted", ["graph"]);
    render(<OnboardingTour />);

    fireEvent.pointerDown(lane);
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("starts the drawer chapter when the node drawer opens", () => {
    vi.useFakeTimers();
    addTarget((el) => {
      el.className = "drawer";
    });
    setTourPrefs("accepted");
    render(<OnboardingTour />);

    act(() => {
      useApp.setState({ activeTab: "node-1" as AppState["activeTab"] });
    });
    act(() => {
      vi.advanceTimersByTime(400);
    });

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("節點編輯面板")).toBeInTheDocument();
  });
});

describe("OnboardingTour replay", () => {
  it("nodevas-tour-start clears done chapters and reopens the welcome dialog", () => {
    setTourPrefs("accepted", ["graph", "timeline"]);
    render(<OnboardingTour />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    act(() => {
      window.dispatchEvent(new Event("nodevas-tour-start"));
    });

    expect(updateUIPreference).toHaveBeenCalledWith("tourChaptersDone", []);
    expect(updateUIPreference).toHaveBeenCalledWith("tourPrompt", "pending");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("歡迎使用 Nodevas")).toBeInTheDocument();
  });
});
