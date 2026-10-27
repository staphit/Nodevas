// The war room: one isometric room per runner (plus the terminal room for
// agents people run on their own machines), a desk per character. Each
// character is a real button; its menu holds what can be done about it.

import { Fragment, memo, useCallback, useMemo, useRef, useState, type CSSProperties } from "react";

import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import { useGameActions } from "../GameActions";
import { GameMenu, type GameMenuState } from "../GameMenu";
import { ZoomControl } from "../Zoom";
import {
  actionLabel,
  actorLabel,
  agentColorKey,
  agentName,
  agentWho,
  energyLabel,
  formatTokens,
  roomName,
  type TitleOf,
} from "../labels";
import type { Actor, Meeting, Office, Room } from "../world";
import { PackArt } from "../packs";
import { at, DeskSvg, RoomShell, roomFrame, SeatedFigure, StandingFigure, TableSvg } from "./OfficeArt";
import { layoutRoom, type Seat } from "./roomLayout";

/** A short glyph per pose: the state has a cue that is not a colour. */
export const POSE_GLYPH: Record<Actor["pose"], string> = {
  offline: "z",
  "asking-user": "?",
  failed: "!",
  "awaiting-accept": "…",
  "waiting-ci": "⧗",
  reviewing: "✎",
  working: "⌨",
  idle: "·",
};

export function WarRoom({ office, titleOf }: { office: Office; titleOf: TitleOf }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const [menu, setMenu] = useState<GameMenuState | null>(null);
  // A wide window has room to draw the office larger than its 1:1 art.
  const [zoom, setZoom] = useState(() => (typeof window !== "undefined" && window.innerWidth >= 1200 ? 1.2 : 1));
  const actorsById = useMemo(() => new Map(office.actors.map((actor) => [actor.id, actor])), [office.actors]);
  const rooms = office.rooms.filter((room) => room.kind !== "terminal" || room.actorIds.length > 0);

  // The sprites are memoised on what they show; a click reads the current
  // character, whose actions may have changed without its look changing.
  const latest = useRef(actorsById);
  latest.current = actorsById;
  const openActor = useCallback(
    (actorId: string, room: Room, anchor: HTMLElement) => {
      const actor = latest.current.get(actorId);
      if (!actor) return;
      setMenu({
        anchor,
        title: agentWho(actor.agent, actor.model),
        detail: actorLabel(t, actor, roomName(t, room)),
        items: actor.actions.map((target, i) => ({
          id: `${target.kind}-${i}`,
          label: actionLabel(t, target, titleOf),
          urgent: target.kind === "ask" || target.kind === "held-message",
          onSelect: () => dispatch(target),
        })),
      });
    },
    [dispatch, t, titleOf],
  );

  const openMeeting = useCallback(
    (meeting: Meeting, anchor: HTMLElement) => {
      setMenu({
        anchor,
        title: t("game.meeting.title", { title: meeting.nodeTitle || meeting.nodeId }),
        detail: meetingLabel(t, meeting),
        items: [
          {
            id: "node",
            label: actionLabel(t, meeting.target, titleOf),
            onSelect: () => dispatch(meeting.target),
          },
        ],
      });
    },
    [dispatch, t, titleOf],
  );

  if (rooms.length === 0) {
    return (
      <div className="game-empty">
        <p>{t("game.warroom.noRooms")}</p>
        <p className="game-empty-hint">{t("game.warroom.noRoomsHint")}</p>
      </div>
    );
  }

  return (
    <div className="game-office">
      <div className="game-office-hud">
        {office.energy && <span>{t("game.energy.today")}</span>}
        {office.energy?.cap ? (
            <span
              className="game-meter"
              role="meter"
              aria-label={t("game.energy.today")}
              aria-valuemin={0}
              aria-valuemax={office.energy.cap}
              aria-valuenow={Math.min(office.energy.used, office.energy.cap)}
              aria-valuetext={energyLabel(t, office.energy.used, office.energy.cap)}
            >
              <span
                className={`game-meter-fill${office.energy.used >= office.energy.cap ? " full" : ""}`}
                style={{ width: `${Math.min(100, (office.energy.used / office.energy.cap) * 100)}%` }}
              />
            </span>
          ) : null}
        {office.energy && (
          <span className="game-office-hud-value">
            {energyLabel(t, office.energy.used, office.energy.cap)}
          </span>
        )}
        <ZoomControl zoom={zoom} onZoom={setZoom} />
      </div>
      <div className="game-rooms" style={{ zoom }}>
        {rooms.map((room) => (
          <RoomView
            key={room.id}
            room={room}
            actorsById={actorsById}
            meetings={office.meetings.filter((meeting) => meeting.roomId === room.id)}
            onActor={openActor}
            onMeeting={openMeeting}
          />
        ))}
      </div>
      {menu && <GameMenu menu={menu} onClose={() => setMenu(null)} />}
    </div>
  );
}

