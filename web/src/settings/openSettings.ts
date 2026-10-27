import { setProjectMode } from "../game/stageSkin";
import { useApp } from "../store";

export type SettingsSection =
  | "project"
  | "appearance"
  | "notifications"
  | "backup"
  | "workstation"
  | "runners"
  | "usage"
  | "budget"
  | "routing"
  | "copilot"
  | "server"
  | "account"
  | "audit";

/**
 * Opens the Settings view on one of its parts. Everything that used to open
 * a settings dialog (the palette, the backup banner, "adjust budget", the
 * agent panel's Runners and Usage buttons) comes here. A part of the Agents
 * group asked for while the workstation is off lands on the workstation
 * switch instead.
 */
export function openSettings(section: SettingsSection): void {
  useApp.getState().updateUIPreference("settingsSection", section);
  setProjectMode("settings");
}
