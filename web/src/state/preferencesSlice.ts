/**
 * Local preference slice [A-03/A-05].
 *
 * `theme` and `paneOpen` are preference values kept under their public names;
 * they are derived, never an independent source of truth.
 */

import {
  hasAnyStoredPreference,
  hasStoredPreference,
  readPreference,
  readPreferences,
  resetPreferences,
  writePreference,
  type PreferenceKey,
  type PreferenceValues,
} from "../preferences";
import type { AppSlice, AppState, PreferencesSlice, Theme } from "./types";

type SetAppState = (
  partial: Partial<AppState> | ((state: AppState) => Partial<AppState>),
) => void;

/**
 * The timeline used to open by default on any window wider than a phone, and a
 * person who never folded it never stored an answer. Before the new default
 * (closed) can apply, such a browser — one that has stored *something*, so has
 * been here before — gets the old default written down, once. A browser with
 * nothing stored is a first visit and simply takes the new default.
 *
 * Writing it, rather than deciding it on every read, is what keeps the answer
 * stable: the first preference a new visitor stores would otherwise make them
 * look like a returning one and pop the timeline open mid-session.
 */
export function settleTimelineDefault(): void {
  if (hasStoredPreference("paneTimeline")) return;
  const returning = hasAnyStoredPreference();
  const wide = typeof window === "undefined" || window.innerWidth > 720;
  writePreference("paneTimeline", returning && wide);
}

/** Both panes hidden leaves an empty stage; timeline wins that tie. */
export function initialPaneOpen(): { graph: boolean; timeline: boolean } {
  settleTimelineDefault();
  const graph = readPreference("paneGraph");
  const timeline = readPreference("paneTimeline");
  if (!graph && !timeline) {
    writePreference("paneTimeline", true);
    return { graph: false, timeline: true };
  }
  return { graph, timeline };
}

export function syncDerivedPreferences(
  set: SetAppState,
  get: () => AppState,
): void {
  const preferences = get().preferences;
  if (get().theme !== preferences.theme) set({ theme: preferences.theme });
  const graph = preferences.paneGraph;
  let timeline = preferences.paneTimeline;
  if (!graph && !timeline) {
    timeline = true;
    writePreference("paneTimeline", true);
    set((state) => ({ preferences: { ...state.preferences, paneTimeline: true } }));
  }
  const paneOpen = get().paneOpen;
  if (paneOpen.graph !== graph || paneOpen.timeline !== timeline) {
    set({ paneOpen: { graph, timeline } });
  }
}

export function preferencesEqual(
  a: PreferenceValues,
  b: PreferenceValues,
): boolean {
  return (Object.keys(a) as PreferenceKey[]).every((key) => {
    const left = a[key];
    const right = b[key];
    if (Array.isArray(left) && Array.isArray(right)) {
      return (
        left.length === right.length &&
        left.every((item, index) => item === right[index])
      );
    }
    return left === right;
  });
}

export const createPreferencesSlice: AppSlice<PreferencesSlice> = (set, get) => ({
  theme: readPreference("theme"),
  toggleTheme: () => {
    const next: Theme = get().theme === "dark" ? "light" : "dark";
    get().updateUIPreference("theme", next);
  },
  paneOpen: initialPaneOpen(),
  togglePane: (p) => {
    const cur = get().paneOpen;
    const next = { ...cur, [p]: !cur[p] };
    if (!next.graph && !next.timeline) {
      const fallback = p === "graph" ? "timeline" : "graph";
      next[fallback] = true;
    }
    get().updateUIPreference("paneGraph", next.graph);
    get().updateUIPreference("paneTimeline", next.timeline);
    set({ paneOpen: next });
  },
  preferences: readPreferences(),
  updateUIPreference: (key, value) => {
    const stored = writePreference(key, value);
    set((state) => ({ preferences: { ...state.preferences, [key]: stored } }));
    syncDerivedPreferences(set, get);
  },
  resetUIPreferences: (scope) => {
    const values = resetPreferences(scope);
    set({ preferences: values });
    syncDerivedPreferences(set, get);
  },
});
