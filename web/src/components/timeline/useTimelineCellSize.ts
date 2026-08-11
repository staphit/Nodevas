/**
 * How large one timeline cell is.
 *
 * Automatic is the default and sizes cells to the longest label and whether
 * notes are present, so text is readable without the grid wasting space.
 * Dragging a grip means "I want this size": automatic turns off and the value
 * applies to every date column, not just the one under the cursor.
 */

import { useMemo, useState } from "react";
import { usePreference } from "../../store";
import type { PreferenceKey, PreferenceValue } from "../../preferences";
import type { Graph, RunState } from "../../types";

export function useTimelineCellSize({
  graph,
  runState,
  updateUIPreference,
}: {
  graph: Graph | null;
  runState: RunState | null;
  updateUIPreference: <K extends PreferenceKey>(
    key: K,
    value: PreferenceValue<K>,
  ) => void;
}) {
  const timelineCellAuto = usePreference("timelineCellAuto");
  const manualTimelineCellWidth = usePreference("timelineCellWidth");
  const manualTimelineCellHeight = usePreference("timelineCellHeight");
  const [timelineWidthResizeEdge, setTimelineWidthResizeEdge] = useState<
    "left" | "right" | null
  >(null);
  const autoTimelineCellWidth = useMemo(() => {
    const content = [
      ...(graph?.nodes ?? []).flatMap((node) => [
        node.title || node.id,
        node.assignee || "",
      ]),
      ...(graph?.ui?.planStatuses ?? []).map((definition) => definition.label),
      ...(graph?.ui?.customStatuses ?? []).map((definition) => definition.label),
      ...(runState?.history ?? []).flatMap((event) => [
        event.note || "",
        event.node || "",
      ]),
    ];
    const longest = content.reduce(
      (max, value) => Math.max(max, Array.from(value).length),
      0,
    );
    return Math.max(200, Math.min(320, 112 + Math.min(longest, 30) * 7));
  }, [graph, runState]);
  const autoTimelineCellHeight = useMemo(
    () =>
      (runState?.history ?? []).some((event) => Boolean(event.note)) ||
      Object.values(graph?.ui?.plans ?? {}).some((plans) =>
        plans.some((plan) => Boolean(plan.note)),
      )
        ? 108
        : 96,
    [graph?.ui?.plans, runState],
  );
  const timelineCellWidth = timelineCellAuto
    ? autoTimelineCellWidth
    : manualTimelineCellWidth;
  const timelineCellHeight = timelineCellAuto
    ? autoTimelineCellHeight
    : manualTimelineCellHeight;
  // Dragging a grip means "I want this size": auto mode turns off, and the
  // value applies to every date column, not just the one under the cursor.
  const setTimelineCellSize = (dimension: "width" | "height", value: number) => {
    updateUIPreference("timelineCellAuto", false);
    updateUIPreference(
      dimension === "width" ? "timelineCellWidth" : "timelineCellHeight",
      value,
    );
  };
  const useAutoTimelineCellSize = () => updateUIPreference("timelineCellAuto", true);

  return {
    timelineCellAuto,
    timelineCellWidth,
    timelineCellHeight,
    timelineWidthResizeEdge,
    setTimelineWidthResizeEdge,
    setTimelineCellSize,
    useAutoTimelineCellSize,
  };
}
