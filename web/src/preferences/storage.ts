/**
 * Preference storage [A-03].
 *
 * The single writer for local UI preferences. Responsibilities:
 *
 * - validate and clamp on read *and* write, so a hand-edited or stale value can
 *   never wedge the layout;
 * - notify subscribers, including writes from another tab or the popout window;
 * - offer reset per scope, for "reset this panel" and "reset all layout".
 */

import {
  PREFERENCE_KEYS,
  PREFERENCE_SCHEMA,
  preferenceKeysInScope,
  preferenceSpec,
  type PreferenceKey,
  type PreferenceScope,
  type PreferenceValue,
  type PreferenceValues,
} from "./schema";

export interface PreferenceStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function memoryStorage(): PreferenceStorage {
  const map = new Map<string, string>();
  return {
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
}

function defaultStorage(): PreferenceStorage {
  try {
    if (typeof localStorage === "undefined") return memoryStorage();
    // Private-mode Safari throws on write, not on access.
    const probe = "vised-prefs-probe";
    localStorage.setItem(probe, "1");
    localStorage.removeItem(probe);
    return localStorage;
  } catch {
    return memoryStorage();
  }
}

let storage: PreferenceStorage = defaultStorage();

/** Test seam. Pass `null` to go back to the real (or in-memory) storage. */
export function configurePreferenceStorage(next: PreferenceStorage | null): void {
  storage = next ?? defaultStorage();
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

type Listener = (key: PreferenceKey) => void;
const listeners = new Set<Listener>();
let windowListenerBound = false;

const keyIndex = new Map<string, PreferenceKey>(
  PREFERENCE_KEYS.map((key) => [PREFERENCE_SCHEMA[key].key, key]),
);

function bindWindowListener(): void {
  if (windowListenerBound || typeof window === "undefined") return;
  windowListenerBound = true;
  // Fires for other tabs and for the popout editor window.
  window.addEventListener("storage", (event) => {
    if (!event.key) {
      for (const key of PREFERENCE_KEYS) notify(key);
      return;
    }
    const key = keyIndex.get(event.key);
    if (key) notify(key);
  });
}

function notify(key: PreferenceKey): void {
  for (const listener of [...listeners]) listener(key);
}

export function subscribePreferences(listener: Listener): () => void {
  bindWindowListener();
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

// ---------------------------------------------------------------------------
// Read / write
// ---------------------------------------------------------------------------

export function readPreference<K extends PreferenceKey>(key: K): PreferenceValue<K> {
  const spec = preferenceSpec(key);
  let raw: string | null = null;
  try {
    raw = storage.getItem(spec.key);
  } catch {
    raw = null;
  }
  if (raw === null) return spec.fallback();
  const decoded = spec.decode(raw);
  return decoded === undefined ? spec.fallback() : decoded;
}

export function readPreferences(): PreferenceValues {
  const values = {} as PreferenceValues;
  for (const key of PREFERENCE_KEYS) {
    values[key] = readPreference(key);
  }
  return values;
}

/** Writes a value and returns what was actually stored (after normalization). */
export function writePreference<K extends PreferenceKey>(
  key: K,
  value: PreferenceValue<K>,
): PreferenceValue<K> {
  const spec = preferenceSpec(key);
  const normalized = spec.normalize ? spec.normalize(value) : value;
  try {
    storage.setItem(spec.key, spec.encode(normalized));
  } catch {
    /* storage full or blocked: keep the in-memory value */
  }
  notify(key);
  return normalized;
}

/** Drops stored values so the defaults apply again. */
export function resetPreferences(scope?: PreferenceScope): PreferenceValues {
  const keys = scope ? preferenceKeysInScope(scope) : PREFERENCE_KEYS;
  for (const key of keys) {
    try {
      storage.removeItem(PREFERENCE_SCHEMA[key].key);
    } catch {
      /* ignore */
    }
  }
  for (const key of keys) notify(key);
  return readPreferences();
}
