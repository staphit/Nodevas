// The boss map's art: Kenney's isometric dungeon sprites (CC0, vendored in
// web/public/game/kenney-isometric-dungeon/, see CREDITS.md) for floors,
// gates, chests, columns and the party leader, and hand-drawn inline SVG for
// what the pack has none of: the monsters and the boss.

import type { CSSProperties } from "react";

import type { Tile } from "../layout";
import { usePackSprite } from "../packs";

/** The map's tile, matching the Kenney floors (160×79 faces at 2×). */
export const DUNGEON_TILE: Tile = { w: 80, h: 40 };

// Vite copies public/ to the root of dist/, which the binary serves at /.
const BASE = "/game/kenney-isometric-dungeon/";

export interface Sprite {
  file: string;
  /** Display size, px (half the file's: the files are 2×). */
  w: number;
  h: number;
  /** Where the sprite's tile's top corner sits inside it, px. */
  dx: number;
  dy: number;
  frames?: number;
}

export const KENNEY = {
  floorStone: { file: "floor-stone.png", w: 80, h: 46.5, dx: 40, dy: 0 },
  floorUneven: { file: "floor-uneven.png", w: 80, h: 47.5, dx: 40, dy: 1.3 },
  floorDirt: { file: "floor-dirt.png", w: 80, h: 43.5, dx: 40, dy: -2.8 },
  chestOpen: { file: "chest-open.png", w: 27, h: 29.5, dx: 14.4, dy: -1.9 },
  chestClosed: { file: "chest-closed.png", w: 25, h: 21.5, dx: 14.4, dy: -10.3 },
  gateClosed: { file: "gate-closed.png", w: 50.5, h: 74, dx: 10.3, dy: 42.8 },
  gateOpen: { file: "gate-open.png", w: 50.5, h: 74, dx: 10.3, dy: 42.8 },
  column: { file: "column.png", w: 20, h: 59, dx: 10, dy: 27.5 },
  barrel: { file: "barrel.png", w: 25, h: 26, dx: 15.3, dy: -4.4 },
  heroIdle: { file: "hero-idle.png", w: 14.5, h: 42, dx: 7.2, dy: 11.9 },
  heroRun: { file: "hero-run.png", w: 29, h: 40, dx: 15.9, dy: 10, frames: 10 },
} satisfies Record<string, Sprite>;

export function spriteUrl(sprite: Sprite): string {
  return BASE + sprite.file;
}

/** The view pack key (docs/view-packs.md) a pack may replace each sprite
 * with. The floors under monsters share the arena's tile. */
const PACK_KEY = new Map<Sprite, string>([
  [KENNEY.floorDirt, "tile.corridor"],
  [KENNEY.floorStone, "tile.arena"],
  [KENNEY.floorUneven, "tile.arena"],
  [KENNEY.chestOpen, "chest.open"],
  [KENNEY.chestClosed, "chest.closed"],
  [KENNEY.gateClosed, "gate.closed"],
  [KENNEY.gateOpen, "gate.open"],
  [KENNEY.column, "column"],
  [KENNEY.barrel, "barrel"],
]);

/** A Kenney sprite whose tile's top corner lands on (x, y). Decorative. */
export function KenneyImg({
  sprite,
  x,
  y,
  className = "",
  style,
}: {
  sprite: Sprite;
  x: number;
  y: number;
  className?: string;
  style?: CSSProperties;
}) {
  const key = PACK_KEY.get(sprite);
  const packed = usePackSprite(...(key ? [key] : []));
  return (
    <img
      className={`game-sprite ${className}${packed ? " pack-sprite" : ""}`}
      src={packed ?? spriteUrl(sprite)}
      alt=""
      aria-hidden="true"
      draggable={false}
      loading="lazy"
      width={sprite.w}
      height={sprite.h}
      style={{ left: x - sprite.dx, top: y - sprite.dy, width: sprite.w, height: sprite.h, ...style }}
    />
  );
}

/** The party leader: Kenney's character, running while the autopilot runs
 * (a 10-frame strip stepped by CSS), standing otherwise. */
