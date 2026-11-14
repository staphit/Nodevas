/**
 * Preference storage.
 *
 * The single writer for local UI preferences. Responsibilities:
 *
 * - validate and clamp on read *and* write, so a hand-edited or stale value can
 *   never wedge the layout;
 * - notify subscribers, including writes from another tab or the popout window;
 * - offer reset per scope, for "reset this panel" and "reset all layout";
 * - tell sync.ts which account choices this window changed (the write hook),
 *   and take the server's copy in without telling it back (applyStored…).
 *
 * localStorage stays the source every read comes from; for an account choice
 * it is the offline cache of the server's copy.
 */

import {
  LEGACY_PREFERENCES,
  PREFERENCE_KEYS,
  PREFERENCE_SCHEMA,
  preferenceKeyForStorageKey,
  preferenceKeysInScope,
  preferenceSpec,
  type PreferenceKey,
  type PreferenceScope,
  type PreferenceSpec,
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

/** The storage legacy keys were last moved into; see ensureMigrated. */
let migratedStorage: PreferenceStorage | null = null;

/** Test seam. Pass `null` to go back to the real (or in-memory) storage. */
export function configurePreferenceStorage(next: PreferenceStorage | null): void {
  storage = next ?? defaultStorage();
  migratedStorage = null;
}

/** The storage in use, for sync.ts's own bookkeeping keys. */
export function preferenceStorage(): PreferenceStorage {
  return storage;
}

/**
 * Moves the values of keys that predate the schema (LEGACY_PREFERENCES) under
 * their schema keys, once per storage. Done on first use rather than at start
 * so a store that reads its preference while modules load sees it too.
 */
function ensureMigrated(): void {
  if (migratedStorage === storage) return;
  migratedStorage = storage;
  for (const legacy of LEGACY_PREFERENCES) {
    try {
      const raw = storage.getItem(legacy.key);
      if (raw === null) continue;
      const values = legacy.read(raw);
      for (const key of Object.keys(values) as PreferenceKey[]) {
        const spec = PREFERENCE_SCHEMA[key] as PreferenceSpec<unknown>;
        if (storage.getItem(spec.key) === null) storage.setItem(spec.key, spec.encode(values[key]));
      }
      storage.removeItem(legacy.key);
    } catch {
      /* blocked storage: the legacy key stays for the next visit */
    }
  }
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

type Listener = (key: PreferenceKey) => void;
const listeners = new Set<Listener>();
let windowListenerBound = false;

function bindWindowListener(): void {
  if (windowListenerBound || typeof window === "undefined") return;
  windowListenerBound = true;
  // Fires for other tabs and for the popout editor window.
  window.addEventListener("storage", (event) => {
    if (!event.key) {
      for (const key of PREFERENCE_KEYS) notify(key);
      return;
    }
    const key = preferenceKeyForStorageKey(event.key);
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
// Sync hook
// ---------------------------------------------------------------------------

/** Told the storage key of every account choice this window changes. */
export type PreferenceWriteHook = (storageKey: string) => void;

let writeHook: PreferenceWriteHook | null = null;

/** sync.ts installs this while it runs; `null` removes it. */
export function setPreferenceWriteHook(hook: PreferenceWriteHook | null): void {
  writeHook = hook;
}

function changed(key: PreferenceKey): void {
  if (writeHook && PREFERENCE_SCHEMA[key].sync === "account") writeHook(PREFERENCE_SCHEMA[key].key);
}

/**
 * Takes in encoded values that come from elsewhere — the server's copy of the
 * account's choices — by storage key; `null` removes one. Unknown keys and
 * values the schema cannot decode are skipped. Not a change made here, so the
 * write hook is not told: an echo never goes back out. Subscribers hear every
 * key that changed, once all of them are stored.
 */
export function applyStoredPreferences(
  values: Readonly<Record<string, string | null>>,
): PreferenceKey[] {
  ensureMigrated();
  const updated: PreferenceKey[] = [];
  for (const [storageKey, value] of Object.entries(values)) {
    const key = preferenceKeyForStorageKey(storageKey);
    if (!key) continue;
    if (value !== null && PREFERENCE_SCHEMA[key].decode(value) === undefined) continue;
    try {
      if (storage.getItem(storageKey) === value) continue;
      if (value === null) storage.removeItem(storageKey);
      else storage.setItem(storageKey, value);
      updated.push(key);
    } catch {
      /* blocked storage: nothing stored, nothing to tell */
    }
  }
  for (const key of updated) notify(key);
  return updated;
}

// ---------------------------------------------------------------------------
// Read / write
// ---------------------------------------------------------------------------

function storedRaw(storageKey: string): string | null {
  try {
    return storage.getItem(storageKey);
  } catch {
    return null;
  }
}

export function readPreference<K extends PreferenceKey>(key: K): PreferenceValue<K> {
  ensureMigrated();
  const spec = preferenceSpec(key);
  const raw = storedRaw(spec.key);
  if (raw === null) return spec.fallback();
  const decoded = spec.decode(raw);
  return decoded === undefined ? spec.fallback() : decoded;
}

/** Whether a value — usable or not — is stored for `key`. */
export function hasStoredPreference(key: PreferenceKey): boolean {
  ensureMigrated();
  return storedRaw(PREFERENCE_SCHEMA[key].key) !== null;
}

/**
 * Whether this browser has stored any preference at all — the closest thing
 * the client has to "this person has used the app here before". Used to keep a
 * changed default from rearranging the screen of someone who never had a
 * reason to store the old one.
 */
export function hasAnyStoredPreference(): boolean {
  return PREFERENCE_KEYS.some(hasStoredPreference);
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
  ensureMigrated();
  const spec = preferenceSpec(key);
  const normalized = spec.normalize ? spec.normalize(value) : value;
  const encoded = spec.encode(normalized);
  const previous = storedRaw(spec.key);
  try {
    storage.setItem(spec.key, encoded);
  } catch {
    /* storage full or blocked: keep the in-memory value */
  }
  // Writing what is already stored changes nothing worth saving elsewhere.
  if (previous !== encoded) changed(key);
  notify(key);
  return normalized;
}

/**
 * The preferences that pick which view opens: Board, Station or Settings, and
 * the Settings section. A view that crashes as it renders
 * opens again on every reload while these point at it; the root error
 * boundary's "Reset view" drops them so the app starts on the board.
 */
export const VIEW_SELECTION_PREFERENCES: readonly PreferenceKey[] = [
  "projectMode",
  "settingsSection",
];

/** Drops stored values so the defaults apply again (for the account too). */
export function resetPreferences(
  scope?: PreferenceScope | readonly PreferenceKey[],
): PreferenceValues {
  ensureMigrated();
  const keys = Array.isArray(scope)
    ? (scope as readonly PreferenceKey[])
    : scope
      ? preferenceKeysInScope(scope as PreferenceScope)
      : PREFERENCE_KEYS;
  for (const key of keys) {
    const storageKey = PREFERENCE_SCHEMA[key].key;
    if (storedRaw(storageKey) === null) continue;
    try {
      storage.removeItem(storageKey);
    } catch {
      /* ignore */
    }
    changed(key);
  }
  for (const key of keys) notify(key);
  return readPreferences();
}
