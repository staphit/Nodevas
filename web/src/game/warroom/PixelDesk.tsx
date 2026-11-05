// One character of the current office, wherever it is: at a desk, at the
// meeting-room table, at the door, on a lounge sofa or out on an outing (and
// a free desk or an empty seat with nobody). An agent is a real button, its menu what can be
// done about it: a run's own controls (station/runMenuItems) plus the
// question it asked, a held message to it and its PR; a session's targets
// otherwise. The name tag under it is plain text, so it stays sharp at any
// zoom.

import { memo, useMemo, type CSSProperties } from "react";

import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import type { ControlRow } from "../../station/control";
import { RunMenu } from "../../station/runMenuItems";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import {
  actionLabel,
  agentColorKey,
  agentName,
  agentWho,
  formatTokens,
  poseLabel,
  type LabelOf,
  type TitleOf,
  type Translate,
} from "../labels";
import type { Activity } from "../activity";
import type { Outing } from "../outing";
import { OUTDOOR_H, OUTDOOR_W } from "../pixel/outdoorSprites";
import { CELL } from "../pixel/sprites";
import { PixelSprite } from "../pixel/PixelSprite";
import type { Actor, ClickTarget, OfficeStyle } from "../world";
import {
  deskLoopMs,
  deskScene,
  doorScene,
  meetingScene,
  OUTDOOR_LOOP_MS,
  outdoorScene,
  POSE_LOOP_MS,
  SEAT_W,
  sofaScene,
  type MeetingOptions,
} from "./deskArt";

export type DeskSceneKind = "desk" | "door" | "meeting" | "sofa" | "outdoor";

/** A free desk (a runner's free slot), or a spare one (padding, no slot):
 * furniture only, nothing to press. */
export const FreeDesk = memo(function FreeDesk({ style, spare = false }: { style: OfficeStyle; spare?: boolean }) {
  const parts = useMemo(() => deskScene("empty", "other", { style, spare }), [style, spare]);
  return (
    <div className={`px-cell ${spare ? "is-spare" : "is-free"}`} aria-hidden="true">
      <span className="px-art">
        <PixelSprite parts={parts} width={CELL} height={CELL} />
      </span>
    </div>
  );
});

/** An empty seat at the meeting-room table. */
export const EmptySeat = memo(function EmptySeat({ style, edge }: { style: OfficeStyle; edge: MeetingOptions["edge"] }) {
  const parts = useMemo(() => meetingScene("empty", "other", { style, edge }), [style, edge]);
  return (
    <div className="px-cell is-seat is-free" aria-hidden="true">
      <span className="px-art">
        <PixelSprite parts={parts} width={CELL} height={CELL} />
      </span>
    </div>
  );
});

export interface AgentDeskProps {
  actor: Actor;
  /** Where it is drawn: at a desk, at the meeting table, at the door, on a
   * sofa, outdoors. */
  scene: DeskSceneKind;
  style: OfficeStyle;
  /** A meeting seat's place along the table. */
  edge?: MeetingOptions["edge"];
  /** The sentence that names it: who, where, doing what, on which node. */
  label: string;
  /** More about it for its menu's heading: a review's votes and round. */
  detail?: string;
  /** The run it is, when it is one in view. */
  row?: ControlRow;
  titleOf: TitleOf;
}

/** What a desk shows and offers: when none of it changed, a heartbeat that
 * rebuilt the world does not redraw it. The label carries the ask, the held
 * mail, the bubble, the swap, the energy and the minutes an outing has
 * left; the row, the run's state. The activity and the outing's step are
 * drawn. */
