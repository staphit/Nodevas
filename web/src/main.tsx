import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ViewErrorBoundary } from "./components/ViewErrorBoundary";
import {
  createPreloadFailureTracker,
  parseLastReload,
  reloadWhenServerAnswers,
  serverAnswers,
  shouldReloadForPreloadError,
} from "./preloadReload";
import { resetPreferences, VIEW_SELECTION_PREFERENCES } from "./preferences";
import { installTruncationTitles } from "./truncationTitle";
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
const previousPreloadFailure = createPreloadFailureTracker();

window.addEventListener("vite:preloadError", (event) => {
  const now = Date.now();
  const previousFailureAt = previousPreloadFailure((event as Event & { payload?: unknown }).payload, now);
  try {
    const lastReloadAt = parseLastReload(sessionStorage.getItem(PRELOAD_RELOAD_KEY));
    if (!shouldReloadForPreloadError(previousFailureAt, now, lastReloadAt)) return;
    sessionStorage.setItem(PRELOAD_RELOAD_KEY, String(now));
  } catch {
    // No session storage (blocked): never reload on our own, so no loop.
    return;
  }
  // Not prevented: the failure reaches the view's error boundary, which shows
  // while the reload waits for a server that may be down (a reload into a dead
  // server would replace the app with the browser's error page).
  reloadWhenServerAnswers(serverAnswers, () => window.location.reload());
});

// The root boundary's way out of a crash tied to the view the app opens on:
// forget which view was open and start again on the board.
function resetView() {
  resetPreferences(VIEW_SELECTION_PREFERENCES);
  window.location.reload();
}

// Text cut with an ellipsis shows its whole value on hover, app-wide.
installTruncationTitles(document);

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ViewErrorBoundary onResetView={resetView}>
      <App />
    </ViewErrorBoundary>
  </React.StrictMode>,
);
