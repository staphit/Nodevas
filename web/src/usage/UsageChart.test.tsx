import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { UsageGroup } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { UsageChart } from "./UsageChart";

function day(key: string, input: number, output: number, cache: number, cost = 0): UsageGroup {
  return {
    key,
    inputTokens: input,
    outputTokens: output,
    cacheReadTokens: cache,
    cacheWriteTokens: 0,
    totalTokens: input + output + cache,
    costUsd: cost,
    reportedCostUsd: 0,
    estimatedCostUsd: cost,
    unpricedTokens: 0,
    runs: 0,
    sessions: 0,
  };
}

const days = [day("2026-10-22", 0, 0, 0), day("2026-10-23", 100, 50, 0, 1), day("2026-10-24", 300, 20, 10, 2)];

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("UsageChart", () => {
  it("stacks a segment per non-zero series, rounding only the top one", () => {
    const { container } = render(<UsageChart days={days} metric="tokens" />);
    // Day one draws nothing; day two has input and output; day three all three.
    expect(container.querySelectorAll(".usage-bar")).toHaveLength(5);
    // The data end of each bar is the one path.
    expect(container.querySelectorAll("path.usage-bar")).toHaveLength(2);
    expect(container.querySelectorAll("path.usage-bar-output, path.usage-bar-cache")).toHaveLength(2);
    expect(screen.getByRole("list", { name: "Legend" })).toHaveTextContent("InputOutputCache");
  });

  it("shows a day's values on hover and on focus", () => {
    const { container } = render(<UsageChart days={days} metric="tokens" />);
    const hits = container.querySelectorAll(".usage-hit");
    expect(hits).toHaveLength(3);
    fireEvent.mouseEnter(hits[2]);
    const tip = screen.getByRole("status");
    expect(tip).toHaveTextContent("Oct 24");
    expect(tip).toHaveTextContent("Input 300");
    expect(tip).toHaveTextContent("Cache 10");
    fireEvent.focus(hits[1]);
    expect(screen.getByRole("status")).toHaveTextContent("Oct 23");
    expect(hits[1]).toHaveAttribute("aria-label", "Oct 23: Input 100, Output 50, Cache 0");
  });

  it("draws one series for cost, with no legend", () => {
    const { container } = render(<UsageChart days={days} metric="cost" />);
    expect(container.querySelectorAll(".usage-bar-cost")).toHaveLength(2);
    expect(screen.queryByRole("list", { name: "Legend" })).not.toBeInTheDocument();
    expect(screen.getByText("Estimated cost per day (UTC)")).toBeInTheDocument();
  });
});
