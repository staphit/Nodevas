// A character drawn from its anime sheet where the skin's figure would be
// (warroom/deskArt.ts asks for it, state by state): one part, the action's
// frame, placed by its anchor on the scene's point for the state (seated, its
// seat contact on the seat's top) and kept to the scene's frame; the skin's
// anchors for the props, the head's from the sheet's head box, a prop's from
// the tip the sheet gives for it; the scene's props the render already has
// left out. Without the sheet or the action, it is the skin's figure as it
// was.

import { ROD } from "../pixel/outdoorSprites";
import type { Placed, Point } from "../pixel/placed";
import type { Figure, PropAnchor } from "../pixel/skins";
import type { AnimeActionName } from "./actions";
import { SOLES_Y, type AnimeSheet } from "./manifest";

/** The scene's parts a manifest's prop stands for: part names, or the
 * start of them (ending in "."). A desk action's "desk" is its whole desk
 * unit: the monitor and its screen, the desk top, the chair and the mouse
 * (a meeting table is the room's, and stays). */
export const PROP_PARTS: Readonly<Record<string, readonly string[]>> = {
  desk: ["monitor", "screen.", "desk", "chair", "desk.mouse"],
  bike: ["cycle.bike", "cycle.wheel."],
  pack: ["hike.pack"],
  mouse: ["desk.mouse"],
  pole: ["stream.pole"],
  wade: ["stream.wade"],
  rod: ["fish.rod"],
  tongs: ["bbq.tongs."],
  apron: ["bbq.apron"],
};

/** The props an action's render stands for: its manifest's, and whatever
 * the manifest says, the desk unit for a desk action (an older manifest
 * does not say so), the backpack on the hike, at the summit too, and the
 * apron at the barbecue (an anime figure never wears the pixel ones: a
 * person's or Claude's skin has a pack at the summit, which no render has). */
export function animeProps(action: AnimeActionName, listed: readonly string[]): readonly string[] {
  const always = action.startsWith("desk-") ? ["desk"] : action.startsWith("hike-") ? ["pack"] : action === "bbq-grill" ? ["apron"] : [];
  return [...new Set([...listed, ...always])];
}

/** A prop's anchor (its sprite's top-left) from its tip: the rod's tip is
 * its top-right pixel, rod + (ROD.w - 1, 0), where the fishing line hangs
 * from (deskArt fish). */
const FROM_TIP: Readonly<Partial<Record<string, Point>>> = { rod: { x: -(ROD.w - 1), y: 0 } };

/** The person's head's width: a bubble hangs off an anime head's right edge
 * as off the person's, so the head anchor is that far left of it. */
const PERSON_HEAD_W = 10;

/** Where a state is in its scene. */
export interface AnimeSpot {
  /** The scene's point for the state (art px, as edges: row r spans r …
   * r + 1), where the action's anchor goes. */
  point: Point;
  /** Seated (on a sofa, a log, a rock): the seat's top, where a seated
   * action's anchor goes, its seat contact (anime/manifest.ts); `point` is
   * then the point under the shoes, for a sheet whose seated anchor is
   * still its soles' line (y SOLES_Y or more). */
  seat?: Point;
  /** The scene's frame, art px: nothing of the render shows outside it. */
  w: number;
  h: number;
  /** Frame 0 only (an offline agent). */
  still?: boolean;
}

/** A box in a scene, art px, as edges. */
export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Drawn<Need extends PropAnchor> {
  /** The figure to draw. */
  figure: Figure<Need>;
  /** The scene's parts, less the props the figure's render has. */
  without(parts: Placed[]): Placed[];
  /** The head's box in the scene, its edges on whole art px (not kept to
   * the frame), when the figure is drawn from its sheet; none for the
   * skin's. What hangs off the head (a desk's badge) goes by it. */
  head?: Box;
  /** The props' anchors the sheet gives, from their tips (the figure's
   * anchors have them too); a prop the render has without one keeps the
   * skin's anchor, which is not where the render's is. */
  tips?: Partial<Record<PropAnchor, Point>>;
}

