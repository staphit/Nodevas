import { useEffect } from "react";

import { AutopilotPill } from "../../autopilot/AutopilotPill";
import { useAutopilot } from "../../autopilot/autopilotStore";
import { openAutopilot } from "../../autopilot/openAutopilot";
import { usePlanningRun } from "../../autopilot/planningRun";
import { IconChevronDown, IconSend, IconTerminal } from "../../icons";
import { useI18n } from "../../i18n";
import { useRuns } from "../../runs/runStore";
import { useNodeType } from "../../nodeTypeTheme";
import { DrawerMenu } from "./DrawerMenu";

/**
 * The node panel's one primary action: hand the node to an agent. The caret
 * beside it holds the other ways an agent can take a node on — Autopilot,
 * which directs agents toward it as a goal, and the node's runs so far.
 *
 * While Autopilot is under way on this node, or planning it, the pill that
 * says how far it got stays in the row: progress is worth seeing without
 * opening anything.
 *
 * The node's type decides what is offered: a type agents cannot work (a
 * Review, a Decision, a Note) shows no agent action at all, and a Goal's
 * primary action is Autopilot rather than a single dispatch.
 */
export function DispatchControl({ id, onShowRuns }: { id: string; onShowRuns: () => void }) {
  const { t } = useI18n();
  const openDispatch = useRuns((s) => s.openDispatch);
  const autopilotId = useAutopilot((s) => s.byGoal[id]);
  const run = useAutopilot((s) => (autopilotId ? s.views[autopilotId]?.run : undefined));
  const loadForGoal = useAutopilot((s) => s.loadForGoal);
  const planning = usePlanningRun(id);
  useEffect(() => {
    void loadForGoal(id);
  }, [id, loadForGoal]);
  const live = Boolean(run && (run.status === "running" || run.status === "paused"));
  const { agentMode } = useNodeType({ nodeId: id });

  if (agentMode === "none") {
    return live || planning ? (
      <div className="dispatch-control">
        <AutopilotPill id={id} />
      </div>
    ) : null;
  }

  if (agentMode === "autopilot") {
    return (
      <div className="dispatch-control">
        {(live || planning) && <AutopilotPill id={id} />}
        <div className="dispatch-split">
          <button
            type="button"
            className="primary dispatch-main"
            title={t("autopilot.banner.startDetail")}
            onClick={() => openAutopilot(id)}
          >
            <span aria-hidden>◎</span>
            <span>{t("drawer.autopilotPrimary")}</span>
          </button>
          <DrawerMenu
            label={t("drawer.dispatchMore")}
            trigger={<IconChevronDown size={13} />}
            triggerClassName="primary dispatch-caret"
            align="end"
            items={[
              {
                id: "runs",
                label: t("drawer.viewRuns"),
                icon: <IconTerminal size={13} />,
                onSelect: onShowRuns,
              },
            ]}
          />
        </div>
      </div>
    );
  }

  return (
    <div className="dispatch-control">
      {(live || planning) && <AutopilotPill id={id} />}
      <div className="dispatch-split">
        <button type="button" className="primary dispatch-main" onClick={() => openDispatch(id)}>
          <IconSend size={13} />
          <span>{t("drawer.dispatch")}</span>
        </button>
        <DrawerMenu
          label={t("drawer.dispatchMore")}
          trigger={<IconChevronDown size={13} />}
          triggerClassName="primary dispatch-caret"
          align="end"
          items={[
            {
              id: "autopilot",
              label: t("drawer.autopilotItem"),
              hint: live ? `${run?.counts.success ?? 0}/${run?.counts.total ?? 0}` : undefined,
              icon: <span>◎</span>,
              title: t("autopilot.banner.startDetail"),
              onSelect: () => openAutopilot(id),
            },
            {
              id: "runs",
              label: t("drawer.viewRuns"),
              icon: <IconTerminal size={13} />,
              onSelect: onShowRuns,
            },
          ]}
        />
      </div>
    </div>
  );
}
