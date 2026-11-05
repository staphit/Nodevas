// What a working agent's screen shows it doing, from the last tool it called
// (game/activity.ts): a page scrolling by (read), code being typed (edit:
// sprites.ts CODE and TYPING_A/B), a terminal prompt with its cursor
// blinking (shell), a search box with the magnifier sweeping its results
// (search), a globe turning (web), and for thinking, a thought bubble over
// the head. Each one is a shape of its own, so it reads without its colours.
// Original art, in the legend of sprites.ts; screens are 16×10 (SCREEN).

import { solid, sprite } from "./sprites";

// ---------- read: a page, its lines scrolling up ----------

/** The page and the scroll bar's track. */
export const PAGE = sprite(`
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
  .uuuuuuuuuuu..m.
`);
/** The page's lines, then the same a row further up: it scrolls. */
export const PAGE_LINES_A = sprite(`
  ................
  ..iiiiiiii......
  ................
  ..iiiiii........
  ................
  ..iiiiiiiii.....
  ................
  ..iiii..........
  ................
  ..iiiiiii.......
`);
export const PAGE_LINES_B = sprite(`
  ..iiiiiiii......
  ................
  ..iiiiii........
  ................
  ..iiiiiiiii.....
  ................
  ..iiii..........
  ................
  ..iiiiiii.......
  ................
`);
/** The scroll bar's thumb, at PAGE's track (x 14). */
export const SCROLL_THUMB = solid(1, 3, "bubble");

// ---------- shell: a terminal, a prompt and its cursor ----------

/** Output above the prompt. */
export const TERMINAL_LINES = sprite(`
  ................
  .cc.cccc.cc.....
  .CCCCCC.........
  .cc.ccccc.CC....
`);
/** ">": the prompt, at the screen's left under the output. */
export const PROMPT = sprite(`
  w.
  .w
  w.
`);
/** "_": the cursor after the prompt, on for half the loop. */
export const CURSOR = solid(2, 1, "bubble");

// ---------- search: a search box, results, the magnifier sweeping ----------

export const SEARCH_BOX = sprite(`
  .wwwwwwwwwwwww..
  .w.iii.ii....w..
  .wwwwwwwwwwwww..
`);
export const SEARCH_RESULTS = sprite(`
  .CCCCCC.........
  .cccccccccc.....
  ................
  .CCCCC..........
  .cccccccc.......
`);

// ---------- web: a globe turning ----------

/** The globe's disc: its rim and its sea; GLOBE_LAND_A/B/C turn on it. */
export const GLOBE = sprite(`
  ..wwww..
  .wCCCCw.
  wCCCCCCw
  wCCCCCCw
  wCCCCCCw
  wCCCCCCw
  .wCCCCw.
  ..wwww..
`);
export const GLOBE_LAND_A = sprite(`
  ........
  ..cc....
  .ccc..c.
  .cc..cc.
  ..c..cc.
  .....c..
  ..cc....
  ........
`);
export const GLOBE_LAND_B = sprite(`
  ........
  ....cc..
  .c..ccc.
  .cc.cc..
  .cc..c..
  .c......
  ....cc..
  ........
`);
export const GLOBE_LAND_C = sprite(`
  ........
  ....c...
  ..c...c.
  .ccc..c.
  ..cc..c.
  ..c.....
  ........
  ........
`);
/** The address bar over the globe. */
export const ADDRESS_BAR = sprite(`
  .wwwwwwwwwwwwww.
`);

/** A mouse on the desk, under the hand that browses or searches. */
export const MOUSE = sprite(`
  .o.
  ooo
  omo
  ooo
`);

// ---------- think: a thought bubble, a hand on the chin ----------

/** A cloud of a bubble trailing small rounds down to the head; THOUGHT_DOT
 * writes "…" into it, a dot per frame. */
export const THOUGHT = sprite(`
  .ooooooooooo.
  owwwwwwwwwwwo
  owwwwwwwwwwwo
  owwwwwwwwwwwo
  owwwwwwwwwwwo
  .owwwwwwwwwo.
  ..ooooooooo..
  ...oo........
  ..owwo.......
  ...oo........
  .oo..........
  .oo..........
`);
export const THOUGHT_DOT = solid(2, 2, "bubble-ink");

/** The right forearm, elbow on the desk, the hand up at the chin: reading or
 * thinking, not typing. Seen from behind, the hand at the head's side. */
export const ARM_CHIN = sprite(`
  ooo..
  okko.
  oaao.
  .oaao
  ..oao
  ..oao
  ..ooo
`);