export function meetingLabel(t: (key: string, values?: Record<string, string | number>) => string, meeting: Meeting): string {
  const parts = [
    t("game.meeting.label", {
      title: meeting.nodeTitle || meeting.nodeId,
      count: meeting.actorIds.length,
    }),
  ];
  if (meeting.round) parts.push(t("game.meeting.round", { round: meeting.round }));
  if (meeting.waitingHuman) parts.push(t("game.meeting.waitingHuman"));
  for (const vote of meeting.votes) {
    parts.push(t("game.meeting.vote", { who: agentWho(vote.agent, vote.model), verdict: t(`game.verdict.${vote.verdict || "pending"}`) }));
  }
  return parts.join(" ");
}

function RoomView({
  room,
  actorsById,
  meetings,
  onActor,
  onMeeting,
}: {
  room: Room;
  actorsById: ReadonlyMap<string, Actor>;
  meetings: Meeting[];
  onActor: (actorId: string, room: Room, anchor: HTMLElement) => void;
  onMeeting: (meeting: Meeting, anchor: HTMLElement) => void;
}) {
  const { t } = useI18n();
  const layout = useMemo(() => layoutRoom(room, actorsById, meetings), [room, actorsById, meetings]);
  const frame = roomFrame(layout.cols, layout.rows);
  const name = roomName(t, room);
  const meetingById = new Map(meetings.map((meeting) => [meeting.pipelineId, meeting]));

  type Item = { key: string; depth: number; node: React.ReactNode };
  const items: Item[] = [];
  const place = (col: number, row: number) => {
    const p = at(frame, col, row);
    return { left: p.x, top: p.y, zIndex: Math.round((col + row) * 10) };
  };
  for (const desk of layout.emptyDesks) {
    items.push({
      key: `empty-${desk.col}-${desk.row}`,
      depth: desk.col + desk.row,
      node: (
        <div
          className={`game-seat desk empty agent--${agentColorKey(desk.agent)}`}
          style={place(desk.col, desk.row)}
          aria-hidden="true"
          title={desk.agent ? t("game.warroom.emptyDesk", { agent: agentName(desk.agent) }) : undefined}
        >
          <PackArt keys={["desk.empty"]} className="game-desk empty" width={80} height={96}>
            <DeskSvg empty />
          </PackArt>
        </div>
      ),
    });
  }
  for (const seat of layout.seats) {
    const actor = actorsById.get(seat.actorId);
    if (!actor) continue;
    items.push({
      key: seat.actorId,
      depth: seat.col + seat.row,
      node: (
        <ActorSprite
          actor={actor}
          seatKind={seat.kind}
          label={actorLabel(t, actor, name)}
          place={place(seat.col, seat.row)}
          room={room}
          onOpen={onActor}
        />
      ),
    });
  }
  for (const table of layout.tables) {
    const meeting = meetingById.get(table.pipelineId);
    if (!meeting) continue;
    items.push({
      key: `table-${table.pipelineId}`,
      depth: table.col + table.row - 0.01,
      node: (
        <button
          type="button"
          className={`game-seat table${meeting.waitingHuman ? " waiting-human" : ""}`}
          style={place(table.col, table.row)}
          aria-label={meetingLabel(t, meeting)}
          onClick={(event) => onMeeting(meeting, event.currentTarget)}
        >
          <PackArt keys={["table"]} className="game-table" width={96} height={56}>
            <TableSvg />
          </PackArt>
          {meeting.votes.length > 0 && (
            <span className="game-votes" aria-hidden="true">
              {meeting.votes.map((vote, i) => (
                <span key={i} className={`game-vote vote--${vote.verdict}`}>
                  {vote.verdict === "approve" ? "✓" : vote.verdict === "changes" ? "✗" : "•"}
                </span>
              ))}
            </span>
          )}
          {meeting.waitingHuman && (
            <span className="game-badge waiting" aria-hidden="true">
              !
            </span>
          )}
          <span className="game-nameplate" aria-hidden="true">
            <span className="game-nameplate-title">✎ {meeting.nodeTitle || meeting.nodeId}</span>
          </span>
        </button>
      ),
    });
  }
  items.sort((a, b) => a.depth - b.depth);

  const queueing = layout.seats.some((seat) => seat.kind === "queue");
  return (
    <section
      className={`game-room${room.online === false ? " offline" : ""}${room.kind === "terminal" ? " terminal" : ""}`}
      aria-label={t("game.room.label", { name })}
    >
      <header className="game-room-head">
        <span className="game-room-name">{name}</span>
        {room.kind === "runner" && room.online !== null && (
          <span className={`game-chip ${room.online ? "online" : "offline"}`}>
            {room.online ? t("game.room.online") : t("game.room.offline")}
          </span>
        )}
        {room.ownerName && <span className="game-room-meta">{room.ownerName}</span>}
        {room.hostname && <span className="game-room-meta mono">{room.hostname}</span>}
      </header>
      <div className="game-room-scroll">
        <div className="game-room-scene" style={{ width: frame.width, height: frame.height }}>
          <RoomShell frame={frame} door={layout.door} terminal={room.kind === "terminal"} doorOpen={queueing} />
          {items.map((item) => (
            <Fragment key={item.key}>{item.node}</Fragment>
          ))}
        </div>
      </div>
    </section>
  );
}

