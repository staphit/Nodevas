// Which action of a character's anime sheet draws each state the war room
// shows (anime/manifest.ts), by the scene it is in. A sheet without the
// action leaves the state in pixel art.

import type { OutingKind } from "../outing";
import type { DeskGesture } from "../pixel/skins";

/** Every action a sheet may draw: the four rendered first, then the rest. */
export const ANIME_ACTIONS = [
  "door-idle",
  "desk-type",
  "hike-walk",
  "cycle-ride",
  "desk-rest",
  "desk-chin",
  "desk-mouse",
  "desk-ask",
  "desk-cheer",
  "desk-slump",
  "desk-sleep",
  "sofa-sit",
  "sofa-sleep",
  "hike-summit",
  "stream-wade",
  "fish-sit",
  "camp-sit",
  "bbq-grill",
] as const;

export type AnimeActionName = (typeof ANIME_ACTIONS)[number];

/** Where a character is, as far as its action goes: at a desk (or the
 * meeting table) in a gesture, at the door, on a sofa, on an outing (the
 * hiker walking or at the top), or at another office's small desk. */
export type AnimeState =
  | { at: "desk"; gesture: DeskGesture }
  | { at: "door" }
  | { at: "sofa"; asleep: boolean }
  | { at: "outing"; kind: OutingKind; walking?: boolean }
  | { at: "mini" };

const OUTING_ACTION: Record<Exclude<OutingKind, "hike">, AnimeActionName> = {
  stream: "stream-wade",
  fish: "fish-sit",
  camp: "camp-sit",
  bbq: "bbq-grill",
  cycle: "cycle-ride",
};

/** The action that draws a state; none for a small desk, too small for one. */
export function animeAction(state: AnimeState): AnimeActionName | undefined {
  switch (state.at) {
    case "desk":
      return `desk-${state.gesture}`;
    case "door":
      return "door-idle";
    case "sofa":
      return state.asleep ? "sofa-sleep" : "sofa-sit";
    case "outing":
      if (state.kind === "hike") return state.walking ? "hike-walk" : "hike-summit";
      return OUTING_ACTION[state.kind];
    default:
      return undefined;
  }
}
