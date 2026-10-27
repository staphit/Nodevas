// The current project's office, in full: the door sign (its name and style,
// a button that changes the style), then the room: the style's wall, the
// work area's desks, the meeting room's table (agents in a meeting, and what
// lies there for the viewer: PRs to merge, reviews, held messages) and the
// entrance, where offered runs wait to be let in.

import { memo, useMemo, useRef, useState, type CSSProperties } from "react";

import { useI18n } from "../../i18n";
import type { ControlRow } from "../../station/control";
import { StationMenu } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import { actorLabel, agentWho, areaName, itemLabel, placeOf, styleName, type TitleOf, type Translate } from "../labels";
import type { Sprite } from "../pixel/compile";
import { PixelSprite } from "../pixel/PixelSprite";
import { CELL, DOOR } from "../pixel/sprites";
import { floorDecorOf, HANGING, wallDecorOf } from "../pixel/styleSprites";
import type { Actor, Meeting, MeetingItem, OfficeStyle, ProjectOffice } from "../world";
import { itemScene, type MeetingOptions } from "./deskArt";
import { layoutOffice } from "./officeLayout";
import { OfficeStylePopover } from "./OfficeStylePopover";
import { AgentDesk, EmptySeat, FreeDesk, menuItem } from "./PixelDesk";

/** A glyph per style for the door sign: the style in one character. */
export const STYLE_GLYPH: Record<OfficeStyle, string> = {
  default: "▣",
  engineering: "⚙",
  study: "❦",
  studio: "✎",
  cafe: "☕",
};

export function meetingLabel(t: Translate, meeting: Meeting): string {
  const parts: string[] = [];
  if (meeting.round) parts.push(t("game.meeting.round", { round: meeting.round }));
  if (meeting.waitingHuman) parts.push(t("game.meeting.waitingHuman"));
  for (const vote of meeting.votes) {
    parts.push(
      t("game.meeting.vote", {
        who: agentWho(vote.agent, vote.model),
        verdict: t(`game.verdict.${vote.verdict || "pending"}`),
      }),
    );
  }
  return parts.join(" ");
}

/** A single sprite at the origin, as PixelSprite parts. */
export function lone(name: string, sprite: Sprite) {
  return [{ name, sprite, x: 0, y: 0 }];
}

/** The back wall: the style's decor in turn, more than fit; the wall clips
 * at a whole piece. */
export const StyleWall = memo(function StyleWall({ style, className }: { style: OfficeStyle; className?: string }) {
  const pieces = useMemo(() => {
    const decor = wallDecorOf(style);
    return Array.from({ length: 10 }, (_, i) => decor[i % decor.length]);
  }, [style]);
  return (
    <div className={`px-wall${className ? ` ${className}` : ""}`} aria-hidden="true">
      {pieces.map((sprite, i) => (
        <span key={i} className={`px-wall-piece${HANGING.has(sprite) ? " is-hanging" : ""}`}>
          <PixelSprite parts={lone("decor", sprite)} width={sprite.w} height={sprite.h} />
        </span>
      ))}
    </div>
  );
});

/** What lies on the meeting-room table for the viewer: a button, its menu
 * what can be done about it. */
const MeetingItemCell = memo(function MeetingItemCell({
  item,
  style,
  edge,
  titleOf,
}: {
  item: MeetingItem;
  style: OfficeStyle;
  edge: MeetingOptions["edge"];
  titleOf: TitleOf;
}) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const parts = useMemo(() => itemScene(item.kind, style, edge), [item.kind, style, edge]);
  const label = itemLabel(t, item);
  return (
    <StationMenu
      items={item.actions.map(menuItem(t, dispatch, titleOf))}
      label={label}
      heading={{ title: item.title, detail: label }}
      className="px-cell is-seat is-item"
      triggerClassName={`px-desk is-item item--${item.kind}`}
      trigger={
        <>
          <span className="px-art">
            <PixelSprite parts={parts} width={CELL} height={CELL} />
          </span>
          <span className="px-tag" aria-hidden="true">
            <span className="px-tag-who">{t(`game.itemKind.${item.kind}`)}</span>
            <span className="px-tag-what">{item.title}</span>
          </span>
        </>
      }
    />
  );
});

function edgeOf(index: number, count: number): MeetingOptions["edge"] {
  if (count === 1) return "s";
  if (index === 0) return "l";
  if (index === count - 1) return "r";
  return "m";
}

