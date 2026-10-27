// The war room's sprites, drawn for it here (original art, nothing fetched):
// a desk seen from behind its occupant, so the monitor faces the room and
// shows what the agent is doing; the agent from behind, seated; standing at
// the door, from the front; a hat or headset per agent kind; glyphs for the
// screen and bubbles over the desk. One character is one art pixel; the
// legend below names every character's palette token.

import { compileSprite, type Legend, type Sprite } from "./compile";
import type { PixelToken } from "./palette";

export const LEGEND: Legend = {
  o: "outline",
  k: "skin",
  h: "hair",
  e: "eye",
  a: "agent",
  A: "agent-dark",
  p: "pants",
  D: "desk",
  d: "desk-dark",
  m: "monitor",
  x: "screen-off",
  n: "screen-on",
  c: "code",
  C: "code-alt",
  F: "fail",
  K: "ok",
  y: "amber",
  q: "chair",
  Q: "chair-dark",
  w: "bubble",
  i: "bubble-ink",
  z: "ink",
  g: "glare",
  T: "wall-trim",
  W: "window",
  Y: "window-star",
  f: "door",
  v: "doorway",
  b: "table",
  B: "table-dark",
  "1": "decor-a",
  "2": "decor-b",
  "3": "decor-c",
  "4": "decor-d",
  G: "plant",
  H: "plant-dark",
  P: "pot",
  L: "lamp",
  s: "sofa",
  S: "sofa-dark",
  r: "rack",
  R: "rack-dark",
  j: "led-on",
  J: "led-off",
  u: "paper",
};

export const sprite = (grid: string) => compileSprite(grid, LEGEND);

/** A plain rectangle of one token. */
export function solid(w: number, h: number, token: PixelToken): Sprite {
  const d = Array.from({ length: h }, (_, y) => `M0 ${y}h${w}v1h-${w}z`).join("");
  return { w, h, layers: [{ token, d }] };
}

/** Every desk sprite shares this frame: 32×32 art pixels. */
export const CELL = 32;

/** The screen's top-left corner and size inside the desk frame. */
export const SCREEN = { x: 8, y: 2, w: 16, h: 10 };

// ---------- furniture ----------

/** The monitor (its screen left open) and its stand: the same in every
 * office style, so a screen's state reads the same everywhere. Rows 0–14 of
 * the desk frame. */
export const MONITOR = sprite(`
  ................................
  ......oooooooooooooooooooo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......om................mo......
  ......ommmmmmmmmmmmmmmmmmo......
  ......oooooooooooooooooooo......
  ..............ommo..............
`);

/** Where a desk top goes in the desk frame: under the monitor. */
export const DESK_TOP_Y = 15;

/** The default office's desk: top and front, legs. 32×14, like every desk
 * top (pixel/styleSprites.ts has the others). */
export const DESK_TOP_DEFAULT = sprite(`
  oooooooooooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oddddddddddddddddddddddddddddddo
  oddddddddddddddddddddddddddddddo
  oooooooooooooooooooooooooooooooo
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
  .odo........................odo.
`);

/** The chair's back, in front of whoever sits on it. */
export const CHAIR = sprite(`
  .oooooooooooo.
  oqqqqqqqqqqqqo
  oqqqqqqqqqqqqo
  oqqqqqqqqqqqqo
  oQQQQQQQQQQQQo
  .oooooooooooo.
  ......oo......
  ...oooooooo...
`);

/** An open doorway, where an offered run waits to be let in. */
export const DOOR = sprite(`
  oooooooooooooooooo
  offffffffffffffffo
  ${"offvvvvvvvvvvvvffo\n".repeat(25)}
  oooooooooooooooooo
`);

