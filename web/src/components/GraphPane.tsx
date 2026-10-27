/**
 * Graph pane [B-06].
 *
 * Named entry point for the dependency canvas. Graph and timeline share one
 * implementation (`LaneView`) because they render the same nodes on the same
 * grid with the same selection and drag rules; the split that matters to
 * callers is which pane they are mounting, so that is what they name.
 *
 * The war room used to be a skin of this pane; it is Station's now
 * (src/station), and Info keeps the cards.
 */

import { LaneView, type PaneProps } from "./LaneView";

export function GraphPane(props: PaneProps) {
  return <LaneView variant="graph" {...props} />;
}
