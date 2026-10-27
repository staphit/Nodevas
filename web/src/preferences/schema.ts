/**
 * Local UI preference schema [A-03].
 *
 * Every `vised-*` localStorage key the app owns is declared here once: its
 * storage key, scope, default, validation and encoding. Nothing else in the
 * codebase may build a storage key by hand.
 *
 * The encoding uses stable key names and compact values ("1"/"0" for
 * booleans, plain numbers, JSON arrays) so the layout is deterministic.
 */

export type PreferenceScope =
  | "appearance"
  | "layout"
  | "explorer"
  | "timeline"
  | "editor"
  | "onboarding";

export type Language = "en" | "zh-TW";

export const PREFERENCE_SCOPES: readonly PreferenceScope[] = [
  "appearance",
  "layout",
  "explorer",
  "timeline",
  "editor",
  "onboarding",
];

export interface PreferenceSpec<T> {
  /** localStorage key. */
  key: string;
  scope: PreferenceScope;
  /** Value used when nothing valid is stored. May depend on the environment. */
  fallback: () => T;
  /** `undefined` means "stored value is unusable" → fall back. */
  decode: (raw: string) => T | undefined;
  encode: (value: T) => string;
  /** Clamps/normalizes a value on write. */
  normalize?: (value: T) => T;
}

