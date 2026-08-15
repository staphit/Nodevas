/**
 * Which board gesture a plain drag means, for pointers that have no modifier
 * keys.
 *
 * Three of the graph's editing gestures are spelled with a held key: Alt+drag
 * from a card draws a connection, Alt+click on a wire inserts a bend, and
 * Ctrl/⌘+drag on empty space is a marquee. A finger has none of those, and
 * inventing a touch-only gesture for each (two-finger drag, double-tap-hold)
 * would be three gestures to discover and none of them visible.
 *
 * So the modifier becomes a mode, chosen from a visible control. The modifier
 * keys keep working exactly as they did — the mode is an additional way to
 * reach the same gesture, not a replacement — which is what lets an iPad with
 * a keyboard use whichever is at hand.
 */

import { createContext, useContext } from "react";

export type GraphGestureMode =
  /** Drag a card to move it, drag empty space to pan. The desktop default. */
  | "select"
  /** Drag from a card to wire it up; tap a wire to add a bend. */
  | "connect"
  /** Drag empty space to rubber-band a selection. */
  | "marquee";

export const GRAPH_GESTURE_MODES: {
  mode: GraphGestureMode;
}[] = [
  { mode: "select" },
  { mode: "connect" },
  { mode: "marquee" },
];

/**
 * Read by the leaves that act on the mode — a dependency line is four
 * components below the toolbar that sets it, and threading a display-only flag
 * through each of them would put it in the props of components that do not
 * otherwise care.
 */
const GraphGestureModeContext = createContext<GraphGestureMode>("select");

export const GraphGestureModeProvider = GraphGestureModeContext.Provider;

export function useGraphGestureMode(): GraphGestureMode {
  return useContext(GraphGestureModeContext);
}
