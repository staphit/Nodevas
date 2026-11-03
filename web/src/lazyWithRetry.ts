import { lazy, type ComponentType, type LazyExoticComponent } from "react";

/** How long a failed chunk import waits before its one retry. */
export const CHUNK_RETRY_DELAY_MS = 1000;

/**
 * Runs a dynamic import, and once more after a short wait if it fails.
 *
 * A lazy view's chunk can fail to load for a moment: the server restarting
 * under the desktop app, a laptop waking up, a dropped connection. React
 * remembers a rejected lazy() for good, so without a retry that one blip
 * leaves the view broken until a reload. A chunk that is really gone (the
 * server was updated) fails twice, and ViewErrorBoundary offers the reload.
 */
export function retryImport<T>(load: () => Promise<T>, delayMs = CHUNK_RETRY_DELAY_MS): Promise<T> {
  return load().catch(
    () =>
      new Promise<T>((resolve, reject) => {
        setTimeout(() => load().then(resolve, reject), delayMs);
      }),
  );
}

/** lazy() whose import is retried once (retryImport). The constraint is
 * lazy()'s own. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function lazyWithRetry<T extends ComponentType<any>>(
  load: () => Promise<{ default: T }>,
  delayMs = CHUNK_RETRY_DELAY_MS,
): LazyExoticComponent<T> {
  return lazy(() => retryImport(load, delayMs));
}
