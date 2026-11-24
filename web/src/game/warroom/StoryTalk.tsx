// The story's dialogue box: who speaks (the agent the stage has turned to, or
// the narrator where nobody is; a character's sign goes before her name, where
// she has one), what it says in its own words (or a themed RunDialog for a run
// actor), what is known about it (as pictures when a theme pack is active),
// and what can be done about it as choices: the very items its menu has in the
// pixel room. The flavour line types itself out; under reduced motion and in a
// background tab it is simply there. Transcripts never animate.

import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { askQuestionText } from "../../agents/AskCard";
import { excerpt } from "../../agents/agentStore";
import { useI18n } from "../../i18n";
import { IconLayers, IconUsage } from "../../icons";
import { ContestDecide } from "../../runs/coord/ContestDecide";
import { useCoordStore } from "../../runs/coord/coordStore";
import { OverlapNotice } from "../../runs/coord/OverlapNotice";
import type { RunComposerHandle } from "../../runs/dialog/RunComposer";
import { RunDialog } from "../../runs/dialog/RunDialog";
import { selectRunById, useRuns } from "../../runs/runStore";
import { useSimpleMode } from "../../simple/simpleMode";
import type { ControlRow } from "../../station/control";
import { RunnerIcon } from "../../station/pickers/PictureCards";
import { StationMenu, type StationMenuItem } from "../../station/StationMenu";
import { useGameActions } from "../GameActions";
import {
  actorLabel,
  agentName,
  agentWho,
  energyLabel,
  formatTokens,
  placeOf,
  poseLabel,
  type LabelOf,
  type TitleOf,
  type Translate,
} from "../labels";
import type { Actor, Meeting } from "../world";
import { Face } from "./castArt";
import { visitorLabel } from "./Lounge";
import { meetingLabel } from "./OfficeRoom";
import { ActorItems, menuItem, roleBadgeKey } from "./PixelDesk";
import {
  figureOfPerson,
  personAlert,
  personWho,
  type StoryAlert,
  type StoryGroup,
  type StoryPerson,
} from "./storyPlaces";
import { useStoryWords, type StoryWords } from "./storyWords";
import { useThemeCast } from "./themeCastContext";

/** A letter's turn, in ms; a long line takes more letters a turn. */
const TICK_MS = 22;
const TURNS = 90;
/** Choices shown as buttons; with more, the last of them is a menu of the rest. */
const MAX_CHOICES = 4;

const NO_ITEMS: StationMenuItem[] = [];

/** One sentence that names a person, as a button's accessible name. */
export function personLabel(t: Translate, person: StoryPerson, simple = false): string {
  return person.kind === "actor"
    ? actorLabel(t, person.actor, placeOf(t, person.actor, simple), simple)
    : visitorLabel(t, person.visitor);
}

/** What a person is on, in a few words. */
export function personWhat(t: Translate, person: StoryPerson): string {
  if (person.kind === "visitor") return person.visitor.label;
  const { actor } = person;
  return actor.repoName || actor.nodeTitle || actor.sessionName || poseLabel(t, actor.pose);
}

/** What an agent without a run row says for itself: what it waits on first,
 * then what went wrong, then what it last said, then what it is doing. Never
 * its tokens: a line that changed with every heartbeat would type itself out
 * again. */
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

/** Inline envelope glyph matching icons.tsx's 1.75 stroke style. */
function IconMail({ size = 13 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="m3.5 7 8.5 6 8.5-6" />
    </svg>
  );
}

/** Pack-active facts strip (§11 item 16): pictures replace text clutter while
 * keeping accessible text visually hidden. Role and node stay visible text. */
