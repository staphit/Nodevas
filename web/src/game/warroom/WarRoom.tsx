// The war room, in pixel art: one room per runner (plus the terminal room
// for agents people run on their own machines), a desk per slot the runner
// has. Each agent is a real button; its menu holds what can be done about it
// (a run's own controls, through station/runMenuItems). Station mounts it
// lazily (StationWarRoom), so its code and styles only reach a browser that
// opens Station. A text list of the same rooms is there for screen readers.
//
// The drawing is inline SVG at whole art pixels (pixel/PixelSprite), coloured
// by CSS tokens, so a theme switch costs nothing; animation is CSS steps()
// over a few frames, off under reduced motion and in a background tab. A
// heartbeat only redraws a desk whose agent changed (PixelDesk is memoised).

import { Fragment, memo, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import "./warroom.css";

import { useI18n } from "../../i18n";
import { useWorkstationVisible } from "../../runners/workstation";
import type { ControlRow } from "../../station/control";
import { setProjectMode } from "../../station/projectMode";
import { useApp } from "../../store";
import { actorLabel, agentWho, energyLabel, roomName, type TitleOf, type Translate } from "../labels";
import { WINDOW, TERMINAL_SIGN } from "../pixel/sprites";
import { PixelSprite } from "../pixel/PixelSprite";
import { usePageHidden, useReducedMotion } from "../useMotion";
import type { Actor, Meeting, Office, Room } from "../world";
import { AgentDesk, FreeDesk } from "./PixelDesk";
import { layoutRoom } from "./roomLayout";

export interface WarRoomProps {
  office: Office;
  titleOf: TitleOf;
  /** The run behind a character, with its autopilot: what its menu acts on. */
  rowOf: (runId: string) => ControlRow | undefined;
}

/** Art pixels per CSS pixel aimed for; the real size rounds to whole device
 * pixels. */
const ART_PIXEL = 3;

/** One art pixel in CSS pixels: the nearest whole number of device pixels
 * to ART_PIXEL CSS pixels, so at a ratio of 1.25 or 1.5 (or a browser zoom)
 * every art pixel is the same width instead of alternating 4 and 5. */
export function artPixel(ratio: number): number {
  const dpr = Number.isFinite(ratio) && ratio > 0 ? ratio : 1;
  return Math.max(1, Math.round(ART_PIXEL * dpr)) / dpr;
}

/** artPixel for this screen, followed across a zoom or a move to another
 * monitor. */
function useArtPixel(): number {
  const ratio = () => (typeof window === "undefined" ? 1 : window.devicePixelRatio || 1);
  const [px, setPx] = useState(() => artPixel(ratio()));
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    let list: MediaQueryList | null = null;
    const watch = () => {
      list?.removeEventListener?.("change", watch);
      setPx(artPixel(ratio()));
      list = window.matchMedia(`(resolution: ${ratio()}dppx)`);
      list.addEventListener?.("change", watch);
    };
    watch();
    return () => list?.removeEventListener?.("change", watch);
  }, []);
  return px;
}

/** The room as Station shows it: still under reduced motion, paused in a
 * background tab, and a way back when the workstation is hidden. */
