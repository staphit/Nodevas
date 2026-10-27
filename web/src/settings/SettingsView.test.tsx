import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useBudget } from "../runs/budgetStore";
import { useRouting } from "../runs/routingStore";
import { useApp } from "../store";
import { openSettings, useSettingsRequest } from "./openSettings";
import SettingsView from "./SettingsView";

// A standalone install: no accounts, so nothing account-only is offered.
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

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useApp.getState().updateUIPreference("agentWorkstation", true);
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  useBudget.getState().reset();
  useRouting.getState().reset();
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
  vi.spyOn(api, "getRunBudget").mockResolvedValue({
    budget: { dailyRuns: 0, dailyCostUsd: 5, dailyTokens: 0 },
    used: { runs: 0, costUsd: 1.2, tokens: 0 },
    exceeded: "",
  });
  vi.spyOn(api, "getRouting").mockResolvedValue({ rules: [] });
  vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });
});

afterEach(() => vi.restoreAllMocks());

const nav = (name = "Settings") => screen.getByRole("navigation", { name });

describe("Settings", () => {
  it("lists four sections, each saying what it holds", () => {
    act(() => openSettings("appearance"));
    render(<SettingsView />);

    const sections = within(nav())
      .getAllByRole("button")
      .map((button) => button.querySelector("span")?.textContent);
    expect(sections).toEqual(["General", "Agents", "This project", "Account"]);
    expect(within(nav()).getByRole("button", { name: /^General/ })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("region", { name: "Appearance and language" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Deadline reminders/ })).toHaveAttribute("aria-expanded", "false");
  });

  it("opens an old part's row: routing under Agents › Advanced", async () => {
    act(() => openSettings("routing"));
    render(<SettingsView />);

    expect(within(nav()).getByRole("button", { name: /^Agents/ })).toHaveAttribute("aria-current", "page");
    const advanced = screen.getByRole("region", { name: "Advanced" });
    expect(within(advanced).getByRole("button", { name: /^Advanced/ })).toHaveAttribute("aria-expanded", "true");
    expect(within(advanced).getByRole("region", { name: "Routing rules" })).toBeInTheDocument();
    // Each row says its value before it is opened.
    expect(await within(advanced).findByRole("button", { name: /Daily budget\s*\$5\.00 a day/ })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(await within(advanced).findByRole("button", { name: /Routing rules\s*None/ })).toBeInTheDocument();
  });

  it("keeps Advanced collapsed until asked, and remembers rows as their parts", async () => {
    const user = userEvent.setup();
    act(() => openSettings("runners"));
    render(<SettingsView />);

    const toggle = screen.getByRole("button", { name: /^Advanced/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: /Daily budget/ })).not.toBeInTheDocument();
    // One primary button in a section.
    const agents = screen.getByRole("region", { name: "Agents" });
    await within(agents).findByText(/No runners yet/);
    expect(agents.querySelectorAll("button.primary")).toHaveLength(1);

    await user.click(toggle);
    await user.click(screen.getByRole("button", { name: /Daily budget/ }));
    expect(useApp.getState().preferences.settingsSection).toBe("budget");
    expect(await screen.findByRole("region", { name: "Daily budget" })).toBeInTheDocument();

    // Auto-accept has no part of its own; it stays read-only.
    await user.click(screen.getByRole("button", { name: /Auto-accept/ }));
    expect(useApp.getState().preferences.settingsSection).toBe("workstation");
    const policy = screen.getByRole("region", { name: "Auto-accept" });
    expect(screen.queryByRole("region", { name: "Daily budget" })).not.toBeInTheDocument();
    expect(within(policy).queryByRole("checkbox")).not.toBeInTheDocument();

    // Asked for from outside again, the part opens even though it was the
    // last one remembered before.
    act(() => openSettings("budget"));
    expect(await screen.findByRole("region", { name: "Daily budget" })).toBeInTheDocument();
  });

  it("searches every section and jumps to the hit, opening its row", async () => {
    const user = userEvent.setup();
    act(() => openSettings("appearance"));
    render(<SettingsView />);

    const search = within(nav()).getByRole("searchbox", { name: "Search settings" });
    await user.type(search, "budget");
    const hit = within(nav()).getByRole("button", { name: /Daily budget\s*Agents › Advanced/ });
    // The sections give way to the hits while searching.
    expect(within(nav()).queryByRole("button", { name: /^General/ })).not.toBeInTheDocument();
    await user.click(hit);

    expect(search).toHaveValue("");
    expect(within(nav()).getByRole("button", { name: /^Agents/ })).toHaveAttribute("aria-current", "page");
    expect(await screen.findByRole("region", { name: "Daily budget" })).toBeInTheDocument();
    // Focus follows to the opened row's toggle.
    expect(screen.getByRole("button", { name: /^Daily budget/ })).toHaveFocus();

    await user.type(search, "zzz");
    expect(within(nav()).getByText("Nothing found")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(search).toHaveValue("");
  });

  it("chooses the project's office style, shown as a row of This project and found by search", async () => {
    const user = userEvent.setup();
    const updateWorkflowDefinition = vi.fn().mockResolvedValue({ ok: true });
    useApp.setState({
      activeProject: "novel",
      graph: { version: 1, type: "story", nodes: [] },
      updateWorkflowDefinition,
    } as never);
    act(() => openSettings("appearance"));
    render(<SettingsView />);

    await user.type(within(nav()).getByRole("searchbox", { name: "Search settings" }), "office");
    await user.click(within(nav()).getByRole("button", { name: /Office style\s*This project/ }));
    const row = screen.getByRole("button", { name: /^Office style\s*Automatic \(Study\)/ });
    expect(row).toHaveAttribute("aria-expanded", "true");
    const choices = screen.getByRole("radiogroup", { name: "Office style" });
    expect(within(choices).getByRole("radio", { name: "Automatic (Study)" })).toHaveAttribute("aria-checked", "true");
    // The arrows move the choice, as in any radio group.
    within(choices).getByRole("radio", { name: "Automatic (Study)" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(updateWorkflowDefinition).toHaveBeenCalledWith({ type: "workflow.setOfficeStyle", style: "default" });
    await user.click(within(choices).getByRole("radio", { name: "Café" }));
    expect(updateWorkflowDefinition).toHaveBeenCalledWith({ type: "workflow.setOfficeStyle", style: "cafe" });

    act(() => useApp.setState({ graph: { version: 1, type: "story", nodes: [], officeStyle: "cafe" } } as never));
    expect(screen.getByRole("button", { name: /^Office style\s*Café/ })).toBeInTheDocument();
    expect(within(choices).getByRole("radio", { name: "Café" })).toHaveAttribute("aria-checked", "true");
  });

  it("moves focus to the section's heading for a hit without a row", async () => {
    const user = userEvent.setup();
    act(() => openSettings("project"));
    render(<SettingsView />);

    await user.type(within(nav()).getByRole("searchbox", { name: "Search settings" }), "language");
    await user.click(within(nav()).getByRole("button", { name: /Language\s*General/ }));
    expect(screen.getByRole("heading", { level: 2, name: "General" })).toHaveFocus();
  });

  // The palette's and Station's "Add a runner".
  it("opens the add-runner form when asked from outside", async () => {
    act(() => openSettings("runners", { add: true }));
    render(<SettingsView />);
    expect(await screen.findByLabelText("Runner name")).toBeInTheDocument();
    // Taken once: a plain open later does not bring it back.
    expect(useSettingsRequest.getState().add).toBe(false);
  });

  it("does not open the add form on a plain open", async () => {
    act(() => openSettings("runners"));
    render(<SettingsView />);
    await screen.findByText(/No runners yet/);
    expect(screen.queryByLabelText("Runner name")).not.toBeInTheDocument();
  });

  it("opens the add-runner form from search", async () => {
    const user = userEvent.setup();
    act(() => openSettings("project"));
    render(<SettingsView />);

    await user.type(within(nav()).getByRole("searchbox", { name: "Search settings" }), "add runner");
    await user.click(within(nav()).getByRole("button", { name: /Add runner/ }));
    expect(await screen.findByLabelText("Runner name")).toBeInTheDocument();
  });

  it("searches in the reader's language", async () => {
    const user = userEvent.setup();
    useApp.getState().updateUIPreference("language", "zh-TW");
    act(() => openSettings("appearance"));
    render(<SettingsView />);

    await user.type(within(nav("設定")).getByRole("searchbox", { name: "搜尋設定" }), "語系");
    expect(within(nav("設定")).getByRole("button", { name: /語系\s*一般/})).toBeInTheDocument();
  });

  it("explains on a standalone install that there are no accounts", () => {
    act(() => openSettings("audit"));
    render(<SettingsView />);

    expect(within(nav()).getByRole("button", { name: /^Account/ })).toHaveAttribute("aria-current", "page");
    expect(screen.getByText(/This install is standalone/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Audit log/ })).not.toBeInTheDocument();
  });
});
