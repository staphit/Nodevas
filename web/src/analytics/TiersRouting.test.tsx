import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { pick, settings, tiers } from "./testFixtures";
import TiersRouting from "./TiersRouting";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

// W-06: tiers and auto routing.
describe("TiersRouting", () => {
  it("has auto routing off by default and saves the toggle", async () => {
    const onChange = vi.fn();
    render(<TiersRouting tiers={tiers()} settings={settings()} picks={[]} readOnly={false} onChange={onChange} />);
    const toggle = screen.getByRole("switch", { name: "Auto routing in this project" });
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(screen.getByText("Off by default. Your routing rules always win; budgets still apply.")).toBeInTheDocument();
    await userEvent.click(toggle);
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ autoRouting: true }));
  });

  it("sets a role's required tier", async () => {
    const onChange = vi.fn();
    render(<TiersRouting tiers={tiers()} settings={settings()} picks={[]} readOnly={false} onChange={onChange} />);
    const executor = screen.getByRole("combobox", { name: "Required tier for Executor" });
    expect(executor).toHaveValue("mid");
    await userEvent.selectOptions(executor, "senior");
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ requiredTiers: expect.objectContaining({ executor: "senior" }) }));
  });

  it("keeps the exploration share between 0 and 50", () => {
    const onChange = vi.fn();
    render(<TiersRouting tiers={tiers()} settings={settings()} picks={[]} readOnly={false} onChange={onChange} />);
    const share = screen.getByRole("spinbutton", { name: "Exploration share" });
    fireEvent.change(share, { target: { value: "80" } });
    fireEvent.blur(share);
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ explorationPct: 50 }));
    fireEvent.change(share, { target: { value: "-5" } });
    fireEvent.blur(share);
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ explorationPct: 0 }));
  });

  it("is read-only for a viewer", () => {
    render(<TiersRouting tiers={tiers()} settings={settings()} picks={[]} readOnly onChange={vi.fn()} />);
    expect(screen.getByRole("switch", { name: "Auto routing in this project" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Required tier for Executor" })).toBeDisabled();
    expect(screen.getByRole("spinbutton", { name: "Exploration share" })).toBeDisabled();
  });

  it("lists recent picks with their reasons", () => {
    render(
      <TiersRouting
        tiers={tiers()}
        settings={settings({ autoRouting: true })}
        picks={[
          pick(),
          pick({ id: "p2", source: "manual", ruleId: "main", reason: { code: "manual" }, agent: "claude", model: "sonnet" }),
          pick({ id: "p3", source: "none", reason: { code: "none_meets_tier" }, agent: "", model: "", requiredTier: "senior" }),
        ]}
        readOnly={false}
        onChange={vi.fn()}
      />,
    );
    const picks = screen.getByRole("region", { name: "Recent picks" });
    expect(within(picks).getByText("Cheapest at Mid or above")).toBeInTheDocument();
    expect(within(picks).getByText("Your rule main")).toBeInTheDocument();
    expect(within(picks).getByText("No combination meets Senior")).toBeInTheDocument();
    expect(
      screen.getByText("Tiers use the last 30 days and at least 10 samples. New combinations start on probation."),
    ).toBeInTheDocument();
  });
});
