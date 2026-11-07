// DeepSeek, as a chibi whale girl (game/character.ts says who): a big head,
// long wavy dark-blue hair going lighter at the ends, a curled strand on top,
// a white frilly headband, whale fins for ears (dark on top, white beneath),
// big blue eyes, a navy dress with a white apron front, a ribbon in the
// agent kind's colour, and a blue whale tail behind. Every state's contract
// (frames, sizes, anchors) is skins.ts'; skins.test.ts checks it. The kind's
// headwear is the person's; she wears her headband instead.
//
// Her own colours: 8 whale-hair, 9 whale-hair-light (the ends), 0 whale-dress,
// + whale-fin (the tail, the eyes); w (bubble) is the white of the headband,
// the fins' undersides and the apron, & (meat) her blush.

import { at, framed, type Placed, type Point } from "./placed";
import type { DeskGesture, Skin } from "./skins";
import { sprite } from "./sprites";

// ---------- from the front ----------

/** The head from the front (16×16): the curl on top (rows 0–2), the head
 * itself (columns 2–13 from row 3, HEAD_DX/HEAD_DY), the fins out at its
 * sides, the headband's frill over the fringe. */
const HEAD_FRONT = sprite(`
  ........99......
  .......9..9.....
  .......8........
  ....oooooooo....
  ...o88888888o...
  ..owwwwwwwwwwo..
  o.ow8w8w8w8w8o.o
  8oo8888888888oo8
  w8o88k8888k88o8w
  ow8o8kkkkkk8o8wo
  .oo8kookkook8oo.
  ..o8k+wkk+wk8o..
  ..o8&++kk++&8o..
  ..o88kkookk88o..
  ...o8okkkko8o...
  ....oooooooo....
`);
/** The same, asleep: the eyes shut in arcs, the curl drooping. */
const HEAD_FRONT_SLEEP = sprite(`
  ................
  ........999.....
  .......9...8....
  ....oooooooo....
  ...o88888888o...
  ..owwwwwwwwwwo..
  o.ow8w8w8w8w8o.o
  8oo8888888888oo8
  w8o88k8888k88o8w
  ow8o8kkkkkk8o8wo
  .oo8kkkkkkkk8oo.
  ..o8kookkook8o..
  ..o8&kkkkkk&8o..
  ..o88kkookk88o..
  ...o8okkkko8o...
  ....oooooooo....
`);
/** Where the head itself starts in HEAD_FRONT (and HEAD_BACK). */
const HEAD_DX = 2;
const HEAD_DY = 3;

/** The hair behind someone facing the room (14×18): it shows past the head
 * and down both sides of the body to the waist, the ends wavy and light. */
const HAIR_FRONT = sprite(`
  ...oooooooo...
  ..o88888888o..
  .o8888888888o.
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o888888888888o
  o988888888889o
  o988888888889o
  o998888888899o
  o99o888888o99o
  o9o.o8888o.o9o
  .o...oooo...o.
`);

/** Standing, from the front (10×9): the ribbon at the collar, the apron's
 * bib and skirt, the arms at her sides, the dress's hem. */
const BODY_STAND = sprite(`
  ..oooooo..
  .o00aa00o.
  o0o0ww0o0o
  o0o0ww0o0o
  oko0ww0oko
  ooo0ww0ooo
  .o0wwww0o.
  o00wwww00o
  oooooooooo
`);
/** The same with the right arm out (BBQ: the tongs' forearm comes from the
 * shoulder). */
const BODY_COOK = sprite(`
  ..oooooo..
  .o00aa00o.
  o0o0ww000o
  o0o0ww000o
  oko0ww000o
  ooo0ww000o
  .o0wwww0o.
  o00wwww00o
  oooooooooo
`);
/** Her forearm on the barbecue tongs, the sleeve her dress, the hand on
 * their handle: down, then up (skins.ts' tongs box, columns 0–4). */
const ARM_TONGS_A = sprite(`
  ooooo
  0000k
  ooooo
`);
const ARM_TONGS_B = sprite(`
  ooook
  0000o
  oooo.
`);
/** Down to the water line: what shows of her wading, her right hand up on
 * the pole. */
const BODY_WADE = sprite(`
  ..oooooo..
  .o00aa00o.
  o0o0ww0oko
  o0o0ww0o0o
  oko0ww0o0o
  ooo0ww0ooo
`);

