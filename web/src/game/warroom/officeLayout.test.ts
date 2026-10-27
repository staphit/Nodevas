import { describe, expect, it } from "vitest";

import { layoutOffice } from "./officeLayout";

const office = (over: Partial<Parameters<typeof layoutOffice>[0]> = {}) => ({
  work: [] as string[],
  freeDesks: 0,
  meeting: [] as string[],
  items: [] as { id: string }[],
  entrance: [] as string[],
  ...over,
});
const byId = (...ids: string[]) => new Map(ids.map((id) => [id, { id }]));

describe("layoutOffice", () => {
  it("seats everyone at work, then the free desks the runners have room for", () => {
    const layout = layoutOffice(office({ work: ["a", "b", "c"], freeDesks: 2 }), byId("a", "b", "c"));
    expect(layout.desks).toEqual(["a", "b", "c", null, null]);
  });

  it("keeps at least two desks, padding only an office with fewer", () => {
    expect(layoutOffice(office(), byId()).desks).toEqual([null, null]);
    expect(layoutOffice(office({ work: ["a"] }), byId("a")).desks).toEqual(["a", null]);
    expect(layoutOffice(office({ work: ["a"], freeDesks: 1 }), byId("a")).desks).toEqual(["a", null]);
    expect(layoutOffice(office({ work: ["a"], freeDesks: 2 }), byId("a")).desks).toEqual(["a", null, null]);
  });

  it("seats the meeting before what lies on the table, and always shows a table", () => {
    const layout = layoutOffice(
      office({ meeting: ["m1"], items: [{ id: "merge:r1" }, { id: "held:x" }] }),
      byId("m1"),
    );
    expect(layout.meetingCells).toEqual([{ actorId: "m1" }, { itemId: "merge:r1" }, { itemId: "held:x" }]);
    expect(layoutOffice(office(), byId()).meetingCells).toEqual([{ empty: true }, { empty: true }]);
    expect(layoutOffice(office({ meeting: ["m1"] }), byId("m1")).meetingCells).toEqual([
      { actorId: "m1" },
      { empty: true },
    ]);
  });

  it("puts who waits at the door at the entrance, and drops actors not in view", () => {
    const layout = layoutOffice(office({ work: ["a", "gone"], entrance: ["d", "gone2"] }), byId("a", "d"));
    expect(layout.desks).toEqual(["a", null]);
    expect(layout.entrance).toEqual(["d"]);
  });
});
