import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  AUTOSAVE_IDLE_MS,
  AUTOSAVE_MAX_WAIT_MS,
  AUTOSAVE_RETRY_BASE_MS,
  createAutosave,
  type SaveReason,
} from "./autosave";

/**
 * A stand-in for the store's save. It reports what it was asked to do and stays
 * dirty until it succeeds, which is what the real slice does too — a failed
 * save leaves the tab dirty so the work is still there to retry.
 */
function harness(options: { failTimes?: number; blocked?: boolean } = {}) {
  const reasons: SaveReason[] = [];
  let remainingFailures = options.failTimes ?? 0;
  const state = { dirty: true, blocked: options.blocked ?? false };
  const autosave = createAutosave({
    save: async (reason) => {
      reasons.push(reason);
      if (remainingFailures > 0) {
        remainingFailures -= 1;
        throw new Error("磁碟已滿");
      }
      state.dirty = false;
    },
    isDirty: () => state.dirty,
    isBlocked: () => state.blocked,
  });
  return { autosave, reasons, state };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("auto-save scheduling", () => {
  // The whole point of the debounce. A keystroke is not a save; a pause is.
  it("turns a burst of typing followed by a pause into exactly one save", async () => {
    const { autosave, reasons } = harness();

    for (let keystroke = 0; keystroke < 40; keystroke += 1) {
      autosave.schedule();
      await vi.advanceTimersByTimeAsync(50);
    }
    expect(reasons).toEqual([]);

    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS);

    expect(reasons).toEqual(["idle"]);
  });

  // Nothing at all should happen until the writer actually stops. A save per
  // character is the failure mode that makes auto-save unusable on a slow disk
  // and expensive for every other client watching the file.
  it("does not save while the keystrokes keep coming", async () => {
    const { autosave, reasons } = harness();

    // Steady typing for well past the idle window, but never a gap that
    // reaches it.
    for (let keystroke = 0; keystroke < 30; keystroke += 1) {
      autosave.schedule();
      await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS - 100);
    }

    // The ceiling has fired a few times — that is its job — but nothing close
    // to one write per character.
    expect(reasons.length).toBeLessThanOrEqual(
      Math.ceil((30 * (AUTOSAVE_IDLE_MS - 100)) / AUTOSAVE_MAX_WAIT_MS) + 1,
    );
    expect(reasons.every((reason) => reason === "ceiling")).toBe(true);
  });

  // Somebody writing steadily never goes idle. Without the ceiling they would
  // never be saved either, and the debounce would have quietly turned into "we
  // save when you stop", which is not what anyone was promised.
  it("saves on the ceiling when the writer never pauses", async () => {
    const { autosave, reasons, state } = harness();

    // Keep it dirty and keep typing, so only the ceiling can fire.
    const typing = setInterval(() => {
      state.dirty = true;
      autosave.schedule();
    }, AUTOSAVE_IDLE_MS - 200);
    await vi.advanceTimersByTimeAsync(AUTOSAVE_MAX_WAIT_MS * 2);
    clearInterval(typing);

    expect(reasons).toContain("ceiling");
  });

  // Looking away from a field is the oldest "I am done with this" signal there
  // is, and it is one the idle timer must not be allowed to lose to a closing
  // laptop lid.
  it("saves immediately when the field loses focus, without waiting for the timer", async () => {
    const { autosave, reasons } = harness();
    autosave.schedule();

    await autosave.flush("blur");

    expect(reasons).toEqual(["blur"]);

    // And the timer it pre-empted does not fire a second, pointless save.
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS * 2);
    expect(reasons).toEqual(["blur"]);
  });

  // Leaving a document — switching nodes, closing the drawer — has to write the
  // one being left before the next one loads, or the last thing typed is gone.
  it("saves on the way out even when nothing else has fired", async () => {
    const { autosave, reasons } = harness();
    autosave.schedule();

    await autosave.flush("switch");

    expect(reasons).toEqual(["switch"]);
  });

  it("costs nothing when there is nothing outstanding", async () => {
    const { autosave, reasons, state } = harness();
    state.dirty = false;

    await autosave.flush("hidden");
    await autosave.flush("blur");

    expect(reasons).toEqual([]);
  });

  // "We save automatically" plus a save that failed in silence is how a day's
  // work disappears. It has to keep trying.
  it("retries a failed save on a backoff until it lands", async () => {
    const { autosave, reasons } = harness({ failTimes: 2 });

    autosave.schedule();
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS);
    expect(reasons).toEqual(["idle"]);

    await vi.advanceTimersByTimeAsync(AUTOSAVE_RETRY_BASE_MS);
    expect(reasons).toEqual(["idle", "retry"]);

    // Doubling: the second retry is not due yet at the first interval.
    await vi.advanceTimersByTimeAsync(AUTOSAVE_RETRY_BASE_MS);
    expect(reasons).toEqual(["idle", "retry"]);
    await vi.advanceTimersByTimeAsync(AUTOSAVE_RETRY_BASE_MS);
    expect(reasons).toEqual(["idle", "retry", "retry"]);

    // Landed. Nothing keeps polling afterwards.
    await vi.advanceTimersByTimeAsync(AUTOSAVE_RETRY_BASE_MS * 20);
    expect(reasons).toEqual(["idle", "retry", "retry"]);
  });

  it("reports every failure so the editor can keep it on screen", async () => {
    const onFailure = vi.fn();
    const autosave = createAutosave({
      save: async () => {
        throw new Error("磁碟已滿");
      },
      isDirty: () => true,
      onFailure,
    });

    autosave.schedule();
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS);

    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(onFailure.mock.calls[0][0]).toBeInstanceOf(Error);
    expect(onFailure.mock.calls[0][1]).toBe(1);
    autosave.dispose();
  });

  // Soft locks [P2]: another person has this document open. Writing in the
  // background while they do is the silent overwrite war the lock exists to
  // prevent, and no amount of retrying resolves it — a person has to.
  it("does not write in the background while something is blocking the document", async () => {
    const { autosave, reasons } = harness({ blocked: true });

    autosave.schedule();
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS + AUTOSAVE_RETRY_BASE_MS * 4);

    expect(reasons).toEqual([]);
  });

  // The exception, and the reason Ctrl + S still earns its keybinding: pressing
  // it against a locked document is a person deciding to contest the lock. The
  // revision check on the server is what adjudicates, not this timer.
  it("still lets an explicit save through a block", async () => {
    const { autosave, reasons } = harness({ blocked: true });

    await autosave.flush("manual");

    expect(reasons).toEqual(["manual"]);
  });

  // Two writes on the same baseRev would make the second a conflict against the
  // first, so a save on the wire is never joined by another.
  it("never runs two writes at once, and catches up on what arrived meanwhile", async () => {
    let inFlight = 0;
    let peak = 0;
    const reasons: SaveReason[] = [];
    // Modelled on the slice: a save writes the content it started with, and
    // only clears the dirty flag if nothing arrived while it was on the wire.
    const state = { content: "one", written: "" };
    const autosave = createAutosave({
      save: async (reason) => {
        reasons.push(reason);
        const snapshot = state.content;
        inFlight += 1;
        peak = Math.max(peak, inFlight);
        await new Promise((resolve) => setTimeout(resolve, 100));
        inFlight -= 1;
        state.written = snapshot;
      },
      isDirty: () => state.content !== state.written,
    });

    autosave.schedule();
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS);
    // Typing carried on while the request was open.
    state.content = "one two";
    void autosave.flush("blur");
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS * 2);

    expect(peak).toBe(1);
    expect(state.written).toBe("one two");
    // The edit that arrived mid-flight was not dropped: it caused a follow-up.
    expect(reasons.length).toBeGreaterThan(1);
    autosave.dispose();
  });

  it("stops scheduling once the editor is gone", async () => {
    const { autosave, reasons } = harness();

    autosave.schedule();
    autosave.dispose();
    await vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS * 4);

    expect(reasons).toEqual([]);
  });
});