/** Short legs and navy shoes (12×3), standing; then mid-stride, one foot
 * forward, then the other. */
const LEGS = sprite(`
  ..okkookko..
  ..o00oo00o..
  ..oooooooo..
`);
const LEGS_WALK_A = sprite(`
  ..okkookko..
  .okko..o00o.
  .o00o..oooo.
`);
const LEGS_WALK_B = sprite(`
  ..okkookko..
  .o00o..okko.
  .oooo..o00o.
`);

/** The tail, from the front, poking out from behind her right side: a
 * fluke, then (sitting at ease) swishing up and down. */
const TAIL_FRONT = sprite(`
  ..oo
  .o+o
  o++o
  o+o.
  .o..
`);
const TAIL_FRONT_UP = sprite(`
  .ooo
  o++o
  o+o.
  .o..
  ....
`);
const TAIL_FRONT_DOWN = sprite(`
  ....
  ..oo
  .o+o
  o++o
  oo..
`);

/** Sitting, from the front (12×7): the skirt spread over the seat, the
 * hands in her lap; then her shins and shoes (10×2). */
const BODY_SIT = sprite(`
  ...oooooo...
  ..o00aa00o..
  .o0o0ww0o0o.
  .o0o0ww0o0o.
  .oko0ww0oko.
  o000wwww000o
  oooooooooooo
`);
/** The same, her right hand up on a fishing rod's grip. */
const BODY_SIT_ROD = sprite(`
  ...oooooo...
  ..o00aa00o..
  .o0o0ww0o0o.
  .o0o0ww0oko.
  .oko0ww0oo..
  o000wwww000o
  oooooooooooo
`);
/** Asleep sitting: her arms folded in her lap. */
const BODY_SIT_FOLDED = sprite(`
  ...oooooo...
  ..o00aa00o..
  .o0o0ww0o0o.
  .o0oooooo0o.
  .o0kk00kk0o.
  o000oooo000o
  oooooooooooo
`);
const LEGS_SIT = sprite(`
  .okkookko.
  .o00oo00o.
`);

// ---------- from behind, at a desk ----------

/** The head from behind (16×17): the curl, the headband over the crown
 * with a ribbon in the agent kind's colour at its end, the fins, the hair
 * falling down her back to its light ends. The head itself at HEAD_DX,
 * HEAD_DY. */
const HEAD_BACK = sprite(`
  ........9.......
  .......9.9......
  ................
  ....oooooooo....
  ...oa8a88888o...
  ..owwAwwwwwwwo..
  o.o8a8a888888o.o
  8oo8998888888oo8
  w8o8898888888o8w
  ow8o88888888o8wo
  .oo8888888888oo.
  ..o8888888888o..
  ..o8888888888o..
  ..o8o888888o8o..
  ..o9o888888o9o..
  ..o99o9889o99o..
  ...oo.oooo.oo...
`);
/** Shoulders and back, seated (16×8): the navy dress. */
const BODY_BACK = sprite(`
  ...oooooooooo...
  ..o0000000000o..
  .o000000000000o.
  o00000000000000o
  o0w0000000000w0o
  o0w0000000000w0o
  o0w0000000000w0o
  o00000000000000o
`);
/** A forearm reaching the desk, hand on top, navy sleeve below; a pixel
 * higher, typing; raised over the head. */
const ARM = sprite(`
  ooo
  oko
  o0o
  o0o
  o0o
`);
const ARM_UP = sprite(`
  ooo
  oko
  o0o
  o0o
  o0o
  o0o
`);
const ARM_RAISED = sprite(`
  ooo
  oko
  oko
  ${"o0o\n".repeat(14)}
`);
/** A hand up to the chin. */
const ARM_CHIN = sprite(`
  ooo..
  okko.
  o00o.
  .o00o
  ..o0o
  ..o0o
  ..ooo
`);
/** Arms folded on the desk, for her head to rest on. */
const ARMS_FOLDED = sprite(`
  oooooooooooooooo
  ok000000000000ko
  oooooooooooooooo
`);
/** The tail, poking out past the chair's right side. */
const TAIL_DESK = sprite(`
  ...oo
  ..o+o
  .o++o
  o++o.
  o+o..
  oo...
`);

// ---------- from the side, on a bike ----------

/** The head in profile, facing right (16×14): the curl, the headband over
 * the crown, the near fin out behind the face, the ribbon at the headband's
 * end, the hair streaming back, the eye and the blush at the front. */
