// Every character's skin, in every state, step and frame, through the scenes
// that draw it: on the grid, inside the frame, anchors in reach, frame 0
// whole, and each state still telling itself apart. A skin that breaks the
// contract (skins.ts) fails here first.

import { describe, expect, it } from "vitest";

import { ACTIVITIES } from "../activity";
import { CHARACTERS, type CharacterId } from "../character";
import { OUTING_KINDS, OUTING_STEPS, type OutingKind } from "../outing";
import {
  deskScene,
  doorScene,
  meetingScene,
  miniDesk,
  outdoorPlace,
  outdoorScene,
  poseAnimated,
  SEAT_W,
  sofaScene,
} from "../warroom/deskArt";
import { POSES, type Pose } from "../world";
import { OUTDOOR_H, OUTDOOR_W, ROD, TONGS_A, TONGS_B } from "./outdoorSprites";
import type { Placed, Point } from "./placed";
import { humanSkin } from "./skinHuman";
import { DESK_GESTURES, FRAMES_OF, skinOf, type DeskGesture, type Figure } from "./skins";
import { CELL, HEADWEAR_KINDS } from "./sprites";

/** Every agent kind with headwear of its own, and one without. */
const KINDS = [...HEADWEAR_KINDS, "other"];
const steps = Array.from({ length: OUTING_STEPS }, (_, step) => step);
const names = (parts: Placed[]) => parts.map((part) => part.name);
const still = (parts: Placed[]) => names(parts.filter((part) => !part.frame || part.frame.index === 0));

/** The parts as drawn in a frame: "x,y" → token, later parts over earlier. */
function paint(parts: Placed[], frame = 0): Map<string, string> {
  const drawn = new Map<string, string>();
  for (const part of parts) {
    if (part.frame && part.frame.index !== frame) continue;
    for (const layer of part.sprite.layers)
      for (const [, x, y, n] of layer.d.matchAll(/M(\d+) (\d+)h(\d+)/g))
        for (let i = 0; i < Number(n); i++) drawn.set(`${part.x + Number(x) + i},${part.y + Number(y)}`, layer.token);
  }
  return drawn;
}
const picture = (parts: Placed[], frame = 0) => [...paint(parts, frame)].sort().join(";");

function expectInside(parts: Placed[], w: number, h: number, where: string) {
  for (const part of parts) {
    const at = `${where} ${part.name}`;
    expect(Number.isInteger(part.x) && Number.isInteger(part.y), at).toBe(true);
    expect(part.x, at).toBeGreaterThanOrEqual(0);
    expect(part.y, at).toBeGreaterThanOrEqual(0);
    expect(part.x + part.sprite.w, at).toBeLessThanOrEqual(w);
    expect(part.y + part.sprite.h, at).toBeLessThanOrEqual(h);
  }
}

function expectPointInside(point: Point | undefined, w: number, h: number, where: string) {
  expect(point, where).toBeDefined();
  expect(Number.isInteger(point!.x) && Number.isInteger(point!.y), where).toBe(true);
  expect(point!.x, where).toBeGreaterThanOrEqual(0);
  expect(point!.y, where).toBeGreaterThanOrEqual(0);
  expect(point!.x, where).toBeLessThan(w);
  expect(point!.y, where).toBeLessThan(h);
}

/** Frames take turns within `of`, with a frame 0 for each `of` (what a still
 * picture shows). */
function expectFrames(parts: Placed[], where: string, allowed?: readonly number[]) {
  const ofs = new Set<number>();
  for (const part of parts) {
    if (!part.frame) continue;
    const { index, of } = part.frame;
    expect(Number.isInteger(index) && index >= 0 && index < of, `${where} ${part.name} ${index}/${of}`).toBe(true);
    if (allowed) expect(allowed, `${where} ${part.name} of ${of}`).toContain(of);
    ofs.add(of);
  }
  for (const of of ofs) {
    expect(parts.some((part) => part.frame?.of === of && part.frame.index === 0), `${where} frame 0 of ${of}`).toBe(true);
  }
}

/** A figure that must move: frames 0 and 1 of 2, showing different pixels. */
function expectMoves(parts: Placed[], where: string) {
  const of2 = parts.filter((part) => part.frame?.of === 2);
  expect(of2.some((part) => part.frame!.index === 0), `${where} frame 0`).toBe(true);
  expect(of2.some((part) => part.frame!.index === 1), `${where} frame 1`).toBe(true);
  expect(picture(parts, 0), `${where} frames 0 and 1 differ`).not.toBe(picture(parts, 1));
}

