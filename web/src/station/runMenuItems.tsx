// What can be done about one run or one autopilot, as Station's menus offer
// it: on a war-room character and on a tile under the room. Each item is the
// control the run's or the goal's own view offers (same question, same store
// action); this only puts them in one menu.

import type { CSSProperties, ReactNode } from "react";

import { runCancellable, runFinal, type AutopilotView, type Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useAutopilotControl } from "../autopilot/useAutopilotControl";
import { useViewer } from "../components/SignIn";
import { useGameActions } from "../game/GameActions";
import { agentWho } from "../game/labels";
import { useI18n } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";
import { confirmCancelRun } from "../runs/confirmCancelRun";
import { openNodeRuns } from "../runs/openRuns";
import { openRunWorkspace } from "../runs/openWorkspace";
import { formatCompact, formatCost, totalTokens } from "../runs/runFormat";
import { useRuns } from "../runs/runStore";
import { reportError, useApp } from "../store";
import { canSteer, controlRunKind, elapsedMs, formatElapsed, type ControlRow } from "./control";
import { openGlobalAgents } from "./openGlobalAgents";
import { StationMenu, type StationMenuHeading, type StationMenuItem } from "./StationMenu";
import { useStation } from "./stationStore";

type Translate = ReturnType<typeof useI18n>["t"];

export function openNode(nodeId: string) {
  void useApp.getState().openTab(nodeId).catch(reportError);
}

export function openGoal(goalNodeId: string) {
  useStation.getState().focusGoal(goalNodeId, useApp.getState().activeProject);
}

/** "Review · Codex gpt-5 · laptop · 4m 05s · 12.3K tokens · $0.042 · Ship login";
 * in Simple only what it cost, no tokens. */
export function runMeta(t: Translate, run: Run, now: number, goal: string, simple = false): string {
  const tokens = simple ? 0 : totalTokens(run.usage);
  const parts = [
    t(`station.control.kind.${controlRunKind(run)}`),
    agentWho(run.agent, run.model),
    runnerDisplayName(t, run.runnerName),
    formatElapsed(elapsedMs(run, now)),
    tokens > 0 ? t("station.control.tokens", { tokens: formatCompact(tokens) }) : "",
    formatCost(run.usage?.costUsd),
    goal,
  ];
  return parts.filter(Boolean).join(" · ");
}

/** Pause or resume (adopting a revised goal) an autopilot, and with
 * `withCancel` cancel it: the goal panel's own controls, as menu items. */
function useSteerItems(view: AutopilotView, withCancel: boolean): StationMenuItem[] {
  const { t } = useI18n();
  const { acting, resumeAction, resumeLabel, control } = useAutopilotControl(view);
  const status = view.run.status;
  const items: StationMenuItem[] = [];
  if (status === "running") {
    items.push({ id: "pause", label: t("station.control.pauseGoal"), disabled: acting, onSelect: () => void control("pause") });
  }
  if (status === "paused") {
    items.push({ id: "resume", label: resumeLabel, disabled: acting, onSelect: () => void control(resumeAction) });
  }
  if (withCancel && (status === "running" || status === "paused")) {
    items.push({ id: "cancel", label: t("autopilot.cancel"), danger: true, disabled: acting, onSelect: () => void control("cancel") });
  }
  return items;
}

/** The trigger and the placement of a menu: the caller's own. */
export interface MenuShell {
  label: string;
  trigger: ReactNode;
  triggerClassName?: string;
  heading?: StationMenuHeading;
  className?: string;
  style?: CSSProperties;
}

export interface RunItemsProps {
  row: ControlRow;
  titleOf: (id: string) => string;
  /** Items about the run that only its character knows (a question it
   * asked, a held message to it, its PR), drawn first. */
  extra?: StationMenuItem[];
  /** Draws the items: as a menu, or as buttons of the caller's own. */
  children: (items: StationMenuItem[]) => ReactNode;
}

export type RunMenuProps = MenuShell & Omit<RunItemsProps, "children">;

/**
 * What can be done about a run: open it, its changes, its node and its goal,
 * pause or resume the autopilot it is a step of (when the viewer may steer
 * it), and cancel it while it can be cancelled. A global agent's has no node,
 * run view or goal: its workspace, its place in Station's Global agents tab,
 * its changes and cancel.
 */
