/**
 * Preference sync: account choices follow the person; device state stays.
 *
 * localStorage stays what every read comes from (storage.ts) and, for an
 * account choice (schema `sync: "account"`), the offline cache of the
 * server's copy at /api/me/preferences. The server keeps the schema's storage
 * keys and encoded strings and knows nothing else about them. Device keys are
 * never sent.
 *
 * - Start: render from the cache at once. A browser that has never synced (no
 *   account marker: a new origin, port or browser) waits up to 500 ms for the
 *   server first, so the default theme does not flash before the person's.
 * - First sync of an account here: what the cache holds and the server lacks
 *   seeds the account (today's browser becomes the starting point).
 * - Writes: a changed account choice is marked pending (persisted, so a closed
 *   tab or a dead server loses nothing) and sent with the others after 800 ms
 *   of quiet, in one PATCH of the current values. Hidden or closing pages
 *   send at once with keepalive. A failure retries with backoff and on
 *   reconnect, focus and the next start; a pending key is cleared only once
 *   the server has the value still stored here.
 * - Loads: the server's value wins for every key without an unsent change
 *   here; a key the server no longer has (reset elsewhere) goes back to its
 *   default. The server orders writes per key as it receives them (last write
 *   wins, no client clocks). Taking a value in never marks it pending, so an
 *   echo never goes back out. Another device's change arrives on window focus.
 * - Accounts: the marker names the account the cache holds. Changes made while
 *   nobody is signed in belong to whoever signs in next; another account's
 *   cached choices and unsent changes are dropped before the new one's load.
 */

import { ACCOUNT_PREFERENCE_KEYS, PREFERENCE_SCHEMA, preferenceKeyForStorageKey } from "./schema";
import { applyStoredPreferences, preferenceStorage, setPreferenceWriteHook } from "./storage";

/** What the server holds for the account: storage key → encoded value. */
export interface RemotePreferences {
  values: Record<string, string>;
}

/** One PATCH: values to store, keys to forget (a reset). */
export interface PreferenceChange {
  set: Record<string, string>;
  remove: string[];
}

/**
 * How a failed request is handled:
 * - `signedOut`: 401. Nothing more until an account is set again.
 * - `unsupported`: the server has no preference API, or refuses this session
 *   (403). This page keeps its preferences locally.
 * - `refused`: the server refused the batch (400, 413). Sending it again cannot
 *   help; the values stay in this browser.
 * - `retry`: offline, a timeout, 5xx, 429. Kept and sent again.
 */
export type SyncFailure = "signedOut" | "unsupported" | "refused" | "retry";

export interface PreferenceTransport {
  load(): Promise<RemotePreferences>;
  save(change: PreferenceChange, options: { keepalive: boolean }): Promise<RemotePreferences>;
  classify(error: unknown): SyncFailure;
}

export interface PreferenceSyncOptions {
  transport: PreferenceTransport;
  /** The first time this page load a save fails and will be retried. */
  onSaveFailed?: () => void;
  /**
   * The account to start as, for a window that has no sign-in of its own (the
   * popout editor, opened by a signed-in window of the same browser).
   */
  account?: string;
}

/** The account the cached account choices belong to; absent: never synced. */
export const ACCOUNT_MARKER_KEY = "vised-prefs-account";
/** Unsent changes: storage key → the account it was made as ("" nobody yet). */
export const PENDING_KEY = "vised-prefs-pending";
export const SAVE_DELAY_MS = 800;
export const COLD_START_WAIT_MS = 500;
/** Focus refetches at most this often. */
export const REFETCH_GAP_MS = 5_000;
export const RETRY_DELAYS_MS: readonly number[] = [2_000, 5_000, 15_000, 30_000, 60_000];

const ACCOUNT_STORAGE_KEYS: readonly string[] = ACCOUNT_PREFERENCE_KEYS.map(
  (key) => PREFERENCE_SCHEMA[key].key,
);
const ACCOUNT_STORAGE_KEY_SET = new Set(ACCOUNT_STORAGE_KEYS);

type Pending = Record<string, string>;

