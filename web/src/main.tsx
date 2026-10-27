import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ViewErrorBoundary } from "./components/ViewErrorBoundary";
import { parseLastReload, preloadChunkKey, shouldReloadForPreloadError } from "./preloadReload";
import { resetPreferences, VIEW_SELECTION_PREFERENCES } from "./preferences";
import "./app.css";

// A chunk that will not load (vite:preloadError) usually means the server was
// updated after this page opened and the old build's files are gone: one
// reload fetches the current build. A lazy view retries its import once
// (lazyWithRetry), so a chunk's first failure is let through to that retry,
// and only the same chunk failing again soon after reloads; at most once in
// PRELOAD_RELOAD_COOLDOWN_MS, by the time stored here, so a chunk missing from
// the new build too reaches the error boundary instead of reloading forever
// (shouldReloadForPreloadError).
const PRELOAD_RELOAD_KEY = "nodevas-preload-reload";
const chunkFailures = new Map<string, number>();

window.addEventListener("vite:preloadError", (event) => {
  const now = Date.now();
  const chunk = preloadChunkKey((event as Event & { payload?: unknown }).payload);
  const previousFailureAt = chunkFailures.get(chunk);
  chunkFailures.set(chunk, now);
  try {
    const lastReloadAt = parseLastReload(sessionStorage.getItem(PRELOAD_RELOAD_KEY));
    if (!shouldReloadForPreloadError(previousFailureAt, now, lastReloadAt)) return;
    sessionStorage.setItem(PRELOAD_RELOAD_KEY, String(now));
  } catch {
    // No session storage (blocked): never reload on our own, so no loop.
    return;
  }
  event.preventDefault();
  window.location.reload();
});

// The root boundary's way out of a crash tied to the view the app opens on:
// forget which view was open and start again on the board.
function resetView() {
  resetPreferences(VIEW_SELECTION_PREFERENCES);
  window.location.reload();
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ViewErrorBoundary onResetView={resetView}>
      <App />
    </ViewErrorBoundary>
  </React.StrictMode>,
);