/** A window for the wall: two panes over two, glare by day, a star by night. */
export const WINDOW = sprite(`
  TTTTTTTTTTTTTTTT
  TWWWWWWTTWWWWWWT
  TWgWWWWTTWgWWWWT
  TWWgWWWTTWWgWWWT
  TWWWWWWTTWWWWYWT
  TWWWWWWTTWWWWWWT
  TTTTTTTTTTTTTTTT
  TWWWWWWTTWWWWWWT
  TWYWWWWTTWWWWWWT
  TWWWWWWTTWWWWWWT
  TTTTTTTTTTTTTTTT
  oooooooooooooooo
`);

/** A prompt on the terminal room's wall: people's own machines. */
export const TERMINAL_SIGN = sprite(`
  oooooooooooooooo
  onnnnnnnnnnnnnno
  onncnnnnnnnnnnno
  onnncnnnnnnnnnno
  onnnncnnnnnnnnno
  onnncnnnnnnnnnno
  onncnnnccccnnnno
  onnnnnnnnnnnnnno
  oooooooooooooooo
`);

// ---------- screens (16×10, at SCREEN) ----------

export const SCREEN_OFF = solid(SCREEN.w, SCREEN.h, "screen-off");
export const SCREEN_ON = solid(SCREEN.w, SCREEN.h, "screen-on");
export const SCREEN_FAIL = solid(SCREEN.w, SCREEN.h, "fail");
export const SCREEN_OK = solid(SCREEN.w, SCREEN.h, "ok");

export const GLARE = sprite(`
  gg
  g.
`);

/** Code on the screen; TYPING_A/B add the line being typed. */
export const CODE = sprite(`
  ................
  .cccc.CCCCC.....
  ...CCC.cccccc...
  ...cccccc.......
  .CCC............
`);
export const TYPING_A = sprite(`
  .....ccw........
  ................
`);
export const TYPING_B = sprite(`
  .....cccCC......
  .w..............
`);

/** A document under review, and the magnifier moving over it. */
export const DOC = sprite(`
  ................
  .CCCCCCC........
  .CCCCC..........
  .CCCCCCCC.......
  .CCCC...........
  .CCCCCC.........
`);
export const MAGNIFIER = sprite(`
  .www..
  w...w.
  w...w.
  .www..
  ....y.
  .....y
`);

export const CROSS = sprite(`
  ww...ww
  www.www
  .wwwww.
  ..www..
  .wwwww.
  www.www
  ww...ww
`);

export const CHECK = sprite(`
  .......ww
  ......www
  .....www.
  ww..www..
  wwwwww...
  .wwww....
  ..ww.....
`);

export const HOURGLASS = sprite(`
  yyyyyyy
  .ywwwy.
  ..ywy..
  ...y...
  ..y.y..
  .ywwwy.
  yyyyyyy
`);

/** A clock: waiting for a usage limit to reset. */
export const CLOCK = sprite(`
  ..yyy..
  .y...y.
  y..w..y
  y..ww.y
  y.....y
  .y...y.
  ..yyy..
`);

export const QUESTION = sprite(`
  .yyyy.
  yy..yy
  ....yy
  ..yyy.
  ..yy..
  ......
  ..yy..
`);

// ---------- the agent ----------

/** The head from behind (10×9: hair, ears, neck). */
export const HEAD_BACK = sprite(`
  ..oooooo..
  .ohhhhhho.
  ohhhhhhhho
  ohhhhhhhho
  ohhhhhhhho
  okhhhhhhko
  okhhhhhhko
  .ohhhhhho.
  ...okko...
`);

/** The head from the front, for someone standing at the door. */
export const HEAD_FRONT = sprite(`
  ..oooooo..
  .ohhhhhho.
  ohhhhhhhho
  ohhkkkkhho
  okkekkekko
  okkkkkkkko
  .okkkkkko.
  ..oooooo..
  ...okko...
`);

