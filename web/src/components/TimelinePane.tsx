/**
 * Timeline pane [B-06].
 *
 * Named entry point for the schedule view: expected milestones and actual
 * journal entries on a day grid. Shares `LaneView` with the graph pane.
 */

import { LaneView, type PaneProps } from "./LaneView";

export function TimelinePane(props: PaneProps) {
  return <LaneView variant="timeline" {...props} />;
}