/** A figure's checks: its parts and anchors inside a w × h frame, its frames
 * as its state allows. */
function expectFigure(figure: Figure<never>, w: number, h: number, where: string, allowed: readonly number[]) {
  const all = [...(figure.behind ?? []), ...figure.parts, ...(figure.front ?? [])];
  expect(figure.parts.length, where).toBeGreaterThan(0);
  expectInside(all, w, h, where);
  expectFrames(all, where, allowed);
  for (const [name, point] of Object.entries(figure.anchors)) expectPointInside(point, w, h, `${where} anchor ${name}`);
}

const outdoorFigure = (character: CharacterId, kind: string, outing: OutingKind, step: number) => {
  const skin = skinOf(character);
  const place = outdoorPlace(outing, step);
  switch (outing) {
    case "hike":
      return skin.hike(kind, place, step < OUTING_STEPS - 1);
    case "stream":
      return skin.stream(kind, place);
    case "fish":
      return skin.fish(kind, place);
    case "camp":
      return skin.camp(kind, place);
    case "bbq":
      return skin.bbq(kind, place);
    case "cycle":
      return skin.cycle(kind, place);
  }
};

/** The anchors an outing's props need. */
const NEEDED: Record<OutingKind, string[]> = {
  hike: [],
  stream: ["pole", "wade"],
  fish: ["rod"],
  camp: [],
  bbq: ["tongs"],
  cycle: [],
};

/** Colours a skin's forearm on the tongs never takes: the person's sleeve is
 * the kind's shirt and the hand skin; the whale's sleeve is her dress; the
 * block and the star are their own colours through and through. */
const NOT_ITS_ARM: Record<CharacterId, string[]> = {
  human: [],
  gpt: [],
  whale: ["agent", "agent-dark"],
  claude: ["agent", "agent-dark", "skin"],
  star: ["agent", "agent-dark", "skin"],
};

describe("the barbecue tongs", () => {
  it("leave the forearm to the skin: the prop is the tongs alone", () => {
    for (const tongs of [TONGS_A, TONGS_B]) {
      const used = tongs.layers.map((layer) => layer.token);
      for (const token of ["agent", "agent-dark", "skin"]) expect(used).not.toContain(token);
    }
  });
});

describe("skinOf", () => {
  it("has a skin for every character, the person for none", () => {
    for (const character of CHARACTERS) expect(skinOf(character).id).toBe(character === "gpt" ? "human" : character);
    expect(skinOf(undefined).id).toBe("human");
  });

  // GPT's own picture is its anime sheet; in pixel art it is the person,
  // in its kind's headwear, exactly as before it had a character.
  it("draws GPT as the person", () => {
    expect(skinOf("gpt")).toBe(humanSkin);
  });
});