export default function WarRoomStage(props: WarRoomProps) {
  const reduced = useReducedMotion();
  const hidden = usePageHidden();
  const workstation = useWorkstationVisible();
  const announcement = useOfficeAnnouncements(props.office, workstation);
  const px = useArtPixel();
  return (
    <div
      className="px-office"
      data-motion={reduced ? "reduced" : "full"}
      data-paused={hidden ? "true" : undefined}
      style={{ ["--px" as string]: `${px}px` } as CSSProperties}
    >
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
    <div className="px-empty" role="status">
      <p>{t("game.warroom.off")}</p>
      <p className="px-empty-hint">{t("game.warroom.offHint")}</p>
      <div className="px-empty-actions">
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

function onlineText(t: Translate, room: Room): string {
  if (room.kind !== "runner" || room.online === null) return "";
  return room.online ? t("game.room.online") : t("game.room.offline");
}

/** The same rooms as plain text, for a screen reader: who sits where doing
 * what, and the free desks. The agents' buttons are the controls. */
function WarRoomText({ office }: { office: Office }) {
  const { t } = useI18n();
  const actorsById = useMemo(() => new Map(office.actors.map((actor) => [actor.id, actor])), [office.actors]);
  const rooms = shownRooms(office);
  if (rooms.length === 0) return null;
  return (
    <div className="visually-hidden" data-testid="war-room-text">
      {rooms.map((room) => {
        const name = roomName(t, room);
        const layout = layoutRoom(room, actorsById);
        const actors = room.actorIds.map((id) => actorsById.get(id)).filter((actor): actor is Actor => Boolean(actor));
        const free = layout.desks.filter((id) => id === null).length;
        const status = onlineText(t, room);
        return (
          <Fragment key={room.id}>
            <h3>
              {t("game.room.label", { name })}
              {status && ` (${status})`}
            </h3>
            <ul>
              {actors.map((actor) => (
                <li key={actor.id}>{actorLabel(t, actor, name)}</li>
              ))}
              {free > 0 && <li>{t("game.warroom.freeDesks", { count: free })}</li>}
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
  const meetingOf = useMemo(() => {
    const byActor = new Map<string, Meeting>();
    for (const meeting of office.meetings) for (const id of meeting.actorIds) byActor.set(id, meeting);
    return byActor;
  }, [office.meetings]);
  const rooms = shownRooms(office);

  if (rooms.length === 0) {
    return (
      <div className="px-empty">
        <p>{t("game.warroom.noRooms")}</p>
        <p className="px-empty-hint">{t("game.warroom.noRoomsHint")}</p>
      </div>
    );
  }

  return (
    <div className="px-war-room">
      {office.energy && (
        <div className="px-hud">
          <span>{t("game.energy.today")}</span>
          {office.energy.cap ? (
            <span
              className="px-meter is-wide"
              role="meter"
              aria-label={t("game.energy.today")}
              aria-valuemin={0}
              aria-valuemax={office.energy.cap}
              aria-valuenow={Math.min(office.energy.used, office.energy.cap)}
              aria-valuetext={energyLabel(t, office.energy.used, office.energy.cap)}
            >
              <span
                className={office.energy.used >= office.energy.cap ? "is-full" : undefined}
                style={{ width: `${Math.min(100, (office.energy.used / office.energy.cap) * 100)}%` }}
              />
            </span>
          ) : null}
          <span className="px-hud-value">{energyLabel(t, office.energy.used, office.energy.cap)}</span>
        </div>
      )}
      <div className="px-rooms">
        {rooms.map((room) => (
          <PixelRoom
            key={room.id}
            room={room}
            actorsById={actorsById}
            meetingOf={meetingOf}
            titleOf={titleOf}
            rowOf={rowOf}
          />
        ))}
      </div>
    </div>
  );
}

export function meetingLabel(t: Translate, meeting: Meeting): string {
  const parts: string[] = [];
  if (meeting.round) parts.push(t("game.meeting.round", { round: meeting.round }));
  if (meeting.waitingHuman) parts.push(t("game.meeting.waitingHuman"));
  for (const vote of meeting.votes) {
    parts.push(t("game.meeting.vote", { who: agentWho(vote.agent, vote.model), verdict: t(`game.verdict.${vote.verdict || "pending"}`) }));
  }
  return parts.join(" ");
}

/** The wall's furniture: windows, and in the terminal room a prompt. Drawn
 * more than fit; the wall clips them at a whole window. */
const Wall = memo(function Wall({ terminal }: { terminal: boolean }) {
  const pane = useMemo(() => [{ name: "window", sprite: WINDOW, x: 0, y: 0 }], []);
  const sign = useMemo(() => [{ name: "sign", sprite: TERMINAL_SIGN, x: 0, y: 0 }], []);
  return (
    <div className="px-wall" aria-hidden="true">
      {terminal && <PixelSprite parts={sign} width={TERMINAL_SIGN.w} height={TERMINAL_SIGN.h} />}
      {Array.from({ length: 8 }, (_, i) => (
        <PixelSprite key={i} parts={pane} width={WINDOW.w} height={WINDOW.h} />
      ))}
    </div>
  );
});

function PixelRoom({
  room,
  actorsById,
  meetingOf,
  titleOf,
  rowOf,
}: {
  room: Room;
  actorsById: ReadonlyMap<string, Actor>;
  meetingOf: ReadonlyMap<string, Meeting>;
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
}) {
  const { t } = useI18n();
  const layout = useMemo(() => layoutRoom(room, actorsById), [room, actorsById]);
  const name = roomName(t, room);
  const status = onlineText(t, room);
  const lightsOff = room.kind === "runner" && room.online === false;
  const cells = layout.desks.length + layout.queue.length;

  const desk = (id: string, atDoor: boolean) => {
    const actor = actorsById.get(id);
    if (!actor) return null;
    const meeting = meetingOf.get(id);
    return (
      <AgentDesk
        key={id}
        actor={actor}
        atDoor={atDoor}
        label={actorLabel(t, actor, name)}
        detail={meeting ? meetingLabel(t, meeting) || undefined : undefined}
        row={actor.runId ? rowOf(actor.runId) : undefined}
        titleOf={titleOf}
      />
    );
  };

  return (
    <section
      className={`px-room${lightsOff ? " is-lights-off" : ""}${room.kind === "terminal" ? " is-terminal" : ""}`}
      aria-label={t("game.room.label", { name })}
      style={{ ["--px-cols" as string]: Math.min(Math.max(cells, 3), 5) }}
    >
      <header className="px-room-head">
        <span className="px-room-name">{name}</span>
        {status && <span className={`px-chip ${room.online ? "is-online" : "is-offline"}`}>{status}</span>}
        {room.ownerName && <span className="px-room-meta">{room.ownerName}</span>}
        {room.hostname && <span className="px-room-meta mono">{room.hostname}</span>}
      </header>
      <div className="px-scene">
        <Wall terminal={room.kind === "terminal"} />
        <div className="px-floor">
          {layout.desks.map((id, i) => (id ? desk(id, false) : <FreeDesk key={`free-${i}`} />))}
          {layout.queue.map((id) => desk(id, true))}
        </div>
      </div>
    </section>
  );
}
