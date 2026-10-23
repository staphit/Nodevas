import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type RunBudgetStatus } from "../api";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { BudgetSettings, parseBudgetDraft } from "./BudgetSettings";
import { useBudget } from "./budgetStore";

function status(overrides: Partial<RunBudgetStatus> = {}): RunBudgetStatus {
  return {
    budget: { dailyRuns: 10, dailyCostUsd: 5, dailyTokens: 0 },
    used: { runs: 3, costUsd: 1.25, tokens: 120000 },
    exceeded: "",
    ...overrides,
  };
}

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useBudget.getState().reset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("parseBudgetDraft", () => {
  it("takes whole counts and a dollar amount with up to two decimals", () => {
    expect(parseBudgetDraft({ runs: "10", cost: "2.5", tokens: "0" })).toEqual({
      budget: { dailyRuns: 10, dailyCostUsd: 2.5, dailyTokens: 0 },
      errors: null,
    });
    expect(parseBudgetDraft({ runs: " 3 ", cost: "0", tokens: "1000000" }).budget).toEqual({
      dailyRuns: 3,
      dailyCostUsd: 0,
      dailyTokens: 1000000,
    });
  });

  it("refuses negatives, fractions of a run, blanks and fine-grained cents", () => {
    expect(parseBudgetDraft({ runs: "-1", cost: "1", tokens: "1" }).errors).toEqual({ runs: "whole" });
    expect(parseBudgetDraft({ runs: "1.5", cost: "1", tokens: "1" }).errors).toEqual({ runs: "whole" });
    expect(parseBudgetDraft({ runs: "1", cost: "", tokens: "" }).errors).toEqual({
      cost: "money",
      tokens: "whole",
    });
    expect(parseBudgetDraft({ runs: "1", cost: "0.001", tokens: "1" }).errors).toEqual({
      cost: "money",
    });
    expect(parseBudgetDraft({ runs: "1", cost: "-2", tokens: "1" }).errors).toEqual({
      cost: "money",
    });
  });
});

describe("BudgetSettings", () => {
  it("shows today's use against each limit", async () => {
    vi.spyOn(api, "getRunBudget").mockResolvedValue(status());
    render(<BudgetSettings />);

    const runs = await screen.findByRole("progressbar", { name: "Runs" });
    expect(runs).toHaveAttribute("aria-valuenow", "30");
    expect(screen.getByText("3 / 10")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Estimated cost" })).toHaveAttribute(
      "aria-valuenow",
      "25",
    );
    expect(screen.getByText("$1.25 / $5.00")).toBeInTheDocument();
    // No token limit: the use, and no bar.
    expect(screen.queryByRole("progressbar", { name: "Tokens" })).not.toBeInTheDocument();
    expect(screen.getByText("120K · no limit")).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "Runs per day" })).toHaveValue(10);
    expect(screen.getByRole("button", { name: "Save budget" })).toBeDisabled();
  });

  it("says which limit is used up", async () => {
    vi.spyOn(api, "getRunBudget").mockResolvedValue(
      status({ used: { runs: 10, costUsd: 1, tokens: 0 }, exceeded: "runs" }),
    );
    render(<BudgetSettings />);
    expect(await screen.findByText(/Today's budget is used up \(Runs\)/)).toBeInTheDocument();
  });

  it("validates while typing and saves the limits", async () => {
    vi.spyOn(api, "getRunBudget").mockResolvedValue(status());
    const put = vi
      .spyOn(api, "putRunBudget")
      .mockResolvedValue(status({ budget: { dailyRuns: 20, dailyCostUsd: 2.5, dailyTokens: 500000 } }));
    render(<BudgetSettings />);

    const runs = await screen.findByRole("spinbutton", { name: "Runs per day" });
    const save = screen.getByRole("button", { name: "Save budget" });

    fireEvent.change(runs, { target: { value: "-3" } });
    expect(runs).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Enter a whole number, 0 or more.")).toBeInTheDocument();
    expect(save).toBeDisabled();

    fireEvent.change(runs, { target: { value: "20" } });
    fireEvent.change(screen.getByRole("spinbutton", { name: "Cost per day (USD)" }), {
      target: { value: "2.5" },
    });
    fireEvent.change(screen.getByRole("spinbutton", { name: "Tokens per day" }), {
      target: { value: "500000" },
    });
    expect(runs).not.toHaveAttribute("aria-invalid");
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith({ dailyRuns: 20, dailyCostUsd: 2.5, dailyTokens: 500000 }),
    );
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "Runs per day" })).toHaveValue(20);
    expect(screen.getByRole("button", { name: "Save budget" })).toBeDisabled();
  });

  it("shows why a save failed", async () => {
    vi.spyOn(api, "getRunBudget").mockResolvedValue(status());
    vi.spyOn(api, "putRunBudget").mockRejectedValue(new Error("dailyRuns must be >= 0"));
    render(<BudgetSettings />);
    fireEvent.change(await screen.findByRole("spinbutton", { name: "Runs per day" }), {
      target: { value: "4" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save budget" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("dailyRuns must be >= 0");
  });
});
