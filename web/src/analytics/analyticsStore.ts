import { create } from "zustand";
import {
  analyticsApi,
  type AnalyticsSettings,
  type Insights,
  type RoutePick,
  type Scorecards,
  type TiersResponse,
  type Track,
} from "../api/analytics";
import { AuthError } from "../api/http";

/** The window every view uses, in days. */
export const ANALYTICS_DAYS = 30;

/** What the Agent performance settings row shows. */
export interface AnalyticsState {
  insights: Insights | null;
  scorecards: Scorecards | null;
  tiers: TiersResponse | null;
  settings: AnalyticsSettings | null;
  picks: RoutePick[];
  track: Track;
  error: string | null;
  /** The hub refused a settings write: the viewer may only read. */
  readOnly: boolean;
  load: (project?: string) => Promise<void>;
  setTrack: (track: Track) => Promise<void>;
  saveSettings: (settings: AnalyticsSettings, project?: string) => Promise<void>;
}

const message = (error: unknown) => (error instanceof Error ? error.message : String(error));

export const useAnalytics = create<AnalyticsState>((set, get) => ({
  insights: null,
  scorecards: null,
  tiers: null,
  settings: null,
  picks: [],
  track: "all",
  error: null,
  readOnly: false,
  load: async (project) => {
    const [insights, scorecards, tiers, settings, picks] = await Promise.allSettled([
      analyticsApi.insights(ANALYTICS_DAYS, project),
      analyticsApi.scorecards(ANALYTICS_DAYS, get().track),
      analyticsApi.tiers(),
      analyticsApi.settings(project),
      analyticsApi.routePicks(50, project),
    ]);
    const failed = [insights, scorecards, tiers, settings, picks].find((r) => r.status === "rejected");
    set({
      insights: insights.status === "fulfilled" ? insights.value : get().insights,
      scorecards: scorecards.status === "fulfilled" ? scorecards.value : get().scorecards,
      tiers: tiers.status === "fulfilled" ? tiers.value : get().tiers,
      settings: settings.status === "fulfilled" ? settings.value : get().settings,
      picks: picks.status === "fulfilled" ? picks.value : get().picks,
      error: failed && failed.status === "rejected" ? message(failed.reason) : null,
    });
  },
  setTrack: async (track) => {
    set({ track });
    try {
      const scorecards = await analyticsApi.scorecards(ANALYTICS_DAYS, track);
      if (get().track === track) set({ scorecards, error: null });
    } catch (error) {
      set({ error: message(error) });
    }
  },
  saveSettings: async (settings, project) => {
    const before = get().settings;
    set({ settings });
    try {
      const saved = await analyticsApi.putSettings(settings, project);
      set({ settings: { ...settings, ...saved, requiredTiers: saved?.requiredTiers ?? settings.requiredTiers }, error: null });
    } catch (error) {
      set({ settings: before, error: message(error), readOnly: error instanceof AuthError && error.status === 403 });
    }
  },
}));
