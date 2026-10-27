import "./analytics.css";

export interface AnalyticsSettingsProps {
  project?: string;
  isAdmin?: boolean;
  canWrite?: boolean;
  onOpenSettings?: (row: string) => void;
}

/** Insights, Scorecards and Tiers & routing, as three segmented tabs. */
export default function AnalyticsSettings(_props: AnalyticsSettingsProps) {
  // TODO(green): implement.
  return null;
}
