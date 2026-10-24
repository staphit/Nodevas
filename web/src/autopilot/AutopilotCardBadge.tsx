import { useI18n } from "../i18n";
import { STATE_GLYPH, stateClass } from "./AutopilotGrid";
import { useAutopilot } from "./autopilotStore";

/** A task node's place in an active autopilot run, on its graph card: the
 * state's glyph and color, and the stage it is at in a word. */
export function AutopilotCardBadge({ nodeId }: { nodeId: string }) {
  const { t } = useI18n();
  const mark = useAutopilot((s) => s.marks[nodeId]);
  if (!mark) return null;
  const state = stateClass(mark.state);
  const stateLabel = t(`autopilot.state.${state}`);
  const stage = mark.stage ? t(`autopilot.stageShort.${mark.stage}`) : "";
  const title = t("autopilot.cardBadge", {
    state: stateLabel,
    stage: mark.stage ? t(`autopilot.stage.${mark.stage}`) : "—",
  });
  return (
    <span className={`ap-card-badge ap-state-${state}`} title={title} aria-label={title}>
      <span className="ap-glyph" aria-hidden="true">
        {STATE_GLYPH[state]}
      </span>
      <span className="ap-card-badge-text" aria-hidden="true">
        {stage || stateLabel}
      </span>
    </span>
  );
}
