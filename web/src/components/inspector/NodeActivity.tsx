/**
 * Activity tab: everything that has happened to a node, in one place.
 *
 * Three records with three owners — the journal and the plan (the schedule),
 * the runner hub (agent runs) and the file history (document versions) — kept
 * as stacked sections rather than interleaved into one list. Each answers a
 * different question and has its own actions (rescheduling, opening a run,
 * restoring a version), and interleaving them would mean one of them owning
 * the others' data.
 */

import type { PageFormat } from "../../api/nodes";
import { useI18n } from "../../i18n";
import { NodeRuns } from "../../runs/NodeRuns";
import { useWorkstationVisible } from "../../runners/workstation";
import { NodeHistory } from "./NodeHistory";
import { NodeTimeline } from "./NodeTimeline";

export function NodeActivity({
  id,
  documentPath,
  format,
  onRestored,
}: {
  id: string;
  /** The file the editor is showing, whose versions the history lists. */
  documentPath: string;
  format: PageFormat;
  onRestored: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  // Runs exist only once the agent workstation is on. They come first: a live
  // run is what "View runs" and a fresh dispatch land here for, and its
  // terminal only draws while it is on screen.
  const workstation = useWorkstationVisible();
  return (
    <div className="node-activity" role="tabpanel" aria-label={t("drawer.activity")}>
      {workstation && <NodeRuns id={id} />}
      <NodeTimeline id={id} />
      <div className="node-activity-history">
        {/* Status changes are already in the timeline above; the history here
            is the document's saved versions. */}
        <NodeHistory
          id={id}
          path={documentPath}
          format={format}
          onRestored={onRestored}
          showStatusEvents={false}
        />
      </div>
    </div>
  );
}
