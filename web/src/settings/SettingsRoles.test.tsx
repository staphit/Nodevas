import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useRouting } from "../runs/routingStore";
import { useApp } from "../store";
import { ADVANCED_ROWS, openSettings, SETTINGS_PLACES, type SettingsSection } from "./openSettings";
import SettingsView from "./SettingsView";

vi.mock("../components/SignIn", () => ({
  useViewer: () => null,
  useTeamServer: () => false,
  useSignOut: () => () => {},
}));

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

const roles = "roles" as unknown as SettingsSection;
const stages = "stages" as unknown as SettingsSection;

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    activeProject: "demo",
    graph: { version: 1, nodes: [] } as never,
  });
  useApp.getState().updateUIPreference("agentWorkstation", true);
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useBudget.getState().reset();
  useRouting.getState().reset();
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 0 },
    used: { runs: 0, costUsd: 0, tokens: 0 },
    exceeded: "",
  });
  vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
  vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });
});

afterEach(() => vi.restoreAllMocks());

const nav = () => screen.getByRole("navigation", { name: "Settings" });

describe("Settings › Agents › Roles and Workflow stages", () => {
  it("places both rows under Agents › Advanced", () => {
    expect(ADVANCED_ROWS).toContain("roles");
    expect(ADVANCED_ROWS).toContain("stages");
    expect(SETTINGS_PLACES[roles]).toEqual({ page: "agents", row: "roles" });
    expect(SETTINGS_PLACES[stages]).toEqual({ page: "agents", row: "stages" });
  });

  it("counts seven items under Advanced", async () => {
    act(() => openSettings("workstation"));
    render(<SettingsView />);
    expect(within(nav()).getByRole("button", { name: /^Agents/ })).toHaveAttribute("aria-current", "page");
    const advanced = screen.getByRole("region", { name: "Advanced" });
    expect(within(advanced).getByRole("button", { name: /^Advanced/ })).toHaveTextContent("7 items");
  });

  it("opens the Roles row", async () => {
    act(() => openSettings(roles));
    render(<SettingsView />);
    const advanced = screen.getByRole("region", { name: "Advanced" });
    expect(within(advanced).getByRole("region", { name: "Roles" })).toBeInTheDocument();
  });

  it("opens the Workflow stages row, naming the project", async () => {
    const user = userEvent.setup();
    act(() => openSettings("workstation"));
    render(<SettingsView />);
    const advanced = screen.getByRole("region", { name: "Advanced" });
    await user.click(within(advanced).getByRole("button", { name: /^Advanced/ }));
    const row = within(advanced).getByRole("button", { name: /Workflow stages · demo/ });
    expect(row).toHaveTextContent(/Legacy/);
    await user.click(row);
    expect(within(advanced).getByRole("radiogroup", { name: "Workflow stages" })).toBeInTheDocument();
  });
});
