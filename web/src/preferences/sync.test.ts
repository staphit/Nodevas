import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PREFERENCE_SCHEMA, type PreferenceKey } from "./schema";
import {
  configurePreferenceStorage,
  readPreference,
  resetPreferences,
  subscribePreferences,
  writePreference,
  type PreferenceStorage,
} from "./storage";
import {
  ACCOUNT_MARKER_KEY,
  COLD_START_WAIT_MS,
  firstPreferenceLoad,
  flushPreferences,
  PENDING_KEY,
  REFETCH_GAP_MS,
  RETRY_DELAYS_MS,
  SAVE_DELAY_MS,
  setPreferenceAccount,
  startPreferenceSync,
  stopPreferenceSync,
  type PreferenceChange,
  type PreferenceTransport,
  type SyncFailure,
} from "./sync";

const THEME = PREFERENCE_SCHEMA.theme.key;
const LANGUAGE = PREFERENCE_SCHEMA.language.key;
const THEME_PACK = PREFERENCE_SCHEMA.themePack.key;
const FONT = PREFERENCE_SCHEMA.editorFontSize.key;
const SPLIT = PREFERENCE_SCHEMA.paneSplit.key;

function fakeStorage(initial: Record<string, string> = {}): PreferenceStorage & { map: Map<string, string> } {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  };
}

function copy(change: PreferenceChange): PreferenceChange {
  return JSON.parse(JSON.stringify(change)) as PreferenceChange;
}

class Failure extends Error {
  constructor(readonly kind: SyncFailure) {
    super(kind);
  }
}

/** A server that keeps one account's values, PATCH semantics included. */
function fakeServer(initial: Record<string, string> = {}) {
  const server = {
    values: { ...initial },
    loads: 0,
    saves: [] as { change: PreferenceChange; keepalive: boolean }[],
    /** Errors the next calls throw, in order (load and save alike). */
    failures: [] as Failure[],
    /** A load that has not answered yet, when set. */
    hold: null as Promise<void> | null,
    /** A save that has not answered yet, when set. */
    saveHold: null as Promise<void> | null,
  };
  const transport: PreferenceTransport = {
    load: async () => {
      server.loads += 1;
      if (server.hold) await server.hold;
      const failure = server.failures.shift();
      if (failure) throw failure;
      return { values: { ...server.values } };
    },
    save: async (change, { keepalive }) => {
      server.saves.push({ change: copy(change), keepalive });
      if (server.saveHold) await server.saveHold;
      const failure = server.failures.shift();
      if (failure) throw failure;
      Object.assign(server.values, change.set);
      for (const key of change.remove) delete server.values[key];
      return { values: { ...server.values } };
    },
    classify: (error) => (error instanceof Failure ? error.kind : "retry"),
  };
  return { server, transport };
}

function pendingIn(storage: { map: Map<string, string> }): Record<string, string> {
  const raw = storage.map.get(PENDING_KEY);
  return raw ? (JSON.parse(raw) as Record<string, string>) : {};
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  stopPreferenceSync();
  configurePreferenceStorage(null);
  vi.useRealTimers();
});

describe("start", () => {
  it("waits at most half a second on a browser that never synced", async () => {
    configurePreferenceStorage(fakeStorage());
    const { server, transport } = fakeServer({ [THEME]: "light" });
    server.hold = new Promise(() => {});
    let ready = false;
    void startPreferenceSync({ transport }).then(() => {
      ready = true;
    });
    await vi.advanceTimersByTimeAsync(COLD_START_WAIT_MS - 1);
    expect(ready).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(ready).toBe(true);
    // Rendered with the default rather than waiting longer.
    expect(readPreference("theme")).toBe("dark");
  });

  it("shows the server's choices before the first render when they arrive in time", async () => {
    configurePreferenceStorage(fakeStorage());
    const { transport } = fakeServer({ [THEME]: "light", [THEME_PACK]: "magic" });
    await startPreferenceSync({ transport });
    expect(readPreference("theme")).toBe("light");
    expect(readPreference("themePack")).toBe("magic");
  });

  it("renders the cache at once on a browser that synced, then takes the server's values", async () => {
    const storage = fakeStorage({ [ACCOUNT_MARKER_KEY]: "local", [THEME]: "dark" });
    configurePreferenceStorage(storage);
    const { server, transport } = fakeServer({ [THEME]: "light" });
    let release!: () => void;
    server.hold = new Promise((resolve) => {
      release = resolve;
    });
    const seen: PreferenceKey[] = [];
    const unsubscribe = subscribePreferences((key) => seen.push(key));
    await startPreferenceSync({ transport });
    expect(readPreference("theme")).toBe("dark");
    expect(setPreferenceAccount("local")).toBeNull();
    release();
    await vi.advanceTimersByTimeAsync(0);
    unsubscribe();
    expect(readPreference("theme")).toBe("light");
    expect(seen).toContain("theme");
  });
});

