import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PROJECT_LIST_POLL_MS, watchProjectList } from "./projectListSync";

// A project can appear without any request from this window and without a
// broadcast: an agent, a script or a sync tool writes the folder straight
// into the workspace. The tree has to find it on its own; before this it
// showed up only after a project switch or a page reload.
describe("watchProjectList", () => {
  let hidden = false;
  beforeEach(() => {
    vi.useFakeTimers();
    hidden = false;
    Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
  });
  afterEach(() => {
    vi.useRealTimers();
    delete (document as { hidden?: boolean }).hidden;
  });

  function setHidden(next: boolean) {
    hidden = next;
    document.dispatchEvent(new Event("visibilitychange"));
  }

  it("reloads the list when the page is shown again", () => {
    const refresh = vi.fn(() => Promise.resolve());
    const stop = watchProjectList(refresh);
    setHidden(true);
    expect(refresh).not.toHaveBeenCalled();
    setHidden(false);
    expect(refresh).toHaveBeenCalledTimes(1);
    stop();
  });

  it("reloads the list when the window gets focus back", () => {
    const refresh = vi.fn(() => Promise.resolve());
    const stop = watchProjectList(refresh);
    window.dispatchEvent(new Event("focus"));
    expect(refresh).toHaveBeenCalledTimes(1);
    stop();
  });

  it("checks again now and then while the page is visible, never while hidden", () => {
    const refresh = vi.fn(() => Promise.resolve());
    const stop = watchProjectList(refresh);
    vi.advanceTimersByTime(PROJECT_LIST_POLL_MS);
    expect(refresh).toHaveBeenCalledTimes(1);
    hidden = true;
    vi.advanceTimersByTime(PROJECT_LIST_POLL_MS * 3);
    expect(refresh).toHaveBeenCalledTimes(1);
    stop();
  });

  it("stops listening once stopped", () => {
    const refresh = vi.fn(() => Promise.resolve());
    const stop = watchProjectList(refresh);
    stop();
    window.dispatchEvent(new Event("focus"));
    setHidden(true);
    setHidden(false);
    vi.advanceTimersByTime(PROJECT_LIST_POLL_MS * 2);
    expect(refresh).not.toHaveBeenCalled();
  });

  it("swallows a failed reload: the next trigger simply tries again", async () => {
    const refresh = vi.fn(() => Promise.reject(new Error("offline")));
    const stop = watchProjectList(refresh);
    window.dispatchEvent(new Event("focus"));
    await Promise.resolve();
    window.dispatchEvent(new Event("focus"));
    expect(refresh).toHaveBeenCalledTimes(2);
    stop();
  });
});
