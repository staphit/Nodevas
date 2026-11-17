// The story's dialogue box: who speaks (the agent the stage has turned to, or
// the narrator where nobody is; a character's sign goes before her name, where
// she has one), what it says in its own words, what is known
// about it, and what can be done about it as choices: the very items its menu
// has in the pixel room. The words are the pack's where it has its own, each
// character's in her voice. The line types itself out; under reduced motion
// and in a background tab it is simply there.

import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { askQuestionText } from "../../agents/AskCard";
import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import { useSimpleMode } from "../../simple/simpleMode";
import type { ControlRow } from "../../station/control";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import {
  actorLabel,
  agentName,
  agentWho,
  energyLabel,
  placeOf,
  poseLabel,
  type LabelOf,
  type TitleOf,
  type Translate,
} from "../labels";
import type { Actor, Meeting } from "../world";
import { visitorLabel } from "./Lounge";
import { meetingLabel } from "./OfficeRoom";
import { ActorItems, menuItem, roleBadgeKey } from "./PixelDesk";
import { personAlert, personWho, type StoryAlert, type StoryGroup, type StoryPerson } from "./storyPlaces";
import { useStoryWords, type StoryWords } from "./storyWords";
import { useThemeCast } from "./themeCastContext";

/** A letter's turn, in ms; a long line takes more letters a turn. */
const TICK_MS = 22;
const TURNS = 90;
/** Choices shown as buttons; with more, the last of them is a menu of the rest. */
const MAX_CHOICES = 4;

const NO_ITEMS: StationMenuItem[] = [];

/** One sentence that names a person, as a button's accessible name. */
export function personLabel(t: Translate, person: StoryPerson): string {
  return person.kind === "actor"
    ? actorLabel(t, person.actor, placeOf(t, person.actor))
    : visitorLabel(t, person.visitor);
}

/** What a person is on, in a few words. */
export function personWhat(t: Translate, person: StoryPerson): string {
  if (person.kind === "visitor") return person.visitor.label;
  const { actor } = person;
  return actor.nodeTitle || actor.sessionName || poseLabel(t, actor.pose);
}

/** What an agent says for itself: what it waits on first, then what went
 * wrong, then what it last said, then what it is doing. Never its tokens: a
 * line that changed with every heartbeat would type itself out again. */
function actorLine(
  t: Translate,
  words: StoryWords,
  character: string | undefined,
  actor: Actor,
  row: ControlRow | undefined,
): string {
  if (actor.ask) return askQuestionText(t, actor.ask, agentWho(actor.agent, actor.model));
  if (actor.heldMail.length) return words.say("heldLine", { count: actor.heldMail.length });
  const said = words.line(character, actor.pose, actor.nodeTitle);
  if (actor.pose === "failed") {
    const why = excerpt(row?.run.error || row?.run.summary || "", 200);
    return why ? `${said} ${why}` : said;
  }
  return actor.bubble ? excerpt(actor.bubble.body, 240) : said;
}

type Fact = [name: string, value: string];

/** In Simple: where it works in plain words (this computer), and no token
 * count. */
function actorFacts(t: Translate, { say }: StoryWords, actor: Actor, simple = false): Fact[] {
  const facts: Fact[] = [[say("status"), poseLabel(t, actor.pose)]];
  if (actor.role) {
    const role = roleBadgeKey(actor.role) === "custom" ? actor.role : t(`roles.name.${actor.role}`);
    facts.push([say("role"), role]);
  }
  if (actor.nodeTitle) facts.push([say("node"), actor.nodeTitle]);
  facts.push([simple ? t("simple.story.where") : say("runner"), placeOf(t, actor, simple)]);
  if (actor.energy && !simple) facts.push([say("tokens"), energyLabel(t, actor.energy.used, actor.energy.cap)]);
  if (actor.heldMail.length) facts.push([say("held"), String(actor.heldMail.length)]);
  if (actor.testState) facts.push([say("tests"), t(`warroom.test.${actor.testState}`)]);
  return facts;
}

