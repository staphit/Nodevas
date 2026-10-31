/**
 * Keeps the explorer's project list current when nothing told this window.
 *
 * Changes made through the hub reach the list two ways: the request's own
 * follow-up (changeProjects) and the `project-changed` broadcast every other
 * window gets. A project written straight into the workspace — by an agent, a
 * script, a sync tool, another hub process — has neither, and used to show up
 * only after a project switch or a reload. So the list is also re-read when
 * the reader comes back to the page, and now and then while it is on screen.
 *
 * Every read is a coalesced, ETag-revalidated GET of a small list (304 when
 * nothing changed), never while the page is hidden, and a failure is left for
 * the next trigger: this is a background check and must not raise errors in
 * the reader's face.
 */

/** How often a visible page re-reads the project list. */
export const PROJECT_LIST_POLL_MS = 30_000;

export function watchProjectList(
  refresh: () => Promise<void>,
  pollMs: number = PROJECT_LIST_POLL_MS,
): () => void {
  const run = () => {
    void refresh().catch(() => {
      // Offline or refused: the connection indicator already says so.
    });
  };
  const onVisibility = () => {
    if (!document.hidden) run();
  };
  const onFocus = () => run();
  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("focus", onFocus);
  const timer = window.setInterval(() => {
    if (!document.hidden) run();
  }, pollMs);
  return () => {
    document.removeEventListener("visibilitychange", onVisibility);
    window.removeEventListener("focus", onFocus);
    window.clearInterval(timer);
  };
}
