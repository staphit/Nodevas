// Pure terminal key sequences, palette helpers, and screen menu detection.
// Kept strictly free of any xterm dependency so RunDialog and RunComposer can
// use them without pulling xterm into the main bundle.

export interface TerminalTheme {
  background?: string;
  foreground?: string;
  cursor?: string;
  cursorAccent?: string;
  selectionBackground?: string;
  black?: string;
  red?: string;
  green?: string;
  yellow?: string;
  blue?: string;
  magenta?: string;
  cyan?: string;
  white?: string;
  brightBlack?: string;
  brightRed?: string;
  brightGreen?: string;
  brightYellow?: string;
  brightBlue?: string;
  brightMagenta?: string;
  brightCyan?: string;
  brightWhite?: string;
}

export type TermKey =
  | "esc"
  | "tab"
  | "shiftTab"
  | "ctrlC"
  | "left"
  | "up"
  | "down"
  | "right"
  | "enter";

export const BRACKETED_PASTE_START = "\x1b[200~";
export const BRACKETED_PASTE_END = "\x1b[201~";

// ANSI colours tuned per background: the stock xterm palette assumes a dark
// screen, and its yellow and white vanish on a light one.
const DARK_ANSI: TerminalTheme = {
  black: "#2b3640",
  red: "#e08a88",
  green: "#7fbd85",
  yellow: "#d9a441",
  blue: "#6cb2e0",
  magenta: "#c79be8",
  cyan: "#62b2c1",
  white: "#d5dde3",
  brightBlack: "#6f7d88",
  brightRed: "#f0a5a3",
  brightGreen: "#9bd6a0",
  brightYellow: "#ecc06a",
  brightBlue: "#8fc8ee",
  brightMagenta: "#dbb8f3",
  brightCyan: "#8dd0dc",
  brightWhite: "#f4f7f9",
};

const LIGHT_ANSI: TerminalTheme = {
  black: "#17212b",
  red: "#b3302d",
  green: "#2f6f34",
  yellow: "#8a5d00",
  blue: "#1f5f9f",
  magenta: "#7340c4",
  cyan: "#1d6d7c",
  white: "#8e99a3",
  brightBlack: "#5a6672",
  brightRed: "#cf4643",
  brightGreen: "#3b8541",
  brightYellow: "#a06c00",
  brightBlue: "#2a71b8",
  brightMagenta: "#8a55d9",
  brightCyan: "#258497",
  brightWhite: "#66727f",
};

export const STORY_CSS_VARS = [
  "--story-night",
  "--story-deep",
  "--story-ink",
  "--story-soft",
  "--story-accent",
  "--story-alert",
  "--story-ask",
  "--story-bronze",
  "--story-panel",
  "--story-line",
  "--story-dim",
] as const;

function token(style: CSSStyleDeclaration, name: string, fallback: string): string {
  return style.getPropertyValue(name).trim() || fallback;
}

/** Reads active `--story-*` CSS variables from an element or its story ancestor,
 * so a portaled GameDialog ("Open large") carries the same theme palette. */
export function readStoryCssVars(element: Element | null): Record<string, string> {
  if (!element) return {};
  const source =
    element.closest(".px-story-talk, .run-dialog-portal-theme, .px-story") ?? element;
  const computed = getComputedStyle(source);
  const vars: Record<string, string> = {};
  for (const name of STORY_CSS_VARS) {
    const val = computed.getPropertyValue(name).trim();
    if (val) vars[name] = val;
  }
  return vars;
}

/** The terminal's colours from the app's CSS tokens, or the story room's
 * `--story-*` palette when mounted inside `.px-story-talk` (or its portal). */
