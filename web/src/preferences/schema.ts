/**
 * Local UI preference schema.
 *
 * Every `vised-*` localStorage key the app owns is declared here once: its
 * storage key, scope, default, validation and encoding, and whether it follows
 * the person (`account`) or stays on this device (`device`). Nothing else in
 * the codebase may build a storage key by hand.
 *
 * The encoding uses stable key names and compact values ("1"/"0" for
 * booleans, plain numbers, JSON arrays) so the layout is deterministic. The
 * server keeps an account's choices under the same storage keys and encoded
 * strings (sync.ts), so neither may change without a migration.
 */

export type PreferenceScope =
  | "appearance"
  | "layout"
  | "explorer"
  | "timeline"
  | "editor"
  | "onboarding"
  | "agents";

export type Language = "en" | "zh-TW";

export const PREFERENCE_SCOPES: readonly PreferenceScope[] = [
  "appearance",
  "layout",
  "explorer",
  "timeline",
  "editor",
  "onboarding",
  "agents",
];

/**
 * Where a preference lives beyond this browser.
 *
 * - `account`: a choice (theme, theme pack, language, text size, editor,
 *   timeline, tour, Station tab…). Saved to the signed-in account — `local`
 *   in the single-user edition — so it follows the person to another browser,
 *   origin (localhost / 127.0.0.1) or the desktop app.
 * - `device`: screen-shaped state (pane and panel sizes, what is folded on
 *   this screen) and the view last open. Kept on this device, never sent.
 */
export type PreferenceSync = "account" | "device";

export interface PreferenceSpec<T> {
  /** localStorage key; also the key the server keeps an account choice under. */
  key: string;
  scope: PreferenceScope;
  sync: PreferenceSync;
  /** Value used when nothing valid is stored. May depend on the environment. */
  fallback: () => T;
  /** `undefined` means "stored value is unusable" → fall back. */
  decode: (raw: string) => T | undefined;
  encode: (value: T) => string;
  /** Clamps/normalizes a value on write. */
  normalize?: (value: T) => T;
}

/** A spec before it says where it lives: `account(...)` or `device(...)`. */
type SpecBody<T> = Omit<PreferenceSpec<T>, "sync">;

/** A choice that follows the person: saved to their account. */
function account<T>(spec: SpecBody<T>): PreferenceSpec<T> {
  return { ...spec, sync: "account" };
}

/** Screen-shaped state or the view last open: stays on this device. */
function device<T>(spec: SpecBody<T>): PreferenceSpec<T> {
  return { ...spec, sync: "device" };
}

function boolPref(
  key: string,
  scope: PreferenceScope,
  // A thunk, for a default that depends on the window rather than being fixed.
  // It is still only a default: once the user has toggled the thing, their
  // stored answer wins and this is never consulted again.
  fallback: boolean | (() => boolean),
): SpecBody<boolean> {
  return {
    key,
    scope,
    fallback: typeof fallback === "function" ? fallback : () => fallback,
    decode: (raw) => (raw === "1" ? true : raw === "0" ? false : undefined),
    encode: (value) => (value ? "1" : "0"),
  };
}

function enumPref<T extends string>(
  key: string,
  scope: PreferenceScope,
  values: readonly T[],
  fallback: () => T,
): SpecBody<T> {
  return {
    key,
    scope,
    fallback,
    decode: (raw) => (values.includes(raw as T) ? (raw as T) : undefined),
    encode: (value) => value,
  };
}

/**
 * Out-of-range numbers are clamped rather than discarded: a window that was
 * dragged wider than today's maximum should come back at the maximum, not jump
 * to the factory default.
 */