interface Engine {
  options: PreferenceSyncOptions;
  /** Who this page is signed in as: undefined until known, null signed out. */
  account: string | null | undefined;
  /** Bumped when the account changes: answers to older requests are dropped. */
  epoch: number;
  /** The server cannot keep preferences for this page: local only. */
  off: boolean;
  saveTimer: ReturnType<typeof setTimeout> | null;
  retryTimer: ReturnType<typeof setTimeout> | null;
  retries: number;
  saving: Promise<void> | null;
  saveAgain: boolean;
  loading: { epoch: number; work: Promise<void> } | null;
  lastLoadAt: number;
  /** The newest server copy taken in before the account was known. */
  unclaimed: Record<string, string> | null;
  noticeShown: boolean;
  unbind: () => void;
}

let engine: Engine | null = null;

// ---------------------------------------------------------------------------
// Bookkeeping in storage
// ---------------------------------------------------------------------------

function getRaw(key: string): string | null {
  try {
    return preferenceStorage().getItem(key);
  } catch {
    return null;
  }
}

function setRaw(key: string, value: string | null): void {
  try {
    if (value === null) preferenceStorage().removeItem(key);
    else preferenceStorage().setItem(key, value);
  } catch {
    /* blocked storage: this page's sync still works from memory-less reads */
  }
}

/** The account whose choices the cache holds, or null if it never synced. */
export function cachedPreferenceAccount(): string | null {
  return getRaw(ACCOUNT_MARKER_KEY);
}

function readPending(): Pending {
  const raw = getRaw(PENDING_KEY);
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const pending: Pending = {};
    for (const [key, owner] of Object.entries(parsed as Record<string, unknown>)) {
      if (ACCOUNT_STORAGE_KEY_SET.has(key) && typeof owner === "string") pending[key] = owner;
    }
    return pending;
  } catch {
    return {};
  }
}

function writePending(pending: Pending): void {
  setRaw(PENDING_KEY, Object.keys(pending).length > 0 ? JSON.stringify(pending) : null);
}

function decodable(storageKey: string, value: string): boolean {
  const key = preferenceKeyForStorageKey(storageKey);
  return key !== undefined && PREFERENCE_SCHEMA[key].decode(value) !== undefined;
}

/** A timer-capped wait that never rejects. */
function settleWithin(work: Promise<unknown>, ms: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    const done = () => {
      clearTimeout(timer);
      resolve();
    };
    work.then(done, done);
  });
}

// ---------------------------------------------------------------------------
// Taking the server's copy in
// ---------------------------------------------------------------------------

/**
 * Takes in what the server holds. Before the account is known (at start) the
 * values still apply — they are the session's — but nothing here is removed,
 * and the first-sync seeding waits until the account is known.
 */
function takeRemote(current: Engine, values: Record<string, string>): void {
  const account = current.account;
  const pending = readPending();
  if (typeof account !== "string") {
    current.unclaimed = values;
  } else if (cachedPreferenceAccount() === null) {
    // First sync here: what this browser holds and the server lacks seeds
    // the account.
    for (const key of ACCOUNT_STORAGE_KEYS) {
      if (key in values || key in pending) continue;
      if (getRaw(key) !== null) pending[key] = account;
    }
    writePending(pending);
    setRaw(ACCOUNT_MARKER_KEY, account);
  }
  const confirmed = typeof account === "string" && cachedPreferenceAccount() === account;
  const changes: Record<string, string | null> = {};
  for (const key of ACCOUNT_STORAGE_KEYS) {
    // An unsent change made here is newer than the server's copy.
    if (key in pending) continue;
    const local = getRaw(key);
    if (Object.prototype.hasOwnProperty.call(values, key)) {
      const remote = values[key];
      // A value this version cannot read (a newer client's) is left alone.
      if (remote !== local && decodable(key, remote)) changes[key] = remote;
    } else if (confirmed && local !== null) {
      // Reset on another device.
      changes[key] = null;
    }
  }
  applyStoredPreferences(changes);
}

function load(): Promise<void> {
  const current = engine;
  if (!current || current.off || current.account === null) return Promise.resolve();
  const epoch = current.epoch;
  if (current.loading && current.loading.epoch === epoch) return current.loading.work;
  current.lastLoadAt = Date.now();
  const work = current.options.transport.load().then(
    (remote) => {
      if (engine !== current || epoch !== current.epoch) return;
      takeRemote(current, remote.values);
      void save(false);
    },
    (error: unknown) => {
      if (engine !== current || epoch !== current.epoch) return;
      failed(current, error, null);
    },
  );
  const entry = { epoch, work };
  current.loading = entry;
  void work.then(() => {
    if (current.loading === entry) current.loading = null;
  });
  return work;
}

// ---------------------------------------------------------------------------
// Sending changes
// ---------------------------------------------------------------------------

