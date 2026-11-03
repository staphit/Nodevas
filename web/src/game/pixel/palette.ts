// The war room's whole palette: a short, fixed list of colours. Each token is
// a CSS custom property, --px-<token>, set per theme in warroom/warroom.css (and dimmed
// for a room with its lights off), so a theme switch repaints nothing in
// JavaScript. An office style (warroom.css .px-style--<id>) redefines the
// furniture, wall and floor tokens only; the characters' and the states'
// colours never change. "agent" and "agent-dark" are the character's agent kind
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
  // Restyled per office style (warroom.css .px-style--<id>).
  "table",
  "table-dark",
  "decor-a",
  "decor-b",
  "decor-c",
  "decor-d",
  "plant",
  "plant-dark",
  "pot",
  "lamp",
  // The building's shared rooms: the lounge and the server room.
  "sofa",
  "sofa-dark",
  "rack",
  "rack-dark",
  "led-on",
  "led-off",
  "paper",
] as const;

export type PixelToken = (typeof PIXEL_TOKENS)[number];

export function fillOf(token: PixelToken): string {
  return `var(--px-${token})`;
}