function numberPref(
  key: string,
  scope: PreferenceScope,
  options: { min: number; max: number; fallback: number; decimals?: number },
): SpecBody<number> {
  const clamp = (value: number) =>
    Math.max(options.min, Math.min(options.max, value));
  return {
    key,
    scope,
    fallback: () => options.fallback,
    decode: (raw) => {
      const value = Number(raw);
      return Number.isFinite(value) ? clamp(value) : undefined;
    },
    encode: (value) =>
      options.decimals === undefined
        ? String(value)
        : value.toFixed(options.decimals),
    normalize: clamp,
  };
}

function stringListPref(
  key: string,
  scope: PreferenceScope,
): SpecBody<string[]> {
  return {
    key,
    scope,
    fallback: () => [],
    decode: (raw) => {
      try {
        const parsed: unknown = JSON.parse(raw);
        if (!Array.isArray(parsed)) return undefined;
        return parsed.filter((item): item is string => typeof item === "string");
      } catch {
        return undefined;
      }
    },
    encode: (value) => JSON.stringify(value),
  };
}

/** "" or a theme pack's id (the word "none" reads as one); see `themePack`. */
function themePackPref(key: string, scope: PreferenceScope): SpecBody<string> {
  return {
    key,
    scope,
    fallback: () => "",
    decode: (raw) => (raw === "" || /^[a-z0-9][a-z0-9-]{0,39}$/.test(raw) ? raw : undefined),
    encode: (value) => value,
  };
}

/** A runner's repo id, as the runner checks one (validRepoID): at most 256
 * UTF-8 bytes, no control characters. "" means none picked. */
const MAX_ID_BYTES = 256;

function idPref(key: string, scope: PreferenceScope): SpecBody<string> {
  return {
    key,
    scope,
    fallback: () => "",
    decode: (raw) =>
      new TextEncoder().encode(raw).length <= MAX_ID_BYTES && !/[\p{Cc}\p{Cs}]/u.test(raw) ? raw : undefined,
    encode: (value) => value,
  };
}

/** The editor's modes and text sizes; the drawer's toolbar and the Settings
 * view's editor defaults both edit these preferences. */
export const EDITOR_MODES = ["live", "source", "preview"] as const;
export const EDITOR_FONT_SIZE = { min: 11, max: 22, fallback: 14 } as const;

/** The interface's text size, as a multiple of the design size. */
export const TEXT_SCALE = { min: 0.9, max: 1.5, fallback: 1, decimals: 2 } as const;

/** Traditional Chinese for a browser that prefers Chinese, else English. */
function browserLanguage(): "en" | "zh-TW" {
  const preferred = typeof navigator === "undefined" ? [] : (navigator.languages ?? [navigator.language]);
  const first = preferred.find((tag) => /^(zh|en)(-|_|$)/i.test(tag ?? ""));
  return first && /^zh/i.test(first) ? "zh-TW" : "en";
}

/** The modes a Copilot conversation can start in (api/copilot.ts COPILOT_MODES;
 * listed here so the schema stays clear of the api module). */
export const COPILOT_START_MODES = ["plan", "default", "acceptEdits", "bypassPermissions"] as const;
/** Copilot intents: "make" drafts nodes, "ask" is the chat. */
export const COPILOT_INTENTS = ["make", "ask"] as const;
/** Preferred dispatch/global run mode: "auto" follows the default interactive rule. */
export const RUN_MODES = ["auto", "interactive", "background"] as const;