function markPending(storageKey: string): void {
  const current = engine;
  if (!current || !ACCOUNT_STORAGE_KEY_SET.has(storageKey)) return;
  const pending = readPending();
  pending[storageKey] = typeof current.account === "string" ? current.account : "";
  writePending(pending);
  scheduleSave(current, SAVE_DELAY_MS);
}

function scheduleSave(current: Engine, delay: number): void {
  if (current.saveTimer) clearTimeout(current.saveTimer);
  current.saveTimer = setTimeout(() => {
    current.saveTimer = null;
    void save(false);
  }, delay);
}

function save(keepalive: boolean): Promise<void> {
  const current = engine;
  if (!current || current.off) return Promise.resolve();
  const account = current.account;
  // Nothing goes out until this cache is known to be the account's.
  if (typeof account !== "string" || cachedPreferenceAccount() !== account) return Promise.resolve();
  if (current.saving && !keepalive) {
    current.saveAgain = true;
    return current.saving;
  }
  const change: PreferenceChange = { set: {}, remove: [] };
  const sent: Record<string, string | null> = {};
  for (const [key, owner] of Object.entries(readPending())) {
    if (owner !== account) continue;
    const value = getRaw(key);
    sent[key] = value;
    if (value === null) change.remove.push(key);
    else change.set[key] = value;
  }
  if (Object.keys(sent).length === 0) return Promise.resolve();
  if (current.saveTimer) {
    clearTimeout(current.saveTimer);
    current.saveTimer = null;
  }
  if (current.retryTimer) {
    clearTimeout(current.retryTimer);
    current.retryTimer = null;
  }
  const epoch = current.epoch;
  const work = current.options.transport.save(change, { keepalive }).then(
    (remote) => {
      if (engine !== current || epoch !== current.epoch) return;
      settle(account, sent);
      current.retries = 0;
      takeRemote(current, remote.values);
    },
    (error: unknown) => {
      if (engine !== current || epoch !== current.epoch) return;
      failed(current, error, sent);
    },
  );
  if (keepalive) return work;
  const saving = work.then(() => {
    if (current.saving === saving) current.saving = null;
    if (current.saveAgain && engine === current) {
      current.saveAgain = false;
      void save(false);
    }
  });
  current.saving = saving;
  return saving;
}

/** Clears the pending keys the server now has, unless they changed again since. */
function settle(account: string, sent: Record<string, string | null>): void {
  const pending = readPending();
  for (const [key, value] of Object.entries(sent)) {
    if (pending[key] === account && getRaw(key) === value) delete pending[key];
  }
  writePending(pending);
}

function failed(current: Engine, error: unknown, sent: Record<string, string | null> | null): void {
  switch (current.options.transport.classify(error)) {
    case "signedOut":
      // The sign-in gate hears the 401 too and sets the next account.
      return;
    case "unsupported":
      current.off = true;
      return;
    case "refused":
      if (sent && typeof current.account === "string") settle(current.account, sent);
      return;
    case "retry":
      if (!sent) return; // a load: focus and reconnect fetch again
      if (!current.noticeShown) {
        current.noticeShown = true;
        current.options.onSaveFailed?.();
      }
      if (current.retryTimer) clearTimeout(current.retryTimer);
      current.retryTimer = setTimeout(
        () => {
          current.retryTimer = null;
          void save(false);
        },
        RETRY_DELAYS_MS[Math.min(current.retries, RETRY_DELAYS_MS.length - 1)],
      );
      current.retries += 1;
      return;
  }
}

// ---------------------------------------------------------------------------
// Page life
// ---------------------------------------------------------------------------

function refetch(): void {
  const current = engine;
  if (!current || typeof current.account !== "string") return;
  if (Date.now() - current.lastLoadAt < REFETCH_GAP_MS) return;
  void load();
}

/** Back online: send what waits and catch up with other devices. */
export function resumePreferenceSync(): void {
  const current = engine;
  if (!current) return;
  current.retries = 0;
  void save(false);
  void load();
}

function bindPage(): () => void {
  if (typeof window === "undefined") return () => {};
  const onVisibility = () => {
    if (document.visibilityState === "hidden") void save(true);
    else refetch();
  };
  const onPageHide = () => void save(true);
  window.addEventListener("focus", refetch);
  window.addEventListener("pagehide", onPageHide);
  window.addEventListener("online", resumePreferenceSync);
  document.addEventListener("visibilitychange", onVisibility);
  return () => {
    window.removeEventListener("focus", refetch);
    window.removeEventListener("pagehide", onPageHide);
    window.removeEventListener("online", resumePreferenceSync);
    document.removeEventListener("visibilitychange", onVisibility);
  };
}

