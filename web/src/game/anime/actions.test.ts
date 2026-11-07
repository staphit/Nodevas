import { describe, expect, it } from "vitest";

import { OUTING_KINDS } from "../outing";
import { DESK_GESTURES } from "../pixel/skins";
import { ANIME_ACTIONS, animeAction } from "./actions";

describe("animeAction", () => {
  it("names a desk gesture's action after it, at a desk and at the meeting table alike", () => {
    for (const gesture of DESK_GESTURES) expect(animeAction({ at: "desk", gesture })).toBe(`desk-${gesture}`);
  });

  it("names the door, the sofa and every outing's action", () => {
    expect(animeAction({ at: "door" })).toBe("door-idle");
    expect(animeAction({ at: "sofa", asleep: false })).toBe("sofa-sit");
    expect(animeAction({ at: "sofa", asleep: true })).toBe("sofa-sleep");
    expect(animeAction({ at: "outing", kind: "hike", walking: true })).toBe("hike-walk");
    expect(animeAction({ at: "outing", kind: "hike", walking: false })).toBe("hike-summit");
    expect(animeAction({ at: "outing", kind: "stream" })).toBe("stream-wade");
    expect(animeAction({ at: "outing", kind: "fish" })).toBe("fish-sit");
    expect(animeAction({ at: "outing", kind: "camp" })).toBe("camp-sit");
    expect(animeAction({ at: "outing", kind: "bbq" })).toBe("bbq-grill");
    expect(animeAction({ at: "outing", kind: "cycle" })).toBe("cycle-ride");
  });

  it("has none for another office's small desk", () => {
    expect(animeAction({ at: "mini" })).toBeUndefined();
  });

  it("lists every action once, the four drawn first in the sheets' order", () => {
    expect(ANIME_ACTIONS.slice(0, 4)).toEqual(["door-idle", "desk-type", "hike-walk", "cycle-ride"]);
    expect(new Set(ANIME_ACTIONS).size).toBe(ANIME_ACTIONS.length);
    const named = [
      ...DESK_GESTURES.map((gesture) => animeAction({ at: "desk", gesture })),
      animeAction({ at: "door" }),
      animeAction({ at: "sofa", asleep: false }),
      animeAction({ at: "sofa", asleep: true }),
      animeAction({ at: "outing", kind: "hike", walking: false }),
      ...OUTING_KINDS.map((kind) => animeAction({ at: "outing", kind, walking: true })),
    ];
    expect([...new Set(named)].sort()).toEqual([...ANIME_ACTIONS].sort());
  });
});
