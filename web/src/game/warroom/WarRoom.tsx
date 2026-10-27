// The war room: one isometric room per runner (plus the terminal room for
// agents people run on their own machines), a desk per character. Each
// character is a real button; its menu holds what can be done about it (a
// run's own controls, through station/runMenuItems). Station mounts it
// lazily (StationWarRoom), so its code and styles only reach a browser that
// opens Station. A text list of the same rooms is there for screen readers.

import { Fragment, memo, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";

import "../game.css";

import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import { useWorkstationVisible } from "../../runners/workstation";
import type { ControlRow } from "../../station/control";
import { setProjectMode } from "../../station/projectMode";
import { RunMenu } from "../../station/runMenuItems";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useApp } from "../../store";
import { useGameActions } from "../GameActions";
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
  type Translate,
} from "../labels";
import { usePageHidden, useReducedMotion } from "../useMotion";
import type { Actor, ClickTarget, Meeting, Office, Room } from "../world";
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

export interface WarRoomProps {
  office: Office;
  titleOf: TitleOf;
  /** The run behind a character, with its autopilot: what its menu acts on. */
  rowOf: (runId: string) => ControlRow | undefined;
}

/** The room as Station shows it: still under reduced motion, paused in a
 * background tab, and a way back when the workstation is hidden. */
export default function WarRoomStage(props: WarRoomProps) {
  const reduced = useReducedMotion();
  const hidden = usePageHidden();
  const workstation = useWorkstationVisible();
  const announcement = useOfficeAnnouncements(props.office, workstation);
  return (
    <div className="game-stage" data-motion={reduced ? "reduced" : "full"} data-paused={hidden ? "true" : undefined}>
      {workstation ? (
        <>
          <WarRoom {...props} />
          <WarRoomText office={props.office} />
        </>
      ) : (
        <WarRoomOff />
      )}
      <div className="visually-hidden" aria-live="polite">
        {announcement}
      </div>
    </div>
  );
}

/** The war room shows the agent workstation; with it hidden there is
 * nothing to draw, so say how to bring it back. */
function WarRoomOff() {
  const { t } = useI18n();
  return (
    <div className="game-empty" role="status">
      <p>{t("game.warroom.off")}</p>
      <p className="game-empty-hint">{t("game.warroom.offHint")}</p>
      <div className="game-empty-actions">
        <button
          type="button"
          className="board-tool-button active"
          onClick={() => useApp.getState().updateUIPreference("agentWorkstation", true)}
        >
          {t("workstation.enable")}
        </button>
        <button type="button" className="board-tool-button" onClick={() => setProjectMode("info")}>
          {t("mode.toInfo")}
        </button>
      </div>
    </div>
  );
}

/** Says a raised hand or a held message once, when it appears: heartbeats
 * rebuild the world every few seconds and must not repeat it. */
function useOfficeAnnouncements(office: Office, active: boolean): string {
  const { t } = useI18n();
  const seen = useRef<Set<string> | null>(null);
  const [text, setText] = useState("");
  useEffect(() => {
    if (!active) return;
    const current = new Set<string>();
    const fresh: string[] = [];
    for (const actor of office.actors) {
      const who = agentWho(actor.agent, actor.model);
      if (actor.ask) {
        current.add(`ask:${actor.ask.id}`);
        if (seen.current && !seen.current.has(`ask:${actor.ask.id}`)) fresh.push(t("game.announce.ask", { who }));
      }
      for (const message of actor.heldMail) {
        current.add(`held:${message.id}`);
        if (seen.current && !seen.current.has(`held:${message.id}`)) fresh.push(t("game.announce.held", { who }));
      }
    }
    seen.current = current;
    if (fresh.length) setText(fresh.join(" "));
  }, [office, active, t]);
  return text;
}

/** Rooms worth drawing: every runner's, and the terminal room once someone
 * is in it. */
function shownRooms(office: Office): Room[] {
  return office.rooms.filter((room) => room.kind !== "terminal" || room.actorIds.length > 0);
}

/** Installed agent kinds with nobody of that kind at a desk: the free desks. */
function freeDesks(room: Room, actors: Actor[]): string[] {
  const busy = new Map<string, number>();
  for (const actor of actors) busy.set(actor.agent, (busy.get(actor.agent) ?? 0) + 1);
  return room.agentKinds.filter((kind) => {
    const left = busy.get(kind) ?? 0;
    if (left > 0) busy.set(kind, left - 1);
    return left === 0;
  });
}

/** The same rooms as plain text, for a screen reader: who sits where doing
 * what, and the free desks. The characters' buttons are the controls. */
function WarRoomText({ office }: { office: Office }) {
  const { t } = useI18n();
  const actorsById = useMemo(() => new Map(office.actors.map((actor) => [actor.id, actor])), [office.actors]);
  const rooms = shownRooms(office);
  if (rooms.length === 0) return null;
  return (
    <div className="visually-hidden" data-testid="war-room-text">
      {rooms.map((room) => {
        const name = roomName(t, room);
        const actors = room.actorIds.map((id) => actorsById.get(id)).filter((actor): actor is Actor => Boolean(actor));
        const free = freeDesks(room, actors);
        const status =
          room.kind === "runner" && room.online !== null
            ? ` (${room.online ? t("game.room.online") : t("game.room.offline")})`
            : "";
        return (
          <Fragment key={room.id}>
            <h3>
              {t("game.room.label", { name })}
              {status}
            </h3>
            <ul>
              {actors.map((actor) => (
                <li key={actor.id}>{actorLabel(t, actor, name)}</li>
              ))}
              {free.map((kind, index) => (
                <li key={`free-${kind}-${index}`}>{t("game.warroom.emptyDesk", { agent: agentName(kind) })}</li>
              ))}
            </ul>
          </Fragment>
        );
      })}
    </div>
  );
}