export function Hero({ running }: { running: boolean }) {
  const sprite: Sprite = running ? KENNEY.heroRun : KENNEY.heroIdle;
  const packed = usePackSprite("hero");
  if (packed) {
    // A pack's hero is one picture: it does not run, it stands.
    return (
      <span
        className="game-hero pack-sprite"
        aria-hidden="true"
        style={{ width: KENNEY.heroRun.w, height: KENNEY.heroRun.h, backgroundImage: `url("${packed}")`, backgroundSize: "contain" }}
      />
    );
  }
  return (
    <span
      className={`game-hero${running ? " running" : ""}`}
      aria-hidden="true"
      style={{
        width: sprite.w,
        height: sprite.h,
        backgroundImage: `url("${spriteUrl(sprite)}")`,
        backgroundSize: running ? `${sprite.w * (sprite.frames ?? 1)}px ${sprite.h}px` : "contain",
      }}
    />
  );
}

/** A monster: a slime that grows horns and a crown with the node's
 * priority. viewBox 48×48, standing on (24, 44). */
export function MonsterSvg({ priority }: { priority: string }) {
  const rank = priority === "urgent" ? 3 : priority === "high" ? 2 : priority === "medium" ? 1 : 0;
  return (
    <svg className={`game-monster rank-${rank}`} width="48" height="48" viewBox="0 0 48 48" aria-hidden="true" focusable="false">
      <ellipse className="shadow" cx="24" cy="44" rx="15" ry="4" />
      <g className="creature">
        <path className="body" d="M8 42 Q6 26 16 18 Q24 12 32 18 Q42 26 40 42 Q24 46 8 42 Z" />
        <path className="belly" d="M14 41 Q14 32 24 30 Q34 32 34 41 Q24 43 14 41 Z" />
        {rank >= 2 && (
          <>
            <path className="horn" d="M15 21 L10 10 L19 18 Z" />
            <path className="horn" d="M33 21 L38 10 L29 18 Z" />
          </>
        )}
        {rank >= 3 && <path className="crown" d="M17 15 L19 7 L22 12 L24 5 L26 12 L29 7 L31 15 Z" />}
        <ellipse className="eye" cx="19" cy="27" rx="3" ry="3.6" />
        <ellipse className="eye" cx="29" cy="27" rx="3" ry="3.6" />
        <circle className="pupil" cx="19.8" cy="27.6" r="1.5" />
        <circle className="pupil" cx="29.8" cy="27.6" r="1.5" />
        <path className="mouth" d={rank >= 1 ? "M19 35 L21 33 L23 35 L25 33 L27 35 L29 33" : "M20 34 Q24 37 28 34"} />
        {/* Shown while locked. */}
        <g className="chains">
          <path d="M6 30 L42 38" />
          <path d="M6 38 L42 28" />
        </g>
      </g>
    </svg>
  );
}

/** The boss: a horned, crowned demon whose look follows its HP. viewBox
 * 140×130, standing on (70, 122). */
export function BossSvg({ defeated }: { defeated: boolean }) {
  return (
    <svg className={`game-boss-art${defeated ? " defeated" : ""}`} width="140" height="130" viewBox="0 0 140 130" aria-hidden="true" focusable="false">
      <ellipse className="shadow" cx="70" cy="122" rx="52" ry="11" />
      <g className="creature">
        <path className="wing" d="M30 70 Q2 40 8 18 Q22 36 40 44 Z" />
        <path className="wing" d="M110 70 Q138 40 132 18 Q118 36 100 44 Z" />
        <path className="body" d="M26 120 Q18 80 36 56 Q52 36 70 36 Q88 36 104 56 Q122 80 114 120 Q70 128 26 120 Z" />
        <path className="belly" d="M44 118 Q42 90 70 82 Q98 90 96 118 Q70 123 44 118 Z" />
        <path className="horn" d="M44 48 L26 14 L56 40 Z" />
        <path className="horn" d="M96 48 L114 14 L84 40 Z" />
        <path className="crown" d="M50 40 L54 22 L62 32 L70 16 L78 32 L86 22 L90 40 Z" />
        <g className="eyes">
          <path className="eye" d="M48 62 L64 66 L60 74 L48 70 Z" />
          <path className="eye" d="M92 62 L76 66 L80 74 L92 70 Z" />
        </g>
        <g className="eyes-defeated">
          <path d="M50 62 L60 72 M60 62 L50 72" />
          <path d="M80 62 L90 72 M90 62 L80 72" />
        </g>
        <path className="mouth" d="M52 92 L58 86 L64 92 L70 86 L76 92 L82 86 L88 92" />
      </g>
    </svg>
  );
}
