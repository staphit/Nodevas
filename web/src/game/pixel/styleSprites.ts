// What changes from one office style to another: the desk top, the chair, the
// meeting table and what hangs on the wall or stands on the floor. Original
// art drawn here, in the same legend as sprites.ts. The monitor, the agents
// and every state glyph are not here: they are the same in every style, so a
// state reads the same in every office. Every desk top is 32×14 (under the
// monitor, at DESK_TOP_Y) and every chair 14 wide, so a style never moves
// what sits on or behind them.

import type { OfficeStyle } from "../world";
import type { Sprite } from "./compile";
import { CHAIR, DESK_TOP_DEFAULT, PLANT, sprite, WINDOW } from "./sprites";

/** Engineering: a steel desk, a thin top, a drawer pedestal on the right. */
export const DESK_TOP_STEEL = sprite(`
  oooooooooooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oddddddddddddddddddddddddddddddo
  oooooooooooooooooooooooooooooooo
  .oo.................oooooooooooo
  .oo.................oDDDDDDDDDDo
  .oo.................oDDDDooDDDDo
  .oo.................oddddddddddo
  .oo.................oooooooooooo
  .oo.................oDDDDDDDDDDo
  .oo.................oDDDDooDDDDo
  .oo.................oddddddddddo
  .oo.................oddddddddddo
  .oo.................oooooooooooo
`);

/** Study: a writing desk, two drawer fronts with brass knobs, turned legs. */
export const DESK_TOP_WRITING = sprite(`
  oooooooooooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oooooooooooooooooooooooooooooooo
  .odddddddddddddoodddddddddddddo.
  .oddddddyddddddooddddddyddddddo.
  .odddddddddddddoodddddddddddddo.
  .oooooooooooooooooooooooooooooo.
  ..odo......................odo..
  ..oDo......................oDo..
  .odddo....................odddo.
  ..oDo......................oDo..
  ..odo......................odo..
  ..odo......................odo..
  .oooo......................oooo.
`);

/** Studio: a trestle table, a thin top on two A-frame legs. */
export const DESK_TOP_TRESTLE = sprite(`
  oooooooooooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oooooooooooooooooooooooooooooooo
  .....dd..................dd.....
  ....d..d................d..d....
  ....d..d................d..d....
  ...d....d..............d....d...
  ...dddddd..............dddddd...
  ..d......d............d......d..
  ..d......d............d......d..
  .d........d..........d........d.
  .d........d..........d........d.
  oo........oo........oo........oo
  ................................
`);

/** Cafe: a round table on a pedestal foot. */
export const DESK_TOP_CAFE = sprite(`
  ....oooooooooooooooooooooooo....
  ..ooDDDDDDDDDDDDDDDDDDDDDDDDoo..
  .oDDDDDDDDDDDDDDDDDDDDDDDDDDDDo.
  .oddddddddddddddddddddddddddddo.
  ..ooddddddddddddddddddddddddoo..
  ....oooooooooooooooooooooooo....
  ..............oddo..............
  ..............oddo..............
  ..............oddo..............
  ..............oddo..............
  ..............oddo..............
  .............oddddo.............
  ..........ooddddddddoo..........
  .........oooooooooooooo.........
`);

/** Engineering: a mesh office chair, the back woven in two shades, on castors. */
export const CHAIR_MESH = sprite(`
  .oooooooooooo.
  oqQqQqQqQqQqQo
  oQqQqQqQqQqQqo
  oqQqQqQqQqQqQo
  oQQQQQQQQQQQQo
  .oooooooooooo.
  ......oo......
  ..oooooooooo..
`);

/** Study: a buttoned wingback chair, taller than the rest. */
export const CHAIR_WINGBACK = sprite(`
  ..oooooooooo..
  .oqqqqqqqqqqo.
  oqqqQqqqqQqqqo
  oqqqqqqqqqqqqo
  oqqqQqqqqQqqqo
  oqqqqqqqqqqqqo
  oQQQQQQQQQQQQo
  .oooooooooooo.
  .oQo......oQo.
  .ooo......ooo.
`);

/** Studio: a round stool on a centre post, no back. */
export const STOOL = sprite(`
  ..............
  ..............
  ..............
  .oooooooooooo.
  oqqqqqqqqqqqqo
  .oQQQQQQQQQQo.
  ......oo......
  ....oooooo....
`);

/** Cafe: a bentwood chair, a curved back of two rails. */
export const CHAIR_BENTWOOD = sprite(`
  ...oooooooo...
  ..oqqqqqqqqo..
  .oqo......oqo.
  .oqo......oqo.
  .oqqqqqqqqqqo.
  ..oQQQQQQQQo..
  ..oo......oo..
  ..o........o..
`);

