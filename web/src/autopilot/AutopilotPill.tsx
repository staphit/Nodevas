import { useEffect } from "react";

import { useI18n } from "../i18n";
import { openAutopilot } from "./openAutopilot";
import { useAutopilot } from "./autopilotStore";

/**
 * This node as Autopilot's goal, in the node panel's top row. Not started, it
 * offers to start; under way, it says how far. Either way it opens the goal
 * in Station, where the decomposition, the start and the tracking live.
 */
export function AutopilotPill({ id }: { id: string }) {
  const { t } = useI18n();
  const autopilotId = useAutopilot((s) => s.byGoal[id]);
  const view = useAutopilot((s) => (autopilotId ? s.views[autopilotId] : undefined));
  const loadForGoal = useAutopilot((s) => s.loadForGoal);

  useEffect(() => {
    void loadForGoal(id);
  }, [id, loadForGoal]);

  const run = view?.run;
  const live = run && (run.status === "running" || run.status === "paused");
  const title = live
    ? `${t("autopilot.banner.running", { done: run.counts.success, total: run.counts.total })}${
        run.counts.waiting > 0 ? ` · ${t("autopilot.banner.waiting", { count: run.counts.waiting })}` : ""
      } · ${t("autopilot.banner.open")}`
    : `${t("autopilot.banner.start")}: ${t("autopilot.banner.startDetail")}`;
  return (
    <button
      type="button"
      className={`autopilot-pill${live ? " is-live" : ""}${live && run.counts.waiting > 0 ? " is-waiting" : ""}`}
      onClick={() => openAutopilot(id)}
      title={title}
      aria-label={title}
    >
      <span aria-hidden="true">◎</span>
      <span>{t("autopilot.title")}</span>
      {live && (
        <span className="autopilot-pill-count" aria-hidden="true">
          {run.counts.success}/{run.counts.total}
        </span>
      )}
    </button>
  );
}
