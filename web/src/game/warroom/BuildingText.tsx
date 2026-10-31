// The building as plain text, for a screen reader: each office and room,
// who is in it doing what, what lies on the meeting table, the racks. The
// drawing's buttons are the controls; this only says what is there.

import { useMemo } from "react";

import { formatList, useI18n } from "../../i18n";
import { actorLabel, agentWho, areaName, compactLabel, itemLabel, placeOf, rackLabel } from "../labels";
import type { Actor, Area, Building } from "../world";
import { drawnInLounge, visitorLabel } from "./Lounge";

export function BuildingText({ building }: { building: Building }) {
  const { t } = useI18n();
  const actorsById = useMemo(() => new Map(building.actors.map((actor) => [actor.id, actor])), [building.actors]);
  const office = building.current;
  if (!office) return null;
  const actors = (ids: string[]) => ids.map((id) => actorsById.get(id)).filter((actor): actor is Actor => Boolean(actor));
  const say = (actor: Actor) => <li key={actor.id}>{actorLabel(t, actor, placeOf(t, actor))}</li>;
  // The runners' free slots, as the office's header says: the drawing pads
  // the floor to two desks, which are not free slots.
  const free = office.freeDesks;
  const area = (name: Area) => <h4>{areaName(t, name)}</h4>;

  const lounge = building.lounge.map((spot) => ({ spot, actor: spot.actorId ? actorsById.get(spot.actorId) : undefined }));
  const offline = lounge.filter(({ actor }) => actor && !drawnInLounge(actor)).map(({ actor }) => actor!);
  const others = [...building.others, ...building.doorSigns];

  return (
    <div className="visually-hidden" data-testid="war-room-text">
      <h3>{t("game.office.label", { name: office.label })}</h3>
      {area("work")}
      <ul>
        {actors(office.work).map(say)}
        {free > 0 && <li>{free === 1 ? t("game.warroom.freeDesksOne") : t("game.warroom.freeDesks", { count: free })}</li>}
      </ul>
      {area("meeting")}
      <ul>
        {actors(office.meeting).map(say)}
        {office.items.map((item) => (
          <li key={item.id}>{itemLabel(t, item)}</li>
        ))}
        {office.meeting.length + office.items.length === 0 && <li>{t("game.meeting.empty")}</li>}
      </ul>
      {area("entrance")}
      <ul>
        {actors(office.entrance).map(say)}
        {office.entrance.length === 0 && <li>{t("game.entrance.empty")}</li>}
      </ul>
      {others.length > 0 && (
        <>
          <h3>{t("game.building.others")}</h3>
          <ul>
            {others.map((other) => (
              <li key={other.project}>{compactLabel(t, other)}</li>
            ))}
          </ul>
        </>
      )}
      <h3>{areaName(t, "lounge")}</h3>
      <ul>
        {lounge.map(({ spot, actor }) => {
          if (spot.visitor) return <li key={spot.id}>{visitorLabel(t, spot.visitor)}</li>;
          return actor && drawnInLounge(actor) ? say(actor) : null;
        })}
        {offline.length > 0 && (
          <li>{t("game.lounge.offline", { names: formatList(offline.map((actor) => agentWho(actor.agent, actor.model))) })}</li>
        )}
        {building.lounge.length === 0 && <li>{t("game.lounge.empty")}</li>}
        {building.loungeMore > 0 && <li>{t("game.lounge.more", { count: building.loungeMore })}</li>}
      </ul>
      <h3>{t("game.area.serverRoom")}</h3>
      <ul>
        {building.racks.map((rack) => (
          <li key={rack.runnerId}>{rackLabel(t, rack)}</li>
        ))}
        {building.racks.length === 0 && <li>{t("game.serverRoom.none")}</li>}
      </ul>
    </div>
  );
}
