import { create } from "zustand";
import type { AnalyticsSettings, Insights, RoutePick, Scorecards, TiersResponse, Track } from "../api/analytics";

/** What the Agent performance settings row shows. */
export interface AnalyticsState {
  insights: Insights | null;
  scorecards: Scorecards | null;
  tiers: TiersResponse | null;
  settings: AnalyticsSettings | null;
  picks: RoutePick[];
  track: Track;
  error: string | null;
  load: (project?: string) => Promise<void>;
}

export const useAnalytics = create<AnalyticsState>(() => ({
  insights: null,
  scorecards: null,
  tiers: null,
  settings: null,
  picks: [],
  track: "all",
  error: null,
  // TODO(green): implement.
  load: async () => {},
}));
