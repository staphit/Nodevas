import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { makeAutopilot, makeRun, makeTask } from "../game/testFixtures";
import { ACTIVE_RUNS_DELAY_MS, useRuns } from "./runStore";

beforeEach(() => {
  useRuns.getState().reset();
  useAutopilot.getState().reset();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("active runs", () => {
  it("are fetched on request and refetched once per burst of runs-changed", async () => {
    const list = vi
      .spyOn(api, "listActiveRuns")
      .mockResolvedValue({ runs: [makeRun({ nodeId: "api" })] } as never);
    vi.spyOn(api, "listRuns").mockResolvedValue({ runs: [] } as never);
    await useRuns.getState().loadActiveRuns();
    expect(useRuns.getState().activeRuns).toHaveLength(1);
    expect(list).toHaveBeenCalledTimes(1);

    useRuns.getState().onRunsChanged("api");
    useRuns.getState().onRunsChanged("login");
    useRuns.getState().onRunsChanged("api");
    await vi.advanceTimersByTimeAsync(ACTIVE_RUNS_DELAY_MS + 10);
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("are not fetched by runs-changed until something asked for them", async () => {
    const list = vi.spyOn(api, "listActiveRuns").mockResolvedValue({ runs: [] } as never);
    useRuns.getState().onRunsChanged("api");
    await vi.advanceTimersByTimeAsync(ACTIVE_RUNS_DELAY_MS + 10);
    expect(list).not.toHaveBeenCalled();
  });

  it("are dropped by a project reset, answer in flight included", async () => {
    let resolve: (value: unknown) => void = () => undefined;
    vi.spyOn(api, "listActiveRuns").mockReturnValue(new Promise((r) => (resolve = r)) as never);
    const load = useRuns.getState().loadActiveRuns();
    useRuns.getState().reset();
    resolve({ runs: [makeRun()] });
    await load;
    expect(useRuns.getState().activeRuns).toEqual([]);
    expect(useRuns.getState().activeRunsLoaded).toBe(false);
  });
});

describe("live autopilots", () => {
  it("are held from the project-wide list, with their task marks", async () => {
    vi.spyOn(api, "listActiveAutopilots").mockResolvedValue([
      makeAutopilot([makeTask({ nodeId: "api", state: "running" })], { id: "ap7", goalNodeId: "goal" }),
    ]);
    await useAutopilot.getState().loadActive();
    const state = useAutopilot.getState();
    expect(state.activeLoaded).toBe(true);
    expect(Object.keys(state.views)).toEqual(["ap7"]);
    expect(state.marks.api?.autopilotId).toBe("ap7");
  });

  it("leave the store as it was when the hub has no such list", async () => {
    vi.spyOn(api, "listActiveAutopilots").mockRejectedValue(new Error("404"));
    await useAutopilot.getState().loadActive();
    expect(useAutopilot.getState().views).toEqual({});
  });
});
