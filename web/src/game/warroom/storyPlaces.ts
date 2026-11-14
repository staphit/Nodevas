// The building as a theme pack's story tells it: six places to go to, and in
// each the people there, one picture per character and state. Whoever wears
// the same picture stands as one, with a count; those the pack has no picture
// for stand as the unnamed, one per kind of agent. Pure, so it is testable
// without a pack or a page.

import { THEME_PLACES, type ThemePlaceId } from "../../theme-pack/themeManifest";
import type { Actor, Building, LoungeSpot } from "../world";
import { drawnInLounge } from "./Lounge";
import { sceneWanted, type CastFigure } from "./themeCast";

export type Visitor = NonNullable<LoungeSpot["visitor"]>;

/** One of those in a place: the project's own agent, or another's at rest. */
export type StoryPerson =
  | { kind: "actor"; id: string; actor: Actor }
  | { kind: "visitor"; id: string; visitor: Visitor };

/** What waits on the viewer: an answer, or a look at what went wrong. */
export type StoryAlert = "ask" | "fail";

export interface StoryGroup {
  /** The picture's key, or UNNAMED and the agent's kind: what the stage tells
   * its figures apart by. */
  key: string;
  /** None for the unnamed. */
  figure?: CastFigure;
  members: StoryPerson[];
  /** The most pressing of its members'. */
  alert?: StoryAlert;
}

export interface StoryPlace {
  id: ThemePlaceId;
  groups: StoryGroup[];
  /** People there; for the runners' place, runners. */
  count: number;
  /** What waits for an answer there: people, and what lies on the table. */
  asks: number;
  fails: number;
}

export type StoryPlaces = Record<ThemePlaceId, StoryPlace>;

/** What the key of those the pack has no picture for starts with. */
export const UNNAMED = "?";

export function personAlert(person: StoryPerson): StoryAlert | undefined {
  if (person.kind === "visitor") return undefined;
  const { actor } = person;
  if (actor.ask || actor.heldMail.length > 0 || actor.pose === "asking-user") return "ask";
  return actor.pose === "failed" ? "fail" : undefined;
}

export function personWho(person: StoryPerson): { agent: string; model: string } {
  return person.kind === "actor" ? person.actor : person.visitor;
}

type FigureOf = (who: { agent: string; model: string }, wanted: string) => CastFigure | undefined;

function figureOfPerson(person: StoryPerson, figureOf: FigureOf): CastFigure | undefined {
  if (person.kind === "actor") return figureOf(person.actor, sceneWanted(person.actor));
  return figureOf(person.visitor, sceneWanted({ id: person.id, pose: person.visitor.pose, area: "lounge" }));
}

function place(id: ThemePlaceId, people: readonly StoryPerson[], figureOf: FigureOf): StoryPlace {
  const groups: StoryGroup[] = [];
  const byKey = new Map<string, StoryGroup>();
  let asks = 0;
  let fails = 0;
  for (const person of people) {
    const figure = figureOfPerson(person, figureOf);
    const key = figure?.key ?? `${UNNAMED}/${personWho(person).agent.toLowerCase()}`;
    let group = byKey.get(key);
    if (!group) {
      group = figure ? { key, figure, members: [] } : { key, members: [] };
      byKey.set(key, group);
      groups.push(group);
    }
    group.members.push(person);
    const alert = personAlert(person);
    if (alert === "ask") asks++;
    if (alert === "fail") fails++;
    if (alert && group.alert !== "ask") group.alert = alert;
  }
  return { id, groups, count: people.length, asks, fails };
}

/** Every place of the story, with who is there now. */
export function storyPlaces(
  building: Building,
  actorsById: ReadonlyMap<string, Actor>,
  figureOf: FigureOf,
): StoryPlaces {
  // The project's office, then the global agents': they work in the same
  // places, the global ones in no project.
  const offices = [building.current, building.global].filter((office) => office !== null);
  const actors = (area: "work" | "meeting" | "entrance"): StoryPerson[] =>
    offices.flatMap((office) =>
      office[area].flatMap((id) => {
        const actor = actorsById.get(id);
        return actor ? [{ kind: "actor" as const, id, actor }] : [];
      }),
    );

  // The lounge's spots: whoever is out on a usage limit is away, not resting.
  const resting: StoryPerson[] = [];
  const away: StoryPerson[] = [];
  for (const spot of building.lounge) {
    const actor = spot.actorId ? actorsById.get(spot.actorId) : undefined;
    let person: StoryPerson | undefined;
    if (spot.visitor) person = { kind: "visitor", id: spot.id, visitor: spot.visitor };
    else if (actor && drawnInLounge(actor)) person = { kind: "actor", id: actor.id, actor };
    if (!person) continue;
    const pose = person.kind === "actor" ? person.actor.pose : person.visitor.pose;
    (pose === "limit" ? away : resting).push(person);
  }

  const meeting = place("meeting", actors("meeting"), figureOf);
  meeting.asks += offices.reduce((sum, office) => sum + office.items.length, 0);
  return {
    work: place("work", actors("work"), figureOf),
    meeting,
    entrance: place("entrance", actors("entrance"), figureOf),
    lounge: place("lounge", resting, figureOf),
    away: place("away", away, figureOf),
    runners: { id: "runners", groups: [], count: building.racks.length, asks: 0, fails: 0 },
  };
}

/** Where the story opens: where something waits on the viewer, else where
 * anyone is, else the work place. */
export function openingPlace(places: StoryPlaces): ThemePlaceId {
  const people = THEME_PLACES.filter((id) => id !== "runners");
  return (
    people.find((id) => places[id].asks + places[id].fails > 0) ??
    people.find((id) => places[id].count > 0) ??
    "work"
  );
}

/** The group the stage turns to: the one chosen while it is still there, else
 * the first with something waiting, else the first. */
export function leadGroup(here: StoryPlace, chosen: string | undefined): StoryGroup | undefined {
  return (
    here.groups.find((group) => group.key === chosen) ?? here.groups.find((group) => group.alert) ?? here.groups[0]
  );
}

/** The same within a group: which of its members speaks. */
export function leadMember(group: StoryGroup, chosen: string | undefined): StoryPerson {
  return (
    group.members.find((person) => person.id === chosen) ??
    group.members.find((person) => personAlert(person)) ??
    group.members[0]
  );
}