export const PREFERENCE_SCHEMA = {
  // appearance
  // Dark until the viewer picks light, whatever the OS prefers.
  theme: account(enumPref("vised-theme", "appearance", ["light", "dark"] as const, () => "dark" as const)),
  // The browser's language until the viewer picks one.
  language: account(enumPref("vised-language", "appearance", ["en", "zh-TW"] as const, browserLanguage)),
  textScale: account(numberPref("vised-text-scale", "appearance", TEXT_SCALE)),
  // The theme pack this viewer picked: "" follows the site default, "none" is
  // the built-in pixel people, anything else a pack's id.
  themePack: account(themePackPref("vised-theme-pack", "appearance")),

  // layout (main window panes)
  paneGraph: device(boolPref("vised-pane-graph", "layout", true)),
  // A first visit opens on the graph alone: two boards at once is a lot to
  // take in before anything is on either, and on a phone split panes get about
  // 200px each and neither is usable. The timeline stays folded to its header,
  // one tap from being opened. A browser that already used the app before this
  // default changed keeps the timeline it had — see `initialPaneOpen`, which
  // stores that answer once so this fallback never takes it away.
  paneTimeline: device(boolPref("vised-pane-timeline", "layout", false)),
  paneSplit: device(
    numberPref("vised-pane-split", "layout", {
      min: 15,
      max: 85,
      fallback: 50,
      decimals: 2,
    }),
  ),
  drawerWidth: device(
    numberPref("vised-drawer-width", "layout", {
      min: 380,
      max: 960,
      fallback: 580,
    }),
  ),
  drawerCollapsed: device(boolPref("vised-drawer-collapsed", "layout", false)),
  /**
   * Whether the new-node form's 「外觀與狀態」 section is unfolded. Closed by
   * default because most nodes are created by typing a title and pressing
   * Enter, and a user who does want to set looks up front usually wants it for
   * the next node too — so the answer is remembered rather than asked twice.
   */
  nodeCreateAdvanced: account(boolPref("vised-node-create-advanced", "layout", false)),
  /**
   * Whether dragging a card lands it on the COL_W × ROW_H lattice. Off by
   * default: free placement is what the canvas has always done, and a user who
   * wants tidy rows can say so once and have it remembered.
   */
  graphSnapToGrid: account(boolPref("vised-graph-snap", "layout", false)),
  /**
   * Whether cards align themselves to their neighbours while they are dragged
   * or resized. On by default — lining a board up by eye is the tedious part —
   * and holding Shift suspends it for one gesture without touching the setting.
   */
  graphSnapGuides: account(boolPref("vised-snap-guides", "layout", true)),
  /**
   * Whether the corner minimap is drawn. It answers "where am I on a big
   * board", which a desktop asks all the time; a phone asks it too, but the
   * map costs a corner of a board that is already all corners, so it starts
   * hidden there. Either way it lives behind a toolbar toggle.
   */
  graphMinimap: device(
    boolPref("vised-graph-minimap", "layout", () =>
      typeof window === "undefined" || window.innerWidth > 720,
    ),
  ),
  /**
   * Which face of the project is shown: Info, the board as it always was
   * (graph, board, timeline, pages), Station, where the project's agents
   * work together — the live war room, who is doing what, what waits on
   * you, and what the agents say to each other — or Settings.
   */
  projectMode: device(
    enumPref(
      "vised-project-mode",
      "layout",
      ["info", "station", "settings"] as const,
      () => "info" as const,
    ),
  ),
  /**
   * How a theme pack's war room lays out the people of a place: "stage", the
   * one turned to in front with a dialogue box under her, or "list", a card
   * and a dialogue box each, one under another.
   */
  storyLayout: account(
    enumPref(
      "vised-story-layout",
      "layout",
      ["stage", "list"] as const,
      () => "stage" as const,
    ),
  ),
  /**
   * The part of the Settings view last open. A first visit opens General
   * (remembered as "appearance"), the top of the list and where the tour
   * sends people for the replay, help and the theme.
   */
  settingsSection: device(
    enumPref(
      "vised-settings-section",
      "layout",
      [
        "project",
        "appearance",
        "themePacks",
        "notifications",
        "backup",
        "workstation",
        "runners",
        "usage",
        "analytics",
        "budget",
        "routing",
        "roles",
        "stages",
        "copilot",
        "server",
        "account",
        "audit",
      ] as const,
      () => "appearance" as const,
    ),
  ),

  // explorer (sidebar)
  explorerWidth: device(
    numberPref("vised-explorer-width", "explorer", {
      // explorerMinWidth (explorerWidth.ts): the panel's minimum beside the tab
      // rail; narrower, the rows stop reading.
      min: 313,
      max: 520,
      fallback: 320,
    }),
  ),
  // On a phone the explorer is an overlay, so leaving it open by default would
  // hide the board behind it on first launch. Wide windows still open with it
  // showing, which is where a first-time user needs it most.
  explorerCollapsed: device(
    boolPref("vised-explorer-collapsed", "explorer", () =>
      typeof window !== "undefined" && window.innerWidth <= 720,
    ),
  ),
  /** The sidebar's tab: the node list, files, git, or agents. */
  explorerTab: account(
    enumPref(
      "vised-explorer-tab",
      "explorer",
      ["nodes", "files", "git", "agent"] as const,
      () => "nodes" as const,
    ),
  ),
  explorerExpandedProjects: device(
    stringListPref("vised-explorer-expanded-projects", "explorer"),
  ),
  explorerNodesExpanded: device(boolPref("vised-explorer-nodes-expanded", "explorer", true)),
  explorerProjectSort: account(
    enumPref(
      "vised-explorer-project-sort",
      "explorer",
      ["name", "name-desc", "nodes", "kind", "manual"] as const,
      () => "name" as const,
    ),
  ),
  explorerWorkspaceExpanded: device(
    boolPref("vised-explorer-workspace-expanded", "explorer", true),
  ),
  /**
   * Whether the explorer's less common actions — new subproject, Markdown
   * import, connecting a workspace, sorting and the project-file row — are
   * unfolded. Folded by default: a first project needs one button, and the
   * rest are one press away and remembered once opened.
   */
  explorerToolsExpanded: account(
    boolPref("vised-explorer-tools-expanded", "explorer", false),
  ),

  // timeline
  timelineOrientation: account(
    enumPref(
      "vised-timeline-orientation",
      "timeline",
      ["vertical", "horizontal"] as const,
      () => "vertical" as const,
    ),
  ),
  timelineDateAxis: account(
    enumPref(
      "vised-timeline-date-axis",
      "timeline",
      ["top", "bottom"] as const,
      () => "top" as const,
    ),
  ),
  timelineNodeAxis: account(
    enumPref(
      "vised-timeline-node-axis",
      "timeline",
      ["left", "right"] as const,
      () => "left" as const,
    ),
  ),
  timelineVerticalNodeAxis: account(
    enumPref(
      "vised-timeline-vnode-axis",
      "timeline",
      ["top", "bottom"] as const,
      () => "top" as const,
    ),
  ),
  timelineVerticalDateAxis: account(
    enumPref(
      "vised-timeline-vdate-axis",
      "timeline",
      ["right", "left"] as const,
      () => "right" as const,
    ),
  ),
  /** Cell size follows content until the user drags the grip. */
  timelineCellAuto: device(boolPref("vised-timeline-cell-auto", "timeline", true)),
  timelineCellWidth: device(
    numberPref("vised-timeline-cell-width", "timeline", {
      min: 160,
      max: 360,
      fallback: 220,
    }),
  ),
  timelineCellHeight: device(
    numberPref("vised-timeline-cell-height", "timeline", {
      min: 72,
      max: 180,
      fallback: 104,
    }),
  ),
  timelineCollapseEmptyDays: account(boolPref("vised-collapse-days", "timeline", false)),

  // editor
  /** How markdown is shown while editing: rendered live, raw, or read-only. */
  editorMode: account(enumPref("vised-editor-mode", "editor", EDITOR_MODES, () => "live" as const)),
  /** Whether the document outline panel is open. */
  editorOutline: account(boolPref("vised-editor-outline", "editor", false)),
  editorFontSize: account(numberPref("vised-editor-fs", "editor", EDITOR_FONT_SIZE)),

  // onboarding
  /** Whether the user has answered the first-run tour invitation yet. */
  tourPrompt: account(
    enumPref(
      "vised-tour-prompt",
      "onboarding",
      ["pending", "accepted", "declined"] as const,
      () => "pending" as const,
    ),
  ),
  /** Ids of tour chapters the user has already finished. */
  tourChaptersDone: account(stringListPref("vised-tour-chapters", "onboarding")),
  /**
   * Whether the agent workstation — runners, runs, autopilot, the agent
   * network and usage — is shown. Off by default so a board used as a local
   * planner is not full of controls for machines it has none of; it turns
   * itself on (and stays on) the first time this browser sees a runner, a run
   * or a connected agent, so nobody who already uses it ever loses it.
   */
  agentWorkstation: account(boolPref("vised-agent-workstation", "onboarding", false)),
  /**
   * Whether the start screen offered to a brand-new workspace (one project,
   * no nodes) was answered. Once is enough: it can be reopened from the
   * command palette or the explorer's menu.
   */
  startScreenDismissed: account(boolPref("vised-start-screen-dismissed", "onboarding", false)),
  /**
   * Whether the board's one-line tip on how to connect two nodes is done
   * with: closed by hand, or no longer needed because this person has made a
   * dependency. It teaches one gesture once, so it never comes back.
   */
  graphConnectTipDismissed: account(
    boolPref("vised-graph-connect-tip-dismissed", "onboarding", false),
  ),
  /**
   * Where this person is with the first-run questions (single-user edition):
   * not decided yet, answered, skipped, or never asked because they already
   * used the app. The answers themselves are not kept: they are written as
   * the settings they choose, once, when the questions end.
   */
  firstRun: account(
    enumPref(
      "vised-first-run",
      "onboarding",
      ["pending", "done", "skipped", "existing"] as const,
      () => "pending" as const,
    ),
  ),
  /**
   * How this person uses Nodevas: "simple", the preset for someone who does
   * not write code, or "full", every option as it always was. Only the
   * single-user edition reads it, and only through useSimpleMode
   * (simple/simpleMode.ts). Full by default, so nobody who upgrades sees a
   * change.
   */
  experience: account(
    enumPref("vised-experience", "onboarding", ["full", "simple"] as const, () => "full" as const),
  ),

  // agents
  /** Whether Copilot reads its replies aloud. */
  copilotSpeakReplies: account(boolPref("vised-copilot-speak", "agents", false)),
  /** The mode a new Copilot conversation starts in. */
  copilotDefaultMode: account(
    enumPref("vised-copilot-mode", "agents", COPILOT_START_MODES, () => "default" as const),
  ),
  /** What Copilot's text box does: propose nodes as a draft, or chat. */
  copilotIntent: account(enumPref("vised-copilot-intent", "agents", COPILOT_INTENTS, () => "make" as const)),
  /** Whether a spoken request is sent when the person stops talking, or left in the box to check. */
  copilotVoiceAutoSend: account(boolPref("vised-copilot-voice-send", "agents", false)),
  /** Preferred mode for new runs; "auto" resolves by the default interactive rule. */
  runMode: account(enumPref("vised-run-mode", "agents", RUN_MODES, () => "auto" as const)),
  /** How a run's changes are shown: side by side (where there is room) or one column. */
  diffLayout: account(
    enumPref("vised-diff-layout", "agents", ["split", "unified"] as const, () => "split" as const),
  ),
  /** Station's face: the project's agents, or the viewer's global agents. */
  stationTab: account(
    enumPref("vised-station-tab", "agents", ["project", "global"] as const, () => "project" as const),
  ),
  /** Whether Station's agent network card is unfolded. */
  stationNetworkOpen: account(boolPref("vised-station-network-open", "agents", false)),
  /** The agent the describe box hands work to; "auto" is the first one
   * installed, in the runner's order. */
  simpleAgentKind: account(
    enumPref(
      "vised-simple-agent-kind",
      "agents",
      ["auto", "claude", "codex", "gemini", "opencode", "antigravity"] as const,
      () => "auto" as const,
    ),
  ),
  /** The folder (a repo id of this computer's runner) the describe box's
   * agent works in; "" is the first one. */
  simpleAgentRepoId: account(idPref("vised-simple-agent-repo", "agents")),
} satisfies Record<string, { sync: PreferenceSync }>;

