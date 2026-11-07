// The outdoors, where an agent goes while its run waits for a usage limit
// to reset (game/outing.ts): a hike up to a summit flag, wading up a stream
// to its falls, fishing from a rock by a lake, a camp with a tent and a fire,
// a barbecue at a campsite, a bike ride along a road to a finish flag.
// Each outing is one tile, OUTDOOR_W × OUTDOOR_H art pixels: a backdrop, the
// agent (seen from the front, or from the side on the bike, its kind's
// headwear from sprites.ts) and the props that say which outing it is
// without colour: a flag, a pole in the water, a rod and a bobber, a tent
// and flames, a grill with smoke and a plate, a bike's wheels and a
// chequered flag. Original art, in the legend of sprites.ts; the backdrops
// are drawn on the sky, so a tile is whole on its own.

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

// ---------- bbq: a grill at the campsite, the skewers cooking ----------

export const BBQ_BACKDROP = sprite(`
  --------------------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  -----wwww--------------------www------------------------
  ----wwwwwww----------------wwwwwwww---------------------
  ---wwwwwwwwww-------------wwwwwwwwwwww------------------
  -------------ooooo--------------------------------------
  -----------oo,,,,,oo------------------------------------
  --ooooo---oo,,,,,,,oo-----------------------------------
  oo,,,,,oo-o,,,,,,,,,o-----------------------------------
  o,,,,,,,oo,,,,,,,,,;;o--ooooo---------------------------
  ,,,,,,,,,o,,,,,,,,;;;o-o,,,,,o-----------------------ooo
  ,,,,,,,,;o,,,,,,,;;;;oo,,,,,,,o---------------------o,,,
  ,,,,,,,;;o,,,,,,;;;;;o,,,,,,,,;o-------------------o,,,,
  ,,,,,,;;;o,,,,,;;;;;;o,,,,,,,;;o------------------o,,,,,
  ,,,,,;;;;;o,,,;;;;;;oo,,,,,,;;;o-ooo--------------o,,,,,
  ,,,,;;;;;;oo,;;;;;;ooo,,,,,;;;;oo,,,o-------------o,,,,,
  ,,,;;;;;;o-oo;;;;;oo-o,,,,;;;;;o,,,,,o------------o,,,,,
  o,;;;;;;oo---ooooo----o,,;;;;;oo,,,,;o------------o,,,,;
  oo;;;;;oo-----olo------o;;;;;o-o,,,;;o-------------o,,;;
  -ooooooo-----oolo--o----ooooo--oo,;;oo-----o-----o--o;;;
  o;ooloo;o---o;olo-o;o---oolo--o;ooooo;o---o;o---o;o--ooo
  ;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;
  ;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;
  ,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,
  ,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,
  ;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,
  ,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;
  ,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,
  ,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,
  ,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,
  ,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,
  ,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,
  ,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,
  ,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,
  ,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,,,,,,,,,,,;,,
  ,,,,;,,::,::,::,::,::,::,::,::,::,::,::,::,::,::,,,,,,,,
  ,,,,,::::O::::::::::O::::::::::O::::::::::O:::::::,,,,,,
  ,,,O::::::::::O::::::::::O::::::::::O::::::::::O::::,,,,
  ;:::::::O::::::::::O::::::::::O::::::::::O::::::::::O:,,
  ::O::::::::::O::::::::::O::::::::::O::::::::::O:::::::::
  :::::::O::::::::::O::::::::::O::::::::::O::::::::::O::::
  :O::::::::::O::::::::::O::::::::::O::::::::::O::::::::::
  ::::::O::::::::::O::::::::::O::::::::::O::::::::::O:::::
`);

/** The grill on its legs: the grate, the coals glowing under it, the box. */
export const GRILL = sprite(`
  oooooooooooooooooooooooo
  oOoOoOoOoOoOoOoOoOoOoOOo
  o!^!^^!^!^^!^!^^!^!^^!^o
  o@@@@@@@@@@@@@@@@@@@@@@o
  .o@@@@@@@@@@@@@@@@@@@@o.
  ..oooooooooooooooooooo..
  ...o@o............o@o...
  ...o@o............o@o...
  ...o@o............o@o...
  ...o@o............o@o...
  ..oo@oo..........oo@oo..
  ..ooooo..........ooooo..
`);

/** A skewer on the grate, meat and a pepper on a stick, as done as the wait
 * is long: raw (plain), seared (a grill mark across), done (browned, two
 * marks each way). */
export const SKEWER_RAW = sprite(`
  .ooooooooo.
  .o&&&o,,,o.
  lo&&&o,,,ol
  .o&&&o,,,o.
  .ooooooooo.
`);
export const SKEWER_SEARED = sprite(`
  .ooooooooo.
  .o&&&o,,,o.
  lo#o#o;o;ol
  .o&&&o,,,o.
  .ooooooooo.
`);
export const SKEWER_DONE = sprite(`
  .ooooooooo.
  .oo##o;o;o.
  lo#o#oo;ool
  .o##oo;;oo.
  .ooooooooo.
`);