/** Shoulders and back, seated; the shirt is the agent kind's colour. */
export const BODY_SEATED = sprite(`
  ...oooooooooo...
  ..oaaaaaaaaaao..
  .oaaaaaaaaaaaao.
  oaaaaaaaaaaaaaao
  oaAaaaaaaaaaaAao
  oaAaaaaaaaaaaAao
  oaAaaaaaaaaaaAao
  oaaaaaaaaaaaaaao
`);

/** Standing, from the front: shirt, arms, hands. */
export const BODY_STANDING = sprite(`
  .oooooooooo.
  oaaaaaaaaaao
  oaaaaaaaaaao
  oaoaaaaaaoao
  oaoaaaaaaoao
  oaoaaaaaaoao
  okoaaaaaaoko
  oooaaaaaaooo
  ..oooooooo..
`);

export const LEGS = sprite(`
  ..oppooppo..
  ..oppooppo..
  ..oppooppo..
  .ooppooppoo.
  .oooo..oooo.
`);

/** A forearm reaching the desk: hand on top, sleeve below. */
export const ARM = sprite(`
  ooo
  oko
  oao
  oao
  oao
`);
/** The same arm a pixel higher: the typing hand. */
export const ARM_UP = sprite(`
  ooo
  oko
  oao
  oao
  oao
  oao
`);
/** An arm raised over the head: a question, or a cheer. */
export const ARM_RAISED = sprite(`
  ooo
  oko
  oko
  ${"oao\n".repeat(14)}
`);
/** Arms folded on the desk, for a head to rest on. */
export const ARMS_FOLDED = sprite(`
  oooooooooooooooo
  okaaaaaaaaaaaako
  oooooooooooooooo
`);

// ---------- what each agent kind wears ----------

export interface Headwear {
  sprite: Sprite;
  /** Offset from the head's top-left corner. */
  dx: number;
  dy: number;
}

/** Claude: a knit beanie with a pompom. */
const BEANIE: Headwear = {
  dx: 0,
  dy: -3,
  sprite: sprite(`
    ....oo....
    ...owwo...
    ...owwo...
    ..oooooo..
    .oaaaaaao.
    oaaaaaaaao
    oaAaAaAaAo
    oAAAAAAAAo
  `),
};

/** Codex: headphones. */
const HEADPHONES: Headwear = {
  dx: -1,
  dy: -2,
  sprite: sprite(`
    ...mmmmmm...
    ..m......m..
    .m........m.
    .m........m.
    oo........oo
    oao......oao
    oao......oao
    oao......oao
    oo........oo
  `),
};

/** Gemini: twin buns, tied in its colour. */
const BUNS: Headwear = {
  dx: 0,
  dy: -3,
  sprite: sprite(`
    .oo....oo.
    ohho..ohho
    ohho..ohho
    .aa....aa.
  `),
};

/** OpenCode: a hoodie, hood up. */
const HOOD: Headwear = {
  dx: -1,
  dy: -1,
  sprite: sprite(`
    ...oooooo...
    ..oAAAAAAo..
    .oAAAAAAAAo.
    oAAAAAAAAAAo
    oAAAAaaAAAAo
    oAAAAaaAAAAo
    oAAAAAAAAAAo
    oAAAAAAAAAAo
    .oAAAAAAAAo.
    ..oAAAAAAo..
    ..oooooooo..
  `),
};

/** The hood from the front leaves the face open. */
const HOOD_FRONT: Headwear = {
  dx: -1,
  dy: -1,
  sprite: sprite(`
    ...oooooo...
    ..oAAAAAAo..
    .oAAAAAAAAo.
    oAAAAAAAAAAo
    oAA......AAo
    oAA......AAo
    oAA......AAo
    oAAA....AAAo
    .oAAAAAAAAo.
    ..oAAAAAAo..
    ..oooooooo..
  `),
};

/** Antigravity: a ring floating over the head. */
const HALO: Headwear = {
  dx: 0,
  dy: -5,
  sprite: sprite(`
    ..aaaaaa..
    .a......a.
    ..aaaaaa..
  `),
};

