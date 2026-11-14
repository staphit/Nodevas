// The theme pack on show, as the war room draws from it: who gets which
// picture, where its images are, and what it calls the places. WarRoomStage
// provides it; with no pack there is none, and the building is drawn in
// pixels.

import { createContext, useCallback, useContext, useMemo, useState } from "react";

import type {
  ThemeLineState,
  ThemeManifest,
  ThemePlaceId,
  ThemeScene,
  ThemeSkin,
  ThemeText,
  ThemeWord,
} from "../../theme-pack/themeManifest";
import { useThemePack } from "../../theme-pack/useThemePack";
import { figureFor, type CastFigure } from "./themeCast";

export interface ThemeCast {
  canvas: readonly [number, number];
  /** The picture for an agent in a state, or none: the pack has nobody for it. */
  figureOf: (who: { agent: string; model: string }, wanted: string) => CastFigure | undefined;
  /** An image of the pack as a URL. */
  url: (path: string) => string;
  /** The room under a scene as a URL, "" for a scene in none. */
  roomUrl: (scene: ThemeScene) => string;
  /** The pack's colours and font, when it has a skin. */
  skin?: ThemeSkin;
  /** The pack's own name for a place, or none: the app's name then. */
  placeName: (place: ThemePlaceId, language: string) => string | undefined;
  /** The pack's own word for something of the story, or none. */
  word: (word: ThemeWord, language: string) => string | undefined;
  /** What a character says in a state: hers, else what anyone of the pack
   * says, else none. No character is one of the unnamed. */
  line: (character: string | undefined, state: ThemeLineState, language: string) => string | undefined;
  /** What a character is called, or none. */
  title: (character: string, language: string) => string | undefined;
  /** Nothing may move: reduced motion, or a background tab. */
  still: boolean;
  /** An image that would not load: whoever wears it has no picture then. */
  broken: (path: string) => void;
}

export const ThemeCastContext = createContext<ThemeCast | undefined>(undefined);

export function useThemeCast(): ThemeCast | undefined {
  return useContext(ThemeCastContext);
}

const NONE: ReadonlySet<string> = new Set();

function inLanguage(text: ThemeText | undefined, language: string): string | undefined {
  return text && ((language === "zh-TW" && text["zh-TW"]) || text.en);
}

/** The cast of the pack this viewer sees, for a provider. */
export function useActiveThemeCast(still: boolean): ThemeCast | undefined {
  const theme = useThemePack();
  const manifest = theme?.manifest;
  const imageUrl = theme?.imageUrl;
  // Held with the manifest they were found in: another pack starts clean.
  const [lost, setLost] = useState<{ manifest: ThemeManifest; paths: ReadonlySet<string> } | null>(null);
  const paths = lost && lost.manifest === manifest ? lost.paths : NONE;

  const broken = useCallback(
    (path: string) => {
      if (!manifest) return;
      setLost((was) => {
        const held = was && was.manifest === manifest ? was.paths : NONE;
        return held.has(path) ? was : { manifest, paths: new Set([...held, path]) };
      });
    },
    [manifest],
  );

  return useMemo(() => {
    if (!manifest || !imageUrl) return undefined;
    return {
      canvas: manifest.canvas,
      figureOf: (who, wanted) => {
        const figure = figureFor(manifest, who, wanted);
        return figure && !paths.has(figure.scene.image) ? figure : undefined;
      },
      url: imageUrl,
      roomUrl: (scene) => {
        const path = scene.room ? manifest.rooms[scene.room] : undefined;
        return path ? imageUrl(path) : "";
      },
      skin: manifest.skin,
      placeName: (place, language) => inLanguage(manifest.places?.[place], language),
      word: (word, language) => inLanguage(manifest.words?.[word], language),
      line: (character, state, language) => {
        // Own, not inherited: a character id may be any name at all.
        const hers =
          character !== undefined && Object.hasOwn(manifest.characters, character)
            ? manifest.characters[character].lines?.[state]
            : undefined;
        return inLanguage(hers ?? manifest.lines?.[state], language);
      },
      title: (character, language) =>
        Object.hasOwn(manifest.characters, character)
          ? inLanguage(manifest.characters[character].title, language)
          : undefined,
      still,
      broken,
    };
  }, [manifest, imageUrl, paths, still, broken]);
}
