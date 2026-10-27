// What the top bar's menus and the command palette both do. Plain functions,
// so either can call them without the other mounted.

import { useApp } from "../../store";

/** The event the command palette opens on; `detail.view` picks its view. */
export const COMMAND_PALETTE_EVENT = "nodevas-command-palette";

export type CommandPaletteView = "list" | "help";

export function openCommandPalette(view: CommandPaletteView = "list"): void {
  window.dispatchEvent(new CustomEvent(COMMAND_PALETTE_EVENT, { detail: { view } }));
}

/** The palette's help view: the shortcuts and the basic workflow. */
export function openShortcutsHelp(): void {
  openCommandPalette("help");
}

export function replayTour(): void {
  window.dispatchEvent(new CustomEvent("nodevas-tour-start"));
}

/**
 * Shows or hides the project explorer. The top bar used to hold this button;
 * the board's explorer holds it now (and the palette has it), so it lives
 * here where either can reach it.
 */
export function toggleExplorer(): void {
  const { preferences, updateUIPreference } = useApp.getState();
  updateUIPreference("explorerCollapsed", !preferences.explorerCollapsed);
}
