/**
 * Auto-save scheduling [A-05].
 *
 * The editor saves on its own. This module owns *when*, and nothing else: it
 * takes a `save` function and decides which of the many things a person does
 * while writing should turn into a request. Keeping it free of React and of the
 * store is what lets the timing be tested for what it is — a state machine over
 * a clock — instead of through an editor.
 *
 * Three timings, and the reasons they are the numbers they are:
 *
 * - `AUTOSAVE_IDLE_MS` (1500) is the pause after which typing counts as
 *   finished. It has to clear the gap a writer leaves mid-sentence while
 *   choosing a word — a few hundred milliseconds — or every clause becomes a
 *   file write. It also has to be short enough that the window in which work is
 *   only in the browser stays under the two seconds nobody notices. It is the
 *   same figure the hot-exit draft has always used, deliberately: a burst of
 *   typing now settles into one draft write and one real save at the same
 *   moment rather than two staggered ones.
 *
 * - `AUTOSAVE_MAX_WAIT_MS` (10000) is the ceiling. Someone typing steadily
 *   never goes idle, and without a ceiling they would never be saved either.
 *   Ten seconds bounds continuous writing to six requests a minute, which is
 *   the point of this file for [C-03]: every save is a file write, the watcher
 *   sees it, the server broadcasts it, and every other client reads it back.
 *   Debouncing without a ceiling loses work; a ceiling without debouncing costs
 *   a write per ten seconds of a document nobody is touching. Both, and a save
 *   costs what a save is worth.
 *
 * - The retry backoff (2s doubling to 30s) is for the case that makes
 *   auto-saving dangerous: a save that failed while the user was not looking.
 *   It retries forever, because the alternative is a person who was told their
 *   work is saved and whose last successful write was an hour ago. The caller
 *   is expected to show the failure the whole time; see OperationStatus in the
 *   drawer footer.
 */

export const AUTOSAVE_IDLE_MS = 1500;
export const AUTOSAVE_MAX_WAIT_MS = 10_000;
export const AUTOSAVE_RETRY_BASE_MS = 2_000;
export const AUTOSAVE_RETRY_MAX_MS = 30_000;

/**
 * Why a save is happening. Only `idle` and `ceiling` are the debounce doing its
 * job; the rest are moments where a person has already decided their work is
 * finished and expects it to be safe, so they bypass the timer entirely.
 */
export type SaveReason =
  | "idle"
  | "ceiling"
  | "blur"
  | "switch"
  | "close"
  | "hidden"
  | "manual"
  | "retry";

export interface AutosaveOptions {
  /**
   * Writes the document. Resolves when the write landed, rejects when it did
   * not. A conflict is *not* a rejection — it is a decision the user has to
   * make, so the caller reports it through `blocked` instead and no retry is
   * scheduled against it.
   */
  save: (reason: SaveReason) => Promise<void>;
  /**
   * Answers "is there anything to save?". Checked immediately before every
   * write so a flush caused by leaving a clean document costs nothing.
   */
  isDirty: () => boolean;
  /**
   * Answers "may we write at all right now?". False while another person holds
   * the soft lock [P2] or while an unresolved conflict is on screen — in both
   * cases writing in the background is the wrong answer and the user has to
   * act. Pending edits stay pending; nothing is dropped.
   */
  isBlocked?: () => boolean;
  /** Told about every failure and every recovery, for the status badge. */
  onFailure?: (error: unknown, attempt: number) => void;
  onSuccess?: () => void;
}

export interface Autosave {
  /** A keystroke happened. Starts or extends the idle timer. */
  schedule(): void;
  /** Save now, timers be damned. Resolves once the write has settled. */
  flush(reason: SaveReason): Promise<void>;
  /** Drops pending timers without saving. For a document that is going away. */
  cancel(): void;
  /** True while a write is on the wire. */
  isSaving(): boolean;
  /** Cancels everything; the controller is dead afterwards. */
  dispose(): void;
}

export function createAutosave(options: AutosaveOptions): Autosave {
  const { save, isDirty, isBlocked, onFailure, onSuccess } = options;

  let idleTimer: ReturnType<typeof setTimeout> | null = null;
  let ceilingTimer: ReturnType<typeof setTimeout> | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let inFlight: Promise<void> | null = null;
  let attempt = 0;
  let disposed = false;
  /** An edit arrived while a save was on the wire: save again when it lands. */
  let dirtyDuringSave = false;

  function clearTimer(timer: ReturnType<typeof setTimeout> | null) {
    if (timer !== null) clearTimeout(timer);
    return null;
  }

  function clearDebounce() {
    idleTimer = clearTimer(idleTimer);
    ceilingTimer = clearTimer(ceilingTimer);
  }

  function scheduleRetry() {
    if (disposed || retryTimer !== null) return;
    // Exponential, capped. The first retry is quick because most failures are a
    // server that was restarting; the cap is there so an editor left open
    // against a dead server does not poll it forever at speed.
    const delay = Math.min(
      AUTOSAVE_RETRY_MAX_MS,
      AUTOSAVE_RETRY_BASE_MS * 2 ** Math.max(0, attempt - 1),
    );
    retryTimer = setTimeout(() => {
      retryTimer = null;
      void run("retry");
    }, delay);
  }

  async function run(reason: SaveReason): Promise<void> {
    if (disposed) return;
    clearDebounce();
    if (inFlight) {
      // One write at a time. Anything that arrived meanwhile is saved by the
      // follow-up below rather than racing this one onto the same baseRev.
      dirtyDuringSave = true;
      await inFlight.catch(() => undefined);
      return;
    }
    if (!isDirty()) {
      // Nothing outstanding: a previously failed save has been overtaken by a
      // successful one, so stop retrying.
      retryTimer = clearTimer(retryTimer);
      attempt = 0;
      return;
    }
    if (reason !== "manual" && isBlocked?.()) {
      // Deliberately not a retry loop: whatever is blocking needs a person, and
      // hammering the server changes nothing. The next edit asks again — and
      // "manual" is exempt, because pressing Ctrl + S against a locked document
      // is the person deciding to contest it, which the revision check on the
      // server then adjudicates.
      return;
    }

    dirtyDuringSave = false;
    const operation = (async () => {
      await save(reason);
    })();
    inFlight = operation;
    try {
      await operation;
      attempt = 0;
      retryTimer = clearTimer(retryTimer);
      onSuccess?.();
    } catch (error) {
      attempt += 1;
      onFailure?.(error, attempt);
      scheduleRetry();
    } finally {
      if (inFlight === operation) inFlight = null;
    }
    // Typing carried on while that was in flight. The store keeps the tab dirty
    // in exactly that case, so this catches up rather than leaving the last few
    // characters waiting for the next keystroke.
    if (dirtyDuringSave && !disposed && isDirty()) schedule();
  }

  function schedule() {
    if (disposed) return;
    idleTimer = clearTimer(idleTimer);
    idleTimer = setTimeout(() => {
      idleTimer = null;
      void run("idle");
    }, AUTOSAVE_IDLE_MS);
    if (ceilingTimer === null) {
      ceilingTimer = setTimeout(() => {
        ceilingTimer = null;
        void run("ceiling");
      }, AUTOSAVE_MAX_WAIT_MS);
    }
  }

  return {
    schedule,
    flush: (reason) => run(reason),
    cancel() {
      clearDebounce();
      retryTimer = clearTimer(retryTimer);
    },
    isSaving: () => inFlight !== null,
    dispose() {
      disposed = true;
      clearDebounce();
      retryTimer = clearTimer(retryTimer);
    },
  };
}
