// A theme pack's skin on the whole app: the palette it starts from, the colours
// it changes, its font. All of it goes on the root element, and this is the
// one writer of the app's light or dark there, so a skin's scheme and the
// viewer's own setting cannot take turns. It fails soft: with no skin, or on
// any error at all, the root holds the viewer's theme and nothing of a pack's.

import { useEffect, useRef } from "react";
import { useApp, type Theme } from "../store";
import { isThemeHexColor, THEME_COLORS, type ThemeColor, type ThemeSkin } from "./themeManifest";
import { useThemePack } from "./useThemePack";

/** The attribute a pack's font goes in; styles/01-tokens.css reads it. */
const FONT = "data-theme-font";
/** The pack on show, skin or not, for the styles that are only a pack's. */
const PACK = "data-theme-pack";
const THEME = "data-theme";

const NOTHING: readonly ThemeColor[] = [];

/** Written only when it changes: a run's terminal repaints on every write. */
function attribute(root: HTMLElement, name: string, value: string | undefined): void {
  if (value === undefined) root.removeAttribute(name);
  else if (root.getAttribute(name) !== value) root.setAttribute(name, value);
}

function builtIn(root: HTMLElement, theme: Theme, colors: readonly ThemeColor[]): ThemeColor[] {
  try {
    for (const color of colors) root.style.removeProperty(`--${color}`);
    attribute(root, FONT, undefined);
    attribute(root, PACK, undefined);
    attribute(root, THEME, theme);
  } catch {
    /* a root that takes nothing has nothing of a pack's on it */
  }
  return [];
}

/**
 * Puts the look on the root: the skin's scheme, colours and font over the
 * viewer's own `theme`, or that theme alone when there is no skin. `before` is
 * what the last call gave back; the colours in it that this look does not set
 * are taken off, and no other property of the root is touched. Gives back the
 * colours it set, for the next call.
 */
export function applyThemeSkin(
  root: HTMLElement,
  theme: Theme,
  skin?: ThemeSkin,
  packId?: string,
  before: readonly ThemeColor[] = NOTHING,
): ThemeColor[] {
  const set: ThemeColor[] = [];
  try {
    for (const color of THEME_COLORS) {
      const value = skin?.colors[color];
      // The parser let nothing but hex through; this is what writes it.
      if (!isThemeHexColor(value)) continue;
      set.push(color);
      root.style.setProperty(`--${color}`, value);
    }
    for (const color of before) {
      if (!set.includes(color)) root.style.removeProperty(`--${color}`);
    }
    attribute(root, FONT, skin?.font);
    attribute(root, PACK, packId);
    attribute(root, THEME, skin?.scheme ?? theme);
    return set;
  } catch {
    return builtIn(root, theme, [...before, ...set]);
  }
}

/**
 * Keeps the root in the look this viewer sees: their own light or dark, under
 * the skin of the pack on show when it has one. Mounted once, by whatever
 * draws the app for someone signed in; gone with it, so is the skin.
 */
export function useThemeSkin(): void {
  const theme = useApp((s) => s.theme);
  const active = useThemePack();
  const skin = active?.manifest.skin;
  const packId = active?.pack.id;
  const set = useRef<readonly ThemeColor[]>(NOTHING);
  const own = useRef(theme);

  useEffect(() => {
    own.current = theme;
    set.current = applyThemeSkin(document.documentElement, theme, skin, packId, set.current);
  }, [theme, skin, packId]);

  useEffect(
    () => () => {
      set.current = applyThemeSkin(document.documentElement, own.current, undefined, undefined, set.current);
    },
    [],
  );
}
