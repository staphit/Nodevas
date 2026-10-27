// The war room's whole palette: a short, fixed list of colours. Each token is
// a CSS custom property, --px-<token>, set per theme in warroom/warroom.css (and dimmed
// for a room with its lights off), so a theme switch repaints nothing in
// JavaScript. "agent" and "agent-dark" are the character's agent kind
// colour, set by an agent--<kind> class.

export const PIXEL_TOKENS = [
  "outline",
  "skin",
  "hair",
  "eye",
  "agent",
  "agent-dark",
  "pants",
  "desk",
  "desk-dark",
  "monitor",
  "screen-off",
  "screen-on",
  "code",
  "code-alt",
  "fail",
  "ok",
  "amber",
  "chair",
  "chair-dark",
  "bubble",
  "bubble-ink",
  "ink",
  "glare",
  "wall-trim",
  "window",
  "window-star",
  "door",
  "doorway",
] as const;

export type PixelToken = (typeof PIXEL_TOKENS)[number];

export function fillOf(token: PixelToken): string {
  return `var(--px-${token})`;
}
