import type { AnalyticsSettings, RoutePick, TiersResponse } from "../api/analytics";

export interface TiersRoutingProps {
  tiers: TiersResponse;
  settings: AnalyticsSettings;
  picks: RoutePick[];
  readOnly: boolean;
  /** Saves the settings (PUT /api/analytics/settings). */
  onChange: (settings: AnalyticsSettings) => void;
}

/** The auto routing switch, required tiers, exploration and recent picks. */
export default function TiersRouting(_props: TiersRoutingProps) {
  // TODO(green): implement.
  return null;
}
