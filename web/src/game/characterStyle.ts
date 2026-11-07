// How this viewer's war room draws its characters: anime (the pre-rendered
// sheets, anime/; a character or state without one stays pixel) or pixel art
// (the skins, pixel/skins.ts). A preference of this browser's
// (preferences/schema.ts: characterStyle), so a person who picks pixel
// changes nobody else's room. Nothing picked, or a value that does not
// read, is the default, DEFAULT_CHARACTER_STYLE: anime.

import { CHARACTER_STYLES, DEFAULT_CHARACTER_STYLE, type CharacterStyle } from "../preferences";
import { useApp, usePreference } from "../store";

export { CHARACTER_STYLES, DEFAULT_CHARACTER_STYLE, type CharacterStyle };

/** A stored or passed value as a style: the default for anything else. */
export function characterStyleOf(value: unknown): CharacterStyle {
  return CHARACTER_STYLES.includes(value as CharacterStyle) ? (value as CharacterStyle) : DEFAULT_CHARACTER_STYLE;
}

function setCharacterStyle(style: CharacterStyle): void {
  useApp.getState().updateUIPreference("characterStyle", characterStyleOf(style));
}

/** The viewer's character style and a way to change it; whoever reads it
 * redraws on a change (a memoised desk too: it reads it itself). */
export function useCharacterStyle(): [CharacterStyle, (style: CharacterStyle) => void] {
  return [characterStyleOf(usePreference("characterStyle")), setCharacterStyle];
}