export function sameDesk(a: AgentDeskProps, b: AgentDeskProps): boolean {
  return (
    a.label === b.label &&
    a.detail === b.detail &&
    a.scene === b.scene &&
    a.style === b.style &&
    a.edge === b.edge &&
    a.actor.id === b.actor.id &&
    a.actor.pose === b.actor.pose &&
    a.actor.nodeTitle === b.actor.nodeTitle &&
    a.actor.heldMail.length === b.actor.heldMail.length &&
    a.actor.bubble?.id === b.actor.bubble?.id &&
    a.actor.energy?.used === b.actor.energy?.used &&
    a.actor.energy?.cap === b.actor.energy?.cap &&
    a.actor.role === b.actor.role &&
    a.actor.testState === b.actor.testState &&
    a.actor.activity === b.actor.activity &&
    a.actor.outing?.kind === b.actor.outing?.kind &&
    a.actor.outing?.step === b.actor.outing?.step &&
    sameTargets(a.actor.actions, b.actor.actions) &&
    a.row === b.row &&
    a.titleOf === b.titleOf
  );
}

export function sameTargets(a: ClickTarget[], b: ClickTarget[]): boolean {
  return a.length === b.length && a.every((target, i) => JSON.stringify(target) === JSON.stringify(b[i]));
}

const BADGED_ROLES: ReadonlySet<string> = new Set(["planner", "qa", "tester", "executor", "reviewer", "researcher"]);

/** A role's badge: a built-in's own, "custom" for any other. */
export function roleBadgeKey(role: string): string {
  return BADGED_ROLES.has(role) ? role : "custom";
}

/** Targets a run's own menu does not already cover: a question it asked, a
 * held message to it (both urgent, so first), its PR. */
const EXTRA_FOR_RUN: ReadonlySet<ClickTarget["kind"]> = new Set(["ask", "held-message", "pr"]);

const urgent = (target: ClickTarget) => target.kind === "ask" || target.kind === "held-message";

export const urgentFirst = (targets: ClickTarget[]) =>
  [...targets].sort((x, y) => Number(urgent(y)) - Number(urgent(x)));

/** A target as a menu item. */
export function menuItem(
  t: Translate,
  dispatch: (target: ClickTarget) => void,
  titleOf: TitleOf,
  labelOf?: LabelOf,
) {
  return (target: ClickTarget, i: number): StationMenuItem => ({
    id: `${target.kind}-${i}`,
    label: actionLabel(t, target, titleOf, labelOf),
    onSelect: () => dispatch(target),
  });
}

/** Where an outdoor actor is when its outing is unknown: just out, by the
 * tent. */
const NO_OUTING: Pick<Outing, "kind" | "step"> = { kind: "camp", step: 0 };

interface SceneOf {
  scene: DeskSceneKind;
  pose: Actor["pose"];
  mail: boolean;
  kind: string;
  style: OfficeStyle;
  edge: AgentDeskProps["edge"];
  activity?: Activity;
  outing: Pick<Outing, "kind" | "step">;
}

function sceneParts({ scene, pose, mail, kind, style, edge, activity, outing }: SceneOf) {
  switch (scene) {
    case "door":
      return doorScene(kind, { mail });
    case "meeting":
      return meetingScene(pose, kind, { mail, style, edge, activity });
    case "sofa":
      return sofaScene(pose, kind);
    case "outdoor":
      return outdoorScene(outing, kind);
    default:
      return deskScene(pose, kind, { mail, style, activity });
  }
}

/** A scene's loop, in ms; none for one that holds still. */
export function sceneLoopMs(
  scene: DeskSceneKind,
  pose: Actor["pose"],
  activity: Activity | undefined,
  outing: Pick<Outing, "kind">,
): number | undefined {
  switch (scene) {
    case "door":
      return undefined;
    case "sofa":
      return POSE_LOOP_MS[pose];
    case "outdoor":
      return OUTDOOR_LOOP_MS[outing.kind];
    default:
      return deskLoopMs(pose, activity);
  }
}

/** A scene's frame in art pixels. */
function sceneSize(scene: DeskSceneKind): { width: number; height: number } {
  if (scene === "outdoor") return { width: OUTDOOR_W, height: OUTDOOR_H };
  return { width: scene === "sofa" ? SEAT_W : CELL, height: CELL };
}

