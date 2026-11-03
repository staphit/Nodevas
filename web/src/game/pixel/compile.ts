// Pixel sprites as text: one character per art pixel, "." transparent, every
// other character named in a legend that maps it to a palette token. A sprite
// compiles once, at module load, to one SVG path per token, each row's runs
// of the same token merged into one rectangle, so a whole sprite is a handful
// of <path> elements whatever its size.

import type { PixelToken } from "./palette";

export interface SpriteLayer {
  token: PixelToken;
  /** Rectangles in art pixels: "M x y h n v1 h-n z" per run. */
  d: string;
}

export interface Sprite {
  w: number;
  h: number;
  layers: SpriteLayer[];
}

export type Legend = Readonly<Record<string, PixelToken>>;

/** Rows of a sprite grid: blank lines and indentation dropped. */
function rows(grid: string): string[] {
  return grid
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
}

export function compileSprite(grid: string, legend: Legend): Sprite {
  const lines = rows(grid);
  if (lines.length === 0) throw new Error("empty sprite");
  const w = lines[0].length;
  const paths = new Map<PixelToken, string[]>();
  lines.forEach((line, y) => {
    if (line.length !== w) throw new Error(`sprite row ${y} is ${line.length} wide, not ${w}: "${line}"`);
    let x = 0;
    while (x < w) {
      const char = line[x];
      let end = x + 1;
      while (end < w && line[end] === char) end++;
      if (char !== ".") {
        const token = legend[char];
        if (!token) throw new Error(`sprite pixel "${char}" (row ${y}) is not in the legend`);
        const run = end - x;
        let list = paths.get(token);
        if (!list) paths.set(token, (list = []));
        list.push(`M${x} ${y}h${run}v1h-${run}z`);
      }
      x = end;
    }
  });
  return { w, h: lines.length, layers: [...paths].map(([token, d]) => ({ token, d: d.join("") })) };
}
