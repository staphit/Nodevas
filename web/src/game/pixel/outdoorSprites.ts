// The outdoors, where an agent goes while its run waits for a usage limit
// to reset (game/outing.ts): a hike up to a summit flag, wading up a stream
// to its falls, fishing from a rock by a lake, a camp with a tent and a fire.
// Each outing is one tile, OUTDOOR_W × OUTDOOR_H art pixels: a backdrop, the
// agent (seen from the front, its kind's headwear from sprites.ts) and the
// props that say which outing it is without colour: a flag, a pole in the
// water, a rod and a bobber, a tent and flames. Original art, in the legend
// of sprites.ts; the backdrops are drawn on the sky, so a tile is whole on
// its own.

import { solid, sprite } from "./sprites";

/** An outing's tile, in art pixels. */
export const OUTDOOR_W = 56;
export const OUTDOOR_H = 44;

// ---------- the agent outdoors ----------

/** Standing, from the front, the backpack's straps over the shirt. */
export const BODY_HIKER = sprite(`
  .oooooooooo.
  oaVaaaaaaVao
  oaVaaaaaaVao
  oaoVaaaaVoao
  oaoaVVVVaoao
  oaoaaaaaaoao
  okoaaaaaaoko
  oooaaaaaaooo
  ..oooooooo..
`);

/** The backpack, behind the body: it shows over the shoulders and at the
 * sides. */
export const BACKPACK = sprite(`
  ..oooooooooo..
  .oVVVVVVVVVVo.
  oVVVVVVVVVVVVo
  oVVVVVVVVVVVVo
  oVVVVVVVVVVVVo
  oVVVVVVVVVVVVo
  oVVVVVVVVVVVVo
  oVVVVVVVVVVVVo
  .oVVVVVVVVVVo.
  ..oooooooooo..
`);

/** Legs mid-stride, one foot forward then the other (12×5, like LEGS). */
export const LEGS_WALK_A = sprite(`
  ..oppooppo..
  .oppo..oppo.
  .oppo..oppo.
  oopo....oppo
  ooo.....oooo
`);
export const LEGS_WALK_B = sprite(`
  ..oppooppo..
  .oppo..oppo.
  .oppo..oppo.
  oppo....opoo
  oooo.....ooo
`);

// ---------- hike: up the trail to the summit flag ----------

export const HIKE_BACKDROP = sprite(`
  --------------------------------------------------------
  -------------------------------------www----------------
  -----------------------------------wwwwww---------------
  ------wwww------------------------wwwwwwww--------------
  -----wwwwwww--------------------------------------------
  ----wwwwwwwwww-------------*----------------------------
  --------------------------***---------------------------
  ------------------------*******-------------------------
  -----------------------MMMMMNNNN------------------------
  ----------*-----------MMMMMMNNNNN-----------------------
  ---------***---------MMMMMMMNNNNNN----------------------
  --------*****------MMMMMMMMMNNNNNNNN-----------*--------
  -------MMMMNNN----MMMMMMMMMMNNNNNNNNN---------***-------
  ------MMMMMNNNN--MMMMMMMMMMMNNNNNNNNNN-------*****------
  ----MMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNN-----MMMMNNN-----
  ---MMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNN--MMMMMNNNN----
  --MMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNN---
  -MMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNN--
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNN-
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNMMMMMMNNNNNNNN
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNNNNoooooooooooooo
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNNNNooo:::::::,,,,,,,
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNNNNooo::::::::::,,,,,,;
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNNNNooo::::::;;;;;;;;;;;;;;
  MMMMMMMMMMMNNNNNNMMMMMMMMMMMNNooo::::::;;;,,,,,,,,,;,,,,
  MMMMMMMMMMMNNNNNNMMMMMMMMMMooo::::::;;;,,,,,,,,oo;,,,,,,
  MMMMMMMMMMMNNNNNNMMMMMMMMoo::::::;;;;,,,,,,,,,oO@o,,,,,,
  MMMMMMMMMMMNNNNNNMMMMMooo:::::;;;,;,,,,,,,,,,;,,,,,,,,,,
  MMMMMMMMMMMNNNNNNMMooo:::::;;;,oo,,,,,,,,,,;,,,,,,,,,,;,
  MMMMMMMMMMMNNNNNooo::::::;;,,,oO@o,,,,,,,;,,,,,,,,,,;,,,
  MMMMMMMMMMMNNooo;:;:::;;;,,,;,,,,,,,,,,;,,,,,,,,,,;,,,,,
  MMMMMMMMMMooo::::;:;;;,,,,;,,,,,,,,,,;,,,,,,,,,,;,,,,,,,
  oooooooooo::::::;;;,,,,,;,,,,,,,,,,;,,,,,,,,,,;,,,,,,,,,
`);

