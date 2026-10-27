import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ViewErrorBoundary } from "./components/ViewErrorBoundary";
import { CHUNK_RETRY_DELAY_MS } from "./lazyWithRetry";
import { resetPreferences, VIEW_SELECTION_PREFERENCES } from "./preferences";
import "./app.css";

// A chunk that will not load (vite:preloadError) usually means the server was
// updated after this page opened and the old build's files are gone: one
// reload fetches the current build. A lazy view retries its import once
// (lazyWithRetry), so the first failure is let through to that retry, and
// only a second one soon after, the retry failing too, reloads. The flag keeps
// that to one reload; it is cleared once a load has stayed up for a while, so
// a later update gets its own reload, and a chunk missing from the new build
// too reaches the error boundary instead of reloading forever.
const PRELOAD_RELOAD_KEY = "nodevas-preload-reload";
const PRELOAD_SETTLED_MS = 10_000;
const PRELOAD_RETRY_WINDOW_MS = CHUNK_RETRY_DELAY_MS + 5_000;
let lastPreloadError = 0;

function sessionFlag(action: "get" | "set" | "clear"): boolean {
  try {
    if (action === "get") return sessionStorage.getItem(PRELOAD_RELOAD_KEY) !== null;
    if (action === "set") sessionStorage.setItem(PRELOAD_RELOAD_KEY, String(Date.now()));
    else sessionStorage.removeItem(PRELOAD_RELOAD_KEY);
    return true;
  } catch {
    // No session storage (blocked): never reload on our own, so no loop.
    return action === "get";
  }
}

window.addEventListener("vite:preloadError", (event) => {
  const now = Date.now();
  const retryFailed = now - lastPreloadError < PRELOAD_RETRY_WINDOW_MS;
  lastPreloadError = now;
  if (!retryFailed) return;
  if (sessionFlag("get") || !sessionFlag("set")) return;
  event.preventDefault();
  window.location.reload();
});
window.setTimeout(() => sessionFlag("clear"), PRELOAD_SETTLED_MS);

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