export function terminalTheme(element?: Element | null): TerminalTheme {
  const storyContainer = element?.closest?.(".px-story-talk, .run-dialog-portal-theme");
  if (storyContainer) {
    const style = getComputedStyle(storyContainer);
    const background = token(style, "--story-night", "#150e08");
    const foreground = token(style, "--story-ink", "#efe6d0");
    const soft = token(style, "--story-soft", "#b3a48c");
    const accent = token(style, "--story-accent", "#d9b86c");
    return {
      ...DARK_ANSI,
      brightBlack: soft,
      background,
      foreground,
      cursor: accent,
      cursorAccent: background,
      selectionBackground: /^#[0-9a-f]{6}$/i.test(accent) ? `${accent}55` : accent,
    };
  }

  const dark = document.documentElement.dataset.theme === "dark";
  const style = getComputedStyle(document.documentElement);
  const accent = token(style, "--accent", dark ? "#62b2c1" : "#28798a");
  return {
    ...(dark ? DARK_ANSI : LIGHT_ANSI),
    background: token(style, "--surface-2", dark ? "#171d22" : "#f7f9fb"),
    foreground: token(style, "--text", dark ? "#e7edf1" : "#17212b"),
    cursor: accent,
    cursorAccent: token(style, "--surface-2", dark ? "#171d22" : "#f7f9fb"),
    selectionBackground: /^#[0-9a-f]{6}$/i.test(accent) ? `${accent}55` : accent,
  };
}

/** Bytes for the keys a phone keyboard lacks. Arrows follow the terminal's
 * cursor-key mode, as a real keyboard's would. */
export function keySequence(key: TermKey, applicationCursor: boolean): string {
  const arrow = (code: string) => (applicationCursor ? `\x1bO${code}` : `\x1b[${code}`);
  switch (key) {
    case "esc":
      return "\x1b";
    case "tab":
      return "\t";
    case "shiftTab":
      return "\x1b[Z";
    case "ctrlC":
      return "\x03";
    case "up":
      return arrow("A");
    case "down":
      return arrow("B");
    case "right":
      return arrow("C");
    case "left":
      return arrow("D");
    case "enter":
      return "\r";
  }
}

const SCREEN_TAIL_LINES = 15;

// Verified CLI trust / permission / update modal markers (smoke report §6.3).
// Never match bare prompt glyphs (`❯`, `›`, `>`) so idle prompts stay writable.
const SINGLE_LINE_MENU_MARKERS: readonly RegExp[] = [
  // Claude Code
  /enter to confirm/i,
  /esc to cancel/i,
  /quick safety check/i,
  /yes,\s*i trust this folder/i,
  /do you want to proceed/i,
  /❯\s*(?:1\.|yes\b|no\b)/i,
  // Codex
  /press enter to continue/i,
  /do you trust the contents/i,
  /›\s*1\./,
  /yes,\s*continue/i,
  /would you like to run/i,
  // OpenCode & shared permission wording
  /permission required/i,
  /allow once/i,
  /allow always/i,
  // Antigravity (agy)
  /enter confirm/i,
  />\s*yes,\s*i trust this folder/i,
  />\s*yes,/i,
];

/**
 * Inspects the last ~15 non-empty lines of an interactive terminal screen for
 * verified CLI trust/permission/modal prompts so the composer blocks Enter
 * instead of silently confirming a focused menu option.
 */
export function hasScreenMenuPrompt(lines: readonly string[]): boolean {
  const tail: string[] = [];
  for (let i = lines.length - 1; i >= 0 && tail.length < SCREEN_TAIL_LINES; i--) {
    const trimmed = lines[i].trim();
    if (trimmed) tail.push(trimmed);
  }
  if (tail.length === 0) return false;

  for (const line of tail) {
    for (const pattern of SINGLE_LINE_MENU_MARKERS) {
      if (pattern.test(line)) return true;
    }
  }

  const joined = tail.join("\n");
  // OpenCode update modal: "Update Available" + "Skip" + "Confirm".
  if (/\bupdate available\b/i.test(joined) && /\bskip\b/i.test(joined) && /\bconfirm\b/i.test(joined)) {
    return true;
  }
  // Codex approval menu with Approve + Deny.
  if (/\bapprove\b/i.test(joined) && /\bdeny\b/i.test(joined)) {
    return true;
  }
  // Permission prompt with Allow + Reject.
  if (/\ballow\b/i.test(joined) && /\breject\b/i.test(joined)) {
    return true;
  }
  return false;
}