const HEADWEAR: Record<string, Headwear> = {
  claude: BEANIE,
  codex: HEADPHONES,
  gemini: BUNS,
  opencode: HOOD,
  antigravity: HALO,
};

/** What an agent kind (agentColorKey) wears, seen from behind or the front;
 * null for a kind with nothing of its own. */
export function headwearOf(kind: string, view: "back" | "front"): Headwear | null {
  if (kind === "opencode" && view === "front") return HOOD_FRONT;
  return HEADWEAR[kind] ?? null;
}

// ---------- bubbles and props over the desk ----------

/** "!" in an amber bubble: it asked you something. */
export const BUBBLE_ASK = sprite(`
  .ooooooo.
  oyyyyyyyo
  oyyyiyyyo
  oyyyiyyyo
  oyyyiyyyo
  oyyyyyyyo
  oyyyiyyyo
  .ooooooo.
  .oo......
  o........
`);

/** An empty bubble that zzz is written into, a letter per frame. */
export const BUBBLE_SLEEP = sprite(`
  .oooooooooooo.
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  owwwwwwwwwwwwo
  .oooooooooooo.
  ..oo..........
  .o............
`);
export const Z = sprite(`
  iii
  ..i
  .i.
  i..
  iii
`);

/** "…": waiting to be let in. */
export const BUBBLE_WAIT = sprite(`
  .ooooooooo.
  owwwwwwwwwo
  owiwwiwwiwo
  owwwwwwwwwo
  .ooooooooo.
  ..oo.......
  .o.........
`);

/** A sealed envelope on the desk: a held message to approve. */
export const ENVELOPE = sprite(`
  ooooooo
  oiwwwio
  owiwiwo
  owwiwwo
  ooooooo
`);

// ---------- the meeting room (shared by every style) ----------

/** The table's ends, over the first or last segment: a leg each. */
export const TABLE_CAP_L = sprite(`
  .ooo
  obbb
  obbb
  oBBB
  oBBB
  oooo
  .oBo
  .oBo
  .oBo
  .oBo
  .oBo
  .oBo
  .oBo
  .ooo
`);
export const TABLE_CAP_R = sprite(`
  ooo.
  bbbo
  bbbo
  BBBo
  BBBo
  oooo
  oBo.
  oBo.
  oBo.
  oBo.
  oBo.
  oBo.
  oBo.
  ooo.
`);

/** A pull request ready to merge: a folder with a green tab. */
export const PR_FOLDER = sprite(`
  .oooo.......
  oKKKKoooooo.
  oyyyyyyyyyyo
  oyuuuuuuuuyo
  oyuzzzzzuuyo
  oyuuuuuuuuyo
  oyuzzzzuuuyo
  oyuuuuuuuuyo
  oyyyyyyyyyyo
  oooooooooooo
`);

/** A review waiting for a person: a clipboard. */
export const CLIPBOARD = sprite(`
  ...oooo...
  .oomyymoo.
  oddmmmmddo
  oduuuuuudo
  oduzzzuudo
  oduuuuuudo
  oduzzzzudo
  oduuuuuudo
  oduzzuuudo
  oduuuuuudo
  oddddddddo
  oooooooooo
`);

// ---------- the lounge and the server room ----------

/** A two-seat sofa, seen from the front; the seats' middles are at x 13
 * and 33 (Lounge places its sitters there). */
export const SOFA = sprite(`
  ....oooooooooooooooooooooooooooooooooooooooo....
  ...osssssssssssssssssssssssssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  ...osssssssssssssssssssoossssssssssssssssssso...
  oooooSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSooooo
  osssoSSSSSSSSSSSSSSSSSSooSSSSSSSSSSSSSSSSSSossso
  osssossssssssssssssssssoossssssssssssssssssossso
  osssossssssssssssssssssoossssssssssssssssssossso
  osssossssssssssssssssssoossssssssssssssssssossso
  oSSSooooooooooooooooooooooooooooooooooooooooSSSo
  oSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSo
  oSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSo
  oooooooooooooooooooooooooooooooooooooooooooooooo
  .oSo........................................oSo.
  .ooo........................................ooo.
`);