export function RunItems(props: RunItemsProps) {
  const viewer = useViewer();
  const autopilot = props.row.autopilot;
  if (autopilot && canSteer(viewer, autopilot.run.createdBy)) {
    return <SteeredRunItems {...props} autopilot={autopilot} />;
  }
  return <RunItemsView {...props} steer={[]} />;
}

/** The same, as a menu on a trigger of the caller's own. */
export function RunMenu({ row, titleOf, extra, ...shell }: RunMenuProps) {
  return (
    <RunItems row={row} titleOf={titleOf} extra={extra}>
      {(items) => <StationMenu items={items} {...shell} />}
    </RunItems>
  );
}

function SteeredRunItems({ autopilot, ...props }: RunItemsProps & { autopilot: AutopilotView }) {
  return <RunItemsView {...props} steer={useSteerItems(autopilot, false)} />;
}

function RunItemsView({ row, titleOf, extra = [], steer, children }: RunItemsProps & { steer: StationMenuItem[] }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const { run, autopilot } = row;
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const stopping = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelRun = useRuns((s) => s.cancelRun);
  const title = titleOf(run.nodeId);

  const onCancel = async () => {
    if (await confirmCancelRun(t, run)) await cancelRun(run.id);
  };

  const diff: StationMenuItem = {
    id: "diff",
    label: t("game.action.runDiff"),
    disabled: !run.commit,
    title: run.commit ? undefined : t("station.progress.noDiff"),
    onSelect: () => dispatch({ kind: "run-diff", runId: run.id, nodeId: run.nodeId }),
  };
  const workspace: StationMenuItem = {
    id: "workspace",
    label: t("station.control.openWorkspace"),
    separatorBefore: extra.length > 0,
    onSelect: () => openRunWorkspace(run.id),
  };
  const items: StationMenuItem[] = run.global
    ? [
        ...extra,
        workspace,
        { id: "global-agents", label: t("game.action.globalAgents"), onSelect: () => openGlobalAgents(run.id) },
        diff,
      ]
    : [
        ...extra,
        workspace,
        {
          id: "run",
          label: t("station.control.openRun"),
          onSelect: () => openNodeRuns(run.nodeId, run.id),
        },
        diff,
        { id: "node", label: t("game.action.node", { title }), onSelect: () => openNode(run.nodeId) },
      ];
  if (autopilot && !run.global) {
    items.push({
      id: "goal",
      label: t("station.progress.openGoal", { name: titleOf(autopilot.run.goalNodeId) }),
      onSelect: () => openGoal(autopilot.run.goalNodeId),
    });
  }
  items.push(...steer.map((item, index) => ({ ...item, separatorBefore: index === 0 })));
  if (stopping) {
    items.push({ id: "cancel", label: t("station.control.stopping"), disabled: true, separatorBefore: true, onSelect: () => undefined });
  } else if (runCancellable(run.status)) {
    items.push({
      id: "cancel",
      label: cancelling ? t("runs.cancelling") : t("runs.cancel"),
      danger: true,
      disabled: cancelling,
      separatorBefore: true,
      onSelect: () => void onCancel(),
    });
  }
  return <>{children(items)}</>;
}

/** A live autopilot's menu: open its goal, pause or resume it, cancel it
 * (the last two only for whoever may steer it). */
export function AutopilotMenu({ view, title, ...shell }: MenuShell & { view: AutopilotView; title: string }) {
  const viewer = useViewer();
  if (canSteer(viewer, view.run.createdBy)) return <SteeredAutopilotMenu view={view} title={title} {...shell} />;
  return <AutopilotMenuView view={view} title={title} steer={[]} {...shell} />;
}

function SteeredAutopilotMenu({ view, title, ...shell }: MenuShell & { view: AutopilotView; title: string }) {
  return <AutopilotMenuView view={view} title={title} steer={useSteerItems(view, true)} {...shell} />;
}

function AutopilotMenuView({
  view,
  title,
  steer,
  ...shell
}: MenuShell & { view: AutopilotView; title: string; steer: StationMenuItem[] }) {
  const { t } = useI18n();
  const items: StationMenuItem[] = [
    { id: "goal", label: t("station.progress.openGoal", { name: title }), onSelect: () => openGoal(view.run.goalNodeId) },
    ...steer.map((item, index) => ({ ...item, separatorBefore: index === 0 })),
  ];
  return <StationMenu items={items} {...shell} />;
}

/** What went wrong steering an autopilot, said where its tile is. */
export function useAutopilotError(view: AutopilotView): string {
  return useAutopilot((s) => s.errors[view.run.goalNodeId] ?? "");
}
