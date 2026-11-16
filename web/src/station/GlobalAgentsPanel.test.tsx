import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";
import { localRunner } from "../local/localFixtures";
import { readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useApp } from "../store";
import { GlobalAgentsPanel } from "./GlobalAgentsPanel";
import { useGlobalAgents } from "./globalAgentsStore";

// The panel's parts are tested on their own; here only which of them shows.
vi.mock("./GlobalAgentForm", () => ({ GlobalAgentForm: () => <div data-testid="global-form" /> }));
vi.mock("./GlobalAgentList", () => ({ GlobalAgentList: () => <div data-testid="global-list" /> }));
vi.mock("./RunnerCapacity", () => ({ RunnerCapacity: () => null }));
vi.mock("../local/ThisComputerCard", () => ({ ThisComputerCard: () => null }));
vi.mock("./SimpleAgentBox", () => ({ SimpleAgentBox: () => <div data-testid="simple-box" /> }));

function renderPanel(edition: "single" | "team", experience: "simple" | "full") {
  useApp.setState({ preferences: { ...readPreferences(), language: "en", experience } });
  return render(
    <EditionProvider edition={edition}>
      <GlobalAgentsPanel />
    </EditionProvider>,
  );
}

beforeEach(() => {
  useRunners.setState({ loaded: true, loading: false, runners: [localRunner()], error: null, creating: false, revoking: {} });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [localRunner()] });
  vi.spyOn(useBudget.getState(), "load").mockResolvedValue(undefined as never);
  // No global agent yet: today's panel opens its form.
  useGlobalAgents.setState({ loaded: true, runs: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("GlobalAgentsPanel", () => {
  it("opens on the describe box in Simple, with the full form behind More options", () => {
    renderPanel("single", "simple");
    expect(screen.getByTestId("simple-box")).toBeInTheDocument();
    expect(screen.queryByTestId("global-form")).not.toBeInTheDocument();
    expect(screen.getByTestId("global-list")).toBeInTheDocument();

    const more = screen.getByRole("button", { name: "More options" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(more);
    expect(screen.getByTestId("global-form")).toBeInTheDocument();
    const fewer = screen.getByRole("button", { name: "Fewer options" });
    expect(fewer).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(fewer);
    expect(screen.queryByTestId("global-form")).not.toBeInTheDocument();
  });

  it("shows today's form in single when Simple is off", () => {
    renderPanel("single", "full");
    expect(screen.queryByTestId("simple-box")).not.toBeInTheDocument();
    expect(screen.getByTestId("global-form")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More options" })).not.toBeInTheDocument();
  });

  it("shows today's form on a team hub, whatever the preference says", () => {
    renderPanel("team", "simple");
    expect(screen.queryByTestId("simple-box")).not.toBeInTheDocument();
    expect(screen.getByTestId("global-form")).toBeInTheDocument();
  });
});