function ActorMeta({
  actor,
  row,
  words,
  notes,
  simple,
  pictureMode,
}: {
  actor: Actor;
  row: ControlRow | undefined;
  words: StoryWords;
  notes: string[];
  simple: boolean;
  pictureMode: boolean;
}) {
  const { t } = useI18n();
  const { say } = words;
  const statusText = poseLabel(t, actor.pose);
  const roleText = actor.role
    ? roleBadgeKey(actor.role) === "custom"
      ? actor.role
      : t(`roles.name.${actor.role}`)
    : "";
  const runnerLabel = simple ? t("simple.story.where") : say("runner");
  const placeText = placeOf(t, actor, simple);
  const energy = actor.energy && !simple ? actor.energy : undefined;
  const energyText = energy ? energyLabel(t, energy.used, energy.cap) : "";
  const heldCount = actor.heldMail.length;
  const testText = actor.testState ? t(`warroom.test.${actor.testState}`) : "";
  const overlapCount = row?.run.overlapCount ?? actor.overlapCount ?? 0;
  const overlapText = overlapCount > 0 ? t("overlap.badge", { count: overlapCount }) : "";

  if (!pictureMode) {
    return (
      <ul className="px-story-meta">
        <li className="is-status">
          {say("status")} <b>{statusText}</b>
        </li>
        {roleText && (
          <li className="is-role">
            {say("role")} <b>{roleText}</b>
          </li>
        )}
        {actor.nodeTitle && (
          <li className="is-node">
            {say("node")} <b>{actor.nodeTitle}</b>
          </li>
        )}
        <li className="is-runner">
          {runnerLabel} <b>{placeText}</b>
        </li>
        {energy && (
          <li className="is-tokens">
            {say("tokens")} <b>{energyText}</b>
          </li>
        )}
        {heldCount > 0 && (
          <li className="is-held">
            {say("held")} <b>{heldCount}</b>
          </li>
        )}
        {actor.testState && (
          <li className="is-tests">
            {say("tests")} <b>{testText}</b>
          </li>
        )}
        {overlapCount > 0 && <li className="is-overlap">{overlapText}</li>}
        {notes.map((note) => (
          <li key={note}>{note}</li>
        ))}
      </ul>
    );
  }

  return (
    <ul className="px-story-meta is-pictured">
      <li className="is-status visually-hidden">
        {say("status")} <b>{statusText}</b>
      </li>
      {roleText && (
        <li className="is-role">
          {say("role")} <b>{roleText}</b>
        </li>
      )}
      {actor.nodeTitle && (
        <li className="is-node">
          {say("node")} <b>{actor.nodeTitle}</b>
        </li>
      )}
      <li className="is-runner">
        <RunnerIcon size={14} title={placeText} />
        <span className="visually-hidden">
          {runnerLabel} {placeText}
        </span>
      </li>
      {energy && (
        <li className="is-tokens">
          {energy.cap ? (
            <span
              className="px-meter px-story-fact-meter"
              role="meter"
              aria-label={say("tokens")}
              aria-valuemin={0}
              aria-valuemax={energy.cap}
              aria-valuenow={Math.min(energy.used, energy.cap)}
              aria-valuetext={energyText}
              title={energyText}
            >
              <span
                className={energy.used >= energy.cap ? "is-full" : undefined}
                style={{ width: `${Math.min(100, Math.max(4, (energy.used / energy.cap) * 100))}%` }}
              />
            </span>
          ) : (
            <span className="px-story-fact-tokens" title={energyText} aria-hidden="true">
              <IconUsage size={13} />
              <span>{formatTokens(energy.used)}</span>
            </span>
          )}
          <span className="visually-hidden">
            {say("tokens")} {energyText}
          </span>
        </li>
      )}
      {heldCount > 0 && (
        <li className="is-held">
          <span className="px-story-fact-mail" title={`${say("held")}: ${heldCount}`} aria-hidden="true">
            <IconMail size={13} />
            <span>{heldCount}</span>
          </span>
          <span className="visually-hidden">
            {say("held")} {heldCount}
          </span>
        </li>
      )}
      {actor.testState && (
        <li className="is-tests">
          <span
            className={`px-story-test-lamp is-${actor.testState}`}
            title={`${say("tests")}: ${testText}`}
            aria-hidden="true"
          />
          <span className="visually-hidden">
            {say("tests")} {testText}
          </span>
        </li>
      )}
      {overlapCount > 0 && (
        <li className="is-overlap">
          <span className="px-story-fact-overlap" title={overlapText} aria-hidden="true">
            <IconLayers size={13} />
            <span>{overlapCount}</span>
          </span>
          <span className="visually-hidden">{overlapText}</span>
        </li>
      )}
      {notes.map((note) => (
        <li key={note}>{note}</li>
      ))}
    </ul>
  );
}

/** Static preview for an unsubscribed list-layout row (§3.3): shows the last
 * loaded `text` event without opening a subscription. */