const HEAD_SIDE = sprite(`
  ...........99...
  ..........9..9..
  .......oooooooo.
  .....oa8a888888o
  ....o88Awwwwwwwo
  ...o88awaw8w8w8o
  oo.o88888kkkk88o
  o8oo8888kkkkkk8o
  ow8o8888kkkookko
  .ow88888kkk++kko
  o9o88888kkk&kkko
  o998888888kkkko.
  .o99888888okkko.
  ..oo99o99o.okko.
`);
/** The torso, leaning forward from the saddle to the bars (13×7), the
 * apron's edge at the front. */
const BODY_RIDER = sprite(`
  ........oooo.
  ......oo000wo
  ....oo0000wwo
  ..oo000000wo.
  .o0000000wo..
  o0000000wo...
  ooooooooo....
`);
/** The near arm reaching to the bars: the navy sleeve, then the forearm and
 * the hand. */
const ARM_REACH = sprite(`
  o00o.....
  .o00o....
  ..o00o...
  ..okko...
  ...okko..
  ...okko..
  ....okko.
  ....okko.
`);
/** The near leg pedalling: the skirt over the thigh, the shin bare, a navy
 * shoe on the pedal; up at the front (a), then pushed down (b). */
const LEG_UP = `
  .ooooooo....
  o0000000oo..
  o000000ookko
  .ooooooookko
  .......okko.
  .......okko.
  .......okko.
  .......o00o.
  .......ooooo
`;
const LEG_DOWN = `
  .ooooooo....
  o0000000o...
  o00000000o..
  .oooooookko.
  ......okkko.
  ......okkko.
  ......okko..
  ......okko..
  ......okko..
  ......okko..
  ......okko..
  ......o00o..
  ......ooooo.
`;
const LEGS_RIDE_A = sprite(LEG_UP);
const LEGS_RIDE_B = sprite(LEG_DOWN);
/** The far leg, behind the bike, a step behind the near one: in shadow. */
const LEGS_RIDE_FAR_A = sprite(LEG_DOWN.replace(/[0k]/g, "o"));
const LEGS_RIDE_FAR_B = sprite(LEG_UP.replace(/[0k]/g, "o"));
/** The tail from the side, trailing off the saddle behind her. */
const TAIL_SIDE = sprite(`
  oo......
  o+o.....
  .o+ooooo
  .o++++++
  o+oooooo
  oo......
`);

// ---------- small, in another office ----------

/** From behind, small (8×8): the curl, the fins, the hair, her shirt in the
 * agent kind's colour. */
const MINI = sprite(`
  ...8....
  .oooooo.
  8o8888o8
  wo8888ow
  .o8888o.
  oa8888ao
  oa9889ao
  oaaaaaao
`);

// ---------- putting her together ----------

const head = (sprite: typeof HEAD_FRONT, name: string, core: Point) => at(name, sprite, core.x - HEAD_DX, core.y - HEAD_DY);

/** Standing, from the front, in STAND_BOX at (x, feet): hair, the tail,
 * then the body and the head; legs and the body itself as the state needs.
 * The head itself is at (x, feet - 23). */
function standing(x: number, feet: number, body: Placed[], legs: Placed[], tail = true): Placed[] {
  const core = { x, y: feet - 23 };
  return [
    at("whale.hair", HAIR_FRONT, x - 1, feet - 23),
    ...(tail ? [at("whale.tail", TAIL_FRONT, x + 10, feet - 7)] : []),
    ...legs,
    ...body,
    head(HEAD_FRONT, "whale.head.front", core),
  ];
}
const standingBody = (x: number, feet: number, sprite = BODY_STAND) => at("whale.body.standing", sprite, x + 1, feet - 11);
const standingLegs = (x: number, feet: number) => at("whale.legs", LEGS, x, feet - 2);
const standingHead = (x: number, feet: number) => ({ x, y: feet - 23 });

/** The head of someone sitting in SIT_BOX at `place`. */
const sittingHead = (place: Point) => ({ x: place.x + 4, y: place.y - 20 });

/** Sitting, from the front, in SIT_BOX at `place`; `tail` the tail's part(s)
 * (it swishes on some outings). */