/** A wooden meeting table, one seat wide: segments side by side make one table. */
export const TABLE_SEG_WOOD = sprite(`
  oooooooooooooooooooooooooooooooo
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  bbbbBbbbbbbbbbbbbbbbBbbbbbbbbbbb
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  ................................
  ................................
  ................................
  ................................
  ................................
  ................................
  ................................
  ................................
`);

/** Engineering: a steel meeting table with a modesty panel. */
export const TABLE_SEG_STEEL = sprite(`
  oooooooooooooooooooooooooooooooo
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  ................................
  ................................
  ................................
`);

/** Studio: a white table with a low shelf. */
export const TABLE_SEG_WHITE = sprite(`
  oooooooooooooooooooooooooooooooo
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  ................................
  ................................
  ................................
  ................................
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  ................................
  ................................
`);

/** Cafe: a counter with a brass foot rail. */
export const TABLE_SEG_CAFE = sprite(`
  oooooooooooooooooooooooooooooooo
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB
  oooooooooooooooooooooooooooooooo
  ................................
  ................................
  ................................
  ................................
  ................................
  LLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLL
  ................................
  ................................
`);

/** Engineering: a whiteboard of boxes and arrows. */
export const WHITEBOARD = sprite(`
  oooooooooooooooooooooooooooooooo
  oTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTo
  oT4444444444444444444444444444To
  oT4111111444444444442222224444To
  oT4144441444444444442444424444To
  oT4144441333333333332444424444To
  oT4144441444444444432444424444To
  oT4111111444444444442222224444To
  oT4444444444444444444444444444To
  oT4444444433333333344444444444To
  oT4444444444444444444444444444To
  oT4433333344444444443333333344To
  oT4444444444444444444444444444To
  oTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTo
  .oooooooooooooooooooooooooooooo.
  ....mmmmmmmmmmmmmmmmmmmmmmmm....
`);

/** Study: a bookshelf, three shelves of spines. */
export const BOOKSHELF = sprite(`
  oooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDo
  oD11223344112233441122Do
  oD11223344112233441122Do
  oD11223344112233441122Do
  oD11223344112233441122Do
  oDddddddddddddddddddddDo
  oD22114433221144332211Do
  oD22114433221144332211Do
  oD22114433221144332211Do
  oD22114433221144332211Do
  oDddddddddddddddddddddDo
  oD33441122334411223344Do
  oD33441122334411223344Do
  oD33441122334411223344Do
  oD33441122334411223344Do
  oDDDDDDDDDDDDDDDDDDDDDDo
  oooooooooooooooooooooooo
`);

/** Studio: a pinboard of colour swatches and a sketch. */
export const PINBOARD = sprite(`
  oooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDo
  oDLLLLLLLLLLLLLLLLLLLLDo
  oDL111LL2222LLuuuuuLLLDo
  oDL111LL2222LLuzzzuLLLDo
  oDL111LL2222LLuuuuuLLLDo
  oDLLLLLLLLLLLLLuzzuLLLDo
  oDLL333LL444LLuuuuuLLLDo
  oDLL333LL444LLLLLLLLLLDo
  oDLL333LL444LL1111LLLLDo
  oDLLLLLLLLLLLL1111LLLLDo
  oDLLLLLLLLLLLLLLLLLLLLDo
  oDDDDDDDDDDDDDDDDDDDDDDo
  oooooooooooooooooooooooo
`);

/** Studio: a large window, four panes. */
export const WINDOW_LARGE = sprite(`
  TTTTTTTTTTTTTTTTTTTTTTTT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWgWWWWWWWWTTWgWWWWWWYWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TTTTTTTTTTTTTTTTTTTTTTTT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWWgWWWWWWWTTWWWWYWWWWWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TWWWWWWWWWWTTWWWWWWWWWWT
  TTTTTTTTTTTTTTTTTTTTTTTT
  oooooooooooooooooooooooo
`);

/** Cafe: a chalkboard menu. */
export const CHALKBOARD = sprite(`
  oooooooooooooooooooooooooooo
  oDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oD111111111111111111111111Do
  oD122212221111111111111111Do
  oD111111111111122222221111Do
  oD121212111111111111111111Do
  oD111111111111112221111111Do
  oD122222221111111111112111Do
  oD111111111111111111121211Do
  oD112221111122222111111111Do
  oD111111111111111111111111Do
  oD111111111111111111111111Do
  oD111111111111111111111111Do
  oDDDDDDDDDDDDDDDDDDDDDDDDDDo
  oooooooooooooooooooooooooooo
  ..dddddddddddddddddddddddd..
`);

/** Cafe: a pendant lamp hanging from the ceiling. */
export const PENDANT_LAMP = sprite(`
  ...oo...
  ...oo...
  ...oo...
  ...oo...
  ..oooo..
  .oLLLLo.
  oLLLLLLo
  oooooooo
  ..LLLL..
  ...LL...
`);