describe("first sync", () => {
  it("seeds the account with what this browser holds and the server lacks; the server's values win", async () => {
    const storage = fakeStorage({ [THEME]: "light", [FONT]: "18", [SPLIT]: "30.00" });
    configurePreferenceStorage(storage);
    const { server, transport } = fakeServer({ [THEME]: "dark" });
    await startPreferenceSync({ transport });
    expect(setPreferenceAccount("local")).toBeNull();
    await vi.advanceTimersByTimeAsync(0);
    expect(readPreference("theme")).toBe("dark");
    expect(storage.map.get(ACCOUNT_MARKER_KEY)).toBe("local");
    expect(server.saves).toHaveLength(1);
    // Device keys never leave the browser.
    expect(server.saves[0].change).toEqual({ set: { [FONT]: "18" }, remove: [] });
    expect(server.values).toEqual({ [THEME]: "dark", [FONT]: "18" });
    expect(pendingIn(storage)).toEqual({});
  });
});

/** A started engine for account `local` whose cache synced before. */
async function synced(server: Record<string, string> = {}, cache: Record<string, string> = {}) {
  const storage = fakeStorage({ [ACCOUNT_MARKER_KEY]: "local", ...cache });
  configurePreferenceStorage(storage);
  const fake = fakeServer(server);
  const onSaveFailed = vi.fn();
  await startPreferenceSync({ transport: fake.transport, onSaveFailed });
  setPreferenceAccount("local");
  await vi.advanceTimersByTimeAsync(0);
  fake.server.saves.length = 0;
  fake.server.loads = 0;
  return { storage, onSaveFailed, ...fake };
}

describe("writes", () => {
  it("batches changes made within 800 ms into one PATCH", async () => {
    const { server } = await synced();
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS - 100);
    writePreference("editorFontSize", 16);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS - 1);
    expect(server.saves).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(server.saves).toHaveLength(1);
    expect(server.saves[0].change).toEqual({ set: { [THEME]: "light", [FONT]: "16" }, remove: [] });
    expect(server.values).toEqual({ [THEME]: "light", [FONT]: "16" });
  });

  it("never sends device keys", async () => {
    const { server } = await synced();
    writePreference("paneSplit", 30);
    writePreference("explorerWidth", 400);
    writePreference("projectMode", "station");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS * 2);
    expect(server.saves).toHaveLength(0);
  });

  it("does not send a value that did not change", async () => {
    const { server } = await synced({ [THEME]: "light" }, { [THEME]: "light" });
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(0);
  });

  it("sends a reset as a removal", async () => {
    const { server } = await synced({ [THEME]: "light" }, { [THEME]: "light" });
    resetPreferences("appearance");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves[0].change).toEqual({ set: {}, remove: [THEME] });
    expect(server.values).toEqual({});
  });

  it("takes the echo in without sending it back", async () => {
    const { server } = await synced();
    // Another device chose a language meanwhile.
    server.values[LANGUAGE] = "zh-TW";
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(1);
    // The PATCH answer carries another device's language too.
    expect(readPreference("language")).toBe("zh-TW");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS * 5);
    expect(server.saves).toHaveLength(1);
  });

  it("keeps a change made while a save is on its way", async () => {
    const { server, storage } = await synced();
    let release!: () => void;
    server.saveHold = new Promise((resolve) => {
      release = resolve;
    });
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(1);
    writePreference("theme", "dark");
    server.saveHold = null;
    release();
    await vi.advanceTimersByTimeAsync(0);
    // The answer to the first save does not undo the second change.
    expect(readPreference("theme")).toBe("dark");
    expect(pendingIn(storage)).toEqual({ [THEME]: "local" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.values[THEME]).toBe("dark");
    expect(pendingIn(storage)).toEqual({});
  });

  it("sends at once, with keepalive, when the page goes away", async () => {
    const { server } = await synced();
    writePreference("theme", "light");
    window.dispatchEvent(new Event("pagehide"));
    await vi.advanceTimersByTimeAsync(0);
    expect(server.saves).toHaveLength(1);
    expect(server.saves[0].keepalive).toBe(true);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(1);
  });
});

