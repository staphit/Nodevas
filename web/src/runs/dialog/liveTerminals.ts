import { useCallback, useId, useLayoutEffect, useRef, useSyncExternalStore } from "react";

// One live xterm per run per page (§3.1). Multiple mounts of the same run
// (e.g. SimpleAgentBox + StoryRoom, or an inline dialog + "Open large") share
// this registry so only one opens a WebSocket and resizes the PTY.

interface Claimant {
  mountId: string;
  takeOver: boolean;
  element: HTMLElement | null;
}

const claimsByRun = new Map<string, Claimant[]>();
const listeners = new Set<() => void>();
let version = 0;

function notify(): void {
  version += 1;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function ownerEntry(runId: string): Claimant | null {
  const list = claimsByRun.get(runId);
  if (!list || list.length === 0) return null;
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].takeOver) return list[i];
  }
  return list[0];
}

/** Returns the current owning mountId for `runId`, or null when unowned. */
export function getTerminalOwner(runId: string): string | null {
  return ownerEntry(runId)?.mountId ?? null;
}

/** Registers `mountId` as a candidate for `runId`; returns true when it owns the terminal. */
export function claimTerminal(
  runId: string,
  mountId: string,
  options?: { takeOver?: boolean; element?: HTMLElement | null },
): boolean {
  const takeOver = Boolean(options?.takeOver);
  let list = claimsByRun.get(runId);
  if (!list) {
    list = [];
    claimsByRun.set(runId, list);
  }
  const existing = list.find((item) => item.mountId === mountId);
  if (existing) {
    existing.takeOver = takeOver;
    if (options && "element" in options) existing.element = options.element ?? null;
  } else {
    list.push({ mountId, takeOver, element: options?.element ?? null });
  }
  notify();
  return getTerminalOwner(runId) === mountId;
}

/** Updates the DOM element attached to an existing claimant so others can jump to it. */
export function setTerminalClaimElement(runId: string, mountId: string, element: HTMLElement | null): void {
  const list = claimsByRun.get(runId);
  if (!list) return;
  const entry = list.find((item) => item.mountId === mountId);
  if (entry) entry.element = element;
}

/** Releases `mountId`'s claim on `runId`, transferring ownership to the next claimant. */
export function releaseTerminal(runId: string, mountId: string): void {
  const list = claimsByRun.get(runId);
  if (!list) return;
  const next = list.filter((item) => item.mountId !== mountId);
  if (next.length === 0) claimsByRun.delete(runId);
  else claimsByRun.set(runId, next);
  notify();
}

/** Scrolls the current owning mount into view and focuses it if possible. */
export function focusTerminalOwner(runId: string): void {
  const el = ownerEntry(runId)?.element;
  if (!el) return;
  el.scrollIntoView?.({ behavior: "smooth", block: "center" });
  el.focus?.();
}

/** Tests only: clears every active terminal claim. */
export function resetLiveTerminals(): void {
  claimsByRun.clear();
  notify();
}

/**
 * Claims single-xterm ownership for `runId` while `active` is true.
 * Pass `takeOver: true` for an "Open large" modal so it temporarily takes over
 * ownership and returns it to the inline mount on close.
 */
export function useLiveTerminalClaim(
  runId: string,
  active: boolean,
  options?: { takeOver?: boolean },
): {
  isOwner: boolean;
  ref: (el: HTMLElement | null) => void;
  focusOwner: () => void;
} {
  const mountId = useId();
  const takeOver = Boolean(options?.takeOver);
  const elementRef = useRef<HTMLElement | null>(null);

  useSyncExternalStore(
    subscribe,
    () => `${version}:${getTerminalOwner(runId) ?? ""}`,
    () => `0:`,
  );

  useLayoutEffect(() => {
    if (!active || !runId) return;
    claimTerminal(runId, mountId, { takeOver, element: elementRef.current });
    return () => releaseTerminal(runId, mountId);
  }, [runId, active, mountId, takeOver]);

  const ref = useCallback(
    (el: HTMLElement | null) => {
      elementRef.current = el;
      if (active && runId) setTerminalClaimElement(runId, mountId, el);
    },
    [runId, active, mountId],
  );

  const focusOwner = useCallback(() => {
    if (runId) focusTerminalOwner(runId);
  }, [runId]);

  const isOwner = active && Boolean(runId) && getTerminalOwner(runId) === mountId;
  return { isOwner, ref, focusOwner };
}
