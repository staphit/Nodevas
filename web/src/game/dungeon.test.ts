import { describe, expect, it } from "vitest";
import { analyzeGraph } from "../analysis";
import type { Graph, Status } from "../types";
import { bossOf, buildDungeon, countsAsWork, partiesOf, xpOf, type DungeonInputs } from "./dungeon";
import { makeAutopilot, makeRun, makeTask, NOW } from "./testFixtures";

const SETTLED_CUSTOM = "custom-status-shipped" as const;
const BLOCKING_CUSTOM = "custom-status-review" as const;

/** start → schema → api → login, with an AND gate (api + design) in front
 * of login. */
function graph(): Graph {
  return {
    version: 1,
    nodes: [
      { id: "start", title: "Start", kind: "start" },
      { id: "schema", title: "DB schema", kind: "task", priority: "high" },
      { id: "design", title: "Design", kind: "task" },
      { id: "api", title: "Auth API", kind: "task", requires: "schema" },
      { id: "login", title: "Login form", kind: "task", requires: "api and design" },
      { id: "launch", title: "Launch", kind: "event", requires: "login" },
      { id: "end", title: "End", kind: "end" },
    ],
    edges: [
      { from: "start", to: "schema" },
      { from: "schema", to: "api" },
      { from: "api", to: "login" },
      { from: "design", to: "login" },
      { from: "login", to: "launch" },
    ],
    ui: {
      customStatuses: [
        { id: SETTLED_CUSTOM, label: "Shipped", color: "#0a0", shape: "circle", settled: true },
        { id: BLOCKING_CUSTOM, label: "In review", color: "#aa0", shape: "circle" },
      ],
      logicGates: [
        { id: "g-and", operator: "and", inputs: ["api", "design"], output: "login", x: 0, y: 0 },
        // Relation gates hold nothing back: no door.
        { id: "g-opt", operator: "optional", inputs: ["design"], outputs: ["api"], x: 0, y: 0 },
        // A gate whose output is not on the board: no door.
        { id: "g-lost", operator: "must", inputs: ["api"], output: "gone", x: 0, y: 0 },
      ],
    },
  };
}

function dungeon(statuses: Record<string, Status>, overrides: Partial<DungeonInputs> = {}) {
  return buildDungeon({
    graph: graph(),
    statuses,
    runs: [],
    autopilots: [],
    projectName: "main",
    now: NOW,
    ...overrides,
  });
}

function monster(d: ReturnType<typeof dungeon>, id: string) {
  const found = d.monsters.find((m) => m.nodeId === id);
  if (!found) throw new Error(`no monster ${id}`);
  return found;
}

describe("monsters", () => {
  it("leaves start, end and notes out; an event is work", () => {
    expect(countsAsWork({ kind: "start" })).toBe(false);
    expect(countsAsWork({ kind: "end" })).toBe(false);
    expect(countsAsWork({ kind: "note" })).toBe(false);
    expect(countsAsWork({ kind: "event" })).toBe(true);
    expect(countsAsWork({ kind: "task" })).toBe(true);
    expect(countsAsWork({})).toBe(true);
    // A custom type that does not count stays out too.
    const custom: Graph = {
      version: 1,
      nodes: [],
      nodeTypes: [
        {
          id: "custom-type-1",
          label: "伏筆",
          agentMode: "none",
          completion: "none",
          countsInProgress: false,
        },
      ],
    };
    expect(countsAsWork({ kind: "custom-type-1" }, custom)).toBe(false);
    expect(dungeon({}).monsters.map((m) => m.nodeId)).toEqual([
      "schema",
      "design",
      "api",
      "login",
      "launch",
    ]);
  });

  it("locks a node that is blocked although the server calls it ready", () => {
    // Nobody touched api: its status is "ready", and schema is not done.
    const d = dungeon({ api: "ready" });
    expect(monster(d, "api")).toMatchObject({ status: "ready", state: "locked", blockers: ["schema"] });
    expect(monster(d, "schema")).toMatchObject({ state: "attackable", blockers: [] });
    expect(monster(d, "login").state).toBe("locked");
  });

  it("opens what its prerequisites settle, custom settled states included", () => {
    const d = dungeon({ schema: SETTLED_CUSTOM });
    expect(monster(d, "schema").state).toBe("defeated");
    expect(monster(d, "api").state).toBe("attackable");
    const blocking = dungeon({ schema: BLOCKING_CUSTOM });
    expect(monster(blocking, "schema").state).toBe("attackable");
    expect(monster(blocking, "api").state).toBe("locked");
  });

  it("counts done, skipped and deprecated as defeated", () => {
    const d = dungeon({ schema: "done", design: "skipped", api: "deprecated" });
    expect(["schema", "design", "api"].map((id) => monster(d, id).state)).toEqual(["defeated", "defeated", "defeated"]);
    expect(monster(d, "login").state).toBe("attackable");
  });

  it("carries what the board's analysis knows, and a click opens the node", () => {
    const d = dungeon({});
    expect(monster(d, "design")).toMatchObject({
      title: "Design",
      kind: "task",
      entry: true,
      faded: false,
      target: { kind: "node", nodeId: "design" },
    });
    expect(monster(d, "schema")).toMatchObject({ priority: "high", entry: false });
    expect(d.monsters.some((m) => m.critical)).toBe(true);
  });

  it("uses an analysis the caller passes instead of computing one", () => {
    const g = graph();
    const analysis = analyzeGraph(g, {});
    analysis.blocked.clear();
    const d = buildDungeon({ graph: g, statuses: {}, analysis, runs: [], autopilots: [], projectName: "p", now: NOW });
    expect(monster(d, "api").state).toBe("attackable");
  });

  it("is engaged by a live run or an autopilot task on it, and marked by a merged PR", () => {
    const d = dungeon(
      {},
      {
        runs: [
          makeRun({ nodeId: "schema", status: "running" }),
          makeRun({ nodeId: "design", status: "succeeded", prUrl: "https://x/pull/1", prState: "merged" }),
          makeRun({ nodeId: "api", status: "failed" }),
        ],
        autopilots: [makeAutopilot([makeTask({ nodeId: "login", state: "waiting_review" })])],
      },
    );
    expect(d.monsters.filter((m) => m.engaged).map((m) => m.nodeId)).toEqual(["schema", "login"]);
    expect(d.mergedNodeIds).toEqual(["design"]);
    expect(monster(d, "design").merged).toBe(true);
  });

  it("is not engaged by an ended autopilot", () => {
    const ended = makeAutopilot([makeTask({ nodeId: "login", state: "running" })], { status: "cancelled" });
    expect(monster(dungeon({}, { autopilots: [ended] }), "login").engaged).toBe(false);
  });
});

