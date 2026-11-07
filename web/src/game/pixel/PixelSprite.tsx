// Paints placed sprites into one SVG at whole art pixels: crispEdges, every
// rectangle on the pixel grid, the CSS size an integer multiple of the art
// (warroom/warroom.css: --px). Colours are tokens, so nothing here depends on the
// theme. Frames of an animation are groups the stylesheet takes turns
// showing (steps(), no script); a still picture shows frame 0.
//
// An anime figure (Placed.anime) is the one part that is not pixels: its
// action's row of a sheet, an <image> inside an <svg> that shows the window
// of the frame inside the scene, stepped along the row by the stylesheet
// (px-anime-strip); smooth, never pixelated, and still, frame 0, wherever
// the pixels hold still.

import { memo, type CSSProperties } from "react";

import type { Placed } from "../warroom/deskArt";
import { fillOf } from "./palette";

function AnimeFigure({ part }: { part: Placed }) {
  const art = part.anime!;
  const { window } = art;
  return (
    <g data-part={part.name} className="px-anime" transform={part.x || part.y ? `translate(${part.x} ${part.y})` : undefined}>
      <svg
        width={part.sprite.w}
        height={part.sprite.h}
        viewBox={`${window.x} ${window.y} ${window.w} ${window.h}`}
        overflow="hidden"
      >
        <image
          className={art.still ? "px-anime-strip" : "px-anime-strip is-looping"}
          href={art.href}
          x={0}
          y={-art.row * art.frameH}
          width={art.sheetW}
          height={art.sheetH}
          preserveAspectRatio="none"
          style={
            {
              "--px-anime-loop": `${art.loopMs}ms`,
              "--px-anime-steps": art.frames,
              "--px-anime-span": `${-art.frames * art.frameW}px`,
            } as CSSProperties
          }
        />
      </svg>
    </g>
  );
}

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
      {parts.map((part, i) =>
        part.anime ? (
          // A scene's one figure, keyed alike whatever its action: a new
          // gesture moves the strip to its row rather than mounting a new
          // image, which could show nothing while it decodes again.
          <AnimeFigure key="anime" part={part} />
        ) : (
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
        ),
      )}
    </svg>
  );
});
