import { describe, expect, it } from "vitest";

import { analyzeGraph } from "../analysis";
import type { Graph, Status } from "../types";
import { waitingOnPrerequisites } from "./prerequisites";

const SETTLED_CUSTOM = "custom-status-shipped" as const;

/** start → schema → api → login (which also needs design) → launch. */
function graph(): Graph {
  return {
    version: 1,
    nodes: [
      { id: "start", title: "Start", kind: "start" },
      { id: "schema", title: "DB schema", kind: "task" },
      { id: "design", title: "Design", kind: "task" },
      { id: "api", title: "Auth API", kind: "task", requires: "schema" },
      { id: "login", title: "Login form", kind: "task", requires: "api and design" },
      { id: "launch", title: "Launch", kind: "event", requires: "login" },
      { id: "end", title: "End", kind: "end", requires: "launch" },
    ],
    edges: [
      { from: "start", to: "schema" },
      { from: "schema", to: "api" },
      { from: "api", to: "login" },
      { from: "design", to: "login" },
      { from: "login", to: "launch" },
      { from: "launch", to: "end" },
    ],
    ui: {
      customStatuses: [{ id: SETTLED_CUSTOM, label: "Shipped", color: "#0a0", shape: "circle", settled: true }],
    },
  };
}

function waiting(statuses: Record<string, Status>) {
  const g = graph();
  return waitingOnPrerequisites(g, statuses, analyzeGraph(g, statuses));
}

describe("waitingOnPrerequisites", () => {
  it("lists the blocked work, with what each waits on, in graph order", () => {
    const tasks = waiting({});
    expect(tasks.map((task) => task.nodeId)).toEqual(["api", "login", "launch"]);
    expect(tasks[0]).toMatchObject({ title: "Auth API", kind: "task", blockers: ["schema"] });
    expect(tasks.find((task) => task.nodeId === "login")?.blockers.sort()).toEqual(["api", "design"]);
  });

  it("leaves out what does not count as work, and what is settled", () => {
    const tasks = waiting({ schema: "done", api: SETTLED_CUSTOM });
    // End waits on launch but is not work; api is settled by a custom state.
    expect(tasks.map((task) => task.nodeId)).toEqual(["login", "launch"]);
    expect(tasks[0].blockers).toEqual(["design"]);
  });

  it("is empty with no graph", () => {
    expect(waitingOnPrerequisites(null, {}, { blocked: new Map() })).toEqual([]);
  });
});
