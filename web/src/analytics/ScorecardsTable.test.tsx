import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import ScorecardsTable from "./ScorecardsTable";
import { scorecardRow, scorecards } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

// W-03: the scorecards table.
describe("ScorecardsTable", () => {
  it("shows a row per role, agent and model, with a dash for no data", () => {
    const data = scorecards([
      scorecardRow(),
      scorecardRow({
        role: "reviewer",
        agent: "codex",
        model: "gpt-5.5",
        samples: 22,
        accuracy: 0.6667,
        delivery: null,
        firstPassRate: null,
        reworkRounds: null,
        costEstimated: true,
        costPerDelivered: 0.2,
        score: 0.85,
        tier: "senior",
      }),
    ]);
    render(<ScorecardsTable data={data} track="all" onTrack={vi.fn()} />);
    const table = screen.getByRole("table", { name: "Scorecards" });
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3);
    const exec = rows[1];
    expect(within(exec).getByText("Executor")).toBeInTheDocument();
    expect(within(exec).getByText("claude")).toBeInTheDocument();
    expect(within(exec).getByText("sonnet")).toBeInTheDocument();
    expect(within(exec).getByText("33%")).toBeInTheDocument();
    expect(within(exec).getByText("$1.50")).toBeInTheDocument();
    expect(within(exec).getByText("Probation")).toBeInTheDocument();
    const review = rows[2];
    expect(within(review).getAllByText("—").length).toBeGreaterThan(0);
    expect(within(review).getByText("$0.20 (est.)")).toBeInTheDocument();
    expect(within(review).getByText("Senior")).toHaveClass("tier--senior");
    expect(within(review).getByText("67%")).toBeInTheDocument();
  });

  it("switches between code, story and all", async () => {
    const onTrack = vi.fn();
    render(<ScorecardsTable data={scorecards()} track="all" onTrack={onTrack} />);
    const tracks = screen.getByRole("group", { name: "Track" });
    expect(within(tracks).getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(tracks).getByRole("button", { name: "Story" }));
    expect(onTrack).toHaveBeenCalledWith("story");
    await userEvent.click(within(tracks).getByRole("button", { name: "Code" }));
    expect(onTrack).toHaveBeenCalledWith("code");
  });

  it("says when there is nothing yet", () => {
    render(<ScorecardsTable data={scorecards([])} track="all" onTrack={vi.fn()} />);
    expect(screen.getByText("Not enough finished runs yet.")).toBeInTheDocument();
  });
});
