/**
 * The alignment lines drawn while something is dragged or resized [B-06].
 *
 * Plain absolutely-positioned divs rather than part of the `board-edges` SVG:
 * that layer sits under the cards, and the board is scaled with CSS `zoom`,
 * which SVG's own coordinate mapping does not account for (see `useMarquee`).
 * A div goes through normal layout, so the only correction needed is keeping
 * the line a hairline as the board grows.
 */

import type { SnapGuide } from "./snapping";

export function SnapGuides({
  guides,
  offsetX,
  offsetY,
  zoom,
}: {
  guides: SnapGuide[];
  offsetX: number;
  offsetY: number;
  zoom: number;
}) {
  if (guides.length === 0) return null;
  const thickness = 1 / Math.max(zoom, 0.01);
  return (
    <>
      {guides.map((guide) =>
        guide.axis === "x" ? (
          <div
            key={`x-${guide.position}`}
            className="snap-guide vertical"
            aria-hidden
            style={{
              left: offsetX + guide.position - thickness / 2,
              top: offsetY + guide.from,
              width: thickness,
              height: guide.to - guide.from,
            }}
          />
        ) : (
          <div
            key={`y-${guide.position}`}
            className="snap-guide horizontal"
            aria-hidden
            style={{
              left: offsetX + guide.from,
              top: offsetY + guide.position - thickness / 2,
              width: guide.to - guide.from,
              height: thickness,
            }}
          />
        ),
      )}
    </>
  );
}