export function WarRoom({ office, titleOf, rowOf }: WarRoomProps) {
  const { t } = useI18n();
  const actorsById = useMemo(() => new Map(office.actors.map((actor) => [actor.id, actor])), [office.actors]);
  const rooms = shownRooms(office);

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
      {office.energy && (
        <div className="game-office-hud">
          <span>{t("game.energy.today")}</span>
          {office.energy.cap ? (
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
          <span className="game-office-hud-value">{energyLabel(t, office.energy.used, office.energy.cap)}</span>
        </div>
      )}
      <div className="game-rooms">
        {rooms.map((room) => (
          <RoomView
            key={room.id}
            room={room}
            actorsById={actorsById}
            meetings={office.meetings.filter((meeting) => meeting.roomId === room.id)}
            titleOf={titleOf}
            rowOf={rowOf}
          />
        ))}
      </div>
    </div>
  );
}

export function meetingLabel(t: Translate, meeting: Meeting): string {
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
  titleOf,
  rowOf,
}: {
  room: Room;
  actorsById: ReadonlyMap<string, Actor>;
  meetings: Meeting[];
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
}) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const layout = useMemo(() => layoutRoom(room, actorsById, meetings), [room, actorsById, meetings]);
  const frame = roomFrame(layout.cols, layout.rows);
  const name = roomName(t, room);
  const meetingById = new Map(meetings.map((meeting) => [meeting.pipelineId, meeting]));

  type Item = { key: string; depth: number; node: ReactNode };
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
          <DeskSvg empty />
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
          row={actor.runId ? rowOf(actor.runId) : undefined}
          titleOf={titleOf}
        />
      ),
    });
  }
  for (const table of layout.tables) {
    const meeting = meetingById.get(table.pipelineId);
    if (!meeting) continue;
    const label = meetingLabel(t, meeting);
    items.push({
      key: `table-${table.pipelineId}`,
      depth: table.col + table.row - 0.01,
      node: (
        <StationMenu
          className="game-slot"
          style={place(table.col, table.row)}
          label={label}
          heading={{ title: t("game.meeting.title", { title: meeting.nodeTitle || meeting.nodeId }), detail: label }}
          triggerClassName={`game-seat table${meeting.waitingHuman ? " waiting-human" : ""}`}
          items={[
            { id: "node", label: actionLabel(t, meeting.target, titleOf), onSelect: () => dispatch(meeting.target) },
          ]}
          trigger={
            <>
              <TableSvg />
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
            </>
          }
        />
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
  /** The run it is, when it is one in view. */
  row?: ControlRow;
  titleOf: TitleOf;
}

/** What a character shows and offers: when none of it changed, a heartbeat
 * that rebuilt the world does not redraw it. The label carries the ask, the
 * held mail, the bubble, the swap and the energy; the row, the run's state. */
function sameSprite(a: ActorSpriteProps, b: ActorSpriteProps): boolean {
  return (
    a.label === b.label &&
    a.actor.id === b.actor.id &&
    a.actor.pose === b.actor.pose &&
    a.actor.nodeTitle === b.actor.nodeTitle &&
    a.actor.energy?.used === b.actor.energy?.used &&
    a.actor.energy?.cap === b.actor.energy?.cap &&
    sameTargets(a.actor.actions, b.actor.actions) &&
    a.seatKind === b.seatKind &&
    a.place.left === b.place.left &&
    a.place.top === b.place.top &&
    a.place.zIndex === b.place.zIndex &&
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

const ActorSprite = memo(function ActorSprite({ actor, seatKind, label, place, row, titleOf }: ActorSpriteProps) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const style: CSSProperties = place;
  const standing = seatKind !== "desk";
  const energy = actor.energy;
  const ratio = energy?.cap ? Math.min(1, energy.used / energy.cap) : undefined;
  const asItem = (target: ClickTarget, i: number): StationMenuItem => ({
    id: `${target.kind}-${i}`,
    label: actionLabel(t, target, titleOf),
    onSelect: () => dispatch(target),
  });
  const urgentFirst = (targets: ClickTarget[]) =>
    [...targets].sort(
      (x, y) => Number(y.kind === "ask" || y.kind === "held-message") - Number(x.kind === "ask" || x.kind === "held-message"),
    );
  const shell = {
    label,
    heading: { title: agentWho(actor.agent, actor.model), detail: label },
    className: "game-slot",
    style,
    triggerClassName: `game-seat actor ${seatKind} pose-${actor.pose} agent--${agentColorKey(actor.agent)}${
      actor.swappedFrom ? " swapped" : ""
    }${actor.ask ? " has-ask" : ""}`,
    trigger: (
      <>
        {standing ? (
          <StandingFigure clipboard={seatKind === "meeting"} />
        ) : (
          <DeskSvg>
            <SeatedFigure />
          </DeskSvg>
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
      </>
    ),
  };
  if (actor.kind === "run" && row) {
    const extra = urgentFirst(actor.actions.filter((target) => EXTRA_FOR_RUN.has(target.kind))).map(asItem);
    return <RunMenu row={row} titleOf={titleOf} extra={extra} {...shell} />;
  }
  return <StationMenu items={urgentFirst(actor.actions).map(asItem)} {...shell} />;
}, sameSprite);
