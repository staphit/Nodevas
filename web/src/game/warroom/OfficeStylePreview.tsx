// A small picture of an office style: its wall and floor, a desk and a
// chair, in its own palette and the current theme. For the style pickers
// (Settings › This project, the door sign in Station).

import { memo, useMemo, type CSSProperties } from "react";

import "./warroom.css";

import { PixelSprite } from "../pixel/PixelSprite";
import { chairOf, deskTopOf } from "../pixel/styleSprites";
import type { OfficeStyle } from "../world";
import { useArtPixel } from "./artPixel";
import type { Placed } from "./deskArt";

export const PREVIEW_W = 48;
export const PREVIEW_H = 24;
/** The wall's height in the preview; the floor is the rest. */
const PREVIEW_WALL = 10;

function previewParts(style: OfficeStyle): Placed[] {
  const desk = deskTopOf(style);
  const chair = chairOf(style);
  return [
    { name: "desk", sprite: desk, x: 8, y: PREVIEW_H - desk.h },
    { name: "chair", sprite: chair, x: 17, y: PREVIEW_H - chair.h },
  ];
}

export const OfficeStylePreview = memo(function OfficeStylePreview({ style }: { style: OfficeStyle }) {
  // Two device-whole CSS pixels an art pixel: small enough for a row of six.
  const px = useArtPixel(2);
  const parts = useMemo(() => previewParts(style), [style]);
  return (
    <span
      className={`px-office px-style-preview px-style--${style}`}
      style={{ ["--px" as string]: `${px}px`, ["--px-preview-wall" as string]: PREVIEW_WALL } as CSSProperties}
      aria-hidden="true"
    >
      <span className="px-preview-wall" />
      <span className="px-preview-floor" />
      <PixelSprite parts={parts} width={PREVIEW_W} height={PREVIEW_H} />
    </span>
  );
});
