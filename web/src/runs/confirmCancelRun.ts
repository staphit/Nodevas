import type { Run } from "../api";
import { confirmAction } from "../components/ConfirmDialog";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** Asks before cancelling a run: the same question wherever cancel is
 * offered (a node's Runs tab, Station's control center). */
export function confirmCancelRun(
  t: Translate,
  run: Pick<Run, "status" | "pipelineId">,
): Promise<boolean> {
  return confirmAction({
    title: t("runs.cancelTitle"),
    description: [
      t(run.status === "running" ? "runs.cancelRunning" : "runs.cancelPending"),
      // The hub cancels every step of a pipeline when one is cancelled.
      run.pipelineId ? t("runs.cancelPipeline") : "",
    ]
      .filter(Boolean)
      .join(" "),
    confirmLabel: t("runs.cancel"),
    cancelLabel: t("runs.keep"),
    tone: "danger",
  });
}
