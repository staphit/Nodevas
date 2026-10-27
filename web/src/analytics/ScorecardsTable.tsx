import type { Scorecards, Track } from "../api/analytics";

export interface ScorecardsTableProps {
  data: Scorecards;
  track: Track;
  onTrack: (track: Track) => void;
}

/** One row per role x agent x model. */
export default function ScorecardsTable(_props: ScorecardsTableProps) {
  // TODO(green): implement.
  return null;
}