/** A plant in a pot, for a corner. */
export const PLANT = sprite(`
  ....oo......
  ...oGGo.oo..
  .oo.oGGoGGo.
  oGGooHGGGo..
  oGHGGoHGoGo.
  .oGGHGGHGGo.
  ..oHGGHGHo..
  .oGGoHGGGGo.
  .oGo.oHo.oo.
  ..o..oHo....
  .oooooooooo.
  .oPPPPPPPPo.
  ..oPPPPPPo..
  ..oPPPPPPo..
  ..oPPPPPPo..
  ...oooooo...
`);

/** A server rack, a runner: six units with vents and a light each, feet. */
export const RACK = sprite(`
  oooooooooooooooo
  oRRRRRRRRRRRRRRo
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  orRJRJRJRrrrrrro
  orRRRRRRRrrrrrro
  oRRRRRRRRRRRRRRo
  orrrrrrrrrrrrrro
  oooooooooooooooo
  .oRo........oRo.
  .oRo........oRo.
  .ooo........ooo.
`);

/** Where a rack's lights go: power at the top, a slot light per unit. */
export const RACK_POWER_AT = { x: 12, y: 1 };
export const RACK_SLOT_AT = (unit: number) => ({ x: 11, y: 4 + 4 * unit });

/** A rack's light: power on, a slot at work, or off. */
export const LED = solid(2, 1, "led-on");
export const LED_BUSY = solid(2, 1, "ok");
export const LED_OFF = solid(2, 1, "led-off");

/** A sleeping face, from the front: eyes shut. */
export const HEAD_FRONT_SLEEP = sprite(`
  ..oooooo..
  .ohhhhhho.
  ohhhhhhhho
  ohhkkkkhho
  okookkooko
  okkkkkkkko
  .okkkkkko.
  ..oooooo..
  ...okko...
`);

/** Knees and shoes of someone sitting on the sofa. */
export const LEGS_SEATED = sprite(`
  .oppppppppo.
  .oppooooppo.
  .ooo....ooo.
`);

// ---------- a glance into another office (compact) ----------

/** A small desk with a small monitor, 16×16. */
export const MINI_DESK = sprite(`
  ................
  ...oooooooooo...
  ...o........o...
  ...o........o...
  ...o........o...
  ...oooooooooo...
  .......oo.......
  oooooooooooooooo
  oDDDDDDDDDDDDDDo
  oooooooooooooooo
  .od..........do.
  .od..........do.
  .od..........do.
  .od..........do.
  .od..........do.
  .oo..........oo.
`);

/** The small monitor's screen, at MINI_SCREEN_AT. */
export const MINI_SCREEN_AT = { x: 4, y: 2 };
export const MINI_SCREEN_ON = solid(8, 3, "screen-on");
export const MINI_SCREEN_OFF = solid(8, 3, "screen-off");
export const MINI_CODE = sprite(`
  .cc.CCc.
  ..Cccc..
`);

/** An agent from behind, small; the shirt in its kind's colour. */
export const MINI_AGENT = sprite(`
  .oooo.
  ohhhho
  ohhhho
  .oaao.
  oaaaao
  oaaaao
`);

/** A hand up over the small agent: it asked you something. */
export const MINI_HAND = sprite(`
  yy
  oo
  ko
  ao
  ao
`);

/** zz over a small agent asleep on a usage limit. */
export const MINI_Z = sprite(`
  iii
  .i.
  iii
`);

/** … over a small agent waiting to be let in. */
export const MINI_WAIT = sprite(`
  ooooooo
  owiwiwo
  ooooooo
`);
