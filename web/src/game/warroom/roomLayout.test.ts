import { describe, expect, it } from "vitest";

import { layoutRoom } from "./roomLayout";

type Pose = "working" | "failed" | "reviewing" | "awaiting-accept" | "idle";

function actors(entries: [string, Pose, string][]) {
  return new Map(entries.map(([id, pose, agent]) => [id, { pose, agent }]));
}

describe("layoutRoom", () => {
  it("seats workers at desks, offered runs at the door, reviewers around a table", () => {
    const byId = actors([
      ["work", "working", "gemini"],
      ["fail", "failed", "opencode"],
      ["rev1", "reviewing", "claude"],
      ["rev2", "reviewing", "codex"],
      ["wait", "awaiting-accept", "codex"],
    ]);
    const layout = layoutRoom(
      { actorIds: ["rev1", "rev2", "work", "fail", "wait"], agentKinds: ["gemini", "opencode"] },
      byId,
      [{ pipelineId: "p1", actorIds: ["rev1", "rev2"] }],
    );
    const kindOf = Object.fromEntries(layout.seats.map((seat) => [seat.actorId, seat.kind]));
    expect(kindOf).toEqual({ work: "desk", fail: "desk", rev1: "meeting", rev2: "meeting", wait: "queue" });
    expect(layout.tables).toHaveLength(1);
    // Installed agents with someone at their desk leave no empty desk.
    expect(layout.emptyDesks).toEqual([]);
    // Nothing overlaps: every seat is at least a tile from every other.
    const seats = layout.seats;
    for (let i = 0; i < seats.length; i++) {
      for (let j = i + 1; j < seats.length; j++) {
        const d = Math.hypot(seats[i].col - seats[j].col, seats[i].row - seats[j].row);
        expect(d, `${seats[i].actorId} vs ${seats[j].actorId}`).toBeGreaterThan(0.9);
      }
    }
    // Everything is inside the room.
    for (const seat of seats) {
      expect(seat.col).toBeGreaterThan(0);
      expect(seat.col).toBeLessThan(layout.cols);
      expect(seat.row).toBeGreaterThan(0);
      expect(seat.row).toBeLessThan(layout.rows);
    }
  });

  it("keeps an empty desk per installed agent nobody sits at", () => {
    const layout = layoutRoom(
      { actorIds: ["a"], agentKinds: ["claude", "codex"] },
      actors([["a", "working", "claude"]]),
      [],
    );
    expect(layout.emptyDesks.map((desk) => desk.agent)).toEqual(["codex"]);
  });

  it("draws one empty desk in a room with nothing installed and nobody in it", () => {
    const layout = layoutRoom({ actorIds: [], agentKinds: [] }, new Map(), []);
    expect(layout.emptyDesks).toHaveLength(1);
    expect(layout.rows).toBeGreaterThanOrEqual(4);
  });
});
