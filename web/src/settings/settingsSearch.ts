import { ADVANCED_ROWS, type SettingsPage, type SettingsRow } from "./openSettings";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** What an entry needs to be offered: the workstation shown, a server with
 * accounts, a signed-in account there, or its administrator. */
export type SettingsNeed = "workstation" | "team" | "account" | "admin";

export interface SettingsEntry {
  /** The label's i18n key: entries are found by the words the page uses. */
  label: string;
  page: SettingsPage;
  /** The row to open, if the entry lives in one. */
  row?: SettingsRow;
  /** Opens the add-runner form. */
  add?: boolean;
  needs?: readonly SettingsNeed[];
}

/** Every setting the search box finds, in page order. */
export const SETTINGS_ENTRIES: readonly SettingsEntry[] = [
  { label: "appearance.theme", page: "general" },
  { label: "appearance.language", page: "general" },
  { label: "settingsPage.layout", page: "general", row: "layout" },
  { label: "editor.mode", page: "general", row: "layout" },
  { label: "editor.fontSize", page: "general", row: "layout" },
  { label: "appearance.resetAll", page: "general", row: "layout" },
  { label: "settingsPage.browserNotifications", page: "general" },
  { label: "notify.title", page: "general", row: "reminders" },
  { label: "settingsPage.part.copilot", page: "general", row: "copilot" },
  { label: "workstation.label", page: "agents" },
  { label: "settingsPage.part.runners", page: "agents", needs: ["workstation"] },
  { label: "runners.add", page: "agents", add: true, needs: ["workstation"] },
  { label: "runners.link.open", page: "agents", needs: ["workstation", "team"] },
  { label: "settingsPage.part.budget", page: "agents", row: "budget", needs: ["workstation"] },
  { label: "settingsPage.part.routing", page: "agents", row: "routing", needs: ["workstation"] },
  { label: "roles.settings.title", page: "agents", row: "roles", needs: ["workstation"] },
  { label: "stages.title", page: "agents", row: "stages", needs: ["workstation"] },
  { label: "runners.autoAccept", page: "agents", row: "autoAccept", needs: ["workstation"] },
  { label: "settingsPage.usageDetails", page: "agents", row: "usage", needs: ["workstation"] },
  { label: "runners.link.accessTitle", page: "agents", row: "accessToken", needs: ["workstation", "admin"] },
  { label: "settings.workflow", page: "project", row: "statuses" },
  { label: "settings.nodeTypes", page: "project", row: "nodeTypes" },
  { label: "settings.officeStyle", page: "project", row: "officeStyle" },
  { label: "settings.milestones", page: "project", row: "milestones" },
  { label: "settings.people", page: "project", row: "people" },
  { label: "members.tab", page: "project", row: "people", needs: ["team"] },
  { label: "settingsPage.part.backup", page: "project", row: "backup" },
  { label: "settingsPage.part.account", page: "account", needs: ["account"] },
  { label: "auth.totp.tab", page: "account", needs: ["account"] },
  { label: "settingsPage.signOut", page: "account", needs: ["account"] },
  { label: "settingsPage.part.server", page: "account", row: "server", needs: ["admin"] },
  { label: "settingsPage.part.audit", page: "account", row: "audit", needs: ["admin"] },
];

export interface SettingsHit {
  entry: SettingsEntry;
  label: string;
  /** "Agents › Advanced": where the hit lives. */
  where: string;
}

/** Where an entry lives, in the reader's language. */
export function settingsWhere(entry: SettingsEntry, t: Translate): string {
  const page = t(`settingsPage.page.${entry.page}`);
  return entry.row && ADVANCED_ROWS.includes(entry.row) ? `${page} › ${t("settingsPage.advanced")}` : page;
}

/**
 * The entries whose label, or the section they live in, contains the query
 * (any case), among those offered here. An empty query finds nothing.
 */
export function searchSettings(
  query: string,
  t: Translate,
  offered: (need: SettingsNeed) => boolean,
): SettingsHit[] {
  const wanted = query.trim().toLocaleLowerCase();
  if (!wanted) return [];
  const seen = new Set<string>();
  const hits: SettingsHit[] = [];
  for (const entry of SETTINGS_ENTRIES) {
    if (!(entry.needs ?? []).every(offered)) continue;
    const label = t(entry.label);
    const where = settingsWhere(entry, t);
    // Two keys can read the same in one language; one hit is enough.
    const key = `${label}\u0000${where}`;
    if (seen.has(key)) continue;
    if (!`${label} ${where}`.toLocaleLowerCase().includes(wanted)) continue;
    seen.add(key);
    hits.push({ entry, label, where });
  }
  return hits;
}