function sitting(place: Point, body = BODY_SIT, tail?: Placed[], face = HEAD_FRONT): Placed[] {
  const core = sittingHead(place);
  return [
    at("whale.hair", HAIR_FRONT, core.x - 1, core.y),
    ...(tail ?? [at("whale.tail", TAIL_FRONT, core.x + 11, place.y - 6)]),
    at("whale.legs.seated", LEGS_SIT, core.x + 1, place.y - 1),
    at("whale.body.seated", body, core.x, place.y - 8),
    head(face, face === HEAD_FRONT ? "whale.head.front" : "whale.head.sleep", core),
  ];
}
/** The tail swishing, in `of` frames (frame 0 the still one). */
function swish(x: number, y: number, of: 2 | 3): Placed[] {
  const tails = [TAIL_FRONT, TAIL_FRONT_UP, TAIL_FRONT_DOWN].slice(0, of);
  return tails.map((tail, i) => framed(at(`whale.tail.${"abc"[i]}`, tail, x, y), i, of));
}

// At a desk, from behind: the head itself at (10, 10), 12 wide, the fins to
// x 8 and 23; the shoulders (8, 19); the arms at x 6 and 23, as the person's.

const HEAD_AT = { x: 10, y: 10 };
const LEFT_ARM_X = 6;
const RIGHT_ARM_X = 23;
const MOUSE_AT = { x: 29, y: 16 };

const backBody = () => at("whale.body.back", BODY_BACK, 8, 19);
const backTail = () => at("whale.tail.desk", TAIL_DESK, 21, 23);
const backHead = (dx = 0, dy = 0) => head(HEAD_BACK, "whale.head.back", { x: HEAD_AT.x + dx, y: HEAD_AT.y + dy });
const restingArms = () => [at("whale.arm.left", ARM, LEFT_ARM_X, 16), at("whale.arm.right", ARM, RIGHT_ARM_X, 16)];
const typingArms = () => [
  framed(at("whale.arm.left.up", ARM_UP, LEFT_ARM_X, 15), 0, 2),
  framed(at("whale.arm.right", ARM, RIGHT_ARM_X, 16), 0, 2),
  framed(at("whale.arm.left", ARM, LEFT_ARM_X, 16), 1, 2),
  framed(at("whale.arm.right.up", ARM_UP, RIGHT_ARM_X, 15), 1, 2),
];

function deskParts(gesture: DeskGesture): { parts: Placed[]; head: Point } {
  const upright = { ...HEAD_AT };
  const base = () => [backTail(), backBody(), backHead()];
  switch (gesture) {
    case "type":
      return { parts: [...base(), ...typingArms()], head: upright };
    case "chin":
      return { parts: [...base(), at("whale.arm.left", ARM, LEFT_ARM_X, 16), at("whale.arm.right.chin", ARM_CHIN, 19, 14)], head: upright };
    case "mouse":
      return { parts: [...base(), at("whale.arm.left", ARM, LEFT_ARM_X, 16), at("whale.arm.right", ARM, 26, 16)], head: upright };
    case "ask":
      return {
        parts: [...base(), at("whale.arm.left", ARM, LEFT_ARM_X, 16), at("whale.arm.right.raised", ARM_RAISED, 21, 5)],
        head: upright,
      };
    case "cheer":
      return {
        parts: [...base(), at("whale.arm.left.raised", ARM_RAISED, 8, 5), at("whale.arm.right.raised", ARM_RAISED, 21, 5)],
        head: upright,
      };
    case "slump":
      return { parts: [backTail(), backBody(), backHead(1, 2), ...restingArms()], head: { x: HEAD_AT.x + 1, y: HEAD_AT.y + 2 } };
    case "sleep":
      // Asleep on folded arms: the head down behind the shoulders.
      return {
        parts: [backTail(), at("whale.arms.folded", ARMS_FOLDED, 8, 17), backHead(0, 4), backBody()],
        head: { x: HEAD_AT.x, y: HEAD_AT.y + 4 },
      };
    default:
      return { parts: [...base(), ...restingArms()], head: upright };
  }
}