// ---------------------------------------------------------------------------
// Public
// ---------------------------------------------------------------------------

/**
 * Starts syncing. Resolves when the app may render: at once on a browser that
 * synced before, within COLD_START_WAIT_MS on one that never did.
 */
export function startPreferenceSync(options: PreferenceSyncOptions): Promise<void> {
  if (engine) return Promise.resolve();
  const current: Engine = {
    options,
    account: undefined,
    epoch: 0,
    off: false,
    saveTimer: null,
    retryTimer: null,
    retries: 0,
    saving: null,
    saveAgain: false,
    loading: null,
    lastLoadAt: 0,
    unclaimed: null,
    noticeShown: false,
    unbind: () => {},
  };
  engine = current;
  setPreferenceWriteHook(markPending);
  current.unbind = bindPage();
  const neverSynced = cachedPreferenceAccount() === null;
  if (options.account !== undefined) void setPreferenceAccount(options.account);
  const loaded = load();
  return neverSynced ? settleWithin(loaded, COLD_START_WAIT_MS) : Promise.resolve();
}

/**
 * Who this page is signed in as: an account id (`local` in the single-user
 * edition), or null once signed out. Returns a promise worth waiting for
 * before showing the app — up to COLD_START_WAIT_MS while the account's
 * choices load into a cache that held none of them — or null when there is
 * nothing to wait for.
 */
export function setPreferenceAccount(id: string | null): Promise<void> | null {
  const current = engine;
  if (!current || current.account === id) return null;
  const previous = current.account;
  // The first answer at start names the session the start's load was made
  // with, so that load (done or still on its way) is this account's. Any
  // later change is another session: answers to older requests are dropped.
  const unclaimed = previous === undefined ? current.unclaimed : null;
  if (previous !== undefined) {
    current.epoch += 1;
    current.saving = null;
    current.saveAgain = false;
  }
  current.account = id;
  current.unclaimed = null;
  current.retries = 0;
  if (current.saveTimer) clearTimeout(current.saveTimer);
  if (current.retryTimer) clearTimeout(current.retryTimer);
  current.saveTimer = null;
  current.retryTimer = null;
  if (id === null) return null;

  // Unsent changes: this account's stay, and so do those made while nobody
  // was signed in (the person signing in made them). Another account's go,
  // with the values they were for.
  const pending = readPending();
  const forgotten: Record<string, null> = {};
  for (const [key, owner] of Object.entries(pending)) {
    if (owner === id) continue;
    if (owner === "") {
      pending[key] = id;
    } else {
      delete pending[key];
      forgotten[key] = null;
    }
  }
  writePending(pending);
  applyStoredPreferences(forgotten);

  const marker = cachedPreferenceAccount();
  if (marker !== null && marker !== id) {
    // Another account's choices: gone before anything shows as this one's.
    const dropped: Record<string, null> = {};
    for (const key of ACCOUNT_STORAGE_KEYS) {
      if (!(key in pending) && getRaw(key) !== null) dropped[key] = null;
    }
    applyStoredPreferences(dropped);
    setRaw(ACCOUNT_MARKER_KEY, id);
  }
  if (unclaimed) {
    // The start's load already brought this session's copy: take it as the
    // account's now (seeding a first sync, dropping what was reset).
    takeRemote(current, unclaimed);
    void save(false);
    return null;
  }
  const loaded = load();
  // A cache that held this account's choices shows them at once; otherwise
  // the caller may hold the app back a moment for them.
  return marker === id ? null : settleWithin(loaded, COLD_START_WAIT_MS);
}

/** Sends what waits now (before signing out), within `timeoutMs`. */
export function flushPreferences(timeoutMs = 2_000): Promise<void> {
  const current = engine;
  if (!current) return Promise.resolve();
  return settleWithin(save(false), timeoutMs);
}

/** Test seam: stops syncing and forgets the engine. */
export function stopPreferenceSync(): void {
  const current = engine;
  if (!current) return;
  if (current.saveTimer) clearTimeout(current.saveTimer);
  if (current.retryTimer) clearTimeout(current.retryTimer);
  current.unbind();
  setPreferenceWriteHook(null);
  engine = null;
}
