// The war room as a theme pack tells it: a story, not a floor plan. Down the
// side, the places to go to (the pack's own names for the work area, the
// meeting room, the entrance, the lounge, wherever those out on a usage limit
// went, and the runners); on the stage, whoever is in the place chosen, one
// picture per character and state, the one turned to in front; under them a
// dialogue box where that agent says what it is doing and the viewer answers
// with a choice. Everything the pixel room can do is here: an agent's menu is
// its choices, the table's items and the other projects are menus still, a
// runner is a button. A viewer who would rather see everyone at once lays the
// place out as a list instead: a card and a dialogue box each, one under
// another.

import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";

import { useI18n } from "../../i18n";
import type { PreferenceValue } from "../../preferences";
import { useSimpleMode } from "../../simple/simpleMode";
import type { ControlRow } from "../../station/control";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useApp, usePreference } from "../../store";
import { THEME_PLACES, type ThemePlaceId, type ThemeScene } from "../../theme-pack/themeManifest";
import { useGameActions } from "../GameActions";
import {
  agentName,
  agentWho,
  compactLabel,
  compactSummary,
  itemLabel,
  rackLabel,
  rackName,
  runnersActionLabel,
  type LabelOf,
  type TitleOf,
  type Translate,
} from "../labels";
import type { Actor, Building, Meeting, ProjectOffice, Rack } from "../world";
import { EnergyHud } from "./EnergyHud";
import { menuItem } from "./PixelDesk";
import { MAX_RACKS } from "./ServerRoom";
import {
  leadGroup,
  leadMember,
  openingPlace,
  personWho,
  storyPlaces,
  type StoryGroup,
  type StoryPlace,
} from "./storyPlaces";
import { personLabel, personWhat, StoryTalk } from "./StoryTalk";
import { useStoryWords } from "./storyWords";
import type { CastFigure } from "./themeCast";
import type { ThemeCast } from "./themeCastContext";
import { ThemeFigure } from "./ThemeFigure";

/** The app's own name for a place, where the pack gives none. */
const BUILT_IN: Record<ThemePlaceId, string> = {
  work: "game.area.work",
  meeting: "game.area.meeting",
  entrance: "game.area.entrance",
  lounge: "game.area.lounge",
  away: "game.story.place.away",
  runners: "game.area.serverRoom",
};

/** Figures drawn to either side of the one in front; the last of them is
 * already out of sight, so one that comes in has somewhere to come from. */
const SIDES = 3;
/** A face in the roster, in CSS px: warroom.css has the same. */
const FACE_PX = 38;
/** How much of the canvas's width a face takes, and how far under her sign
 * it starts, as a share of the canvas's height. */
const FACE_REACH = 0.29;
const FACE_UNDER_SIGN = 0.025;
/** Slots of a runner drawn as bars. */
const MAX_SLOTS = 8;
/** The ways a place can be laid out, as its switch offers them. */
const LAYOUTS: readonly PreferenceValue<"storyLayout">[] = ["stage", "list"];

/** What every one of them has, or nothing when they differ. */
function shared(values: string[]): string {
  return values.every((value) => value === values[0]) ? values[0] : "";
}

function namesOf(group: StoryGroup): string[] {
  return group.members.map((person) => agentName(personWho(person).agent));
}

/** A group in a sentence: the one in it, or who they all are and how many. */
function groupLabel(t: Translate, group: StoryGroup): string {
  const count = group.members.length;
  if (count === 1) return personLabel(t, group.members[0]);
  const whos = group.members.map((person) => {
    const who = personWho(person);
    return agentWho(who.agent, who.model);
  });
  const who = shared(whos) || shared(namesOf(group));
  return who ? t("game.group.label", { who, count }) : t("game.group.mixed", { count });
}

/** A scene's still as a background: the first frame of its strip, as wide as
 * what it fills. */
function still(cast: ThemeCast, scene: ThemeScene, down: string): CSSProperties {
  return {
    backgroundImage: `url(${JSON.stringify(cast.url(scene.image))})`,
    backgroundSize: `${(scene.frames?.count ?? 1) * 100}% auto`,
    backgroundPosition: `0 ${down}`,
  };
}

/** A character's face, cut from her picture: under her sign where she has
 * one, else the top of what is drawn of her. */
