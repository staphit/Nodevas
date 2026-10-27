import { describe, expect, it } from "vitest";
import type { Graph } from "../types";
import { buildWorld, nodeTitles, type WorldInputs } from "./buildWorld";
import { runActorId, sessionActorId } from "./office";
import { makeAutopilot, makeRun, makeRunner, makeSession, makeTask, NOW } from "./testFixtures";

const GRAPH: Graph = {
  version: 1,
  nodes: [
    { id: "schema", title: "DB schema" },
    { id: "login", title: "Login form", requires: "schema" },
  ],
  edges: [{ from: "schema", to: "login" }],
};

function inputs(overrides: Partial<WorldInputs> = {}): WorldInputs {
  return {
    graph: GRAPH,
    statuses: {},
    projectName: "main",
    runs: [],
    runners: [makeRunner()],
    sessions: [],
    asks: [],
    held: [],
    messages: [],
    autopilots: [],
    now: NOW,
    ...overrides,
  };
}

describe("buildWorld", () => {
  it("titles nodes from the graph", () => {
    expect(nodeTitles(GRAPH).get("login")).toBe("Login form");
    expect(nodeTitles(null).size).toBe(0);
  });

  it("builds the war room and the boss map from the same snapshot", () => {
    const run = makeRun({ nodeId: "schema" });
    const world = buildWorld(
      inputs({
        runs: [run],
        sessions: [makeSession({ id: "mine", nodeId: "login", state: "busy" })],
        autopilots: [makeAutopilot([makeTask({ nodeId: "schema", state: "running" })])],
        history: [{ t: "1", event: "status", to: "done" }],
      }),
    );
    expect(world.now).toBe(NOW);
    expect(world.office.actors.map((a) => [a.id, a.pose, a.nodeTitle])).toEqual([
      [runActorId(run.id), "working", "DB schema"],
      [sessionActorId("mine"), "working", "Login form"],
    ]);
    expect(world.dungeon.boss).toMatchObject({ hp: 2, maxHp: 2 });
    expect(world.dungeon.monsters.map((m) => [m.nodeId, m.state, m.engaged])).toEqual([
      ["schema", "attackable", true],
      ["login", "locked", false],
    ]);
    expect(world.dungeon.parties[0].positions).toEqual(["schema"]);
    expect(world.dungeon.xp).toBe(1);
  });

  it("is pure: the same inputs build an equal world", () => {
    const snapshot = inputs({ runs: [makeRun()], sessions: [makeSession()] });
    expect(buildWorld(snapshot)).toEqual(buildWorld(snapshot));
  });

  it("ages sessions with the clock it is given", () => {
    const snapshot = inputs({ sessions: [makeSession({ lastSeen: NOW })] });
    expect(buildWorld(snapshot).office.actors[0].pose).toBe("idle");
    expect(buildWorld({ ...snapshot, now: NOW + 90_000 }).office.actors[0].pose).toBe("offline");
  });
});
