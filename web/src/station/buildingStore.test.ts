import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type BuildingResponse } from "../api";
import { COALESCE_MS, useBuilding } from "./buildingStore";

const EMPTY: BuildingResponse = { projects: [], truncated: false, total: 0 };

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
}

describe("building store", () => {
  let getBuilding: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    setVisibility("visible");
    useBuilding.getState().reset();
    getBuilding = vi.spyOn(api, "getBuilding").mockResolvedValue(EMPTY);
  });

  afterEach(() => {
    useBuilding.getState().reset();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("loads when Station starts watching, and keeps the answer", async () => {
    useBuilding.getState().setWatching(true);
    await vi.waitFor(() => expect(useBuilding.getState().loaded).toBe(true));
    expect(getBuilding).toHaveBeenCalledTimes(1);
    expect(useBuilding.getState().data).toEqual(EMPTY);
  });

  it("coalesces a burst of pings into one request", async () => {
    useBuilding.getState().setWatching(true);
    await vi.waitFor(() => expect(useBuilding.getState().loaded).toBe(true));
    getBuilding.mockClear();
    for (let i = 0; i < 2; i++) {
      useBuilding.getState().onChanged();
      vi.advanceTimersByTime(COALESCE_MS / 3);
    }
    expect(getBuilding).not.toHaveBeenCalled();
    vi.advanceTimersByTime(COALESCE_MS);
    expect(getBuilding).toHaveBeenCalledTimes(1);
  });

  it("still refetches while pings never stop (several busy projects)", async () => {
    useBuilding.getState().setWatching(true);
    await vi.waitFor(() => expect(useBuilding.getState().loaded).toBe(true));
    getBuilding.mockClear();
    for (let i = 0; i < 12; i++) {
      useBuilding.getState().onChanged();
      vi.advanceTimersByTime(COALESCE_MS / 3);
      await Promise.resolve();
    }
    expect(getBuilding.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(getBuilding.mock.calls.length).toBeLessThanOrEqual(4);
  });

  it("asks once at a time, and once more after a load that was asked for meanwhile", async () => {
    let finish: (value: BuildingResponse) => void = () => undefined;
    getBuilding.mockImplementationOnce(() => new Promise<BuildingResponse>((resolve) => (finish = resolve)));
    useBuilding.setState({ watching: true });
    const first = useBuilding.getState().load();
    void useBuilding.getState().load();
    void useBuilding.getState().load();
    expect(getBuilding).toHaveBeenCalledTimes(1);
    finish(EMPTY);
    await first;
    await vi.waitFor(() => expect(getBuilding).toHaveBeenCalledTimes(2));
  });

  it("ignores pings while Station is not on screen, or the tab is hidden", () => {
    useBuilding.getState().onChanged();
    vi.advanceTimersByTime(COALESCE_MS * 2);
    expect(getBuilding).not.toHaveBeenCalled();

    useBuilding.setState({ watching: true });
    setVisibility("hidden");
    useBuilding.getState().onChanged();
    vi.advanceTimersByTime(COALESCE_MS * 2);
    expect(getBuilding).not.toHaveBeenCalled();

    // Shown again: what changed meanwhile is fetched.
    setVisibility("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(getBuilding).toHaveBeenCalledTimes(1);
  });

  it("keeps drawing the current office when the hub has no building", async () => {
    getBuilding.mockRejectedValueOnce(new Error("404"));
    useBuilding.getState().setWatching(true);
    await vi.waitFor(() => expect(useBuilding.getState().loaded).toBe(true));
    expect(useBuilding.getState().data).toBeNull();
  });
});