function UnsubscribedPreview({ runId }: { runId: string }) {
  const events = useRuns((s) => s.eventsByRun[runId]);
  const lastText = useMemo(() => {
    if (!events) return "";
    for (let i = events.length - 1; i >= 0; i--) {
      const ev = events[i];
      if (ev.type === "text" && ev.text?.trim()) return ev.text.trim();
    }
    return "";
  }, [events]);
  if (!lastText) return null;
  return <p className="px-story-preview">{excerpt(lastText, 240)}</p>;
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
  hasRun = false,
  tabs,
  line,
  facts = [],
  notes = [],
  meta,
  body,
  still,
  children,
}: {
  plate: string;
  /** Her sign's URL, "" for none. */
  sign?: string;
  model?: string;
  alert?: StoryAlert;
  hasRun?: boolean;
  tabs?: ReactNode;
  line?: string;
  facts?: Fact[];
  notes?: string[];
  meta?: ReactNode;
  body?: ReactNode;
  still: boolean;
  children?: ReactNode;
}) {
  return (
    <div className={`px-story-talk${hasRun ? " has-run" : ""}${alert ? ` is-${alert}` : ""}`}>
      <p className="px-story-plate">
        <PlateSign src={sign} />
        {plate}
        {model && <small>{model}</small>}
      </p>
      <div className="px-story-said">
        {tabs}
        {body ?? (
          <>
            {line !== undefined && <StoryLine text={line} still={still} />}
            {meta ??
              ((facts.length > 0 || notes.length > 0) && (
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
              ))}
          </>
        )}
      </div>
      {children}
    </div>
  );
}

/** Run-backed story dialog: mounts RunDialog variant="story" with the canned
 * flavour line, pictured facts and OverlapNotice in the header (§3.2–§3.5, §C.8). */