function actorNotes(t: Translate, actor: Actor, meeting: Meeting | undefined): string[] {
  const notes: string[] = [];
  if (actor.swappedFrom) {
    notes.push(
      t("game.actor.swapped", {
        from: agentWho(actor.swappedFrom.agent, actor.swappedFrom.model),
        reason: t(`game.failure.${actor.swappedFrom.failureKind}`),
      }),
    );
  }
  const votes = meeting ? meetingLabel(t, meeting) : "";
  if (votes) notes.push(votes);
  return notes;
}

/** How much of a line is typed, and a way to have all of it at once. */
function useTyped(length: number, text: string, instant: boolean): [number, () => void] {
  const [shown, setShown] = useState({ text, count: 0 });
  // Another line starts from nothing, before any effect runs.
  const count = instant ? length : shown.text === text ? shown.count : 0;
  useEffect(() => {
    if (count >= length) return;
    const step = Math.max(1, Math.ceil(length / TURNS));
    const timer = window.setTimeout(() => setShown({ text, count: Math.min(length, count + step) }), TICK_MS);
    return () => window.clearTimeout(timer);
  }, [text, length, count]);
  const finish = useCallback(() => setShown({ text, count: length }), [text, length]);
  return [count, finish];
}

/** The line. What is still to come holds its place, so the box never grows
 * while it types; a click has all of it. A screen reader gets it whole. */
const StoryLine = memo(function StoryLine({ text, still }: { text: string; still: boolean }) {
  const letters = useMemo(() => Array.from(text), [text]);
  const [count, finish] = useTyped(letters.length, text, still);
  return (
    <p className="px-story-line" onClick={finish}>
      <span aria-hidden="true">
        {letters.slice(0, count).join("")}
        <span className="px-story-untyped">{letters.slice(count).join("")}</span>
      </span>
      <span className="visually-hidden">{text}</span>
    </p>
  );
});

/** What can be done, as the story's choices: a menu's items, each a button. */
export function StoryChoices({ items }: { items: StationMenuItem[] }) {
  const { say } = useStoryWords();
  if (!items.length) return null;
  const shown = items.length <= MAX_CHOICES ? items : items.slice(0, MAX_CHOICES - 1);
  const rest = items.slice(shown.length);
  return (
    <div className="px-story-choices">
      {shown.map((item, i) => (
        <button
          key={item.id}
          type="button"
          className={`px-story-choice${i === 0 ? " is-main" : ""}${item.danger ? " is-danger" : ""}`}
          disabled={item.disabled}
          title={item.title}
          aria-pressed={item.selected ?? item.checked}
          onClick={item.onSelect}
        >
          {item.label}
        </button>
      ))}
      {rest.length > 0 && (
        <StationMenu
          items={rest}
          label={say("more")}
          trigger={say("more")}
          className="px-story-more"
          triggerClassName="px-story-choice"
        />
      )}
    </div>
  );
}

/** Her sign before her name on the plate, as still as the name is. One that
 * will not load is left out, and nothing else about her changes. */
function PlateSign({ src }: { src: string }) {
  const [lost, setLost] = useState("");
  if (!src || src === lost) return null;
  return (
    <img
      className="px-story-plate-sign"
      src={src}
      alt=""
      aria-hidden="true"
      draggable={false}
      onError={() => setLost(src)}
    />
  );
}