export function OfficeRoom({
  office,
  actorsById,
  titleOf,
  rowOf,
}: {
  office: ProjectOffice;
  actorsById: ReadonlyMap<string, Actor>;
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
}) {
  const { t } = useI18n();
  const layout = useMemo(() => layoutOffice(office, actorsById), [office, actorsById]);
  const meetingOf = useMemo(() => {
    const byActor = new Map<string, Meeting>();
    for (const meeting of office.meetings) for (const id of meeting.actorIds) byActor.set(id, meeting);
    return byActor;
  }, [office.meetings]);
  const itemsById = useMemo(() => new Map(office.items.map((item) => [item.id, item])), [office.items]);
  const { style } = office;
  const floorDecor = floorDecorOf(style);

  const desk = (id: string, scene: "desk" | "door" | "meeting", edge?: MeetingOptions["edge"]) => {
    const actor = actorsById.get(id);
    if (!actor) return null;
    const meeting = meetingOf.get(id);
    return (
      <AgentDesk
        key={id}
        actor={actor}
        scene={scene}
        style={style}
        edge={edge}
        label={actorLabel(t, actor, placeOf(t, actor))}
        detail={meeting ? meetingLabel(t, meeting) || undefined : undefined}
        row={actor.runId ? rowOf(actor.runId) : undefined}
        titleOf={titleOf}
      />
    );
  };

  const running = office.work.filter((id) => {
    const pose = actorsById.get(id)?.pose;
    return pose === "working" || pose === "reviewing";
  }).length;
  const waiting =
    office.items.length + [...office.work, ...office.meeting].filter((id) => actorsById.get(id)?.ask).length;
  const cells = layout.meetingCells.length;

  return (
    <section
      className={`px-office-room px-style--${style}`}
      aria-label={t("game.office.label", { name: office.label })}
      style={{ ["--px-cols" as string]: Math.min(Math.max(layout.desks.length, 3), 6) } as CSSProperties}
    >
      <header className="px-office-head">
        <DoorSign office={office} />
        <span className="px-office-chips">
          <Chip glyph="▶" label={t("game.office.running", { count: running })} count={running} />
          <Chip glyph="✋" label={t("game.office.waiting", { count: waiting })} count={waiting} attention />
          <Chip glyph="🚪" label={t("game.office.offered", { count: layout.entrance.length })} count={layout.entrance.length} />
        </span>
        <span className="px-office-free">{t("game.office.free", { count: office.freeDesks })}</span>
      </header>
      <div className="px-scene">
        <StyleWall style={style} />
        <div className="px-areas">
          <div className="px-area is-work" role="group" aria-label={areaName(t, "work")}>
            <span className="px-area-name" aria-hidden="true">
              {areaName(t, "work")}
            </span>
            <div className="px-floor px-desks">
              {layout.desks.map((id, i) => (id ? desk(id, "desk") : <FreeDesk key={`free-${i}`} style={style} />))}
            </div>
          </div>
          <div className="px-area is-meeting" role="group" aria-label={areaName(t, "meeting")}>
            <span className="px-area-name" aria-hidden="true">
              {areaName(t, "meeting")}
            </span>
            <div className="px-floor px-meeting">
              <div className="px-table">
                {layout.meetingCells.map((cell, i) => {
                  const edge = edgeOf(i, cells);
                  if ("actorId" in cell) return desk(cell.actorId, "meeting", edge);
                  if ("itemId" in cell) {
                    const item = itemsById.get(cell.itemId);
                    return item ? (
                      <MeetingItemCell key={item.id} item={item} style={style} edge={edge} titleOf={titleOf} />
                    ) : null;
                  }
                  return <EmptySeat key={`empty-${i}`} style={style} edge={edge} />;
                })}
              </div>
            </div>
          </div>
        </div>
        <div className="px-entrance px-floor" role="group" aria-label={areaName(t, "entrance")}>
          {layout.entrance.length === 0 ? (
            <span className="px-doorway" aria-hidden="true">
              <PixelSprite parts={lone("door", DOOR)} width={DOOR.w} height={DOOR.h} />
            </span>
          ) : (
            layout.entrance.map((id) => desk(id, "door"))
          )}
          <span className="px-floor-decor" aria-hidden="true">
            <PixelSprite parts={lone("decor", floorDecor)} width={floorDecor.w} height={floorDecor.h} />
          </span>
        </div>
      </div>
    </section>
  );
}

function Chip({ glyph, label, count, attention }: { glyph: string; label: string; count: number; attention?: boolean }) {
  return (
    <span className={`px-chip-count${attention && count > 0 ? " is-attention" : ""}`} title={label}>
      <span aria-hidden="true">
        {glyph} {count}
      </span>
      <span className="visually-hidden">{label}</span>
    </span>
  );
}

/** The office's door sign: its name and style. A button: it opens the style
 * picker (the hub refuses an agent's change; a person may). */
function DoorSign({ office }: { office: ProjectOffice }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement | null>(null);
  const style = styleName(t, office.style);
  return (
    <span className="px-sign-wrap">
      <button
        ref={button}
        type="button"
        className="px-sign"
        aria-label={t("game.office.styleButton", { name: office.label, style })}
        title={t("game.office.styleButton", { name: office.label, style })}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((was) => !was)}
      >
        <span className="px-sign-name">{office.label}</span>
        <span className="px-sign-style" aria-hidden="true">
          {STYLE_GLYPH[office.style]} {style}
        </span>
      </button>
      {open && (
        <OfficeStylePopover
          label={t("game.office.stylePicker", { name: office.label })}
          onClose={(returnFocus) => {
            setOpen(false);
            if (returnFocus) button.current?.focus();
          }}
          anchor={button}
        />
      )}
    </span>
  );
}
