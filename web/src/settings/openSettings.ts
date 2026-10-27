import { create } from "zustand";

import { setProjectMode } from "../station/projectMode";
import { useApp } from "../store";

/**
 * The parts Settings used to have, one page each. They are still how a
 * place in Settings is asked for and remembered (the `settingsSection`
 * preference): each now names a section of the page and, for most, a row of
 * it to open (SETTINGS_PLACES).
 */
export type SettingsSection =
  | "project"
  | "appearance"
  | "notifications"
  | "backup"
  | "workstation"
  | "runners"
  | "usage"
  | "analytics"
  | "budget"
  | "routing"
  | "roles"
  | "stages"
  | "copilot"
  | "server"
  | "account"
  | "audit";

/** The four sections of the Settings page, in the order the list shows them. */
export type SettingsPage = "general" | "agents" | "project" | "account";

export const SETTINGS_PAGES: readonly SettingsPage[] = ["general", "agents", "project", "account"];

/** A row of a section that opens in place. Those of Agents › Advanced are
 * ADVANCED_ROWS. */
export type SettingsRow =
  | "layout"
  | "reminders"
  | "copilot"
  | "budget"
  | "routing"
  | "roles"
  | "stages"
  | "autoAccept"
  | "usage"
  | "analytics"
  | "accessToken"
  | "statuses"
  | "nodeTypes"
  | "officeStyle"
  | "milestones"
  | "people"
  | "backup"
  | "server"
  | "audit";

export const ADVANCED_ROWS: readonly SettingsRow[] = [
  "budget",
  "routing",
  "roles",
  "stages",
  "autoAccept",
  "usage",
  "analytics",
  "accessToken",
];

export interface SettingsPlace {
  page: SettingsPage;
  row?: SettingsRow;
}

/** Where each old part lives now. */
export const SETTINGS_PLACES: Record<SettingsSection, SettingsPlace> = {
  appearance: { page: "general" },
  notifications: { page: "general", row: "reminders" },
  copilot: { page: "general", row: "copilot" },
  workstation: { page: "agents" },
  runners: { page: "agents" },
  budget: { page: "agents", row: "budget" },
  routing: { page: "agents", row: "routing" },
  roles: { page: "agents", row: "roles" },
  // A project's workflow stages belong in "This project"; they sit under
  // Agents › Advanced until that section is free to change.
  stages: { page: "agents", row: "stages" },
  usage: { page: "agents", row: "usage" },
  analytics: { page: "agents", row: "analytics" },
  project: { page: "project" },
  backup: { page: "project", row: "backup" },
  account: { page: "account" },
  server: { page: "account", row: "server" },
  audit: { page: "account", row: "audit" },
};

/** The part a section, or a row of it, is remembered as: the row's own part
 * when it had one, else the section's first. */
export function sectionFor(page: SettingsPage, row: SettingsRow | null): SettingsSection {
  if (row) {
    const own = (Object.keys(SETTINGS_PLACES) as SettingsSection[]).find(
      (id) => SETTINGS_PLACES[id].page === page && SETTINGS_PLACES[id].row === row,
    );
    if (own) return own;
  }
  return page === "general" ? "appearance" : page === "agents" ? "workstation" : page;
}

/** What to do once the part is open, beyond opening it. */
export interface OpenSettingsOptions {
  /** Open the add-runner form (the palette's and Station's "Add a runner"). */
  add?: boolean;
}

/**
 * A one-shot request for the Settings page, beside the part it opens on.
 * Held here because the page loads on demand and may not exist yet: it
 * consumes the request when it mounts, or straight away when it shows.
 */
export const useSettingsRequest = create<{
  add: boolean;
  clearRequest: () => void;
}>((set) => ({
  add: false,
  clearRequest: () => set({ add: false }),
}));

/**
 * Opens the Settings view on one of its parts. Everything that used to open
 * a settings dialog (the palette, the backup banner, "adjust budget", the
 * agent panel's Runners and Usage buttons) comes here; the part picks the
 * section, and the row of it to open (budget, routing and usage open their
 * row of Agents › Advanced). A part of Agents asked for while the
 * workstation is off lands on the workstation switch instead, and opens once
 * it is on. `{ add: true }` also opens the add-runner form.
 */
export function openSettings(section: SettingsSection, options: OpenSettingsOptions = {}): void {
  // A plain open drops an add asked for earlier and not yet taken.
  useSettingsRequest.setState({ add: Boolean(options.add) });
  useApp.getState().updateUIPreference("settingsSection", section);
  setProjectMode("settings");
}