export type PreferenceKey = keyof typeof PREFERENCE_SCHEMA;

export type PreferenceValue<K extends PreferenceKey> =
  (typeof PREFERENCE_SCHEMA)[K] extends PreferenceSpec<infer T> ? T : never;

export type PreferenceValues = { [K in PreferenceKey]: PreferenceValue<K> };

export const PREFERENCE_KEYS = Object.keys(PREFERENCE_SCHEMA) as PreferenceKey[];

/**
 * Typed accessor. The schema object is a union of differently-parameterized
 * specs, so callers generic over `K` need this bridge to keep encode/decode
 * matched to the value type.
 */
export function preferenceSpec<K extends PreferenceKey>(
  key: K,
): PreferenceSpec<PreferenceValue<K>> {
  return PREFERENCE_SCHEMA[key] as unknown as PreferenceSpec<PreferenceValue<K>>;
}

export function preferenceKeysInScope(scope: PreferenceScope): PreferenceKey[] {
  return PREFERENCE_KEYS.filter((key) => PREFERENCE_SCHEMA[key].scope === scope);
}

/** Every storage key this app is allowed to own. Used by reset and by tests. */
export const PREFERENCE_STORAGE_KEYS: string[] = PREFERENCE_KEYS.map(
  (key) => PREFERENCE_SCHEMA[key].key,
);

