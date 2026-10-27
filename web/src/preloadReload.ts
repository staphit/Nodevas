import { CHUNK_RETRY_DELAY_MS } from "./lazyWithRetry";

/** A second failure of the same chunk this soon after the first is its retry
 * (lazyWithRetry) failing too. */
export const PRELOAD_RETRY_WINDOW_MS = CHUNK_RETRY_DELAY_MS + 5_000;

/** At most one reload for a missing chunk in this long: a chunk missing from
 * the new build too reaches the error boundary instead of reloading forever. */
export const PRELOAD_RELOAD_COOLDOWN_MS = 5 * 60_000;

const CHUNK_URL = /(?:https?:\/\/[^\s'"]+|\/[^\s'"]+\.(?:m?js|css))(?:\?[^\s'"]*)?/;

/**
 * Which chunk a vite:preloadError is about: the URL in the error's message
 * ("Failed to fetch dynamically imported module: <url>", "Unable to preload
 * CSS for <url>"), or the whole message when it names none (Safari's).
 */
export function preloadChunkKey(payload: unknown): string {
  const message = payload instanceof Error ? payload.message : String(payload ?? "");
  return message.match(CHUNK_URL)?.[0] ?? message;
}

/**
 * Whether a vite:preloadError should reload the page.
 *
 * A chunk that will not load usually means the server was updated after this
 * page opened and the old build's files are gone; one reload fetches the
 * current build. The first failure of a chunk is let through to its retry;
 * only the same chunk failing again within the retry window reloads, so two
 * different chunks failing once each do not. And only when the last such
 * reload (lastReloadAt, from session storage; null if none) is at least
 * PRELOAD_RELOAD_COOLDOWN_MS ago. A clock that went back counts as recent.
 *
 * previousFailureAt is when this same chunk last failed, if it did.
 */
export function shouldReloadForPreloadError(
  previousFailureAt: number | undefined,
  now: number,
  lastReloadAt: number | null,
): boolean {
  if (previousFailureAt === undefined || now - previousFailureAt >= PRELOAD_RETRY_WINDOW_MS) return false;
  return lastReloadAt === null || now - lastReloadAt >= PRELOAD_RELOAD_COOLDOWN_MS;
}

/** The stored time of the last reload, or null when there is none (or what is
 * stored is not a time: the reload that follows writes a real one). */
export function parseLastReload(stored: string | null): number | null {
  if (stored === null || stored.trim() === "") return null;
  const at = Number(stored);
  return Number.isFinite(at) ? at : null;
}
