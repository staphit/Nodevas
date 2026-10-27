import type { Suggestion } from "../api/analytics";

export interface SuggestionListProps {
  suggestions: Suggestion[];
  project?: string;
  /** Opens a Settings row (an open_settings change). */
  onOpenSettings?: (row: string) => void;
  /** Called after an apply or dismiss so the caller reloads. */
  onChanged?: () => void;
}

/** Suggestions, each applied only after the person confirms it. */
export default function SuggestionList(_props: SuggestionListProps) {
  // TODO(green): implement.
  return null;
}
