// Who an agent is drawn as in the war room: a person (in its kind's
// headwear and shirt colour), or a character of its own for the model behind
// it: Claude as a blocky orange creature, DeepSeek as a whale, Gemini as a
// living four-pointed star, GPT as GPT (in pixel art still the person: its
// own picture is its anime sheet, game/anime). The model decides first,
// whatever agent runs it (opencode on a DeepSeek model is the whale); with a
// model that says nothing, the agent kind does. The pictures are
// pixel/skins.ts and anime/manifest.ts.

export const CHARACTERS = ["human", "claude", "whale", "star", "gpt"] as const;

export type CharacterId = (typeof CHARACTERS)[number];

/** Words in a model's name that say which character it is, in order. */
const BY_MODEL: readonly [words: readonly string[], character: CharacterId][] = [
  [["deepseek"], "whale"],
  [["claude", "opus", "sonnet", "haiku"], "claude"],
  [["gemini"], "star"],
  [["gpt", "openai", "codex"], "gpt"],
];

/** OpenAI's o-series (o1, o3, o4-mini…): "o" and a number as a word of its
 * own, so "neo3" or "pro4" is not one. */
const O_SERIES = /(?:^|[^a-z0-9])o\d+(?![a-z0-9])/;

/** Agent kinds with a character of their own, for a model that names none. */
const BY_AGENT: Readonly<Record<string, CharacterId>> = {
  claude: "claude",
  gemini: "star",
  codex: "gpt",
};

export function characterOf(agent: string, model: string): CharacterId {
  const name = (model ?? "").toLowerCase();
  for (const [words, character] of BY_MODEL) if (words.some((word) => name.includes(word))) return character;
  if (O_SERIES.test(name)) return "gpt";
  return BY_AGENT[(agent ?? "").toLowerCase()] ?? "human";
}
