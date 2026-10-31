// The lounge every office shares: agents with nothing to do. Connected
// sessions idling with no node, and runs asleep until their agent's usage
// limit resets (zzz on the sofa), from this project (their full menu) and
// from the others (a tag says which project; the menu goes there). An
// offline agent sits here only while it still holds a question or a held
// message; otherwise only the screen-reader list names it.

import { memo, useMemo } from "react";

import { useI18n } from "../../i18n";
import type { ControlRow } from "../../station/control";
import { StationMenu } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import {
  actorLabel,
  agentColorKey,
  agentName,
  agentWho,
  placeOf,
  poseLabel,
  type LabelOf,
  type TitleOf,
  type Translate,
} from "../labels";
import { PixelSprite } from "../pixel/PixelSprite";
import { CELL, PLANT, SOFA } from "../pixel/sprites";
import type { Actor, Building, LoungeSpot } from "../world";
import { SEAT_W, sofaScene } from "./deskArt";
import { lone } from "./OfficeRoom";
import { AgentDesk, menuItem } from "./PixelDesk";

/** Whether an actor in the lounge is drawn: an offline one only while it
 * holds something for the viewer. */
export function drawnInLounge(actor: Pick<Actor, "pose" | "ask" | "heldMail">): boolean {
  return actor.pose !== "offline" || Boolean(actor.ask) || actor.heldMail.length > 0;
}

type Visitor = NonNullable<LoungeSpot["visitor"]>;

export function visitorLabel(t: Translate, visitor: Visitor): string {
  return t("game.lounge.visitor", {
    who: agentWho(visitor.agent, visitor.model),
    project: visitor.label,
    pose: poseLabel(t, visitor.pose),
  });
}

const VisitorSpot = memo(function VisitorSpot({ visitor, labelOf }: { visitor: Visitor; labelOf: LabelOf }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const kind = agentColorKey(visitor.agent);
  const parts = useMemo(() => sofaScene(visitor.pose, kind), [visitor.pose, kind]);
  const label = visitorLabel(t, visitor);
  return (
    <StationMenu
      items={visitor.actions.map(menuItem(t, dispatch, () => "", labelOf))}
      label={label}
      heading={{ title: agentWho(visitor.agent, visitor.model), detail: label }}
      className="px-cell is-sofa is-visitor"
      triggerClassName={`px-desk pose-${visitor.pose} agent--${kind}`}
      trigger={
        <>
          <span className="px-art">
            <PixelSprite
              parts={parts}
              width={SEAT_W}
              height={CELL}
              className={visitor.pose === "limit" ? "px-anim" : undefined}
              style={visitor.pose === "limit" ? { ["--px-loop" as string]: "1800ms" } : undefined}
            />
          </span>
          <span className="px-tag" aria-hidden="true">
            <span className="px-tag-who">{agentName(visitor.agent)}</span>
            <span className="px-tag-what px-tag-project">{visitor.label}</span>
          </span>
        </>
      }
    />
  );
});

export function Lounge({
  building,
  actorsById,
  titleOf,
  rowOf,
  labelOf,
}: {
  building: Building;
  actorsById: ReadonlyMap<string, Actor>;
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
  labelOf: LabelOf;
}) {
  const { t } = useI18n();
  const style = building.current?.style ?? "default";
  const spots = building.lounge.filter((spot) => {
    if (spot.visitor) return true;
    const actor = spot.actorId ? actorsById.get(spot.actorId) : undefined;
    return Boolean(actor && drawnInLounge(actor));
  });
  const sofas: LoungeSpot[][] = [];
  for (let i = 0; i < Math.max(spots.length, 1); i += 2) sofas.push(spots.slice(i, i + 2));

  const seat = (spot: LoungeSpot) => {
    if (spot.visitor) return <VisitorSpot key={spot.id} visitor={spot.visitor} labelOf={labelOf} />;
    const actor = spot.actorId ? actorsById.get(spot.actorId) : undefined;
    if (!actor) return null;
    return (
      <AgentDesk
        key={spot.id}
        actor={actor}
        scene="sofa"
        style={style}
        label={actorLabel(t, actor, placeOf(t, actor))}
        row={actor.runId ? rowOf(actor.runId) : undefined}
        titleOf={titleOf}
      />
    );
  };

  return (
    <section className="px-lounge" aria-label={t("game.area.lounge")}>
      <h3 className="px-room-title">{t("game.area.lounge")}</h3>
      <div className="px-lounge-floor">
        {sofas.map((pair, i) => (
          <div key={i} className="px-sofa">
            <span className="px-sofa-art" aria-hidden="true">
              <PixelSprite parts={lone("sofa", SOFA)} width={SOFA.w} height={SOFA.h} />
            </span>
            {pair.map((spot, j) => (
              <div key={spot.id} className={`px-seat is-${j === 0 ? "left" : "right"}`}>
                {seat(spot)}
              </div>
            ))}
          </div>
        ))}
        <span className="px-lounge-plant" aria-hidden="true">
          <PixelSprite parts={lone("plant", PLANT)} width={PLANT.w} height={PLANT.h} />
        </span>
      </div>
      {spots.length === 0 && <p className="px-room-note">{t("game.lounge.empty")}</p>}
      {building.loungeMore > 0 && (
        <p className="px-room-note">{t("game.lounge.more", { count: building.loungeMore })}</p>
      )}
    </section>
  );
}