/** The tongs out over the grill: down on a skewer, then up, turning it.
 * Their box's first five columns are the forearm's, the skin's own (its hand
 * at column 4, on the handle's row: 1 down, 2 up); the tongs alone here. */
export const TONGS_A = sprite(`
  .........
  .....OOOO
  ........O
  ........O
`);
export const TONGS_B = sprite(`
  ......OO.
  .....oO..
  .....o...
  .........
  .........
`);
/** The person's forearm on the tongs, in the shirt's colour, the hand on
 * their handle: down (at the tongs' corner), and up (a row lower than
 * theirs, TONGS_B's rows 2–4). */
export const ARM_TONGS_A = sprite(`
  ooooo
  aaaak
  ooooo
`);
export const ARM_TONGS_B = sprite(`
  ooook
  aaaao
  oooo.
`);

/** An apron over the shirt: the cook. */
export const APRON = sprite(`
  .oooo.
  .o**o.
  o****o
  o****o
  o****o
  oooooo
`);

/** Smoke off the grill, puffs rising in three frames. */
export const SMOKE_A = sprite(`
  ...O........
  ..OwO.......
  ...O........
  ............
  ............
  .......OO...
  ......OwwO..
  ......OwwO..
  .......OO...
  ............
  ............
  ....OOO.....
  ...OOwwO....
  ...OwwwO....
  ....OOO.....
  ............
`);
export const SMOKE_B = sprite(`
  .......O....
  ......OwO...
  .......O....
  ............
  ...OO.......
  ..OwwO......
  ..OwwO......
  ...OO.......
  ............
  ............
  .......OOO..
  ......OOwwO.
  ......OwwwO.
  .......OOO..
  ............
  ............
`);
export const SMOKE_C = sprite(`
  ............
  ............
  ............
  .........O..
  ........OwO.
  .........O..
  ............
  ......OO....
  .....OwwO...
  .....OwwO...
  ......OO....
  ............
  ...OO.......
  ..OwwO......
  ..OwwO......
  ...OO.......
`);

/** The folding table, a plate on it, the cooler under it. */
export const CAMP_TABLE = sprite(`
  oooooooooooooo
  ollllllllllllo
  oooooooooooooo
  .o..........o.
  .o..........o.
  .o..........o.
  .o..........o.
  .o..........o.
  .o..........o.
  oo..........oo
`);
export const PLATE = sprite(`
  .oooooooooo.
  o**********o
  .oooooooooo.
`);
/** A done skewer on the plate, one more per stretch of the wait. */
export const PLATED = sprite(`
  .oooooooo.
  l##o;;o##l
  .oooooooo.
`);
export const COOLER = sprite(`
  ooooooooo
  o*******o
  ooooooooo
  o~~~~~~~o
  o~~ooo~~o
  ooooooooo
`);

// ---------- cycle: along the road to the finish flag ----------

export const CYCLE_BACKDROP = sprite(`
  --------------------------------------------------------
  --------------------------------------------------------
  ------------------------------wwwww---------------------
  ----------------------------wwwwwwwwww------------------
  -------wwww----------------wwwwwwwwwwwwww---------------
  -----wwwwwwwww------------------------------------------
  --------------------------------------------------------
  --------------------------------------------------------
  -----------------------------------------------www------
  ----------------------------------------------wwwwww----
  -----o--------------------------------------------------
  ----o,o-------------------------------------------------
  ----o,o-----------o-------------------------------------
  -----looooooo----o,o---------------------------o--------
  ----oo;;;;;;;oo--o,o--------------------------o,o-------
  --oo;;;;;;;;;;;oo-l---------------------------o,o-------
  oo;;;;;;;;;;;;;;;oo----------------------------l--------
  ;;;;;;;;;;;;;;;;;;;oo------------o----------ooooooo-----
  ;;;;;;;;;;;;;;;;;;;;;o----------o,o-----oooo;;;;;;;ooo--
  ;;;;;;;;;;;;;;;;;;;;;;oo--------o,o---oo;;;;;;;;;;;;;;oo
  ;;;;;;;;;;;;;;;;;;;;;;;;oo-------l-ooo;;;;;;;;;;;;;;;;;;
  ;;;;;;;;;;;;;;;;;;;;;;;;;;ooooooooo;;;;;;;;;;;;;;;;;;;;;
  ;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;;
  ;;;;;;;;;;;;;;;;;;;;;;;;;;;oooo;;;;;;;;;;;;;;;;;;;;;;;;;
  ooooo;;;;;;;;;;;;;;;;;;oooo,,,,oooo;;;;;;;;;;;;;;;;;;;;;
  ,,;,,ooo;;;;;;;;;;;;ooo,,,,,,,,,,,,oo;;;;;;;;;;;;;;;;;;;
  ,,,,,,,,ooooo;;;oooo,,,,,,,,,,,,;,,,,oo;;;;;;;;;;;;;;;;;
  ,,,,,,,,,,,;,ooo,,,,,,,,,,,,;,,,,,,,,,,oo;;;;;;;;;;;;;oo
  ,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,,,,,,,,,,,,oo;;;;;;;;;oo,,
  ,,,;,,,,,,,,,,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,ooooooooo,,;,
  ,,,,,,,,,,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,
  ,,,,,,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,,,,,,,,,,,,;,,,,,,,,,
  oooooooooooooooooooooooooooooooooooooooooooooooooooooooo
  OOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOO
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  ****@@@@****@@@@****@@@@****@@@@****@@@@****@@@@****@@@@
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  @@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
  OOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOO
  oooooooooooooooooooooooooooooooooooooooooooooooooooooooo
  ;,,,,,,,,;,,,,,,,,;,,,,,,,,;,,,,,,,,;,,,,,,,,;,,,,,,,,;,
`);