export const whaleSkin: Skin = {
  id: "whale",

  desk(gesture) {
    const { parts, head: headAt } = deskParts(gesture);
    return { parts, anchors: { head: headAt, mouse: MOUSE_AT } };
  },

  door() {
    const x = 10;
    const feet = 29;
    return { parts: standing(x, feet, [standingBody(x, feet)], [standingLegs(x, feet)]), anchors: { head: standingHead(x, feet) } };
  },

  sofa(_kind, asleep) {
    // Shoes on row 26, centred on the seat; asleep, the head nods down and
    // aside over her folded arms.
    const place = { x: 2, y: 26 };
    if (!asleep) return { parts: sitting(place), anchors: { head: sittingHead(place) } };
    const parts = sitting(place, BODY_SIT_FOLDED, undefined, HEAD_FRONT_SLEEP);
    const nod = { x: 1, y: 2 };
    const headPart = parts[parts.length - 1];
    parts[parts.length - 1] = { ...headPart, x: headPart.x - nod.x, y: headPart.y + nod.y };
    const headAt = sittingHead(place);
    return { parts, anchors: { head: { x: headAt.x - nod.x, y: headAt.y + nod.y } } };
  },

  hike(_kind, { x, y: feet }, walking) {
    const legs = walking
      ? [
          framed(at("whale.legs.walk.a", LEGS_WALK_A, x, feet - 2), 0, 2),
          framed(at("whale.legs.walk.b", LEGS_WALK_B, x, feet - 2), 1, 2),
        ]
      : [standingLegs(x, feet)];
    // No backpack: her hair would hide it.
    return { parts: standing(x, feet, [standingBody(x, feet)], legs), anchors: { head: standingHead(x, feet) } };
  },

  stream(_kind, { x: left, y: feet }) {
    // Her fins reach two columns past the box: at the first step, one in.
    const x = Math.max(left, 2);
    // Under the water from the waist; the tail up out of it behind her.
    return {
      parts: [
        at("whale.hair", HAIR_FRONT, x - 1, feet - 23),
        at("whale.tail", TAIL_FRONT_UP, x + 10, feet - 9),
        at("whale.body.wading", BODY_WADE, x + 1, feet - 11),
        head(HEAD_FRONT, "whale.head.front", standingHead(x, feet)),
      ],
      anchors: { head: standingHead(x, feet), pole: { x: x + 10, y: feet - 10 }, wade: { x: x - 1, y: feet - 6 } },
    };
  },

  fish(_kind, place) {
    const headAt = sittingHead(place);
    // The rod's grip in her right hand, raised a little off her lap.
    const grip = { x: headAt.x + 10, y: place.y - 5 };
    return {
      parts: sitting(place, BODY_SIT_ROD, swish(headAt.x + 11, place.y - 6, 2)),
      anchors: { head: headAt, rod: { x: grip.x, y: grip.y - 20 } },
    };
  },

  camp(_kind, place) {
    const headAt = sittingHead(place);
    return { parts: sitting(place, BODY_SIT, swish(headAt.x + 11, place.y - 6, 3)), anchors: { head: headAt } };
  },

  bbq(_kind, { x, y: feet }) {
    // Her own apron already on: no other. The forearm on the tongs from her
    // right shoulder, in her dress.
    const tongs = { x: x + 10, y: feet - 11 };
    return {
      parts: standing(x, feet, [standingBody(x, feet, BODY_COOK)], [standingLegs(x, feet)]),
      front: [
        framed(at("whale.arm.tongs.a", ARM_TONGS_A, tongs.x, tongs.y), 0, 2),
        framed(at("whale.arm.tongs.b", ARM_TONGS_B, tongs.x, tongs.y - 1), 1, 2),
      ],
      anchors: { head: standingHead(x, feet), tongs },
    };
  },

  cycle(_kind, { x, y }) {
    // Leaning to the bars, the near arm on them, the head in profile, her
    // hair and tail streaming back; the legs pedalling, the far one behind
    // the bike.
    return {
      behind: [
        framed(at("whale.cycle.legs.far.a", LEGS_RIDE_FAR_A, x + 3, y - 3), 0, 2),
        framed(at("whale.cycle.legs.far.b", LEGS_RIDE_FAR_B, x + 3, y - 3), 1, 2),
      ],
      parts: [
        at("whale.tail.side", TAIL_SIDE, x, y - 7),
        framed(at("whale.cycle.legs.a", LEGS_RIDE_A, x + 3, y - 3), 0, 2),
        framed(at("whale.cycle.legs.b", LEGS_RIDE_B, x + 3, y - 3), 1, 2),
        at("whale.body.rider", BODY_RIDER, x + 3, y - 9),
        at("whale.arm.reach", ARM_REACH, x + 10, y - 8),
        at("whale.head.side", HEAD_SIDE, x + 2, y - 22),
      ],
      anchors: { head: { x: x + 6, y: y - 20 } },
    };
  },

  mini: MINI,
};