function boolPref(
  key: string,
  scope: PreferenceScope,
  // A thunk, for a default that depends on the window rather than being fixed.
  // It is still only a default: once the user has toggled the thing, their
  // stored answer wins and this is never consulted again.
  fallback: boolean | (() => boolean),
): PreferenceSpec<boolean> {
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
): PreferenceSpec<T> {
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
): PreferenceSpec<number> {
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
): PreferenceSpec<string[]> {
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

/** The editor's modes and text sizes; the drawer's toolbar and the Settings
 * view's editor defaults both edit these preferences. */
export const EDITOR_MODES = ["live", "source", "preview"] as const;
export const EDITOR_FONT_SIZE = { min: 11, max: 22, fallback: 14 } as const;

export const PREFERENCE_SCHEMA = {
  // appearance
  // Dark until the viewer picks light, whatever the OS prefers.
  theme: enumPref("vised-theme", "appearance", ["light", "dark"] as const, () => "dark" as const),
  language: enumPref("vised-language", "appearance", ["en", "zh-TW"] as const, () => "en" as const),

  // layout (main window panes)
  paneGraph: boolPref("vised-pane-graph", "layout", true),
  // A first visit opens on the graph alone: two boards at once is a lot to
  // take in before anything is on either, and on a phone split panes get about
  // 200px each and neither is usable. The timeline stays folded to its header,
  // one tap from being opened. A browser that already used the app before this
  // default changed keeps the timeline it had — see `initialPaneOpen`, which
  // stores that answer once so this fallback never takes it away.
  paneTimeline: boolPref("vised-pane-timeline", "layout", false),
  paneSplit: numberPref("vised-pane-split", "layout", {
    min: 15,
    max: 85,
    fallback: 50,
    decimals: 2,
  }),
  drawerWidth: numberPref("vised-drawer-width", "layout", {
    min: 380,
    max: 960,
    fallback: 580,
  }),
  drawerCollapsed: boolPref("vised-drawer-collapsed", "layout", false),
  /**
   * Whether the new-node form's 「外觀與狀態」 section is unfolded. Closed by
   * default because most nodes are created by typing a title and pressing
   * Enter, and a user who does want to set looks up front usually wants it for
   * the next node too — so the answer is remembered rather than asked twice.
   */
  nodeCreateAdvanced: boolPref("vised-node-create-advanced", "layout", false),
  /**
   * Whether dragging a card lands it on the COL_W × ROW_H lattice. Off by
   * default: free placement is what the canvas has always done, and a user who
   * wants tidy rows can say so once and have it remembered.
   */
  graphSnapToGrid: boolPref("vised-graph-snap", "layout", false),
  /**
   * Whether cards align themselves to their neighbours while they are dragged
   * or resized. On by default — lining a board up by eye is the tedious part —
   * and holding Shift suspends it for one gesture without touching the setting.
   */
  graphSnapGuides: boolPref("vised-snap-guides", "layout", true),
  /**
   * Whether the corner minimap is drawn. It answers "where am I on a big
   * board", which a desktop asks all the time; a phone asks it too, but the
   * map costs a corner of a board that is already all corners, so it starts
   * hidden there. Either way it lives behind a toolbar toggle.
   */
  graphMinimap: boolPref("vised-graph-minimap", "layout", () =>
    typeof window === "undefined" || window.innerWidth > 720,
  ),
  /**
   * Which face of the project is shown: Info, the board as it always was
   * (graph, board, timeline, pages), Station, where the project's agents
   * work together — the live war room, who is doing what, what waits on
   * you, and what the agents say to each other — or Settings.
   */
  projectMode: enumPref(
    "vised-project-mode",
    "layout",
    ["info", "station", "settings"] as const,
    () => "info" as const,
  ),
  /** The part of the Settings view last open. */
  settingsSection: enumPref(
    "vised-settings-section",
    "layout",
    [
      "project",
      "appearance",
      "notifications",
      "backup",
      "workstation",
      "runners",
      "usage",
      "budget",
      "routing",
      "copilot",
      "server",
      "account",
      "audit",
    ] as const,
    () => "project" as const,
  ),

  // explorer (sidebar)
  explorerWidth: numberPref("vised-explorer-width", "explorer", {
    min: 200,
    max: 520,
    fallback: 304,
  }),
  // On a phone the explorer is an overlay, so leaving it open by default would
  // hide the board behind it on first launch. Wide windows still open with it
  // showing, which is where a first-time user needs it most.
  explorerCollapsed: boolPref("vised-explorer-collapsed", "explorer", () =>
    typeof window !== "undefined" && window.innerWidth <= 720,
  ),
  explorerExpandedProjects: stringListPref(
    "vised-explorer-expanded-projects",
    "explorer",
  ),
  explorerNodesExpanded: boolPref("vised-explorer-nodes-expanded", "explorer", true),
  explorerProjectSort: enumPref(
    "vised-explorer-project-sort",
    "explorer",
    ["name", "name-desc", "nodes", "kind", "manual"] as const,
    () => "name" as const,
  ),
  explorerWorkspaceExpanded: boolPref(
    "vised-explorer-workspace-expanded",
    "explorer",
    true,
  ),
  /**
   * Whether the explorer's less common actions — new subproject, Markdown
   * import, connecting a workspace, sorting and the project-file row — are
   * unfolded. Folded by default: a first project needs one button, and the
   * rest are one press away and remembered once opened.
   */
  explorerToolsExpanded: boolPref(
    "vised-explorer-tools-expanded",
    "explorer",
    false,
  ),

  // timeline
  timelineOrientation: enumPref(
    "vised-timeline-orientation",
    "timeline",
    ["vertical", "horizontal"] as const,
    () => "vertical" as const,
  ),
  timelineDateAxis: enumPref(
    "vised-timeline-date-axis",
    "timeline",
    ["top", "bottom"] as const,
    () => "top" as const,
  ),
  timelineNodeAxis: enumPref(
    "vised-timeline-node-axis",
    "timeline",
    ["left", "right"] as const,
    () => "left" as const,
  ),
  timelineVerticalNodeAxis: enumPref(
    "vised-timeline-vnode-axis",
    "timeline",
    ["top", "bottom"] as const,
    () => "top" as const,
  ),
  timelineVerticalDateAxis: enumPref(
    "vised-timeline-vdate-axis",
    "timeline",
    ["right", "left"] as const,
    () => "right" as const,
  ),
  /** Cell size follows content until the user drags the grip. */
  timelineCellAuto: boolPref("vised-timeline-cell-auto", "timeline", true),
  timelineCellWidth: numberPref("vised-timeline-cell-width", "timeline", {
    min: 160,
    max: 360,
    fallback: 220,
  }),
  timelineCellHeight: numberPref("vised-timeline-cell-height", "timeline", {
    min: 72,
    max: 180,
    fallback: 104,
  }),
  timelineCollapseEmptyDays: boolPref("vised-collapse-days", "timeline", false),

  // editor
  /** How markdown is shown while editing: rendered live, raw, or read-only. */
  editorMode: enumPref("vised-editor-mode", "editor", EDITOR_MODES, () => "live" as const),
  /** Whether the document outline panel is open. */
  editorOutline: boolPref("vised-editor-outline", "editor", false),
  editorFontSize: numberPref("vised-editor-fs", "editor", EDITOR_FONT_SIZE),

  // onboarding
  /** Whether the user has answered the first-run tour invitation yet. */
  tourPrompt: enumPref(
    "vised-tour-prompt",
    "onboarding",
    ["pending", "accepted", "declined"] as const,
    () => "pending" as const,
  ),
  /** Ids of tour chapters the user has already finished. */
  tourChaptersDone: stringListPref("vised-tour-chapters", "onboarding"),
  /**
   * Whether the agent workstation — runners, runs, autopilot, the agent
   * network and usage — is shown. Off by default so a board used as a local
   * planner is not full of controls for machines it has none of; it turns
   * itself on (and stays on) the first time this browser sees a runner, a run
   * or a connected agent, so nobody who already uses it ever loses it.
   */
  agentWorkstation: boolPref("vised-agent-workstation", "onboarding", false),
  /**
   * Whether the start screen offered to a brand-new workspace (one project,
   * no nodes) was answered. Once is enough: it can be reopened from the
   * command palette or the explorer's menu.
   */
  startScreenDismissed: boolPref("vised-start-screen-dismissed", "onboarding", false),
};

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
