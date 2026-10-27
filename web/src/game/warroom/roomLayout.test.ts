import { describe, expect, it } from "vitest";

import type { Pose } from "../world";
import { layoutRoom } from "./roomLayout";

function actors(entries: [string, Pose][]) {
  return new Map(entries.map(([id, pose]) => [id, { pose }]));
}

const runner = { kind: "runner" as const, online: true };

describe("layoutRoom", () => {
  it("seats everyone in the order they came, and offered runs at the door", () => {
    const byId = actors([
      ["work", "working"],
      ["fail", "failed"],
      ["sleep", "limit"],
      ["wait", "awaiting-accept"],
      ["done", "finished"],
    ]);
    const layout = layoutRoom({ ...runner, slots: 2, actorIds: ["work", "wait", "fail", "sleep", "done"] }, byId);
    // More are seated than the runner takes at once: a desk each.
    expect(layout.desks).toEqual(["work", "fail", "sleep", "done"]);
    expect(layout.queue).toEqual(["wait"]);
  });

  it("has a desk per slot the runner has, the free ones empty", () => {
    const layout = layoutRoom({ ...runner, slots: 3, actorIds: ["a"] }, actors([["a", "working"]]));
    expect(layout.desks).toEqual(["a", null, null]);
  });

  it("gives a runner that does not say its slots one desk", () => {
    expect(layoutRoom({ ...runner, actorIds: [] }, new Map()).desks).toEqual([null]);
    // Offline, it keeps its desks: the lights are off, the room is there.
    expect(layoutRoom({ ...runner, online: false, slots: 2, actorIds: [] }, new Map()).desks).toEqual([null, null]);
  });

  it("gives the terminal room and an unknown runner only the desks in use", () => {
    const byId = actors([["s", "idle"]]);
    expect(layoutRoom({ kind: "terminal", online: null, actorIds: ["s"] }, byId).desks).toEqual(["s"]);
    expect(layoutRoom({ kind: "runner", online: null, slots: 4, actorIds: [] }, byId).desks).toEqual([]);
  });

  it("skips ids it does not know", () => {
    expect(layoutRoom({ ...runner, slots: 1, actorIds: ["gone"] }, new Map()).desks).toEqual([null]);
  });
});
