import { describe, expect, it } from "vitest";
import type { Graph } from "../types";
import type { BuildingInputs } from "./building";
import { buildWorld, nodeTitles } from "./buildWorld";
import { runActorId, sessionActorId } from "./office";
import { makeRun, makeRunner, makeSession, NOW } from "./testFixtures";

const GRAPH: Graph = {
  version: 1,
  nodes: [
    { id: "schema", title: "DB schema" },
    { id: "login", title: "Login form", requires: "schema" },
  ],
  edges: [{ from: "schema", to: "login" }],
};

function inputs(overrides: Partial<BuildingInputs> = {}): BuildingInputs {
  return {
    activeProject: "main",
    projectLabel: "Main",
    remote: null,
    waitingItems: [],
    graph: GRAPH,
    runs: [],
    runners: [makeRunner()],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    now: NOW,
    ...overrides,
  };
}

describe("buildWorld", () => {
  it("titles nodes from the graph", () => {
    expect(nodeTitles(GRAPH).get("login")).toBe("Login form");
    expect(nodeTitles(null).size).toBe(0);
  });

  it("builds the war room from the snapshot", () => {
    const run = makeRun({ nodeId: "schema" });
    const world = buildWorld(
      inputs({
        runs: [run],
        sessions: [makeSession({ id: "mine", nodeId: "login", state: "busy" })],
      }),
    );
    expect(world.now).toBe(NOW);
    expect(world.building.actors.map((a) => [a.id, a.pose, a.nodeTitle])).toEqual([
      [runActorId(run.id), "working", "DB schema"],
      [sessionActorId("mine"), "working", "Login form"],
    ]);
  });

  it("is pure: the same inputs build an equal world", () => {
    const snapshot = inputs({ runs: [makeRun()], sessions: [makeSession()] });
    expect(buildWorld(snapshot)).toEqual(buildWorld(snapshot));
  });

  it("ages sessions with the clock it is given", () => {
    const snapshot = inputs({ sessions: [makeSession({ lastSeen: NOW })] });
    expect(buildWorld(snapshot).building.actors[0].pose).toBe("idle");
    expect(buildWorld({ ...snapshot, now: NOW + 90_000 }).building.actors[0].pose).toBe("offline");
  });
});
