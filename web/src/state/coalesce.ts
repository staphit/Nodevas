/**
 * Read coalescing [A-05].
 *
 * One logical change fans out into several triggers: the mutation refreshes
 * eagerly, the server broadcasts the same change back over the WebSocket, and
 * the file watcher sees the write land on disk a moment later. Each of those
 * used to start its own GET, so creating one node fetched `/api/graph`,
 * `/api/state` and `/api/trash` two or three times inside about fifty
 * milliseconds — three round trips where one would do.
 *
 * This module turns "please refresh X" into a request that may be shared:
 *
 * - triggers for the same resource inside a short window collapse into one
 *   fetch, and every caller gets that fetch's result;
 * - a trigger that arrives while a fetch is already in flight is *never*
 *   answered by it, because that fetch left the server before the change did.
 *   It waits and causes exactly one follow-up fetch afterwards, however many
 *   triggers arrived in the meantime.
 *
 * That second rule is the whole reason this is not a cache. Nothing here ever
 * hands back a stored response: every fetch that completes is a fresh read of
 * the server at that moment, and any trigger it could not possibly cover still
 * gets a read of its own.
 */

/**
 * How long triggers are gathered before the fetch starts.
 *
 * Two frames. The burst that motivated this — a mutation's own refresh plus the
 * server's echo of it — arrives inside a few milliseconds, so a window this
 * size catches it comfortably. Going wider would buy almost nothing and would
 * be felt directly: this delay is also how long a collaborator's edit takes to
 * appear, and beyond roughly a tenth of a second that stops reading as instant.
 */
export const COALESCE_WINDOW_MS = 30;

type Deferred = {
  promise: Promise<void>;
  resolve: () => void;
  reject: (reason: unknown) => void;
};

function defer(): Deferred {
  let resolve!: () => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** The triggers gathered so far that no fetch has started for yet. */
type Batch = {
  waiters: Deferred;
  /** The loader of whoever opened the batch; every trigger for a key loads the
   * same resource, so which caller's closure runs does not matter. */
  load: () => Promise<void>;
};

type Entry = {
  batch: Batch | null;
  timer: ReturnType<typeof setTimeout> | null;
  /** The batch's window has elapsed; it is only waiting for the fetch in
   * flight to finish before it starts. */
  due: boolean;
  running: boolean;
};

const entries = new Map<string, Entry>();

/**
 * Requests a refresh of `key`, joining a request already being gathered for it.
 *
 * The returned promise settles when a fetch that reflects this trigger has
 * completed, so callers keep the "await it and the store is up to date"
 * contract they had when each call was its own request.
 */
export function coalesceRequest(
  key: string,
  load: () => Promise<void>,
  windowMs: number = COALESCE_WINDOW_MS,
): Promise<void> {
  let entry = entries.get(key);
  if (!entry) {
    entry = { batch: null, timer: null, due: false, running: false };
    entries.set(key, entry);
  }
  const current = entry;
  if (!current.batch) {
    current.batch = { waiters: defer(), load };
    current.due = false;
    current.timer = setTimeout(() => {
      current.timer = null;
      current.due = true;
      // A fetch already on the wire cannot answer for this batch: it was sent
      // before these triggers existed. Start when it is done, not now.
      if (!current.running) void execute(key, current);
    }, windowMs);
  }
  return current.batch.waiters.promise;
}

async function execute(key: string, entry: Entry): Promise<void> {
  const batch = entry.batch;
  if (!batch) return;
  entry.batch = null;
  entry.due = false;
  entry.running = true;
  try {
    await batch.load();
    batch.waiters.resolve();
  } catch (error) {
    // A failed fetch must not leave the key looking busy forever, or every
    // later refresh would silently do nothing. The batch is already cleared;
    // the next trigger opens a new one.
    batch.waiters.reject(error);
  } finally {
    entry.running = false;
    if (entry.due && entry.batch) {
      void execute(key, entry);
    } else if (!entry.batch && entries.get(key) === entry) {
      // Only if this is still the live entry for the key. A reset can drop it
      // from the map while its fetch is in flight, and a later trigger will
      // then have put a *different* entry there; deleting that one would throw
      // away a batch that has a timer pending and callers waiting on it.
      entries.delete(key);
    }
  }
}

/**
 * Drops every gathered trigger. For tests, which must not inherit timers.
 *
 * Callers already waiting are resolved rather than left hanging. They are owed
 * an answer either way, and an unsettled promise here is not a dangling promise
 * there: `writeGraph` awaits a refresh from inside the operation it puts on
 * `queues.graphSave`, so one abandoned waiter wedges the app's only write lane
 * for the life of the process — every later graph write queues behind a promise
 * that can never settle. Rejecting would be the more literal answer, but a
 * reset is a teardown, not a failure, and it would turn every one of those into
 * an unhandled rejection.
 */
export function resetCoalescing(): void {
  for (const entry of entries.values()) {
    if (entry.timer !== null) clearTimeout(entry.timer);
    entry.batch?.waiters.resolve();
    entry.batch = null;
  }
  entries.clear();
}