function TalkBox({
  plate,
  sign = "",
  model,
  alert,
  tabs,
  line,
  facts = [],
  notes = [],
  still,
  children,
}: {
  plate: string;
  /** Her sign's URL, "" for none. */
  sign?: string;
  model?: string;
  alert?: StoryAlert;
  tabs?: ReactNode;
  line: string;
  facts?: Fact[];
  notes?: string[];
  still: boolean;
  children?: ReactNode;
}) {
  return (
    <div className={`px-story-talk${alert ? ` is-${alert}` : ""}`}>
      <p className="px-story-plate">
        <PlateSign src={sign} />
        {plate}
        {model && <small>{model}</small>}
      </p>
      <div className="px-story-said">
        {tabs}
        <StoryLine text={line} still={still} />
        {(facts.length > 0 || notes.length > 0) && (
          <ul className="px-story-meta">
            {facts.map(([name, value]) => (
              <li key={name}>
                {name} <b>{value}</b>
              </li>
            ))}
            {notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        )}
      </div>
      {children}
    </div>
  );
}

export interface StoryTalkProps {
  /** Whom the stage has turned to, and which of them speaks: none where
   * nobody is, and the narrator speaks. */
  group?: StoryGroup;
  person?: StoryPerson;
  /** The group's name, for its members' tabs. */
  groupLabel: string;
  onPick: (id: string) => void;
  /** What the narrator says, and offers. */
  narration: string;
  narratorChoices?: StationMenuItem[];
  meetingOf: ReadonlyMap<string, Meeting>;
  titleOf: TitleOf;
  rowOf: (runId: string) => ControlRow | undefined;
  labelOf: LabelOf;
  still: boolean;
}

export function StoryTalk({
  group,
  person,
  groupLabel,
  onPick,
  narration,
  narratorChoices = NO_ITEMS,
  meetingOf,
  titleOf,
  rowOf,
  labelOf,
  still,
}: StoryTalkProps) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const words = useStoryWords();
  const cast = useThemeCast();
  const simple = useSimpleMode();
  if (!group || !person) {
    return (
      <TalkBox plate={words.say("narrator")} line={narration} still={still}>
        <StoryChoices items={narratorChoices} />
      </TalkBox>
    );
  }

  const who = personWho(person);
  const character = group.figure?.character;
  // What the pack calls her comes first, and who she is after it.
  const title = words.title(character);
  const model = who.model && who.model !== "default" ? who.model : "";
  const plate = title ?? agentName(who.agent);
  const under = (title ? `${agentName(who.agent)} ${model}` : model).trim() || undefined;
  // Only one of the pack's characters has a sign: the unnamed have none.
  const sign = cast && group.figure?.sign ? cast.url(group.figure.sign) : "";
  const tabs = group.members.length > 1 && (
    <div className="px-story-members" role="group" aria-label={groupLabel}>
      {group.members.map((member, i) => {
        const alert = personAlert(member);
        return (
          <button
            key={member.id}
            type="button"
            className={`px-story-member${alert ? ` is-${alert}` : ""}`}
            aria-label={personLabel(t, member)}
            aria-pressed={member.id === person.id}
            onClick={() => onPick(member.id)}
          >
            <span aria-hidden="true">
              {i + 1}. {personWhat(t, member)}
            </span>
          </button>
        );
      })}
    </div>
  );

  if (person.kind === "visitor") {
    const { visitor } = person;
    return (
      <TalkBox
        plate={plate}
        sign={sign}
        model={under}
        tabs={tabs}
        line={words.line(character, visitor.pose, visitor.title)}
        facts={[
          [words.say("project"), visitor.label],
          [words.say("status"), poseLabel(t, visitor.pose)],
        ]}
        still={still}
      >
        <StoryChoices items={visitor.actions.map(menuItem(t, dispatch, () => "", labelOf))} />
      </TalkBox>
    );
  }

  const { actor } = person;
  const row = actor.runId ? rowOf(actor.runId) : undefined;
  return (
    <TalkBox
      plate={plate}
      sign={sign}
      model={under}
      alert={personAlert(person)}
      tabs={tabs}
      line={actorLine(t, words, character, actor, row)}
      facts={actorFacts(t, words, actor, simple)}
      notes={actorNotes(t, actor, meetingOf.get(actor.id))}
      still={still}
    >
      <ActorItems actor={actor} row={row} titleOf={titleOf}>
        {(items) => <StoryChoices items={items} />}
      </ActorItems>
    </TalkBox>
  );
}
