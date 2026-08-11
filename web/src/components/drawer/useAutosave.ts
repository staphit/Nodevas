/**
 * Wires the editor's document to the auto-save scheduler.
 *
 * The scheduler (state/autosave.ts) knows about time; this knows about people.
 * An idle timer on its own is a promise that holds right up until the moment it
 * matters — a laptop lid closing, a browser tab being switched, a window being
 * shut — so every one of those moments saves directly instead of hoping the
 * timer wins the race:
 *
 * - the field losing focus, because looking away from a document is the oldest
 *   signal there is that a person considers it finished;
 * - the page becoming hidden, which is what a lid closing, a tab switch and an
 *   app going to the background all look like from here, and the last event a
 *   browser reliably delivers;
 * - `pagehide`, the close itself, where the request has to outlive the page;
 * - the editor unmounting, which is how switching nodes and closing the drawer
 *   arrive here.
 *
 * Ctrl/⌘ + S stays, and stays meaningful: it is `flush("manual")`, the way to
 * force a write before doing something risky, and the only save that also
 * commits a staged lifecycle status. Removing it would break years of muscle
 * memory to gain nothing.
 */

import { useEffect, useMemo, useRef } from "react";

import { createAutosave, type Autosave, type SaveReason } from "../../state/autosave";

export interface AutosaveTarget {
  /** Writes the currently open document. Rejects so a retry gets scheduled. */
  save: (reason: SaveReason) => Promise<void>;
  /** Whether there is anything outstanding. Read fresh before every write. */
  dirty: boolean;
  /**
   * Why writing is not allowed right now. Another person holding the soft lock
   * [P2] is the case that matters: the editor is read-only, so there should be
   * nothing to save, and if a takeover left a buffer behind then flushing it in
   * the background is exactly the silent overwrite war the lock exists to stop.
   * The user can still force the issue with Ctrl + S, which goes through the
   * revision check and surfaces a conflict like any other.
   */
  blocked: boolean;
  /** Text being edited. Any change to it restarts the idle timer. */
  content: string;
  /** Identifies the open document; changing it abandons pending timers. */
  documentKey: string;
  onFailure?: (error: unknown, attempt: number) => void;
}

export function useAutosave(target: AutosaveTarget): Autosave {
  const latest = useRef(target);
  latest.current = target;

  // One controller for the life of the editor. Everything that varies is read
  // through the ref, so a keystroke does not rebuild the timers it just set.
  const autosave = useMemo(
    () =>
      createAutosave({
        save: (reason) => latest.current.save(reason),
        isDirty: () => latest.current.dirty,
        isBlocked: () => latest.current.blocked,
        onFailure: (error, attempt) => latest.current.onFailure?.(error, attempt),
      }),
    [],
  );

  // A change to the text is the only thing that starts the clock. Mounting is
  // not: opening a document that a previous session left dirty should show the
  // draft banner, not quietly write somebody's abandoned draft to disk.
  const started = useRef(false);
  useEffect(() => {
    if (!started.current) {
      started.current = true;
      return;
    }
    if (target.dirty) autosave.schedule();
  }, [target.content, target.dirty, autosave]);

  // Switching between the main document and a subpage. The switch itself saves
  // the outgoing one (useNodePages.selectPage), so all that is left here is to
  // drop timers that would otherwise fire against the document that arrived.
  useEffect(() => {
    autosave.cancel();
  }, [target.documentKey, autosave]);

  useEffect(() => {
    const onHidden = () => {
      if (document.visibilityState === "hidden") void autosave.flush("hidden");
    };
    const onPageHide = () => void autosave.flush("close");
    document.addEventListener("visibilitychange", onHidden);
    window.addEventListener("pagehide", onPageHide);
    return () => {
      document.removeEventListener("visibilitychange", onHidden);
      window.removeEventListener("pagehide", onPageHide);
    };
  }, [autosave]);

  // Unmount is a node switch, a drawer close, or the popout being torn down.
  // The flush is not awaited — nothing may block a component going away — but
  // it starts the request before `dispose` stops the timers, the store queues it
  // per document, and the slice writes a draft for whatever it could not land,
  // so the work is safe either way. Flush and dispose share one effect because
  // cleanups run in declaration order: a separate dispose declared earlier would
  // silence the flush that has to come first.
  useEffect(
    () => () => {
      void autosave.flush("switch");
      autosave.dispose();
    },
    [autosave],
  );

  return autosave;
}
