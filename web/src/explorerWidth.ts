/**
 * The explorer's width range. Below about 260px its rows stop reading: the
 * heading wraps, and with the indent, file icon and status dot a node title
 * is left a few letters. Larger text needs more of it, so the minimum grows
 * with the text size (300px at 150%).
 */
export const EXPLORER_MIN_W = 260;
export const EXPLORER_MAX_W = 520;
export const EXPLORER_DEFAULT_W = 304;

/** Extra minimum width per unit of text scale above 100%. */
const MIN_W_PER_SCALE = 80;

export function explorerMinWidth(textScale: number): number {
  const extra = Math.max(0, textScale - 1) * MIN_W_PER_SCALE;
  return Math.min(EXPLORER_MAX_W, Math.round(EXPLORER_MIN_W + extra));
}

/** The width to draw: the stored one, widened when the text size needs it. */
export function explorerWidthFor(width: number, textScale: number): number {
  return Math.max(width, explorerMinWidth(textScale));
}

/**
 * The explorer shell's inline size. Beside the board it is the dragged width;
 * on a phone-width screen it is an overlay sized by the stylesheet, and an
 * inline width would beat that and push the panel past the screen's edge.
 */
export function explorerShellStyle({
  width,
  collapsed,
  narrow,
}: {
  width: number;
  collapsed: boolean;
  narrow: boolean;
}): { width: number; flexBasis: number } | undefined {
  if (collapsed || narrow) return undefined;
  return { width, flexBasis: width };
}