/** Engineering: a water cooler. */
export const WATER_COOLER = sprite(`
  ..oooooo..
  .oWWWWWWo.
  .oWgWWWWo.
  .oWgWWWWo.
  .oWWWWWWo.
  .oWWWWWWo.
  ..oooooo..
  ...o..o...
  .oooooooo.
  ouuuuuuuuo
  ouo3uu2ouo
  ouuuuuuuuo
  ouuuuuuuuo
  ouuuuuuuuo
  ouuuuuuuuo
  ouuuuuuuuo
  ouuuuuuuuo
  ouuuuuuuuo
  oooooooooo
  .oo....oo.
`);

/** Study: a floor lamp with a warm shade. */
export const FLOOR_LAMP = sprite(`
  ..oooo..
  .oLLLLo.
  .oLLLLo.
  oLLLLLLo
  oLLLLLLo
  oooooooo
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ...om...
  ..oomo..
  .oooooo.
  .oooooo.
`);

/** Studio: an easel with a painting on it. */
export const EASEL = sprite(`
  ......oo........
  .....oDDo.......
  ..oooooooooooo..
  ..ouuuuuuuuuuo..
  ..ou11uuuu22uo..
  ..ou111uu222uo..
  ..ouu1uuuu2uuo..
  ..ouuuu33uuuuo..
  ..ouuu3333uuuo..
  ..ouuuu33uuuuo..
  ..ou444uuuuuuo..
  ..ouuuuuuuuuuo..
  ..oooooooooooo..
  ...oDo.oo.oDo...
  ...oDo.oo.oDo...
  ..oDo..oo..oDo..
  ..oDo..oo..oDo..
  .oDo...oo...oDo.
  .oDo...oo...oDo.
  .oDo...oo...oDo.
  oDo....oo....oDo
  oDo....oo....oDo
  oo.....oo.....oo
  ................
`);

/** Cafe: a coffee bar, an espresso machine and cups on a counter. */
export const COFFEE_BAR = sprite(`
  ....oooooooo............
  ....ommmmmmo...oo..oo...
  ....om33mmmo..ouo.ouo...
  ....ommmmmmo..ouo.ouo...
  ....omo..omo...o...o....
  ....ommmmmmo............
  oooooooooooooooooooooooo
  obbbbbbbbbbbbbbbbbbbbbbo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oooooooooooooooooooooooo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oBBBBBBBBBBBBBBBBBBBBBBo
  oooooooooooooooooooooooo
`);

const DESK_TOPS: Record<OfficeStyle, Sprite> = {
  default: DESK_TOP_DEFAULT,
  engineering: DESK_TOP_STEEL,
  study: DESK_TOP_WRITING,
  studio: DESK_TOP_TRESTLE,
  cafe: DESK_TOP_CAFE,
};

const CHAIRS: Record<OfficeStyle, Sprite> = {
  default: CHAIR,
  engineering: CHAIR_MESH,
  study: CHAIR_WINGBACK,
  studio: STOOL,
  cafe: CHAIR_BENTWOOD,
};

const TABLES: Record<OfficeStyle, Sprite> = {
  default: TABLE_SEG_WOOD,
  engineering: TABLE_SEG_STEEL,
  study: TABLE_SEG_WOOD,
  studio: TABLE_SEG_WHITE,
  cafe: TABLE_SEG_CAFE,
};

/** Drawn in turn along the wall, as many as fit. */
const WALL_DECOR: Record<OfficeStyle, readonly Sprite[]> = {
  default: [WINDOW],
  engineering: [WINDOW, WHITEBOARD],
  study: [BOOKSHELF, WINDOW],
  studio: [PINBOARD, WINDOW_LARGE],
  cafe: [CHALKBOARD, PENDANT_LAMP],
};

const FLOOR_DECOR: Record<OfficeStyle, Sprite> = {
  default: PLANT,
  engineering: WATER_COOLER,
  study: FLOOR_LAMP,
  studio: EASEL,
  cafe: COFFEE_BAR,
};

/** Wall decor that hangs from the ceiling rather than sitting on the trim. */
export const HANGING: ReadonlySet<Sprite> = new Set([PENDANT_LAMP]);

export const deskTopOf = (style: OfficeStyle): Sprite => DESK_TOPS[style] ?? DESK_TOP_DEFAULT;
export const chairOf = (style: OfficeStyle): Sprite => CHAIRS[style] ?? CHAIR;
export const tableSegOf = (style: OfficeStyle): Sprite => TABLES[style] ?? TABLE_SEG_WOOD;
export const wallDecorOf = (style: OfficeStyle): Sprite[] => [...(WALL_DECOR[style] ?? WALL_DECOR.default)];
export const floorDecorOf = (style: OfficeStyle): Sprite => FLOOR_DECOR[style] ?? PLANT;
