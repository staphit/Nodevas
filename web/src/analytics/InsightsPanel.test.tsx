import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import InsightsPanel from "./InsightsPanel";
import { insights } from "./testFixtures";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

// W-04: the insights panel.
describe("InsightsPanel", () => {
  it("shows cost per delivered task, first pass, rework and waste", () => {
    render(<InsightsPanel insights={insights()} />);
    expect(screen.getByText("Cost per delivered task").parentElement).toHaveTextContent("$1.80");
    expect(screen.getByText("First-pass rate").parentElement).toHaveTextContent("33%");
    expect(screen.getByText("Rework rounds").parentElement).toHaveTextContent("0.33");
    expect(screen.getByText("Includes estimated costs for CLIs that report $0.")).toBeInTheDocument();

    const waste = screen.getByRole("region", { name: "Waste" });
    expect(within(waste).getByText("Failed or cancelled runs").parentElement).toHaveTextContent("$0.50");
    expect(within(waste).getByText("Retried failures").parentElement).toHaveTextContent("$0.10");
    expect(within(waste).getByText("Undelivered tasks").parentElement).toHaveTextContent("$0.50");

    const hotspots = screen.getByRole("region", { name: "Waste hotspots" });
    expect(within(hotspots).getByText("Checkout retries")).toBeInTheDocument();

    const byRole = screen.getByRole("region", { name: "By role" });
    expect(within(byRole).getByText("Executor")).toBeInTheDocument();
    expect(within(byRole).getByText("$1.50")).toBeInTheDocument();
  });

  it("says when there is not enough yet", () => {
    render(
      <InsightsPanel
        insights={insights({
          totals: { costUsd: 0, estimatedCostUsd: 0, runs: 0, units: 0, delivered: 0, costPerDelivered: null, firstPassRate: null, reworkRounds: null },
          byRole: [],
          waste: { costUsd: 0, failedCostUsd: 0, retriedCostUsd: 0, abandonedCostUsd: 0, hotspots: [] },
          suggestions: [],
        })}
      />,
    );
    expect(screen.getByText("Not enough finished runs yet.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Waste hotspots" })).not.toBeInTheDocument();
  });
});
