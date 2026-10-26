import { useState } from "react";
import type { AutopilotView } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { useI18n } from "../i18n";
import { useAutopilot } from "./autopilotStore";

/**
 * What the goal gained or changed since the autopilot froze its task set: new
 * members, and pending tasks held because their node changed or left the
 * goal (a revision's replan does both). A plain resume keeps them out or
 * held; adopting takes the goal as it stands now.
 */
export function revisedSincePause(view: AutopilotView): {
  added: number;
  changed: number;
  removed: number;
  total: number;
} {
  const tasks = view.tasks ?? [];
  const added = view.added?.length ?? 0;
  const changed = tasks.filter((task) => task.hold === "changed").length;
  const removed = tasks.filter((task) => task.hold === "removed").length;
  return { added, changed, removed, total: added + changed + removed };
}

export type AutopilotControlAction = "pause" | "resume" | "adopt" | "cancel";

/**
 * Pause, resume and cancel for one autopilot, with the questions they ask:
 * the goal's panel and Station's control center share it, so both resume a
 * revised goal by adopting it (after saying what changed) and both confirm a
 * cancel the same way.
 */
export function useAutopilotControl(view: AutopilotView) {
  const { t } = useI18n();
  const act = useAutopilot((s) => s.act);
  const [acting, setActing] = useState(false);
  const revision = revisedSincePause(view);
  /** A paused autopilot resumes by adopting the goal when it was revised. */
  const resumeAction: "resume" | "adopt" = revision.total > 0 ? "adopt" : "resume";

  const control = async (action: AutopilotControlAction): Promise<boolean> => {
    if (action === "adopt") {
      const ok = await confirmAction({
        title: t("autopilot.adoptTitle"),
        description: t("autopilot.adoptDescription", revision),
        confirmLabel: t("autopilot.adopt"),
      });
      if (!ok) return false;
    }
    if (action === "cancel") {
      const ok = await confirmAction({
        title: t("autopilot.cancelTitle"),
        description: t("autopilot.cancelDescription"),
        confirmLabel: t("autopilot.cancel"),
        tone: "danger",
      });
      if (!ok) return false;
    }
    setActing(true);
    try {
      return await act(view.run.id, action);
    } finally {
      setActing(false);
    }
  };

  return {
    acting,
    revision,
    resumeAction,
    resumeLabel: resumeAction === "adopt" ? t("autopilot.adopt") : t("autopilot.resume"),
    control,
  };
}