interface ActorSpriteProps {
  actor: Actor;
  seatKind: Seat["kind"];
  label: string;
  place: { left: number; top: number; zIndex: number };
  room: Room;
  onOpen: (actorId: string, room: Room, anchor: HTMLElement) => void;
}

/** What a character shows: when none of it changed, a heartbeat that
 * rebuilt the world does not redraw it. The label carries the ask, the held
 * mail, the bubble, the swap and the energy. */
function sameSprite(a: ActorSpriteProps, b: ActorSpriteProps): boolean {
  return (
    a.label === b.label &&
    a.actor.id === b.actor.id &&
    a.actor.pose === b.actor.pose &&
    a.actor.nodeTitle === b.actor.nodeTitle &&
    a.actor.energy?.used === b.actor.energy?.used &&
    a.actor.energy?.cap === b.actor.energy?.cap &&
    a.seatKind === b.seatKind &&
    a.place.left === b.place.left &&
    a.place.top === b.place.top &&
    a.place.zIndex === b.place.zIndex &&
    a.room.id === b.room.id &&
    a.room.name === b.room.name &&
    a.onOpen === b.onOpen
  );
}

const ActorSprite = memo(function ActorSprite({ actor, seatKind, label, place, room, onOpen }: ActorSpriteProps) {
  const style: CSSProperties = place;
  const standing = seatKind !== "desk";
  const energy = actor.energy;
  const ratio = energy?.cap ? Math.min(1, energy.used / energy.cap) : undefined;
  return (
    <button
      type="button"
      className={`game-seat actor ${seatKind} pose-${actor.pose} agent--${agentColorKey(actor.agent)}${
        actor.swappedFrom ? " swapped" : ""
      }${actor.ask ? " has-ask" : ""}`}
      style={style}
      aria-label={label}
      data-actor={actor.id}
      data-pose={actor.pose}
      onClick={(event) => onOpen(actor.id, room, event.currentTarget)}
    >
      {standing ? (
        <PackArt keys={["agent.standing"]} className="game-standing" width={40} height={64}>
          <StandingFigure clipboard={seatKind === "meeting"} />
        </PackArt>
      ) : (
        <PackArt keys={[`agent.${actor.pose}`, "agent"]} className="game-desk" width={80} height={96}>
          <DeskSvg>
            <SeatedFigure />
          </DeskSvg>
        </PackArt>
      )}
      <span className="game-pose-glyph" aria-hidden="true">
        {POSE_GLYPH[actor.pose]}
      </span>
      {actor.ask && (
        <span className="game-badge hand" aria-hidden="true">
          ✋?
        </span>
      )}
      {actor.heldMail.length > 0 && (
        <span className="game-badge mail" aria-hidden="true">
          ✉{actor.heldMail.length > 1 ? actor.heldMail.length : ""}
        </span>
      )}
      {actor.swappedFrom && (
        <span className="game-badge swap" aria-hidden="true">
          ⇄ {agentName(actor.swappedFrom.agent)}
        </span>
      )}
      {actor.bubble && (
        <span className="game-bubble" aria-hidden="true">
          {excerpt(actor.bubble.body, 48)}
        </span>
      )}
      {/* At a meeting the table carries the node's name. */}
      {seatKind !== "meeting" && (actor.nodeTitle || energy) && (
        <span className="game-nameplate" aria-hidden="true">
          <span className="game-nameplate-title">{actor.nodeTitle || agentName(actor.agent)}</span>
          {energy && (
            <span className="game-energy" title={formatTokens(energy.used)}>
              {ratio !== undefined ? (
                <span className="game-energy-bar">
                  <span style={{ width: `${Math.max(4, ratio * 100)}%` }} />
                </span>
              ) : (
                <span className="game-energy-count">⚡{formatTokens(energy.used)}</span>
              )}
            </span>
          )}
        </span>
      )}
    </button>
  );
}, sameSprite);