/** The choices that follow the account; the only ones sync.ts ever sends. */
export const ACCOUNT_PREFERENCE_KEYS: PreferenceKey[] = PREFERENCE_KEYS.filter(
  (key) => PREFERENCE_SCHEMA[key].sync === "account",
);

/** The schema key stored under a localStorage key, if the app owns it. */
const STORAGE_KEY_INDEX = new Map<string, PreferenceKey>(
  PREFERENCE_KEYS.map((key) => [PREFERENCE_SCHEMA[key].key, key]),
);

export function preferenceKeyForStorageKey(storageKey: string): PreferenceKey | undefined {
  return STORAGE_KEY_INDEX.get(storageKey);
}

/**
 * Keys that lived outside the schema before preferences followed the account.
 * Each is read once, written under its schema key (unless that already holds
 * a value), then removed. `read` returns only the parts that were valid.
 */
export interface LegacyPreference {
  key: string;
  read: (raw: string) => Partial<PreferenceValues>;
}

export const LEGACY_PREFERENCES: readonly LegacyPreference[] = [
  {
    // {speakReplies, defaultMode}, written whole on every change.
    key: "nodevas.copilot.prefs",
    read: (raw) => {
      try {
        const parsed: unknown = JSON.parse(raw);
        if (!parsed || typeof parsed !== "object") return {};
        const { speakReplies, defaultMode } = parsed as Record<string, unknown>;
        const values: Partial<PreferenceValues> = {};
        if (typeof speakReplies === "boolean") values.copilotSpeakReplies = speakReplies;
        const mode = COPILOT_START_MODES.find((item) => item === defaultMode);
        if (mode) values.copilotDefaultMode = mode;
        return values;
      } catch {
        return {};
      }
    },
  },
  {
    key: "nodevas.runs.diffLayout",
    read: (raw) => (raw === "unified" || raw === "split" ? { diffLayout: raw } : {}),
  },
  {
    key: "nodevas-station-tab",
    read: (raw) => (raw === "project" || raw === "global" ? { stationTab: raw } : {}),
  },
  {
    key: "nodevas-station-network-open",
    read: (raw) => (raw === "1" || raw === "0" ? { stationNetworkOpen: raw === "1" } : {}),
  },
];