describe("boss", () => {
  it("has HP for every unsettled work node", () => {
    // The launch event is work too: five monsters, two of them settled.
    const d = dungeon({ schema: "done", design: SETTLED_CUSTOM });
    expect(d.boss).toEqual({ name: "main", hp: 3, maxHp: 5, ratio: 0.6, defeated: false });
  });

  it("is defeated when everything settled, and an empty project is no fight", () => {
    const d = dungeon({
      schema: "done",
      design: "done",
      api: "done",
      login: "skipped",
      launch: "done",
    });
    expect(d.boss).toMatchObject({ hp: 0, maxHp: 5, ratio: 0, defeated: true });
    expect(bossOf([], "empty")).toEqual({ name: "empty", hp: 0, maxHp: 0, ratio: 0, defeated: false });
    expect(buildDungeon({ graph: null, statuses: {}, runs: [], autopilots: [], projectName: "x", now: NOW }).boss.maxHp).toBe(0);
  });
});

describe("doors", () => {
  it("puts a door in front of a boolean gate's output only", () => {
    const d = dungeon({});
    expect(d.doors.map((door) => door.gateId)).toEqual(["g-and"]);
    expect(d.doors[0]).toMatchObject({ operator: "and", inputs: ["api", "design"], output: "login", open: false });
  });

  it("points a shut door at the first blocker and an open one at its room", () => {
    const shut = dungeon({ schema: "done", api: "done" }).doors[0];
    expect(shut).toMatchObject({ open: false, blockers: ["design"], target: { kind: "node", nodeId: "design" } });
    const open = dungeon({ schema: "done", api: "done", design: "done" }).doors[0];
    expect(open).toMatchObject({ open: true, blockers: [], target: { kind: "node", nodeId: "login" } });
  });

  it("is open once its output settled, whatever its inputs", () => {
    expect(dungeon({ login: "done" }).doors[0].open).toBe(true);
  });
});

describe("parties and XP", () => {
  it("places a live autopilot's party on the tasks it is working", () => {
    const view = makeAutopilot([
      makeTask({ nodeId: "schema", state: "success" }),
      makeTask({ nodeId: "api", state: "running" }),
      makeTask({ nodeId: "design", state: "waiting_human" }),
      makeTask({ nodeId: "login", state: "none" }),
    ]);
    const [party] = partiesOf([view]);
    expect(party).toEqual({
      autopilotId: "ap1",
      goalNodeId: "goal",
      status: "running",
      positions: ["api", "design"],
      waitingHuman: ["design"],
      total: 4,
      done: 1,
      failed: 0,
      target: { kind: "autopilot", autopilotId: "ap1", goalNodeId: "goal" },
    });
  });

  it("keeps a paused party and drops an ended one", () => {
    const paused = makeAutopilot([], { id: "p", status: "paused" });
    const done = makeAutopilot([], { id: "d", status: "succeeded" });
    expect(partiesOf([paused, done]).map((p) => p.autopilotId)).toEqual(["p"]);
  });

  it("earns XP for every completion in the history", () => {
    expect(
      xpOf([
        { t: "1", event: "status", node: "a", to: "done" },
        { t: "2", event: "status", node: "a", to: "in_progress" },
        { t: "3", event: "status", node: "a", to: "done" },
      ]),
    ).toBe(2);
    expect(xpOf(undefined)).toBe(0);
    expect(dungeon({}, { history: [{ t: "1", event: "status", to: "done" }] }).xp).toBe(1);
  });
});