function StoryRunBody({
  actor,
  row,
  flavourLine,
  meta,
  simple,
  still,
  subscribed,
}: {
  actor: Actor;
  row: ControlRow;
  flavourLine: string;
  meta: ReactNode;
  simple: boolean;
  still: boolean;
  subscribed: boolean;
}) {
  const run = row.run;
  const composerRef = useRef<RunComposerHandle>(null);
  const overlaps = useCoordStore((s) => s.overlapsByRun[run.id]);
  const loadOverlaps = useCoordStore((s) => s.loadOverlaps);
  const overlapCount = run.overlapCount ?? actor.overlapCount ?? 0;

  // Ensure the run record is in useRuns so RunDialog can resolve it even when
  // rowOf came from Station's recent or global lists before putRun ran.
  useEffect(() => {
    if (!subscribed) return;
    if (!selectRunById(useRuns.getState(), run.id)) {
      useRuns.getState().putRun(run);
    }
  }, [subscribed, run]);

  useEffect(() => {
    if (!subscribed || overlapCount <= 0 || overlaps !== undefined) return;
    void loadOverlaps(run.id);
  }, [subscribed, overlapCount, overlaps, run.id, loadOverlaps]);

  const prefill = useCallback((text: string) => {
    composerRef.current?.prefill(text);
  }, []);

  if (!subscribed) {
    return (
      <>
        <StoryLine text={flavourLine} still={still} />
        <UnsubscribedPreview runId={run.id} />
        {meta}
      </>
    );
  }

  const header = (
    <div className="px-story-dlg-head">
      <StoryLine text={flavourLine} still={still} />
      {meta}
      <OverlapNotice overlaps={overlaps} run={run} simple={simple} onTell={prefill} onOpenFollowUp={prefill} />
    </div>
  );

  return (
    <RunDialog
      runId={run.id}
      variant="story"
      simple={simple}
      header={header}
      emptyLine={flavourLine}
      subscribed
      composerRef={composerRef}
    />
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
  /** Whether a run dialog inside this box should subscribe live (default true). */
  subscribed?: boolean;
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
  subscribed = true,
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
  const activeFigure = cast ? figureOfPerson(person, cast.figureOf) : group.figure;
  const character = activeFigure?.character ?? group.figure?.character;
  // What the pack calls her comes first, and who she is after it.
  const title = words.title(character);
  const model = who.model && who.model !== "default" ? who.model : "";
  const plate = title ?? agentName(who.agent);
  const under = (title ? `${agentName(who.agent)} ${model}` : model).trim() || undefined;
  // Only one of the pack's characters has a sign: the unnamed have none.
  const signPath = activeFigure?.sign ?? group.figure?.sign;
  const sign = cast && signPath ? cast.url(signPath) : "";

  const isMultiRepoGroup = group.kind === "group";
  const isContest = group.kind === "contest";
  // A contest pair is decided here too (node contests have no list row).
  const contestId = isContest && group.key.startsWith("contest:") ? group.key.slice("contest:".length) : "";

  const pips = isMultiRepoGroup && group.members.length > 1 && (
    <div className="px-story-pips" aria-hidden="true">
      {group.members.map((member) => {
        const pose = member.kind === "actor" ? member.actor.pose : member.visitor.pose;
        const repo = member.kind === "actor" ? member.actor.repoName || personWhat(t, member) : member.visitor.label;
        return (
          <span
            key={member.id}
            className={`px-story-pip is-${pose}${member.id === person.id ? " is-current" : ""}`}
            title={`${repo}: ${poseLabel(t, pose)}`}
          />
        );
      })}
    </div>
  );

  const tabs = group.members.length > 1 && (
    <>
      {pips}
      <div className="px-story-members" role="group" aria-label={groupLabel}>
        {group.members.map((member, i) => {
          const alert = personAlert(member);
          const memberFigure = cast ? figureOfPerson(member, cast.figureOf) : undefined;
          if (isMultiRepoGroup) {
            const repo =
              (member.kind === "actor" && member.actor.repoName) || personWhat(t, member);
            return (
              <button
                key={member.id}
                type="button"
                className={`px-story-member is-repo${alert ? ` is-${alert}` : ""}`}
                aria-label={personLabel(t, member, simple)}
                aria-pressed={member.id === person.id}
                onClick={() => onPick(member.id)}
              >
                {cast && (
                  <span className="px-story-member-face" aria-hidden="true">
                    <Face figure={memberFigure} cast={cast} />
                  </span>
                )}
                <span className="px-story-member-label" aria-hidden="true">
                  {repo}
                </span>
              </button>
            );
          }
          if (isContest) {
            const label =
              member.kind === "actor" && member.actor.contestLabel
                ? t("contest.entry", { label: member.actor.contestLabel })
                : personWhat(t, member);
            return (
              <button
                key={member.id}
                type="button"
                className={`px-story-member is-contest${alert ? ` is-${alert}` : ""}`}
                aria-label={personLabel(t, member, simple)}
                aria-pressed={member.id === person.id}
                onClick={() => onPick(member.id)}
              >
                {cast && (
                  <span className="px-story-member-face" aria-hidden="true">
                    <Face figure={memberFigure} cast={cast} />
                  </span>
                )}
                <span className="px-story-member-label" aria-hidden="true">
                  {label}
                </span>
              </button>
            );
          }
          return (
            <button
              key={member.id}
              type="button"
              className={`px-story-member${alert ? ` is-${alert}` : ""}`}
              aria-label={personLabel(t, member, simple)}
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
      {contestId && (
        <div className="px-story-contest">
          <ContestDecide contestId={contestId} simple={simple} />
        </div>
      )}
    </>
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
  const notes = actorNotes(t, actor, meetingOf.get(actor.id));
  const meta = (
    <ActorMeta
      actor={actor}
      row={row}
      words={words}
      notes={notes}
      simple={simple}
      pictureMode={Boolean(cast)}
    />
  );

  if (row) {
    const flavourLine = words.line(character, actor.pose, actor.nodeTitle);
    return (
      <TalkBox
        plate={plate}
        sign={sign}
        model={under}
        alert={personAlert(person)}
        hasRun
        tabs={tabs}
        still={still}
        body={
          <StoryRunBody
            actor={actor}
            row={row}
            flavourLine={flavourLine}
            meta={meta}
            simple={simple}
            still={still}
            subscribed={subscribed}
          />
        }
      >
        <ActorItems actor={actor} row={row} titleOf={titleOf}>
          {(items) => <StoryChoices items={items} />}
        </ActorItems>
      </TalkBox>
    );
  }

  return (
    <TalkBox
      plate={plate}
      sign={sign}
      model={under}
      alert={personAlert(person)}
      tabs={tabs}
      line={actorLine(t, words, character, actor, row)}
      meta={meta}
      still={still}
    >
      <ActorItems actor={actor} row={row} titleOf={titleOf}>
        {(items) => <StoryChoices items={items} />}
      </ActorItems>
    </TalkBox>
  );
}
