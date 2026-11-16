// Station's "Waiting on you" keeps a result the person applied where they
// pressed Apply (single-user edition): which results it saw waiting, at
// which place, and which applied ones the person dismissed. Held while
// Station is open, across its tabs; StationView forgets it on leaving.

import { create } from "zustand";

interface KeptResultsState {
  /** Results seen waiting during this visit, at their last place. */
  placed: Record<string, number>;
  /** Applied results the person dismissed. */
  dismissed: Record<string, true>;
}

export const useKeptResults = create<KeptResultsState>(() => ({ placed: {}, dismissed: {} }));

/** Remembers where each waiting result is, as `ids` (in list order) says. */
export function placeResults(ids: readonly (string | null)[]): void {
  const prev = useKeptResults.getState().placed;
  let next = prev;
  ids.forEach((id, index) => {
    if (!id || prev[id] === index) return;
    if (next === prev) next = { ...prev };
    next[id] = index;
  });
  if (next !== prev) useKeptResults.setState({ placed: next });
}

export function dismissResult(runId: string): void {
  useKeptResults.setState((state) => ({ dismissed: { ...state.dismissed, [runId]: true } }));
}

/** Leaving Station: applied results are no longer kept. */
export function forgetKeptResults(): void {
  useKeptResults.setState({ placed: {}, dismissed: {} });
}
