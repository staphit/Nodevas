// Paints placed sprites into one SVG at whole art pixels: crispEdges, every
// rectangle on the pixel grid, the CSS size an integer multiple of the art
// (warroom/warroom.css: --px). Colours are tokens, so nothing here depends on the
// theme. Frames of an animation are groups the stylesheet takes turns
// showing (steps(), no script); a still picture shows frame 0.

import { memo, type CSSProperties } from "react";

import type { Placed } from "../warroom/deskArt";
import { fillOf } from "./palette";

export const PixelSprite = memo(function PixelSprite({
  parts,
  width,
  height,
  className,
  style,
}: {
  parts: Placed[];
  /** In art pixels. */
  width: number;
  height: number;
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <svg
      className={`px-sprite${className ? ` ${className}` : ""}`}
      viewBox={`0 0 ${width} ${height}`}
      style={{ ...style, ["--px-w" as string]: width, ["--px-h" as string]: height }}
      shapeRendering="crispEdges"
      aria-hidden="true"
      focusable="false"
    >
      {parts.map((part, i) => (
        <g
          key={`${part.name}-${i}`}
          data-part={part.name}
          className={part.frame ? `px-f px-f${part.frame.index}of${part.frame.of}` : undefined}
          transform={part.x || part.y ? `translate(${part.x} ${part.y})` : undefined}
        >
          {part.sprite.layers.map((layer) => (
            <path key={layer.token} d={layer.d} style={{ fill: fillOf(layer.token) }} />
          ))}
        </g>
      ))}
    </svg>
  );
});
