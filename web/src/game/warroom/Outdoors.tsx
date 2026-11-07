// Out of the office: agents whose run waits for a usage limit to reset go
// on an outing until it does (game/outing.ts), a hike, a stream, fishing, a
// camp, a camp barbecue or a bike ride, each in a tile of its own, as far
// along as the wait is. This
// project's agents keep their full menu (AgentDesk, as in the lounge);
// another project's are visitors, a tag saying which project, the menu
// going there. With nobody out, there is nothing to draw.

import { memo, useMemo, type CSSProperties } from "react";

import { useI18n } from "../../i18n";
import { useAnimeSheet } from "../anime/sheets";
import { characterOf } from "../character";
import type { ControlRow } from "../../station/control";
import { StationMenu } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import {
  actorLabel,
  agentColorKey,
  agentName,
  agentWho,
  areaName,
  outdoorVisitorLabel,
  placeOf,
  type LabelOf,
  type TitleOf,
} from "../labels";
import { OUTDOOR_H, OUTDOOR_W } from "../pixel/outdoorSprites";
import { PixelSprite } from "../pixel/PixelSprite";
import type { Actor, Building, OutdoorSpot } from "../world";
import { OUTDOOR_LOOP_MS, outdoorScene } from "./deskArt";
import { AgentDesk, menuItem } from "./PixelDesk";

type Visitor = NonNullable<OutdoorSpot["visitor"]>;

const OutdoorVisitor = memo(function OutdoorVisitor({ visitor, labelOf }: { visitor: Visitor; labelOf: LabelOf }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const kind = agentColorKey(visitor.agent);
  const { kind: outing, step } = visitor.outing;
  const character = characterOf(visitor.agent, visitor.model);
  const anime = useAnimeSheet(character);
  const parts = useMemo(
    () => outdoorScene({ kind: outing, step }, kind, { character, anime }),
    [outing, step, kind, character, anime],
  );
  const label = outdoorVisitorLabel(t, visitor);
  return (
    <StationMenu
      items={visitor.actions.map(menuItem(t, dispatch, () => "", labelOf))}
      label={label}
      heading={{ title: agentWho(visitor.agent, visitor.model), detail: label }}
      className="px-cell is-outdoor is-visitor"
      triggerClassName={`px-desk pose-limit outing--${outing} agent--${kind} char--${character}`}
      trigger={
        <>
          <span className="px-art">
            <PixelSprite
              parts={parts}
              width={OUTDOOR_W}
              height={OUTDOOR_H}
              className="px-anim"
              style={{ ["--px-loop" as string]: `${OUTDOOR_LOOP_MS[outing]}ms` } as CSSProperties}
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

export function Outdoors({
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
  const spots = building.outdoors.filter((spot) => spot.visitor || (spot.actorId && actorsById.has(spot.actorId)));
  if (spots.length === 0 && building.outdoorsMore === 0) return null;

  const tile = (spot: OutdoorSpot) => {
    if (spot.visitor) return <OutdoorVisitor key={spot.id} visitor={spot.visitor} labelOf={labelOf} />;
    const actor = actorsById.get(spot.actorId!)!;
    return (
      <AgentDesk
        key={spot.id}
        actor={actor}
        scene="outdoor"
        style={style}
        label={actorLabel(t, actor, placeOf(t, actor))}
        row={actor.runId ? rowOf(actor.runId) : undefined}
        titleOf={titleOf}
      />
    );
  };

  return (
    <section className="px-outdoors" aria-label={areaName(t, "outdoors")}>
      <h3 className="px-room-title">{areaName(t, "outdoors")}</h3>
      <div className="px-outdoors-field">{spots.map(tile)}</div>
      {building.outdoorsMore > 0 && (
        <p className="px-room-note">{t("game.outdoors.more", { count: building.outdoorsMore })}</p>
      )}
    </section>
  );
}
