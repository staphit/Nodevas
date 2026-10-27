import { describe, expect, it } from "vitest";

import { areaOf } from "./areas";
import type { Actor, Area } from "./world";

type A = Pick<Actor, "pose" | "kind" | "nodeId">;
const run = (pose: Actor["pose"], nodeId = "n1"): A => ({ kind: "run", pose, nodeId });
const session = (pose: Actor["pose"], nodeId?: string): A => ({ kind: "session", pose, nodeId });

describe("areaOf", () => {
  const cases: [string, A, { kind?: string; status: string } | undefined, Area][] = [
    ["a raised hand sits at the meeting table", run("asking-user"), { status: "running" }, "meeting"],
    ["a session's question too", session("asking-user"), undefined, "meeting"],
    ["a plan run is a meeting", run("working"), { kind: "plan", status: "running" }, "meeting"],
    ["a queued plan run too", run("awaiting-accept"), { kind: "plan", status: "queued" }, "meeting"],
    ["a finished plan run is back at a desk", run("finished"), { kind: "plan", status: "succeeded" }, "work"],
    ["a usage limit sleeps in the lounge", run("limit"), { status: "queued" }, "lounge"],
    ["an offer waits at the door", run("awaiting-accept"), { status: "offered" }, "entrance"],
    ["a queued run too", run("awaiting-accept"), { status: "queued" }, "entrance"],
    ["a pending merge stays at its desk", run("awaiting-accept"), { status: "succeeded" }, "work"],
    ["an idle session with no node lounges", session("idle"), undefined, "lounge"],
    ["an offline session with no node too", session("offline"), undefined, "lounge"],
    ["an idle session on a node is at a desk", session("idle", "n1"), undefined, "work"],
    ["a busy session works", session("working"), undefined, "work"],
    ["a working run works", run("working"), { status: "running" }, "work"],
    ["a failed run stays at its desk", run("failed"), { status: "failed" }, "work"],
    ["a review run works", run("reviewing"), { kind: "review", status: "running" }, "work"],
    ["an offline run stays at its desk", run("offline"), { status: "offered" }, "work"],
  ];
  it.each(cases)("%s", (_name, actor, r, area) => {
    expect(areaOf(actor, r)).toBe(area);
  });
});