/** Where the hiker is at each step: its body's left edge and the row its
 * feet stand on, up the trail of HIKE_BACKDROP. */
export const HIKE_PATH: readonly { x: number; feet: number }[] = [{"x":2,"feet":42},{"x":7,"feet":40},{"x":12,"feet":39},{"x":17,"feet":37},{"x":22,"feet":35},{"x":27,"feet":33},{"x":32,"feet":32},{"x":37,"feet":30}];

/** The summit's flag: its pole, and the cloth flapping (two frames). */
export const FLAG_POLE = sprite(`
  o
  ${"l\n".repeat(12)}
  o
`);
export const FLAG_POLE_AT = { x: 50, y: 17 };
export const FLAG_A = sprite(`
  ooooo
  FFFFo
  FFFo.
  FFFFo
  ooooo
`);
export const FLAG_B = sprite(`
  oooo.
  FFFFo
  FFFFo
  FFFo.
  oooo.
`);

/** A spark over the summit: it got there. */
export const SPARK = sprite(`
  ..o..
  .oUo.
  oUUUo
  .oUo.
  ..o..
`);

// ---------- stream: wading up to the falls ----------

export const STREAM_BACKDROP = sprite(`
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------wwww--------------------------------
  -------------------wwwwwww------------------------------
  ------------------wwwwwwwwww----------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  ------------------------------*-------------------------
  -----------------------------***------------------------
  --------*-------------------MMMNN-----------------------
  -------***-----------------MMMMNNN----------------------
  ------MMMNN---------------MMMMMNNNN---------oooooooooooo
  ---o-MMMMNNo------o-----MMoMMMMNNNoNN---o---oOOO~==~OOOO
  --o;oMMMMNo;o----o;o---MMo;oMMMNNo;oNN-o;o--oOOO~==~OOOO
  --o;oMMMMNo;oN---o;o--MMMo;oMMMNNo;oNNNo;o--oOO@~==~OO@O
  -o;;;oMMMo;;;oN-o;;;oMMMo;;;oMMNo;;;oNo;;;o-o@OO~==~@OOO
  -o;;;oMMMo;;;oNNo;;;oMMMo;;;oMMNo;;;oNo;;;o-oOOO~==~OOOO
  o;;;;;oMo;;;;;oo;;;;;oMo;;;;;oMo;;;;;o;;;;;ooOOO~==~OOO@
  ooo;oooMooo;oooooo;oooMooo;oooMooo;ooooo;ooooO@O~==~O@OO
  ;;;l;;;;;;;l;;;;;;l;;;;;;;l;;;;;;;l;;;;;l;;;oOOO~==~OOOO
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oOOO~==~OOOO
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oOO@~==~OO@O
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,o@OO~==~@OOO
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oOOO~==~OOOO
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oOOO~==~OOO@
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oO@O~==~O@OO
  ,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,,oOOO~==~OOOO
  ========================================================
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~========~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~oo~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~oO@o~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~ooo~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~oOO@o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~o@@@o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~oooo~~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~oOOO@o~~~~~~~~~~~~~~~~~~~~~~~
  ~~~~~~~~~~~~~~~~~~~~~~~~~~~o@@@@o~~~~~~~~~~~~~~~~~~~~~~~
  @OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@OO@O
`);

/** The open water's ripples, drifting downstream in three frames (from y
 * 30; the first row is the foam under the falls). */
export const STREAM_RIPPLE_A = sprite(`
  ................................................=.=.=...
  .......===.........===.........===.........=............
  ........................................................
  ........................................................
  ....===.........===.........===.........=...............
  ........................................................
  ........................................................
  .===.........===.........===.........===................
  ........................................................
  ........................................................
  =.........===.........===.........===...................
  ........................................................
  ........................................................
`);
export const STREAM_RIPPLE_B = sprite(`
  ...............................................=.=.=....
  .....===.........===.........===.........===............
  ........................................................
  ........................................................
  ..===.........===.........===.........===...............
  ........................................................
  ........................................................
  ==.........===.........===.........===..................
  ........................................................
  ........................................................
  ........===.........===.........===.....................
  ........................................................
  ........................................................
`);
export const STREAM_RIPPLE_C = sprite(`
  ................................................=.=.=...
  ...===.........===.........===.........===..............
  ........................................................
  ........................................................
  ===.........===.........===.........===.................
  ........................................................
  ........................................................
  ..........==.........===.........===....................
  ........................................................
  ........................................................
  ......===.........===...........=.........==............
  ........................................................
  ........................................................
`);
export const STREAM_RIPPLE_Y = 30;

