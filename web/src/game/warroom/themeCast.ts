// Who plays whom when a theme pack is on: which of the pack's characters an
// agent is, and which of her scenes its state asks for. Pure, so the casting
// is testable without a pack or a page; a pack that has no picture for an
// agent gives undefined, and the story shows it as one of the unnamed.

import type { ThemeManifest, ThemeScene } from "../../theme-pack/themeManifest";
import type { Actor } from "../world";

/** One of a pack's pictures, as an agent wears it. */
export interface CastFigure {
  /** Character and scene: agents with the same key stand as one. */
  key: string;
  character: string;
  sceneName: string;
  scene: ThemeScene;
  /** Her sign's image, when she has one. */
  sign?: string;
}

/** Where a run asleep on a usage limit has gone. */
export const OUTINGS: readonly string[] = ["hike", "stream", "fish", "camp", "bbq", "ride"];

const ROLE_SCENE: Readonly<Record<string, string>> = {
  planner: "think",
  researcher: "search",
  reviewer: "read",
  qa: "read",
  tester: "cast",
  executor: "write",
};

/** FNV-1a: a run goes on the same outing however often the world is rebuilt. */
export function hash(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/** A model pattern: plain text, but for "*" (anything). Whole, any case. */
function matches(pattern: string, model: string): boolean {
  const source = pattern
    .split("*")
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
    .join(".*");
  return new RegExp(`^${source}$`, "i").test(model);
}

/** The pack's character for an agent: by its model first, then by its agent,
 * then the pack's fallback character. */
export function characterFor(manifest: ThemeManifest, agent: string, model: string): string | undefined {
  const all = Object.entries(manifest.characters);
  if (model && model !== "default") {
    const byModel = all.find(([, character]) => character.match.models.some((pattern) => matches(pattern, model)));
    if (byModel) return byModel[0];
  }
  const kind = agent.toLowerCase();
  const byAgent = all.find(([, character]) => character.match.agents.some((name) => name.toLowerCase() === kind));
  if (byAgent) return byAgent[0];
  return all.find(([, character]) => character.fallback)?.[0];
}

/** The scene an agent's state asks for. What waits on someone comes first,
 * then the meeting it sits in, then the work its role does. */
export function sceneWanted(actor: Pick<Actor, "id" | "pose" | "area" | "role" | "bubble">): string {
  switch (actor.pose) {
    case "asking-user":
      return "ask";
    case "failed":
      return "fail";
    case "finished":
      return "done";
    case "awaiting-accept":
    case "waiting-ci":
      return "wait";
    case "offline":
      return "sleep";
    case "limit":
      return OUTINGS[hash(actor.id) % OUTINGS.length];
  }
  if (actor.area === "meeting") return actor.bubble ? "speak" : "listen";
  if (actor.pose === "idle") return "idle";
  if (actor.pose === "reviewing") return "read";
  return ROLE_SCENE[actor.role ?? ""] ?? "cast";
}

/** The picture for an agent in a state: her scene of that name, else her
 * idle (her sleep first, for an outing she does not go on), else none. */
export function figureFor(
  manifest: ThemeManifest,
  who: { agent: string; model: string },
  wanted: string,
): CastFigure | undefined {
  const id = characterFor(manifest, who.agent, who.model);
  if (!id) return undefined;
  const character = manifest.characters[id];
  const names = OUTINGS.includes(wanted) ? [wanted, "sleep", "idle"] : [wanted, "idle"];
  const sceneName = names.find((name) => Object.hasOwn(character.scenes, name));
  if (!sceneName) return undefined;
  const figure: CastFigure = { key: `${id}/${sceneName}`, character: id, sceneName, scene: character.scenes[sceneName] };
  if (character.sign) figure.sign = character.sign;
  return figure;
}