function Face({ figure, cast }: { figure?: CastFigure; cast: ThemeCast }) {
  if (!figure) {
    return (
      <span className="px-story-face is-unnamed" aria-hidden="true">
        ?
      </span>
    );
  }
  const { scene } = figure;
  const [width, height] = cast.canvas;
  const reach = FACE_REACH * width;
  const scale = FACE_PX / reach;
  const centre = scene.sign ? scene.sign[0] : (scene.box[0] + scene.box[2]) / 2;
  const top = scene.sign ? scene.sign[1] + FACE_UNDER_SIGN * height : scene.box[1];
  return (
    <span
      className="px-story-face"
      aria-hidden="true"
      style={{
        backgroundImage: `url(${JSON.stringify(cast.url(scene.image))})`,
        backgroundSize: `${(scene.frames?.count ?? 1) * scene.w * scale}px ${scene.h * scale}px`,
        backgroundPosition: `${-(centre - reach / 2 - scene.x) * scale}px ${-(top - scene.y) * scale}px`,
      }}
    />
  );
}

/** What a card shows: her picture, or the mark of the unnamed; and how many
 * wear it. The stage's figures and the list's cards alike. */
function CardArt({ group }: { group: StoryGroup }) {
  const { say } = useStoryWords();
  return (
    <>
      <span className="px-story-art">
        {group.figure ? (
          <ThemeFigure figure={group.figure} />
        ) : (
          <span className="px-story-unnamed">
            ?<small>{say("unnamed")}</small>
          </span>
        )}
      </span>
      {group.members.length > 1 && <span className="px-story-count">×{group.members.length}</span>}
    </>
  );
}

function RackCard({ rack }: { rack: Rack }) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  // Simple: the built-in runner is this computer, opening its settings.
  const simple = useSimpleMode();
  const label = rackLabel(t, rack, simple);
  const slots = Math.min(rack.slots ?? 0, MAX_SLOTS);
  return (
    <button
      type="button"
      className={`px-story-rack${rack.online === false ? " is-off" : ""}`}
      aria-label={label}
      title={label}
      onClick={() => dispatch(rack.target)}
    >
      <span className="px-story-rack-name">{rackName(t, rack, simple)}</span>
      <span className="px-story-slots" aria-hidden="true">
        {Array.from({ length: slots }, (_, i) => (
          <span key={i} className={i < rack.busy ? "is-busy" : undefined} />
        ))}
      </span>
      <span className="px-story-rack-load">{rack.slots ? `${rack.busy}/${rack.slots}` : `▶ ${rack.busy}`}</span>
    </button>
  );
}

/** What the pack's skin gives the stage: its accent, and under a dark skin
 * its night, ink and panels. A light skin keeps the stage's own night: the
 * pictures are lit for one. */
function skinVars(cast: ThemeCast): CSSProperties {
  const skin = cast.skin;
  if (!skin) return {};
  const vars: Record<string, string> = {};
  if (skin.colors.accent) vars["--story-accent"] = skin.colors.accent;
  if (skin.scheme === "dark") {
    if (skin.colors.bg) vars["--story-night"] = skin.colors.bg;
    if (skin.colors.text) vars["--story-ink"] = skin.colors.text;
    if (skin.colors["text-2"]) vars["--story-soft"] = skin.colors["text-2"];
    if (skin.colors.surface) vars["--story-deep"] = skin.colors.surface;
  }
  return vars as CSSProperties;
}

