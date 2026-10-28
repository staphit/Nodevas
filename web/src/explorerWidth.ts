/**
 * The explorer's width range. The width dragged is the whole shell: the tab
 * rail (explorer.css .ex-rail, 52px and its 1px border at 100% text, wider
 * with larger text) and the panel beside it. Below about 260px the panel's rows stop reading: a project name is cut
 * and its current-project badge wraps, and with the indent, file icon and
 * status dot a node title is left a few letters. Larger text needs more of
 * it, so the panel's minimum grows with the text size (300px at 150%).
 */
export const EXPLORER_MIN_W = 260;
export const EXPLORER_MAX_W = 520;
export const EXPLORER_DEFAULT_W = 320;

/**
 * The tab rail's width with its border: its 6px padding each side around a
 * 40px tab that widens with the text size, never below its 100% width.
 */
export function explorerRailWidth(textScale: number): number {
  return Math.round(12 + 40 * Math.max(1, textScale)) + 1;
}
export const EXPLORER_RAIL_W = explorerRailWidth(1);

/** Extra minimum panel width per unit of text scale above 100%. */
const MIN_W_PER_SCALE = 80;

/** The narrowest the shell goes: the panel's minimum beside the rail. */
export function explorerMinWidth(textScale: number): number {
  const extra = Math.max(0, textScale - 1) * MIN_W_PER_SCALE;
  return Math.min(EXPLORER_MAX_W, Math.round(EXPLORER_MIN_W + explorerRailWidth(textScale) + extra));
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