export const AgentDesk = memo(function AgentDesk({
  actor,
  scene,
  style,
  edge,
  label,
  detail,
  row,
  titleOf,
}: AgentDeskProps) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const kind = agentColorKey(actor.agent);
  const mail = actor.heldMail.length > 0;
  const pose = actor.pose;
  const activity = actor.activity;
  const outingKind = actor.outing?.kind ?? NO_OUTING.kind;
  const outingStep = actor.outing?.step ?? NO_OUTING.step;
  const parts = useMemo(
    () =>
      sceneParts({ scene, pose, mail, kind, style, edge, activity, outing: { kind: outingKind, step: outingStep } }),
    [scene, pose, mail, kind, style, edge, activity, outingKind, outingStep],
  );
  const loop = sceneLoopMs(scene, pose, activity, { kind: outingKind });
  const who = agentWho(actor.agent, actor.model);
  const energy = actor.energy;
  const ratio = energy?.cap ? Math.min(1, energy.used / energy.cap) : undefined;
  const { width, height } = sceneSize(scene);
  /** Out of the office or on a sofa: a short tag, no meter. */
  const away = scene === "sofa" || scene === "outdoor";
  const asItem = menuItem(t, dispatch, titleOf);

  const shell = {
    label,
    heading: { title: who, detail: detail ? `${label} ${detail}` : label },
    className: `px-cell is-${scene}`,
    triggerClassName: `px-desk pose-${actor.pose} agent--${kind}${scene === "door" ? " at-door" : ""}`,
    trigger: (
      <>
        <span className="px-art">
          <PixelSprite
            parts={parts}
            width={width}
            height={height}
            className={loop ? "px-anim" : undefined}
            // The loop's length, for the frames' steps() animation.
            style={loop ? ({ ["--px-loop" as string]: `${loop}ms` } as CSSProperties) : undefined}
          />
          {actor.role && (
            <span className={`px-badge is-role role--${roleBadgeKey(actor.role)}`} aria-hidden="true">
              {t(`warroom.roleBadge.${roleBadgeKey(actor.role)}`)}
            </span>
          )}
          {actor.testState && (
            <span
              className={`px-badge is-test is-${actor.testState}`}
              role="img"
              aria-label={t(`warroom.test.${actor.testState}`)}
            />
          )}
          {actor.heldMail.length > 1 && (
            <span className="px-badge is-mail" aria-hidden="true">
              {actor.heldMail.length}
            </span>
          )}
          {actor.swappedFrom && (
            <span className="px-badge is-swap" aria-hidden="true">
              ⇄ {agentName(actor.swappedFrom.agent)}
            </span>
          )}
          {actor.bubble && (
            <span className="px-say" aria-hidden="true">
              {excerpt(actor.bubble.body, 48)}
            </span>
          )}
        </span>
        <span className="px-tag" aria-hidden="true">
          <span className="px-tag-who">{away ? agentName(actor.agent) : who}</span>
          <span className="px-tag-what">{actor.nodeTitle || actor.sessionName || poseLabel(t, actor.pose)}</span>
          {energy &&
            !away &&
            (ratio !== undefined ? (
              <span className="px-meter" title={formatTokens(energy.used)}>
                <span style={{ width: `${Math.max(4, ratio * 100)}%` }} />
              </span>
            ) : (
              <span className="px-tag-tokens">⚡{formatTokens(energy.used)}</span>
            ))}
        </span>
      </>
    ),
  };
  if (actor.kind === "run" && row) {
    const extra = urgentFirst(actor.actions.filter((target) => EXTRA_FOR_RUN.has(target.kind))).map(asItem);
    return <RunMenu row={row} titleOf={titleOf} extra={extra} {...shell} />;
  }
  return <StationMenu items={urgentFirst(actor.actions).map(asItem)} {...shell} />;
}, sameDesk);
