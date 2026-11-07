// A scene is a list of sprites at places in its frame (warroom/deskArt.ts
// builds them, PixelSprite paints them). Shared by the scenes and the
// characters' skins (skins.ts), which hand the scenes their figure's parts.

import type { Sprite } from "./compile";

/** A sprite at a place in a scene's frame (the 32×32 desk frame, a 24×32
 * sofa seat, a 56×44 outdoor tile…). With `frame`, it is one of `of` frames
 * that take turns (CSS steps), frame 0 the one shown still. */
export interface Placed {
  /** What it is, for tests and debugging: "screen.fail", "arm.raised"… */
  name: string;
  sprite: Sprite;
  x: number;
  y: number;
  frame?: { index: number; of: number };
  /** An anime figure (anime/figure.ts) instead of pixels: `sprite` is then
   * empty, the size of the window onto its frame, and x, y may fall
   * between art pixels (on the frame's own pixels). */
  anime?: AnimeArt;
}

/** What draws an anime figure: its action's row of a sheet, the frames
 * taking turns along it (CSS steps), seen through a window of its frame. */
export interface AnimeArt {
  /** The sheet image's URL. */
  href: string;
  /** The sheet's size, and one frame's, in the sheet's own px. */
  sheetW: number;
  sheetH: number;
  frameW: number;
  frameH: number;
  /** The action's row, its frames along it from the left. */
  row: number;
  frames: number;
  /** One loop of the frames, in ms. */
  loopMs: number;
  /** The part of the frame the scene shows (frame px): all of it unless it
   * reaches past the scene's edge. */
  window: { x: number; y: number; w: number; h: number };
  /** Frame 0 only, whatever the motion (an offline agent holds still). */
  still: boolean;
}

/** A point in a scene's frame, in art pixels. */
export interface Point {
  x: number;
  y: number;
}

export const at = (name: string, sprite: Sprite, x: number, y: number): Placed => ({ name, sprite, x, y });
export const framed = (placed: Placed, index: number, of: number): Placed => ({ ...placed, frame: { index, of } });
