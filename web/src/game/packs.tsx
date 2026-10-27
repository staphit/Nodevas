// Drawing with a view pack (docs/view-packs.md): the pack Station shows is
// put in context, and each piece of built-in art asks it for a picture by
// sprite key before drawing its own. Pictures are only ever <img> (or a CSS
// background), never inlined: an SVG's scripts do not run there.

import { createContext, useContext, type CSSProperties, type ReactNode } from "react";

import type { ViewPack } from "../api";

export const PackContext = createContext<ViewPack | null>(null);

/** The first of keys the pack has a picture for. */
export function packSprite(pack: ViewPack | null, keys: readonly string[]): string | undefined {
  if (!pack) return undefined;
  for (const key of keys) {
    const url = pack.sprites[key];
    if (url) return url;
  }
  return undefined;
}

export function usePackSprite(...keys: string[]): string | undefined {
  return packSprite(useContext(PackContext), keys);
}

/**
 * The pack's picture for the first of keys it has, in a box of the built-in
 * art's size and class (so it sits where the built-in art sits), else the
 * built-in art. The picture keeps its proportions, bottom centre on the spot.
 */
export function PackArt({
  keys,
  className,
  width,
  height,
  style,
  children,
}: {
  keys: readonly string[];
  className: string;
  width: number;
  height: number;
  style?: CSSProperties;
  /** The built-in art. */
  children: ReactNode;
}) {
  const url = packSprite(useContext(PackContext), keys);
  if (!url) return <>{children}</>;
  return (
    <img
      className={`${className} pack-sprite`}
      src={url}
      alt=""
      aria-hidden="true"
      draggable={false}
      width={width}
      height={height}
      style={style}
    />
  );
}