describe("loads", () => {
  it("keeps an unsent change over the server's value", async () => {
    const { server, storage } = await synced({ [THEME]: "dark" }, { [THEME]: "dark" });
    server.failures.push(new Failure("retry"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(pendingIn(storage)).toEqual({ [THEME]: "local" });
    vi.setSystemTime(Date.now() + REFETCH_GAP_MS);
    window.dispatchEvent(new Event("focus"));
    await vi.advanceTimersByTimeAsync(0);
    expect(server.loads).toBe(1);
    expect(readPreference("theme")).toBe("light");
    // The load's success sends what waits.
    expect(server.values[THEME]).toBe("light");
  });

  it("brings in another device's change on focus, at most every few seconds", async () => {
    const { server } = await synced({ [THEME]: "dark" }, { [THEME]: "dark" });
    server.values[THEME] = "light";
    window.dispatchEvent(new Event("focus"));
    await vi.advanceTimersByTimeAsync(0);
    expect(server.loads).toBe(0);
    vi.setSystemTime(Date.now() + REFETCH_GAP_MS);
    window.dispatchEvent(new Event("focus"));
    await vi.advanceTimersByTimeAsync(0);
    expect(server.loads).toBe(1);
    expect(readPreference("theme")).toBe("light");
  });

  it("goes back to the default when another device reset the choice", async () => {
    const { server } = await synced({ [THEME]: "light" }, { [THEME]: "light" });
    delete server.values[THEME];
    vi.setSystemTime(Date.now() + REFETCH_GAP_MS);
    window.dispatchEvent(new Event("focus"));
    await vi.advanceTimersByTimeAsync(0);
    expect(readPreference("theme")).toBe("dark");
  });

  it("ignores a value it cannot read and keys it does not know", async () => {
    const { server, storage } = await synced({ [THEME]: "light" }, { [THEME]: "light" });
    server.values[THEME] = "sepia";
    server.values["vised-from-the-future"] = "1";
    vi.setSystemTime(Date.now() + REFETCH_GAP_MS);
    window.dispatchEvent(new Event("focus"));
    await vi.advanceTimersByTimeAsync(0);
    expect(readPreference("theme")).toBe("light");
    expect(storage.map.has("vised-from-the-future")).toBe(false);
  });
});

describe("offline", () => {
  it("keeps unsent changes in storage, retries, and says so once", async () => {
    const { server, storage, onSaveFailed } = await synced();
    server.failures.push(new Failure("retry"), new Failure("retry"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(pendingIn(storage)).toEqual({ [THEME]: "local" });
    await vi.advanceTimersByTimeAsync(RETRY_DELAYS_MS[0]);
    expect(server.saves).toHaveLength(2);
    expect(onSaveFailed).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(RETRY_DELAYS_MS[1]);
    expect(server.saves).toHaveLength(3);
    expect(server.values).toEqual({ [THEME]: "light" });
    expect(pendingIn(storage)).toEqual({});
  });

  it("sends what waited at the next start", async () => {
    const { server, storage } = await synced();
    server.failures.push(new Failure("retry"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    stopPreferenceSync();
    expect(pendingIn(storage)).toEqual({ [THEME]: "local" });

    // The next visit: the server is back.
    await startPreferenceSync({ transport: fakeServerFor(server) });
    setPreferenceAccount("local");
    await vi.advanceTimersByTimeAsync(0);
    expect(server.values).toEqual({ [THEME]: "light" });
    expect(pendingIn(storage)).toEqual({});
  });

  it("sends what waits when the connection comes back", async () => {
    const { server } = await synced();
    server.failures.push(new Failure("retry"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    window.dispatchEvent(new Event("online"));
    await vi.advanceTimersByTimeAsync(0);
    expect(server.values).toEqual({ [THEME]: "light" });
  });

  it("stops asking a server without the preference API", async () => {
    const { server } = await synced();
    server.failures.push(new Failure("unsupported"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS * 2 + RETRY_DELAYS_MS[0]);
    writePreference("theme", "dark");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS * 2);
    expect(server.saves).toHaveLength(1);
    expect(readPreference("theme")).toBe("dark");
  });

  it("drops a batch the server refused instead of sending it forever", async () => {
    const { server, storage } = await synced();
    server.failures.push(new Failure("refused"));
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS + RETRY_DELAYS_MS[RETRY_DELAYS_MS.length - 1]);
    expect(server.saves).toHaveLength(1);
    expect(pendingIn(storage)).toEqual({});
    expect(readPreference("theme")).toBe("light");
  });
});

describe("accounts", () => {
  it("drops another account's choices and unsent changes before loading the new one's", async () => {
    const storage = fakeStorage({
      [ACCOUNT_MARKER_KEY]: "alice",
      [THEME]: "light",
      [FONT]: "20",
      [SPLIT]: "30.00",
      [PENDING_KEY]: JSON.stringify({ [FONT]: "alice" }),
    });
    configurePreferenceStorage(storage);
    const { server, transport } = fakeServer({ [LANGUAGE]: "zh-TW" });
    server.failures.push(new Failure("signedOut"));
    await startPreferenceSync({ transport });
    expect(setPreferenceAccount(null)).toBeNull();
    const waiting = setPreferenceAccount("bob");
    expect(waiting).not.toBeNull();
    // Gone before anything renders as bob's.
    expect(readPreference("theme")).toBe("dark");
    expect(readPreference("editorFontSize")).toBe(14);
    expect(storage.map.get(SPLIT)).toBe("30.00");
    expect(storage.map.get(ACCOUNT_MARKER_KEY)).toBe("bob");
    await waiting;
    expect(readPreference("language")).toBe("zh-TW");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    // Alice's change never reaches bob's account.
    expect(server.saves).toHaveLength(0);
    expect(pendingIn(storage)).toEqual({});
  });

  it("gives a change made while nobody was signed in to whoever signs in", async () => {
    const storage = fakeStorage({ [ACCOUNT_MARKER_KEY]: "alice", [THEME]: "light" });
    configurePreferenceStorage(storage);
    const { server, transport } = fakeServer({ [THEME]: "dark" });
    server.failures.push(new Failure("signedOut"));
    await startPreferenceSync({ transport });
    setPreferenceAccount(null);
    // The sign-in screen's language picker.
    writePreference("language", "zh-TW");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(0);
    await setPreferenceAccount("bob");
    await vi.advanceTimersByTimeAsync(0);
    expect(server.saves[0].change).toEqual({ set: { [LANGUAGE]: "zh-TW" }, remove: [] });
    expect(readPreference("theme")).toBe("dark");
    expect(readPreference("language")).toBe("zh-TW");
  });

  it("never sends a change another account made in another tab", async () => {
    const { server, storage } = await synced();
    storage.map.set(THEME, "light");
    storage.map.set(PENDING_KEY, JSON.stringify({ [THEME]: "alice" }));
    writePreference("editorFontSize", 16);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    expect(server.saves).toHaveLength(1);
    expect(server.saves[0].change).toEqual({ set: { [FONT]: "16" }, remove: [] });
  });

  it("saves the last changes as the account signing out", async () => {
    const { server, storage } = await synced();
    writePreference("theme", "light");
    await flushPreferences();
    setPreferenceAccount(null);
    expect(server.saves[0].change).toEqual({ set: { [THEME]: "light" }, remove: [] });
    expect(pendingIn(storage)).toEqual({});
  });

  it("drops an answer that arrives after the account changed", async () => {
    const { server, storage } = await synced();
    server.values[THEME] = "light";
    let release!: () => void;
    server.hold = new Promise((resolve) => {
      release = resolve;
    });
    vi.setSystemTime(Date.now() + REFETCH_GAP_MS);
    window.dispatchEvent(new Event("focus"));
    setPreferenceAccount(null);
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(storage.map.has(THEME)).toBe(false);
  });
});

describe("across origins", () => {
  it("brings the theme pack chosen on localhost to 127.0.0.1", async () => {
    const { server, transport } = fakeServer();
    // localhost: its own localStorage.
    configurePreferenceStorage(fakeStorage());
    await startPreferenceSync({ transport });
    setPreferenceAccount("local");
    await vi.advanceTimersByTimeAsync(0);
    writePreference("themePack", "magic");
    writePreference("theme", "light");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY_MS);
    stopPreferenceSync();

    // 127.0.0.1: a different, empty localStorage.
    configurePreferenceStorage(fakeStorage());
    await startPreferenceSync({ transport: fakeServerFor(server) });
    expect(readPreference("themePack")).toBe("magic");
    expect(readPreference("theme")).toBe("light");
  });
});

describe("firstPreferenceLoad", () => {
  /** What the promise settled to, read after the timers ran. */
  function track(promise: Promise<boolean>): { value: boolean | undefined } {
    const seen: { value: boolean | undefined } = { value: undefined };
    void promise.then((value) => {
      seen.value = value;
    });
    return seen;
  }

  it("is true once the account's copy is in", async () => {
    configurePreferenceStorage(fakeStorage({ [ACCOUNT_MARKER_KEY]: "local" }));
    const { server, transport } = fakeServer({ [THEME]: "light" });
    let release!: () => void;
    server.hold = new Promise((resolve) => {
      release = resolve;
    });
    await startPreferenceSync({ transport, account: "local" });
    const seen = track(firstPreferenceLoad());
    await vi.advanceTimersByTimeAsync(0);
    expect(seen.value).toBeUndefined();
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(seen.value).toBe(true);
    expect(readPreference("theme")).toBe("light");
    // Asked again later, the answer is the same.
    expect(await firstPreferenceLoad()).toBe(true);
  });

  it("is true through the unclaimed path: loaded first, the account named after", async () => {
    configurePreferenceStorage(fakeStorage());
    const { transport } = fakeServer({ [THEME]: "light" });
    await startPreferenceSync({ transport });
    const seen = track(firstPreferenceLoad());
    await vi.advanceTimersByTimeAsync(0);
    // The values are in, but whose they are is not known yet.
    expect(seen.value).toBeUndefined();
    setPreferenceAccount("local");
    await vi.advanceTimersByTimeAsync(0);
    expect(seen.value).toBe(true);
  });

  it("is false when the load fails", async () => {
    configurePreferenceStorage(fakeStorage({ [ACCOUNT_MARKER_KEY]: "local" }));
    const { server, transport } = fakeServer();
    server.failures.push(new Failure("retry"));
    await startPreferenceSync({ transport, account: "local" });
    const seen = track(firstPreferenceLoad());
    await vi.advanceTimersByTimeAsync(0);
    expect(seen.value).toBe(false);
  });

  it("is false when the server keeps no preferences for this page", async () => {
    configurePreferenceStorage(fakeStorage({ [ACCOUNT_MARKER_KEY]: "local" }));
    const { server, transport } = fakeServer();
    server.failures.push(new Failure("unsupported"));
    await startPreferenceSync({ transport, account: "local" });
    await vi.advanceTimersByTimeAsync(0);
    expect(await firstPreferenceLoad()).toBe(false);
  });

  it("is false with no sync running", async () => {
    expect(await firstPreferenceLoad()).toBe(false);
  });
});

/** Another page's transport onto the same fake server's values. */
function fakeServerFor(server: ReturnType<typeof fakeServer>["server"]): PreferenceTransport {
  return {
    load: async () => {
      server.loads += 1;
      return { values: { ...server.values } };
    },
    save: async (change, { keepalive }) => {
      server.saves.push({ change: copy(change), keepalive });
      Object.assign(server.values, change.set);
      for (const key of change.remove) delete server.values[key];
      return { values: { ...server.values } };
    },
    classify: () => "retry",
  };
}
