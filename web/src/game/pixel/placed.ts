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
}

/** A point in a scene's frame, in art pixels. */
export interface Point {
  x: number;
  y: number;
}

export const at = (name: string, sprite: Sprite, x: number, y: number): Placed => ({ name, sprite, x, y });
export const framed = (placed: Placed, index: number, of: number): Placed => ({ ...placed, frame: { index, of } });
