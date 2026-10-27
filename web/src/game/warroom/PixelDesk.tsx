// One desk of a war-room room: a free one, or an agent at it (or waiting at
// the door). An agent's desk is a real button, its menu what can be done
// about it: a run's own controls (station/runMenuItems) plus the question it
// asked, a held message to it and its PR; a session's targets otherwise. The
// name tag under it is plain text, so it stays sharp at any zoom.

import { memo, useMemo, type CSSProperties } from "react";

import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import type { ControlRow } from "../../station/control";
import { RunMenu } from "../../station/runMenuItems";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import { actionLabel, agentColorKey, agentName, agentWho, formatTokens, poseLabel, type TitleOf } from "../labels";
import { CELL } from "../pixel/sprites";
import { PixelSprite } from "../pixel/PixelSprite";
import type { Actor, ClickTarget } from "../world";
import { deskScene, doorScene, POSE_LOOP_MS } from "./deskArt";

/** A free desk: furniture only, nothing to press. */
export const FreeDesk = memo(function FreeDesk() {
  const parts = useMemo(() => deskScene("empty", "other"), []);
  return (
    <div className="px-cell is-free" aria-hidden="true">
      <span className="px-art">
        <PixelSprite parts={parts} width={CELL} height={CELL} />
      </span>
    </div>
  );
});

export interface AgentDeskProps {
  actor: Actor;
  /** At the door, waiting to be let in, rather than at a desk. */
  atDoor: boolean;
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
 * mail, the bubble, the swap and the energy; the row, the run's state. */
function sameDesk(a: AgentDeskProps, b: AgentDeskProps): boolean {
  return (
    a.label === b.label &&
    a.detail === b.detail &&
    a.atDoor === b.atDoor &&
    a.actor.id === b.actor.id &&
    a.actor.pose === b.actor.pose &&
    a.actor.nodeTitle === b.actor.nodeTitle &&
    a.actor.heldMail.length === b.actor.heldMail.length &&
    a.actor.energy?.used === b.actor.energy?.used &&
    a.actor.energy?.cap === b.actor.energy?.cap &&
    sameTargets(a.actor.actions, b.actor.actions) &&
    a.row === b.row &&
    a.titleOf === b.titleOf
  );
}

function sameTargets(a: ClickTarget[], b: ClickTarget[]): boolean {
  return a.length === b.length && a.every((target, i) => JSON.stringify(target) === JSON.stringify(b[i]));
}

/** Targets a run's own menu does not already cover: a question it asked, a
 * held message to it (both urgent, so first), its PR. */
const EXTRA_FOR_RUN: ReadonlySet<ClickTarget["kind"]> = new Set(["ask", "held-message", "pr"]);

const urgent = (target: ClickTarget) => target.kind === "ask" || target.kind === "held-message";

export const AgentDesk = memo(function AgentDesk({ actor, atDoor, label, detail, row, titleOf }: AgentDeskProps) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const kind = agentColorKey(actor.agent);
  const mail = actor.heldMail.length > 0;
  const parts = useMemo(
    () => (atDoor ? doorScene(kind, { mail }) : deskScene(actor.pose, kind, { mail })),
    [atDoor, actor.pose, kind, mail],
  );
  const loop = atDoor ? undefined : POSE_LOOP_MS[actor.pose];
  const who = agentWho(actor.agent, actor.model);
  const energy = actor.energy;
  const ratio = energy?.cap ? Math.min(1, energy.used / energy.cap) : undefined;

  const asItem = (target: ClickTarget, i: number): StationMenuItem => ({
    id: `${target.kind}-${i}`,
    label: actionLabel(t, target, titleOf),
    onSelect: () => dispatch(target),
  });
  const urgentFirst = (targets: ClickTarget[]) => [...targets].sort((x, y) => Number(urgent(y)) - Number(urgent(x)));

  const shell = {
    label,
    heading: { title: who, detail: detail ? `${label} ${detail}` : label },
    className: "px-cell",
    triggerClassName: `px-desk pose-${actor.pose} agent--${kind}${atDoor ? " at-door" : ""}`,
    trigger: (
      <>
        <span className="px-art">
          <PixelSprite
            parts={parts}
            width={CELL}
            height={CELL}
            className={loop ? "px-anim" : undefined}
            // The loop's length, for the frames' steps() animation.
            style={loop ? ({ ["--px-loop" as string]: `${loop}ms` } as CSSProperties) : undefined}
          />
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
          <span className="px-tag-who">{who}</span>
          <span className="px-tag-what">{actor.nodeTitle || actor.sessionName || poseLabel(t, actor.pose)}</span>
          {energy &&
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