describe.each(CHARACTERS)("the %s skin", (character) => {
  const skin = skinOf(character);

  it("sits at a desk in every gesture, inside the desk frame, the mouse and the head in reach", () => {
    for (const kind of KINDS) {
      for (const gesture of DESK_GESTURES) {
        const figure = skin.desk(gesture, kind);
        expectFigure(figure, CELL, CELL, `desk ${gesture} ${kind}`, FRAMES_OF.desk[gesture]);
        expect(figure.behind ?? [], `desk ${gesture}`).toEqual([]);
        expect(figure.front ?? [], `desk ${gesture}`).toEqual([]);
      }
      expectMoves(skin.desk("type", kind).parts, `desk type ${kind}`);
    }
  });

  it("tells every desk gesture apart by its still pixels", () => {
    for (const kind of KINDS) {
      const seen = new Map<string, DeskGesture>();
      for (const gesture of DESK_GESTURES) {
        const key = picture(skin.desk(gesture, kind).parts);
        expect(seen.get(key), `${kind}: ${gesture} looks like ${seen.get(key)}`).toBeUndefined();
        seen.set(key, gesture);
      }
    }
  });

  it("stands at the door and sits on the sofa, awake and asleep, inside their frames", () => {
    for (const kind of KINDS) {
      expectFigure(skin.door(kind), CELL, CELL, `door ${kind}`, FRAMES_OF.door);
      expectFigure(skin.sofa(kind, false), SEAT_W, CELL, `sofa ${kind}`, FRAMES_OF.sofa);
      expectFigure(skin.sofa(kind, true), SEAT_W, CELL, `sofa asleep ${kind}`, FRAMES_OF.sofaAsleep);
      expect(picture(skin.sofa(kind, true).parts), `sofa ${kind} asleep looks awake`).not.toBe(picture(skin.sofa(kind, false).parts));
    }
  });

  it("goes on every outing at every step inside the tile, with the anchors its props need", () => {
    for (const kind of KINDS) {
      for (const outing of OUTING_KINDS) {
        for (const step of steps) {
          const where = `${outing} ${step} ${kind}`;
          const figure = outdoorFigure(character, kind, outing, step) as Figure<never>;
          expectFigure(figure, OUTDOOR_W, OUTDOOR_H, where, FRAMES_OF[outing]);
          for (const anchor of NEEDED[outing]) expect(Object.keys(figure.anchors), where).toContain(anchor);
          if (outing !== "cycle") expect(figure.behind ?? [], where).toEqual([]);
          if (outing !== "bbq") expect(figure.front ?? [], where).toEqual([]);
        }
      }
    }
  });

  it("walks up the trail (standing still at the top) and pedals the bike, short of the finish pole", () => {
    for (const kind of KINDS) {
      for (const step of steps) {
        const hike = outdoorFigure(character, kind, "hike", step);
        if (step < OUTING_STEPS - 1) expectMoves(hike.parts, `hike ${step} ${kind}`);
        else expect(hike.parts.filter((part) => part.frame), `hike summit ${kind}`).toEqual([]);
        const ride = outdoorFigure(character, kind, "cycle", step);
        const rider = [...(ride.behind ?? []), ...ride.parts];
        expectMoves(rider, `cycle ${step} ${kind}`);
        for (const part of rider) expect(part.x + part.sprite.w, `cycle ${step} ${kind} ${part.name}`).toBeLessThanOrEqual(50);
      }
    }
  });

  it("holds the barbecue tongs in a hand of its own, in its own colours, where the tongs' handle starts", () => {
    for (const kind of KINDS) {
      for (const step of steps) {
        const where = `bbq ${step} ${kind}`;
        const figure = skin.bbq(kind, outdoorPlace("bbq", step));
        const { tongs } = figure.anchors;
        const arm = figure.front ?? [];
        expectMoves(arm, `${where} arm`);
        // The hand: the tongs' column 4, on their handle's row (frame 0 at
        // the anchor, frame 1 three rows up).
        expect(paint(arm, 0).has(`${tongs.x + 4},${tongs.y + 1}`), `${where} hand, frame 0`).toBe(true);
        expect(paint(arm, 1).has(`${tongs.x + 4},${tongs.y - 1}`), `${where} hand, frame 1`).toBe(true);
        const used = new Set([...paint(arm, 0).values(), ...paint(arm, 1).values()]);
        for (const token of NOT_ITS_ARM[character]) expect(used.has(token), `${where} ${token}`).toBe(false);
      }
    }
  });

  it("has a small figure for another office's desk, in the kind's colour", () => {
    expect(skin.mini.w).toBeGreaterThan(0);
    expect(skin.mini.w).toBeLessThanOrEqual(8);
    expect(skin.mini.h).toBeGreaterThan(0);
    expect(skin.mini.h).toBeLessThanOrEqual(8);
    // Nothing else at that size says which agent it is.
    expect(skin.mini.layers.map((layer) => layer.token)).toContain("agent");
  });

  describe("in the scenes", () => {
    const options = { character };

    it("keeps every desk and table seat, in every pose and activity, on the desk's grid with whole frames", () => {
      for (const pose of [...POSES, "empty" as const]) {
        for (const activity of [undefined, ...ACTIVITIES]) {
          for (const scene of [
            deskScene(pose, "codex", { ...options, activity, mail: true }),
            meetingScene(pose, "gemini", { ...options, activity, edge: "m" }),
          ]) {
            expectInside(scene, CELL, CELL, `${pose} ${activity}`);
            expectFrames(scene, `${pose} ${activity}`);
          }
        }
      }
    });

    it("animates only the poses that move, and gives each pose and activity a still picture of its own", () => {
      const seen = new Map<string, Pose>();
      for (const pose of POSES) {
        const scene = deskScene(pose, "claude", options);
        expect(scene.some((part) => part.frame), pose).toBe(poseAnimated(pose));
        if (pose === "awaiting-accept" || pose === "offline") continue; // at the door; greyed by the stylesheet
        const key = still(scene).join(",");
        expect(seen.get(key), `${pose} looks like ${seen.get(key)}`).toBeUndefined();
        seen.set(key, pose);
      }
      for (const pose of ["working", "reviewing"] as const) {
        const pictures = new Set(ACTIVITIES.map((activity) => picture(deskScene(pose, "claude", { ...options, activity }))));
        expect(pictures.size, pose).toBe(ACTIVITIES.length);
      }
    });

    it("leaves the screen's glyph showing in every pose, the figure clear of it", () => {
      for (const kind of KINDS) {
        for (const pose of POSES) {
          const scene = deskScene(pose, kind, options);
          // The bubbles over the desk are the scene's own, not the figure.
          const drawn = paint(scene.filter((part) => !part.name.startsWith("bubble.")));
          for (const glyph of scene.filter((part) => part.name.startsWith("screen.") && part.name.endsWith(".glyph"))) {
            const pixels = [...paint([glyph])];
            const shown = pixels.filter(([key, token]) => drawn.get(key) === token).length;
            // Two thirds at least: the person's buns and headphones already cross the
            // hourglass's foot.
            expect(shown / pixels.length, `${kind} ${pose} ${glyph.name}: ${shown} of ${pixels.length} show`).toBeGreaterThanOrEqual(2 / 3);
          }
        }
      }
    });

    it("keeps the door, the sofa and the small desk inside their frames, bubbles and all", () => {
      for (const mail of [false, true]) {
        const door = doorScene("opencode", { ...options, mail });
        expectInside(door, CELL, CELL, "door");
        expect(names(door)).toContain("bubble.wait");
      }
      for (const pose of POSES) {
        const sofa = sofaScene(pose, "codex", options);
        expectInside(sofa, SEAT_W, CELL, `sofa ${pose}`);
        expectFrames(sofa, `sofa ${pose}`);
        expect(sofa.some((part) => part.frame), `sofa ${pose}`).toBe(pose === "limit");
        expectInside(miniDesk(pose, options), 16, 16, `mini ${pose}`);
      }
      expect(still(sofaScene("limit", "claude", options))).toEqual(expect.arrayContaining(["bubble.z1", "bubble.z2", "bubble.z3"]));
    });

    it("hangs the fishing line from the rod's tip at every step", () => {
      for (const step of steps) {
        const scene = outdoorScene({ kind: "fish", step }, "codex", options);
        const rod = scene.find((part) => part.name === "fish.rod")!;
        const line = scene.find((part) => part.name === "fish.line")!;
        expect({ x: line.x, y: line.y }, `${step}`).toEqual({ x: rod.x + ROD.w - 1, y: rod.y + 2 });
      }
    });

    it("draws every outing, at every step, inside its tile, its props there", () => {
      const PROPS: Record<OutingKind, string[]> = {
        hike: ["hike.flag.pole"],
        stream: ["stream.pole", "stream.wade"],
        fish: ["fish.rod", "fish.line", "fish.bobber.a"],
        camp: ["camp.tent", "camp.fire.a"],
        bbq: ["bbq.grill", "bbq.tongs.a", "bbq.tongs.b"],
        cycle: ["cycle.bike", "cycle.wheel.back.a"],
      };
      for (const outing of OUTING_KINDS) {
        for (const step of steps) {
          const scene = outdoorScene({ kind: outing, step }, "antigravity", options);
          const where = `${outing} ${step}`;
          expectInside(scene, OUTDOOR_W, OUTDOOR_H, where);
          expectFrames(scene, where);
          expect(still(scene), where).toEqual(expect.arrayContaining(PROPS[outing].filter((name) => !name.endsWith(".b"))));
          expect(names(scene), where).toEqual(expect.arrayContaining(PROPS[outing]));
          expect(scene.some((part) => part.frame && part.frame.index > 0), where).toBe(true);
        }
      }
    });
  });
});
