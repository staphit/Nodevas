// The same world as plain text: a list per room (war room) or per monster
// state (boss map), each entry a sentence and the same actions the sprites
// offer. For screen readers, small screens, and anyone who would rather read.

import { useI18n } from "../i18n";
import { monsterActions } from "./dungeon/Dungeon";
import { useGameActions } from "./GameActions";
import {
  actionLabel,
  actorLabel,
  doorLabel,
  energyLabel,
  monsterLabel,
  partyLabel,
  roomName,
  type TitleOf,
} from "./labels";
import { meetingLabel } from "./warroom/WarRoom";
import type { Actor, MonsterState, World } from "./world";

export function GameList({
  skin,
  world,
  titleOf,
  canDispatch,
}: {
  skin: "warroom" | "dungeon";
  world: World;
  titleOf: TitleOf;
  canDispatch: boolean;
}) {
  return (
    <div className="game-list">
      {skin === "warroom" ? (
        <OfficeList world={world} titleOf={titleOf} />
      ) : (
        <DungeonList world={world} titleOf={titleOf} canDispatch={canDispatch} />
      )}
    </div>
  );
}

function OfficeList({ world, titleOf }: { world: World; titleOf: TitleOf }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const office = world.office;
  const byId = new Map(office.actors.map((actor) => [actor.id, actor]));
  const rooms = office.rooms.filter((room) => room.kind !== "terminal" || room.actorIds.length > 0);
  return (
    <>
      {office.energy && <p className="game-list-line">{t("game.energy.today")}: {energyLabel(t, office.energy.used, office.energy.cap)}</p>}
      {rooms.length === 0 && <p className="game-list-line">{t("game.warroom.noRooms")}</p>}
      {rooms.map((room) => {
        const name = roomName(t, room);
        const actors = room.actorIds.map((id) => byId.get(id)).filter((actor): actor is Actor => Boolean(actor));
        const meetings = office.meetings.filter((meeting) => meeting.roomId === room.id);
        return (
          <section key={room.id} className="game-list-group" aria-label={t("game.room.label", { name })}>
            <h3>
              {name}
              {room.kind === "runner" && room.online !== null && (
                <span className={`game-chip ${room.online ? "online" : "offline"}`}>
                  {room.online ? t("game.room.online") : t("game.room.offline")}
                </span>
              )}
            </h3>
            {actors.length === 0 ? (
              <p className="game-list-line muted">{t("game.room.empty")}</p>
            ) : (
              <ul>
                {actors.map((actor) => (
                  <li key={actor.id} data-pose={actor.pose}>
                    <span className="game-list-text">{actorLabel(t, actor, name)}</span>
                    <span className="game-list-actions">
                      {actor.actions.map((target, i) => (
                        <button
                          key={`${target.kind}-${i}`}
                          type="button"
                          className={`board-tool-button${target.kind === "ask" || target.kind === "held-message" ? " active" : ""}`}
                          onClick={() => dispatch(target)}
                        >
                          {actionLabel(t, target, titleOf)}
                        </button>
                      ))}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {meetings.length > 0 && (
              <ul>
                {meetings.map((meeting) => (
                  <li key={meeting.pipelineId}>
                    <span className="game-list-text">{meetingLabel(t, meeting)}</span>
                    <span className="game-list-actions">
                      <button type="button" className="board-tool-button" onClick={() => dispatch(meeting.target)}>
                        {actionLabel(t, meeting.target, titleOf)}
                      </button>
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        );
      })}
    </>
  );
}

const STATE_ORDER: MonsterState[] = ["attackable", "locked", "defeated"];

function DungeonList({ world, titleOf, canDispatch }: { world: World; titleOf: TitleOf; canDispatch: boolean }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const dungeon = world.dungeon;
  const boss = dungeon.boss;
  const ctx = { t, titleOf, canDispatch };
  return (
    <>
      <p className="game-list-line">
        {t("game.boss.label", { name: boss.name, hp: boss.hp, max: boss.maxHp })} · {t("game.xp.total", { xp: dungeon.xp })}
      </p>
      {dungeon.parties.length > 0 && (
        <section className="game-list-group" aria-label={t("game.party.heading")}>
          <h3>{t("game.party.heading")}</h3>
          <ul>
            {dungeon.parties.map((party) => (
              <li key={party.autopilotId}>
                <span className="game-list-text">{partyLabel(t, party, titleOf)}</span>
                <span className="game-list-actions">
                  <button type="button" className="board-tool-button" onClick={() => dispatch(party.target)}>
                    {actionLabel(t, party.target, titleOf)}
                  </button>
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {STATE_ORDER.map((state) => {
        const monsters = dungeon.monsters.filter((monster) => monster.state === state);
        if (!monsters.length) return null;
        return (
          <section key={state} className="game-list-group" aria-label={t(`game.list.${state}`)}>
            <h3>
              {t(`game.list.${state}`)} <span className="muted">({monsters.length})</span>
            </h3>
            <ul>
              {monsters.map((monster) => (
                <li key={monster.nodeId} data-state={monster.state}>
                  <span className="game-list-text">{monsterLabel(t, monster, titleOf)}</span>
                  <span className="game-list-actions">
                    {monsterActions(monster, ctx).map((action) => (
                      <button key={action.id} type="button" className="board-tool-button" onClick={action.run}>
                        {action.label}
                      </button>
                    ))}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
      {dungeon.doors.length > 0 && (
        <section className="game-list-group" aria-label={t("game.list.doors")}>
          <h3>{t("game.list.doors")}</h3>
          <ul>
            {dungeon.doors.map((door) => (
              <li key={door.gateId}>
                <span className="game-list-text">{doorLabel(t, door, titleOf)}</span>
                <span className="game-list-actions">
                  <button type="button" className="board-tool-button" onClick={() => dispatch(door.target)}>
                    {actionLabel(t, door.target, titleOf)}
                  </button>
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
