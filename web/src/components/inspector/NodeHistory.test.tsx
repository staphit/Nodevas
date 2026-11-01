import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { NodeHistory } from "./NodeHistory";

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    runState: {
      nodes: {},
      history: [{ t: "2026-10-01T00:00:00Z", event: "status", node: "a", from: "ready", to: "done", by: "alice" }],
    },
  });
  vi.spyOn(api, "getHistory").mockResolvedValue({
    path: "a.md",
    versions: [{ name: "v1", at: "2026-10-01T00:00:00Z", size: 12, author: "bob" }],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("NodeHistory", () => {
  it("names who made a change in the chosen language", async () => {
    const { container } = render(<NodeHistory id="a" path="a.md" format="md" onRestored={vi.fn()} />);
    const details = container.querySelector("details")!;
    details.open = true;
    fireEvent(details, new Event("toggle"));

    expect(await screen.findByText("由 bob")).toBeInTheDocument();
    expect(screen.getByText("由 alice")).toBeInTheDocument();
    expect(screen.queryByText(/\bby\b/)).not.toBeInTheDocument();
  });
});