/** Where the water meets the wader: over its body's last rows, so it is up
 * to its waist. */
export const WADE = sprite(`
  ==~~~~~~~~~~==
  ~~~~~~~~~~~~~~
  .=..........=.
`);

/** The walking pole, from the hand down into the water. */
export const POLE = solid(1, 16, "wood");

// ---------- fish: a rod from a rock, the bobber, the catch ----------

export const FISH_BACKDROP = sprite(`
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------wwww--------------------
  -------------------------------wwwwwww------------------
  ------------------------------wwwwwwwwww----------------
  -----------www------------------------------------------
  ---------wwwwww-----------------------------------------
  --------wwwwwwww----------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  ----------------------------------------M---------------
  --------------------------------------MMMNN-------------
  -------------------------------------MMMMNNN------------
  ------------------M-----------------MMMMMNNNN-----------
  -----------------MMN--------------MMMMMMMNNNNNN---------
  ---------------MMMMNNN-----------MMMMMMMMNNNNNNN--------
  --------------MMMMMNNNN--------MlMlMMMMMMNNNNNNNNN------
  -------------MMMMMMNNNNN------MMlMllMMMMMNNNNNNNNNN-----
  ------------MMMMMMMNNNNNN---MMMMllllMMMMMNNNNNNNNNNNN---
  ----------MMMMMMMMMNNNNNNNNMMMMM;l;lMMMMMNNNNNNNNNNNNN--
  ;;;;;;;;;;;;;;;;;;;NNNNNNNNMMMMMMMM;MMMMMNNNNNNNNNNNNNN-
  ;;;;;;;;;;;;;;;;;;o=====================================
  ,,,,,,;,,,,,,,,;,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,;,,,,,,,,;,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,;,,,,,,,,;,,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ;,,,,,,,,;,,,,,,,,;,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,,,;,,,,,,,,;,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,;,,,,,,,,;,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,;,,,,,,,,;,,,,,,,,;o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,;,,,,,,,,;,,,,,,,,;,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,,,,;,,,,,,,,;,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,oooooooooooo,,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,oOOOOOOOOOOO@o,,,,,;,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,oOOOOOOOOOOO@@@o,,;,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ;,o@@@@@@@@@@@@@@o;,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,oooooooooooooo,,,,,,,,;o~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,;,,,,,,,,;,,,,,,,,;,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,;,,,,,,,,;,,,,,,,,;,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,;,,,,,,,,;,,,,,,,,;,,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,,,,;,,,,,,,,;,,,,,,,,;,,o~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,,,;,,,,,,,,;,,,,,,,,;,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~~
  ,,,,;,,,,,,,,;,,,,,,,,;,,,,,,,o~~~~~~~~~~~~~~~~~~~~~~~~~
`);

/** The rod, from the hand at its bottom left up to its tip at the top
 * right, where the line hangs. */
export const ROD = sprite(`
  ......................l
  .....................l.
  ....................l..
  ...................l...
  ..................l....
  ................ll.....
  ...............l.......
  ..............l........
  .............l.........
  ............l..........
  ...........l...........
  ..........l............
  .........l.............
  ........l..............
  .......l...............
  .....lll...............
  ....lll................
  ...ll..................
  ..ll...................
  .ll....................
  ll.....................
`);
export const LINE = solid(1, 12, "ink");

/** The bobber, afloat and then dipped. */
export const BOBBER = sprite(`
  .oo.
  oFFo
  owwo
  .oo.
`);
/** Rings on the water round the bobber, out and back. */
export const RING_A = sprite(`
  ==......==
`);
export const RING_B = sprite(`
  .==....==.
`);

/** A fish under the water by the bobber: one is biting. */
export const FISH = sprite(`
  ..ooo..o
  .oyyyyoo
  oyiyyyyo
  .oyyyyoo
  ..ooo..o
`);

