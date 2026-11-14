// The story's words: a pack's own where it has them, in its world's terms and
// each character's voice, and the app's wherever it has none.

import { useMemo } from "react";

import { useI18n } from "../../i18n";
import type { ThemeWord } from "../../theme-pack/themeManifest";
import type { Pose } from "../world";
import { useThemeCast } from "./themeCastContext";

/** The app's own text for each word a pack may replace. */
const BUILT_IN: Record<ThemeWord, string> = {
  narrator: "game.story.narrator",
  unnamed: "game.story.unnamed",
  task: "game.story.task",
  people: "game.story.people",
  notices: "game.story.notices",
  others: "game.building.others",
  nobody: "game.story.nobody",
  count: "game.story.count",
  runnerCount: "game.story.runnerCount",
  failed: "game.story.failed",
  empty: "game.story.empty",
  noticesLine: "game.story.noticesLine",
  runnersLine: "game.story.runnersLine",
  heldLine: "game.story.heldLine",
  more: "game.story.more",
  status: "game.story.meta.status",
  role: "game.story.meta.role",
  node: "game.story.meta.node",
  runner: "game.story.meta.runner",
  tokens: "game.story.meta.tokens",
  project: "game.story.meta.project",
  held: "game.story.meta.held",
  tests: "game.story.meta.tests",
};

type Values = Record<string, string | number>;

/** A pack's text with its {names} filled in; one it has no value for stays. */
function fill(text: string, values: Values): string {
  return text.replace(/\{(\w+)\}/g, (whole, name: string) =>
    Object.hasOwn(values, name) ? String(values[name]) : whole,
  );
}

export interface StoryWords {
  say: (word: ThemeWord, values?: Values) => string;
  /** What someone says of the state it is in: a character of the pack, or
   * with none one of the unnamed. */
  line: (character: string | undefined, pose: Pose, node: string) => string;
  /** What a character of the pack is called, or none. */
  title: (character: string | undefined) => string | undefined;
}

export function useStoryWords(): StoryWords {
  const { t, language } = useI18n();
  const cast = useThemeCast();
  return useMemo(() => {
    const say = (word: ThemeWord, values: Values = {}) => {
      const own = cast?.word(word, language);
      return own ? fill(own, values) : t(BUILT_IN[word], values);
    };
    return {
      say,
      line: (character, pose, node) => {
        const own = cast?.line(character, pose, language);
        // A pack writes one line for a state: with no node, it is about "the task".
        if (own) return fill(own, { node: node ? t("game.story.quoted", { text: node }) : say("task") });
        return node ? t(`game.story.line.${pose}`, { node }) : t(`game.story.lineNoNode.${pose}`);
      },
      title: (character) => (character === undefined ? undefined : cast?.title(character, language)),
    };
  }, [t, language, cast]);
}