/** The bike's frame, from the side: saddle, bars, tubes; the hubs on its
 * bottom row, where the wheels' centres go. */
export const BIKE = sprite(`
  ..............oooo....
  ......oooo.....^......
  ........o......^......
  ........^^^^^^^^^.....
  .......^^.....^.^.....
  ......^..^...^..^.....
  ......^..^..^...^.....
  .....^....^^.....^....
  ....o^^^^^^......o....
`);
/** Where the wheels' hubs are on BIKE's bottom row. */
export const BIKE_HUBS = { back: 4, front: 17 };

/** A wheel, its spokes turning in two frames: + then ×. Solid inside, so
 * the kerb and road behind it never show through, its spokes in snow so they
 * stand out from the rock-dark inside in either theme. */
export const WHEEL_A = sprite(`
  ..ooooo..
  .o@@*@@o.
  o@@@*@@@o
  o@@@*@@@o
  o*******o
  o@@@*@@@o
  o@@@*@@@o
  .o@@*@@o.
  ..ooooo..
`);
export const WHEEL_B = sprite(`
  ..ooooo..
  .o@@@@@o.
  o@*@@@*@o
  o@@*@*@@o
  o@@@*@@@o
  o@@*@*@@o
  o@*@@@*@o
  .o@@@@@o.
  ..ooooo..
`);

// The rider, from the side, facing the way the bike goes (HEAD_SIDE and its
// headwear on top): leaning over the top tube, a hand on the bars, the seat
// of the pants on the saddle and the feet on the pedals.

/** Where the saddle's top and the bars' top are on BIKE: what the rider
 * sits on and holds. */
export const BIKE_SADDLE = { x: 6, y: 1, w: 4 };
export const BIKE_BARS = { x: 14, y: 0, w: 4 };

/** The torso, leaning forward from the hips on the saddle to the shoulders
 * over the bars; the back's shading in the kind's dark colour. */
export const BODY_RIDER = sprite(`
  ........oooo.
  ......ooaaaao
  ....ooAaaaaao
  ..ooAaaaaaao.
  .oAaaaaaaao..
  oAaaaaaaao...
  ooooooooo....
`);

/** The near arm reaching forward and down to the bars: a sleeve, then the
 * forearm and the hand. */
export const ARM_REACH = sprite(`
  oAAo.....
  .oAAo....
  ..oAAo...
  ..okko...
  ...okko..
  ...okko..
  ....okko.
  ....okko.
`);

/** The near leg, pedalling, its pedal turning with the wheels: the thigh
 * level, knee up and the foot on the pedal at the front (a), then the leg
 * pushed down, the foot at the bottom (b). The seat of the pants rests on
 * the saddle. */
const LEG_UP = `
  .ooooooo....
  opppppppoo..
  opppppppppo.
  .oooooooppo.
  .......oppo.
  .......oppo.
  .......oppo.
  .......oppo.
  .......ooooo
`;
const LEG_DOWN = `
  .ooooooo....
  opppppppo...
  oppppppppo..
  .oooopppppo.
  ......opppo.
  ......opppo.
  ......oppo..
  ......oppo..
  ......oppo..
  ......oppo..
  ......oppo..
  ......oppo..
  ......ooooo.
`;
export const LEGS_RIDE_A = sprite(LEG_UP);
export const LEGS_RIDE_B = sprite(LEG_DOWN);
/** The far leg, behind the bike, a step behind the near one (down while it
 * is up): in shadow, all outline. */
export const LEGS_RIDE_FAR_A = sprite(LEG_DOWN.replace(/p/g, "o"));
export const LEGS_RIDE_FAR_B = sprite(LEG_UP.replace(/p/g, "o"));

/** The finish: a chequered flag by the road. */
export const CHEQUER = sprite(`
  ooooo
  o*o*o
  *o*o*
  o*o*o
  ooooo
`);