export function StoryRoom({
  building,
  office,
  actorsById,
  titleOf,
  rowOf,
  labelOf,
  cast,
}: {
  building: Building;
  /** The project's office; null when only global agents are seated. */
  office: ProjectOffice | null;
  actorsById: ReadonlyMap<string, Actor>;
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
  labelOf: LabelOf;
  cast: ThemeCast;
}) {
  const { t, language } = useI18n();
  const { dispatch } = useGameActions();
  const { say } = useStoryWords();
  // Simple: the runners' place is this computer, in plain words.
  const simple = useSimpleMode();
  const places = useMemo(
    () => storyPlaces(building, actorsById, cast.figureOf),
    [building, actorsById, cast.figureOf],
  );
  const [place, setPlace] = useState<ThemePlaceId>(() => openingPlace(places));
  // Whom the stage was last turned to in each place, and who spoke last in
  // each group: coming back finds them as they were left.
  const [chosen, setChosen] = useState<Partial<Record<ThemePlaceId, string>>>({});
  const [picked, setPicked] = useState<Record<string, string>>({});
  // The project's office and the global agents', as storyPlaces seats them.
  const offices = useMemo(
    () => [office, building.global].filter((each) => each !== null),
    [office, building.global],
  );
  const meetingOf = useMemo(() => {
    const byActor = new Map<string, Meeting>();
    for (const each of offices) for (const meeting of each.meetings) for (const id of meeting.actorIds) byActor.set(id, meeting);
    return byActor;
  }, [offices]);
  const vars = useMemo(() => skinVars(cast), [cast]);
  const layout = usePreference("storyLayout");
  // The list's rows by group, to bring the one turned to into view.
  const rows = useRef(new Map<string, HTMLLIElement>());

  const here: StoryPlace = places[place];
  const lead = leadGroup(here, chosen[place]);
  const at = lead ? here.groups.indexOf(lead) : 0;
  const speaker = lead && leadMember(lead, picked[`${place}/${lead.key}`]);
  // Laid out as a list only where there is anyone to list: the runners and an
  // empty place look the same either way, the narrator under them.
  const listed = layout === "list" && place !== "runners" && here.groups.length > 0;
  const turnTo = (key: string) => {
    setChosen((was) => ({ ...was, [place]: key }));
    // From the roster or a key she may be far down the list. "nearest" moves
    // nothing for a row already in view.
    rows.current.get(key)?.scrollIntoView?.({ block: "nearest" });
  };
  const pick = (group: StoryGroup) => (id: string) =>
    setPicked((was) => ({ ...was, [`${place}/${group.key}`]: id }));

  const builtIn = (id: ThemePlaceId) => t(BUILT_IN[id]);
  const nameOf = (id: ThemePlaceId) => cast.placeName(id, language) || builtIn(id);
  const countOf = (spot: StoryPlace) => {
    if (spot.id === "runners") return simple ? t("simple.rack.count") : say("runnerCount", { count: spot.count });
    return spot.count ? say("count", { count: spot.count }) : say("nobody");
  };
  const name = nameOf(place);
  const under = [name === builtIn(place) ? "" : builtIn(place), countOf(here)].filter(Boolean).join(" · ");

  const racks = place === "runners" ? building.racks : [];
  const notices = place === "meeting" ? offices.flatMap((each) => each.items) : [];
  let narration = say("empty", { place: name });
  let narratorChoices: StationMenuItem[] | undefined;
  if (place === "runners") {
    const busy = racks.reduce((sum, rack) => sum + rack.busy, 0);
    if (simple) narration = racks.length ? t("simple.rack.line", { busy }) : t("simple.rack.none");
    else narration = racks.length ? say("runnersLine", { count: racks.length, busy }) : t("game.serverRoom.none");
    narratorChoices = [
      { id: "runners", label: runnersActionLabel(t, simple), onSelect: () => dispatch({ kind: "runners" }) },
    ];
  } else if (notices.length) {
    narration = say("noticesLine", { count: notices.length });
  }

  // The arrows turn the stage to whoever stands next, a digit goes to a
  // place. Not from a field or an open menu, which have their own keys.
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const from = event.target as HTMLElement;
    if (from.closest('input, textarea, select, [contenteditable="true"], [role="menu"]')) return;
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      const next = here.groups[at + (event.key === "ArrowLeft" ? -1 : 1)];
      if (!next) return;
      event.preventDefault();
      turnTo(next.key);
    } else if (/^[1-6]$/.test(event.key)) {
      event.preventDefault();
      setPlace(THEME_PLACES[Number(event.key) - 1]);
    }
  };

  const [width, height] = cast.canvas;
  const scene = lead?.figure?.scene;
  const room = scene ? cast.roomUrl(scene) : "";
  const backdrop: CSSProperties | undefined = !scene
    ? undefined
    : room
      ? { backgroundImage: `url(${JSON.stringify(room)})`, backgroundSize: "cover", backgroundPosition: "50% 40%" }
      : still(cast, scene, "40%");

  const hidden = Math.max(0, building.total - (1 + building.others.length + building.doorSigns.length));
  const elsewhere = building.others.length > 0 || building.doorSigns.length > 0 || building.truncated || hidden > 0;

  const noticeList = notices.length > 0 && (
    <ul className="px-story-notices" aria-label={say("notices")}>
      {notices.map((item) => {
        const label = itemLabel(t, item);
        return (
          <li key={item.id}>
            <StationMenu
              items={item.actions.map(menuItem(t, dispatch, titleOf))}
              label={label}
              heading={{ title: item.title, detail: label }}
              className="px-story-notice-menu"
              triggerClassName={`px-story-notice is-${item.kind}`}
              trigger={
                <>
                  <span className="px-story-notice-kind">{t(`game.itemKind.${item.kind}`)}</span>
                  <span className="px-story-notice-title">{item.title}</span>
                </>
              }
            />
          </li>
        );
      })}
    </ul>
  );

  return (
    <div className="px-story" style={vars} onKeyDown={onKeyDown}>
      <div className="px-story-world">
        <nav className="px-story-rail" aria-label={t("game.story.places")}>
          <p className="px-story-project">{office ? office.label : t("globalAgents.office")}</p>
          <ul className="px-story-places">
            {THEME_PLACES.map((id, i) => {
              const spot = places[id];
              const thumb = spot.groups.find((group) => group.figure)?.figure?.scene;
              return (
                <li key={id}>
                  <button
                    type="button"
                    className={`px-story-place${id === place ? " is-on" : ""}`}
                    aria-pressed={id === place}
                    aria-keyshortcuts={String(i + 1)}
                    onClick={() => setPlace(id)}
                  >
                    {thumb && <span className="px-story-thumb" aria-hidden="true" style={still(cast, thumb, "28%")} />}
                    <span className="px-story-shade" aria-hidden="true" />
                    <span className="px-story-place-name">{nameOf(id)}</span>
                    <span className="px-story-place-sub">{countOf(spot)}</span>
                    {spot.asks > 0 ? (
                      <span className="px-story-mark is-ask">
                        <span aria-hidden="true">✋ {spot.asks}</span>
                        <span className="visually-hidden">{t("game.office.waiting", { count: spot.asks })}</span>
                      </span>
                    ) : spot.fails > 0 ? (
                      <span className="px-story-mark is-fail">
                        <span aria-hidden="true">!{spot.fails}</span>
                        <span className="visually-hidden">{say("failed", { count: spot.fails })}</span>
                      </span>
                    ) : null}
                  </button>
                </li>
              );
            })}
          </ul>
          {elsewhere && (
            <div className="px-story-elsewhere" role="group" aria-label={say("others")}>
              <p className="px-story-rail-title" aria-hidden="true">
                {say("others")}
              </p>
              {building.others.map((other) => {
                const label = compactLabel(t, other);
                return (
                  <StationMenu
                    key={other.project}
                    items={other.actions.map(menuItem(t, dispatch, () => "", labelOf))}
                    label={label}
                    heading={{ title: other.label, detail: compactSummary(t, other) }}
                    className="px-story-door-menu"
                    triggerClassName="px-story-door is-busy"
                    trigger={
                      <>
                        <span className="px-story-door-name">{other.label}</span>
                        <span className="px-story-door-counts" aria-hidden="true">
                          {other.counts.running > 0 && <span>▶ {other.counts.running}</span>}
                          {other.counts.waiting > 0 && <span className="is-ask">✋ {other.counts.waiting}</span>}
                          {other.counts.sleeping > 0 && <span>zZ {other.counts.sleeping}</span>}
                        </span>
                      </>
                    }
                  />
                );
              })}
              {building.doorSigns.map((other) => {
                const label = compactLabel(t, other);
                return (
                  <button
                    key={other.project}
                    type="button"
                    className={`px-story-door${other.busy ? " is-busy" : ""}`}
                    aria-label={label}
                    title={label}
                    onClick={() => dispatch(other.target)}
                  >
                    <span className="px-story-door-name">{other.label}</span>
                  </button>
                );
              })}
              {(building.truncated || hidden > 0) && (
                <p className="px-story-rail-note">{t("game.building.more", { count: hidden })}</p>
              )}
            </div>
          )}
          {building.energy && <EnergyHud energy={building.energy} />}
        </nav>

        <section
          className="px-story-stage"
          aria-label={name}
          data-place={place}
          data-layout={listed ? "list" : "stage"}
        >
          {backdrop && (
            <span key={backdrop.backgroundImage} className="px-story-backdrop" aria-hidden="true" style={backdrop} />
          )}
          <span className="px-story-vignette" aria-hidden="true" />
          <div className="px-story-head">
            <h3 className="px-story-banner">
              {name}
              <small>{under}</small>
              {place === "lounge" && building.loungeMore > 0 && (
                <small>{t("game.lounge.more", { count: building.loungeMore })}</small>
              )}
            </h3>
            <div className="px-story-layout" role="group" aria-label={t("game.story.layout")}>
              {LAYOUTS.map((id) => (
                <button
                  key={id}
                  type="button"
                  aria-pressed={layout === id}
                  onClick={() => useApp.getState().updateUIPreference("storyLayout", id)}
                >
                  {t(`game.story.layout.${id}`)}
                </button>
              ))}
            </div>
          </div>

          {here.groups.length > 0 && (
            <div className="px-story-roster" role="group" aria-label={say("people")}>
              {here.groups.map((group) => {
                const count = group.members.length;
                const one = shared(namesOf(group));
                const front = group === lead && speaker ? speaker : leadMember(group, picked[`${place}/${group.key}`]);
                return (
                  <button
                    key={group.key}
                    type="button"
                    className={`px-story-who${group === lead ? " is-on" : ""}${group.alert ? ` is-${group.alert}` : ""}`}
                    aria-label={groupLabel(t, group)}
                    aria-pressed={group === lead}
                    onClick={() => turnTo(group.key)}
                  >
                    <Face figure={group.figure} cast={cast} />
                    <span className="px-story-who-text" aria-hidden="true">
                      <span className="px-story-who-name">
                        {one || t("game.group.mixed", { count })}
                        {one && count > 1 ? ` ×${count}` : ""}
                      </span>
                      <span className="px-story-who-what">{personWhat(t, front)}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          )}

          {listed ? (
            // A row each: her card, and beside it her own dialogue box with
            // all the bottom box would have for her. The list scrolls, and a
            // card scrolled out of it is out of sight, so its clip holds still.
            <div className="px-story-list">
              {noticeList}
              <ul className="px-story-rows">
                {here.groups.map((group) => {
                  const on = group === lead;
                  return (
                    <li
                      key={group.key}
                      ref={(row) => {
                        if (row) rows.current.set(group.key, row);
                        else rows.current.delete(group.key);
                      }}
                      className={`px-story-row${on ? " is-on" : ""}`}
                      onClick={() => turnTo(group.key)}
                    >
                      {/* As on the stage, the card is for the eye and the
                          pointer; the roster has her for the keyboard. */}
                      <button
                        type="button"
                        tabIndex={-1}
                        aria-hidden="true"
                        className="px-story-card"
                        style={{ aspectRatio: `${width} / ${height}` }}
                      >
                        <CardArt group={group} />
                      </button>
                      <StoryTalk
                        group={group}
                        person={on && speaker ? speaker : leadMember(group, picked[`${place}/${group.key}`])}
                        groupLabel={groupLabel(t, group)}
                        onPick={pick(group)}
                        narration={narration}
                        meetingOf={meetingOf}
                        titleOf={titleOf}
                        rowOf={rowOf}
                        labelOf={labelOf}
                        still={cast.still}
                      />
                    </li>
                  );
                })}
              </ul>
            </div>
          ) : (
            <>
              {noticeList}

              <div className="px-story-people">
                {place === "runners" ? (
                  racks.length > 0 && (
                    <div className="px-story-tower">
                      {racks.slice(0, MAX_RACKS).map((rack) => (
                        <RackCard key={rack.runnerId} rack={rack} />
                      ))}
                      {racks.length > MAX_RACKS && (
                        <button
                          type="button"
                          className="px-story-rack is-more"
                          aria-label={`${t(simple ? "simple.rack.more" : "game.rack.more", { count: racks.length - MAX_RACKS })}. ${runnersActionLabel(t, simple)}`}
                          onClick={() => dispatch({ kind: "runners" })}
                        >
                          +{racks.length - MAX_RACKS}
                        </button>
                      )}
                    </div>
                  )
                ) : (
                  // The pictures are for the eye and the pointer; the roster's
                  // buttons are the same people for the keyboard and a reader.
                  here.groups.map((group, i) => {
                    const offset = i - at;
                    const far = Math.abs(offset);
                    if (far > SIDES) return null;
                    return (
                      <button
                        key={group.key}
                        type="button"
                        tabIndex={-1}
                        aria-hidden="true"
                        className={`px-story-figure${far ? " is-side" : ""}${far === SIDES ? " is-far" : ""}`}
                        style={{
                          aspectRatio: `${width} / ${height}`,
                          transform: `translateX(${offset * 62 - 50}%) scale(${far ? 0.8 - (far - 1) * 0.12 : 1})`,
                          zIndex: 10 - far,
                        }}
                        onClick={far ? () => turnTo(group.key) : undefined}
                      >
                        <CardArt group={group} />
                      </button>
                    );
                  })
                )}
              </div>

              <StoryTalk
                group={lead}
                person={speaker}
                groupLabel={lead ? groupLabel(t, lead) : ""}
                onPick={(id) => lead && pick(lead)(id)}
                narration={narration}
                narratorChoices={narratorChoices}
                meetingOf={meetingOf}
                titleOf={titleOf}
                rowOf={rowOf}
                labelOf={labelOf}
                still={cast.still}
              />
            </>
          )}
        </section>
      </div>
    </div>
  );
}