const same = (parts: Placed[]) => parts;
const clamp = (value: number, max: number) => Math.min(Math.max(value, 0), max);

function covers(name: string, props: readonly string[]): boolean {
  return props.some((prop) => (PROP_PARTS[prop] ?? []).some((part) => (part.endsWith(".") ? name.startsWith(part) : name === part)));
}

/** The figure a scene draws for a state: the sheet's action when there is
 * one (and its frame shows in the scene at all), the skin's otherwise. */
export function drawFigure<Need extends PropAnchor>(
  pixel: Figure<Need>,
  sheet: AnimeSheet | undefined,
  action: AnimeActionName | undefined,
  spot: AnimeSpot,
): Drawn<Need> {
  const drawn = sheet && action ? sheet.manifest.actions[action] : undefined;
  if (!sheet || !action || !drawn) return { figure: pixel, without: same };
  const { frameW, frameH, scale } = sheet.manifest;
  // Seated, the render's seat contact goes on the seat's top; a sheet's
  // from before has its soles' line for a seated anchor, which goes under
  // the shoes, as a standing one's does.
  const point = spot.seat && drawn.anchor.y < SOLES_Y ? spot.seat : spot.point;
  // In the scene's px at the sheet's scale, so a whole anchor stays whole:
  // the frame's top-left, and what of it is inside the scene.
  const left = point.x * scale - drawn.anchor.x;
  const top = point.y * scale - drawn.anchor.y;
  const x0 = Math.max(0, left);
  const y0 = Math.max(0, top);
  const x1 = Math.min(spot.w * scale, left + frameW);
  const y1 = Math.min(spot.h * scale, top + frameH);
  if (x1 <= x0 || y1 <= y0) return { figure: pixel, without: same };
  const part: Placed = {
    name: `anime.${action}`,
    sprite: { w: (x1 - x0) / scale, h: (y1 - y0) / scale, layers: [] },
    x: x0 / scale,
    y: y0 / scale,
    anime: {
      href: sheet.href,
      sheetW: sheet.width,
      sheetH: sheet.height,
      frameW,
      frameH,
      row: drawn.row,
      frames: drawn.frames,
      loopMs: Math.round((drawn.frames / drawn.fps) * 1000),
      window: { x: x0 - left, y: y0 - top, w: x1 - x0, h: y1 - y0 },
      still: Boolean(spot.still),
    },
  };
  // The head's box, its edges rounded to the art pixels'.
  const edge = (px: number) => Math.round(px / scale);
  const hx = edge(left + drawn.head.x);
  const hy = edge(top + drawn.head.y);
  const head: Box = { x: hx, y: hy, w: edge(left + drawn.head.x + drawn.head.w) - hx, h: edge(top + drawn.head.y + drawn.head.h) - hy };
  // A prop's anchor from its tip: the art pixel the tip is in, less the
  // tip's place in the prop.
  const tips: Partial<Record<PropAnchor, Point>> = {};
  for (const [name, tip] of Object.entries(drawn.tips ?? {})) {
    const offset = FROM_TIP[name];
    if (!offset) continue;
    tips[name as PropAnchor] = { x: Math.floor((left + tip.x) / scale) + offset.x, y: Math.floor((top + tip.y) / scale) + offset.y };
  }
  const props = animeProps(action, drawn.props);
  return {
    figure: {
      parts: [part],
      anchors: {
        ...pixel.anchors,
        ...tips,
        head: { x: clamp(head.x + head.w - PERSON_HEAD_W, spot.w - 1), y: clamp(head.y, spot.h - 1) },
      },
    },
    without: (parts) => (props.length ? parts.filter((placed) => !covers(placed.name, props)) : parts),
    head,
    tips,
  };
}