/** The bucket by the rock, and a fish's tail sticking out of it per catch. */
export const BUCKET = sprite(`
  ooooooo
  oiiiiio
  ommmmmo
  ommmmmo
  .ommmo.
  .ooooo.
`);
export const TAIL = sprite(`
  oyo
  .y.
  .y.
`);

// ---------- camp: a tent, a fire, the sun (the moon) going over ----------

export const CAMP_BACKDROP = sprite(`
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  -------o-----------------------o----------------------o-
  ------o;o----------o----------o;o----------o---------o;o
  -o----o;o---------o;o---------o;o----o----o;o--------o;o
  o;o--o;;;o---o----o;o----o---o;;;o--o;o---o;o----o--o;;;
  o;o--o;;;o--o;o--o;;;o--o;o--o;;;o--o;o--o;;;o--o;o-o;;;
  ;;;oo;;;;;o-o;o--o;;;o--o;o-o;;;;;oo;;;o-o;;;o--o;oo;;;;
  ;;;oooo;oooo;;;oo;;;;;oo;;;oooo;oooo;;;oo;;;;;oo;;;ooo;o
  ;;;;o--N---o;;;oooo;oooo;;;o---N--o;;;;;ooo;oooo;;;o--N-
  o;ooo--N--o;;;;;o--N--o;;;;;o--N--ooo;ooo--N--o;;;;;o-N-
  NNNNNNNNNNooo;oooNNNNNooo;oooNNNNNNNNNNNNNNNNNooo;oooNNN
  NNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNN
  NNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNNN
  ;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;
  ;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,
  ,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,
  ,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,
  ,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,
  ,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,
  ,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,
  ,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;
  ,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,
  ,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,
  ,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,
  ,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,
  ,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,
  ,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,
  ;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,oo,oo,oo,;,,,
  ,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,o@Oo@Oo@Oo,,,,
  ,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,oo,oo,oo,,;,,
  ,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,
  ,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,
`);

export const TENT = sprite(`
  ...........o...........
  ..........ooo..........
  ..........ooo..........
  .........otoEo.........
  .........otoEo.........
  ........ottoEEo........
  ........ottoEEo........
  .......otttoEEEo.......
  ......ottttoEEEEo......
  ......otttoooEEEo......
  .....ottttoooEEEEo.....
  .....ottttoooEEEEo.....
  ....ottttoioioEEEEo....
  ...otttttoioioEEEEEo...
  ...otttttoioioEEEEEo...
  ..otttttoiioiioEEEEEo..
  ..otttttoiioiioEEEEEo..
  .ottttttoiioiioEEEEEEo.
  .otttttoiiioiiioEEEEEo.
  ooooooooooooooooooooooo
`);

/** A log to sit on by the fire. */
export const LOG = sprite(`
  .oooooooooooooo.
  ollllllllllllllo
  ollllllllllllllo
  .oooooooooooooo.
`);

/** The fire's logs, crossed, and its flames in three frames. */
export const FIRE_LOGS = sprite(`
  ooo....ooo
  ollooooolo
  .oooooooo.
`);
export const FLAME_A = sprite(`
  ...^....
  ..^^^...
  ..^!^.^.
  .^^!^^^.
  .^!!!^^.
  ^^!!!!^^
  ^!!!!!!^
  .^!!!!^.
`);
export const FLAME_B = sprite(`
  ....^...
  ...^^^..
  .^.^!^..
  .^^^!^^.
  .^^!!!^.
  ^^!!!!^^
  ^!!!!!!^
  .^!!!!^.
`);
export const FLAME_C = sprite(`
  ........
  ..^..^..
  ..^^.^^.
  .^^!^^^.
  .^!!!!^.
  ^^!!!!^^
  ^!!!!!!^
  .^!!!!^.
`);

/** The sun by day, the moon by night (the token's dark value). */
export const SUN = sprite(`
  ..ooo..
  .oUUUo.
  oUUUUUo
  oUUUUUo
  oUUUUUo
  .oUUUo.
  ..ooo..
`);
/** Where the sun is at each step: up over the camp and down again. */
export const SUN_PATH: readonly { x: number; y: number }[] = [
  { x: 2, y: 6 },
  { x: 8, y: 4 },
  { x: 14, y: 2 },
  { x: 20, y: 1 },
  { x: 26, y: 1 },
  { x: 32, y: 2 },
  { x: 38, y: 4 },
  { x: 44, y: 6 },
];
