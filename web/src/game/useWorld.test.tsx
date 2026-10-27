import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAgents } from "../agents/agentStore";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useRuns } from "../runs/runStore";
import { BUBBLE_MS, PRESENCE_TICK_MS, SESSION_TTL_MS } from "./presence";
import { makeRun, NOW, runId } from "./testFixtures";
import { RUN_WORKING, seedScene, stubLoaders } from "./testScene";
import { mergeRuns, useWorld } from "./useWorld";

afterEach(() => {
  vi.useRealTimers();
});

describe("mergeRuns", () => {
  it("lets a final copy win over an unfinished one, wherever each came from", () => {
    const id = runId("d1");
    const running = makeRun({ id, nodeId: "api", status: "running" });
    const done = makeRun({ id, nodeId: "api", status: "succeeded" });
    const merged = mergeRuns([running], {}, { api: [done] });
    expect(merged[merged.length - 1]).toBe(done);
    // …even when the stale copy comes last in the stores.
    const reversed = mergeRuns([done], { api: running }, {});
    expect(reversed[reversed.length - 1]).toBe(done);
  });
});

describe("useWorld", () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: NOW });
  });

  it("loads runners, budget, live autopilots and the unfinished runs on mount", () => {
    seedScene(NOW);
    useRuns.setState({ activeRunsLoaded: false });
    useAutopilot.setState({ activeLoaded: false });
    const loaders = stubLoaders();
    renderHook(() => useWorld());
    expect(loaders.refreshRunners).toHaveBeenCalledTimes(1);
    expect(loaders.loadBudget).toHaveBeenCalledTimes(1);
    expect(loaders.loadActiveRuns).toHaveBeenCalledTimes(1);
    expect(loaders.loadActive).toHaveBeenCalledTimes(1);
    expect(loaders.loadProjectRuns).toHaveBeenCalledTimes(1);
  });

  it("asks again when a project switch reset the stores after it mounted", () => {
    seedScene(NOW);
    const loaders = stubLoaders();
    renderHook(() => useWorld());
    expect(loaders.loadActiveRuns).not.toHaveBeenCalled();
    act(() => {
      useRuns.setState({ activeRunsLoaded: false, activeRuns: [] });
      useAutopilot.setState({ activeLoaded: false, views: {} });
    });
    expect(loaders.loadActiveRuns).toHaveBeenCalledTimes(1);
    expect(loaders.loadActive).toHaveBeenCalledTimes(1);
  });

  it("merges the newest run per node into the world with the active ones", () => {
    seedScene(NOW);
    const { result } = renderHook(() => useWorld());
    const office = result.current.office;
    // The active list's runs sit at desks…
    expect(office.actors.map((actor) => actor.id)).toContain(`run:${RUN_WORKING}`);
    // …and latestByNode's merged run counts on the map.
    expect(result.current.dungeon.mergedNodeIds).toEqual(["schema"]);
  });

  it("fetches the pipeline of a review run and the node of a fallback run once", () => {
    seedScene(NOW);
    const loaders = stubLoaders();
    useRuns.setState({
      pipelines: {},
      runsByNode: {},
      activeRuns: [
        makeRun({ id: runId("e1"), nodeId: "login", kind: "review", pipelineId: "p9" }),
        makeRun({ id: runId("e2"), nodeId: "docs", retryOf: runId("e0") }),
      ],
    });
    const { rerender } = renderHook(() => useWorld());
    expect(loaders.loadPipeline).toHaveBeenCalledWith("p9");
    expect(loaders.loadRuns).toHaveBeenCalledWith("docs");
    act(() => {
      useRuns.setState({ activeRuns: [...useRuns.getState().activeRuns] });
    });
    rerender();
    expect(loaders.loadPipeline).toHaveBeenCalledTimes(1);
    expect(loaders.loadRuns).toHaveBeenCalledTimes(1);
  });

  it("ages a quiet session to offline on the presence tick", () => {
    seedScene(NOW);
    useAgents.setState({ asks: [], held: [] });
    const { result } = renderHook(() => useWorld());
    const laptop = () => result.current.office.actors.find((actor) => actor.id === "session:s1");
    expect(laptop()?.pose).toBe("working");
    act(() => {
      vi.advanceTimersByTime(SESSION_TTL_MS + PRESENCE_TICK_MS);
    });
    expect(laptop()?.pose).toBe("offline");
  });

  it("shows a fresh message as a bubble and drops it when it runs out, between ticks", () => {
    seedScene(NOW);
    const { result } = renderHook(() => useWorld());
    const claude = () => result.current.office.actors.find((actor) => actor.id === `run:${RUN_WORKING}`);
    expect(claude()?.bubble?.body).toBe("Schema is ready.");
    act(() => {
      vi.advanceTimersByTime(BUBBLE_MS);
    });
    expect(claude()?.bubble).toBeUndefined();
  });

  it("shows a message that arrives mid-tick right away", () => {
    seedScene(NOW);
    useAgents.setState({ messages: [] });
    const { result } = renderHook(() => useWorld());
    act(() => {
      vi.advanceTimersByTime(4_000);
    });
    act(() => {
      useAgents.setState({
        messages: [
          {
            id: "m2",
            project: "p",
            from: "x",
            senderActor: "u",
            senderSession: "visual-editor-claude-opus-x",
            to: "*",
            body: "New one",
            createdAt: Date.now(),
            status: "delivered",
          },
        ],
      });
    });
    const claude = result.current.office.actors.find((actor) => actor.id === `run:${RUN_WORKING}`);
    expect(claude?.bubble?.body).toBe("New one");
  });
});
