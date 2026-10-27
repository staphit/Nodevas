// Hand-drawn isometric office art, inline SVG. Colours are CSS custom
// properties (25-game.css sets them per theme and per agent kind), so the
// same drawing works in light and dark and nothing is fetched. Every part a
// pose changes carries a class the stylesheet animates or hides.

import { isoProject, type Tile } from "../layout";

export const OFFICE_TILE: Tile = { w: 64, h: 32 };
/** Height of the back walls, in px. */
export const WALL_H = 70;
/** Space above the walls for heads, bubbles and raised hands. */
export const HEADROOM = 18;

export interface RoomFrame {
  cols: number;
  rows: number;
  width: number;
  height: number;
  /** Screen offset of grid point (0, 0). */
  ox: number;
  oy: number;
}

export function roomFrame(cols: number, rows: number, tile: Tile = OFFICE_TILE): RoomFrame {
  return {
    cols,
    rows,
    width: ((cols + rows) * tile.w) / 2,
    height: ((cols + rows) * tile.h) / 2 + WALL_H + HEADROOM,
    ox: (rows * tile.w) / 2,
    oy: WALL_H + HEADROOM,
  };
}

/** A grid point's screen position inside the room. */
export function at(frame: RoomFrame, col: number, row: number, tile: Tile = OFFICE_TILE) {
  const p = isoProject(col, row, tile);
  return { x: frame.ox + p.x, y: frame.oy + p.y };
}

function pts(points: { x: number; y: number }[]): string {
  return points.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
}

/** Floor, back walls, window and door of one room. */
export function RoomShell({
  frame,
  door,
  terminal,
  doorOpen,
}: {
  frame: RoomFrame;
  door: { from: number; to: number };
  terminal: boolean;
  doorOpen: boolean;
}) {
  const { cols, rows } = frame;
  const p = (c: number, r: number) => at(frame, c, r);
  const up = (q: { x: number; y: number }, h: number) => ({ x: q.x, y: q.y - h });
  const top = p(0, 0);
  const right = p(cols, 0);
  const bottom = p(cols, rows);
  const left = p(0, rows);

  const gridLines: string[] = [];
  for (let c = 1; c < cols; c++) {
    const a = p(c, 0);
    const b = p(c, rows);
    gridLines.push(`M${a.x},${a.y}L${b.x},${b.y}`);
  }
  for (let r = 1; r < rows; r++) {
    const a = p(0, r);
    const b = p(cols, r);
    gridLines.push(`M${a.x},${a.y}L${b.x},${b.y}`);
  }

  // A window on the left wall, a door on the right one.
  const wl = 0.8;
  const wr = Math.min(rows - 0.6, 2.6);
  const win = [up(p(0, wl), 26), up(p(0, wr), 26), up(p(0, wr), 54), up(p(0, wl), 54)];
  const doorPoly = [p(door.from, 0), p(door.to, 0), up(p(door.to, 0), 50), up(p(door.from, 0), 50)];

  return (
    <svg
      className={`game-room-shell${terminal ? " terminal" : ""}`}
      width={frame.width}
      height={frame.height}
      viewBox={`0 0 ${frame.width} ${frame.height}`}
      aria-hidden="true"
      focusable="false"
    >
      <polygon className="floor" points={pts([top, right, bottom, left])} />
      <path className="floor-grid" d={gridLines.join("")} />
      <polygon className="wall-left" points={pts([left, top, up(top, WALL_H), up(left, WALL_H)])} />
      <polygon className="wall-right" points={pts([top, right, up(right, WALL_H), up(top, WALL_H)])} />
      <polyline className="wall-cap" points={pts([up(left, WALL_H), up(top, WALL_H), up(right, WALL_H)])} />
      <polygon className="window" points={pts(win)} />
      <line
        className="window-bar"
        x1={(win[0].x + win[1].x) / 2}
        y1={(win[0].y + win[1].y) / 2}
        x2={(win[2].x + win[3].x) / 2}
        y2={(win[2].y + win[3].y) / 2}
      />
      <polygon className={`door${doorOpen ? " open" : ""}`} points={pts(doorPoly)} />
      {terminal && (
        // A terminal prompt on the wall: this room is people's own machines.
        <text className="wall-sign" x={(top.x + right.x) / 2 - 18} y={top.y - WALL_H / 2 + 10}>
          {">_"}
        </text>
      )}
    </svg>
  );
}

/** A desk with a monitor; the character sits behind it. viewBox 80×96,
 * anchored at (40, 80) on the floor. */
export function DeskSvg({ children, empty }: { children?: React.ReactNode; empty?: boolean }) {
  return (
    <svg
      className={`game-desk${empty ? " empty" : ""}`}
      width="80"
      height="96"
      viewBox="0 0 80 96"
      aria-hidden="true"
      focusable="false"
    >
      <ellipse className="shadow" cx="40" cy="84" rx="30" ry="9" />
      {/* Chair back, behind the character. */}
      <path className="chair" d="M28 52 L28 40 Q40 34 52 40 L52 52 Z" />
      {children}
      {/* Desk: top face, left and right faces. */}
      <polygon className="desk-left" points="14,66 40,79 40,92 14,79" />
      <polygon className="desk-right" points="40,79 66,66 66,79 40,92" />
      <polygon className="desk-top" points="14,66 40,53 66,66 40,79" />
      {/* Keyboard. */}
      <polygon className="keyboard" points="26,66 36,61 44,65 34,70" />
      {/* Monitor, its screen turned to the room. */}
      <polygon className="monitor-back" points="45,64 61,56 61,40 45,48" />
      <polygon className="screen" points="44,63 60,55 60,40 44,48" />
      <g className="screen-lines">
        <line x1="47" y1="50" x2="56" y2="45.5" />
        <line x1="47" y1="54" x2="53" y2="51" />
        <line x1="47" y1="58" x2="57" y2="53" />
      </g>
      <line className="monitor-stand" x1="52" y1="59" x2="52" y2="64" />
      {/* Coffee for a character waiting on CI. */}
      <g className="coffee">
        <rect x="18" y="58" width="6" height="7" rx="1" />
        <path d="M20 56 q1 -2 0 -4 M22 56 q1 -2 0 -4" className="steam" />
      </g>
    </svg>
  );
}

/** The seated character, drawn inside DeskSvg (same 80×96 frame). */
export function SeatedFigure() {
  return (
    <g className="figure">
      <path className="torso" d="M30 64 L30 42 Q40 34 50 42 L50 64 Z" />
      <circle className="head" cx="40" cy="26" r="8.5" />
      <path className="hair" d="M31.6 24 Q33 15.5 40 15.5 Q47 15.5 48.4 24 Q44 20 40 20.5 Q36 20 31.6 24 Z" />
      <path className="arm arm-l" d="M31 42 L27 56 L32 62" />
      <path className="arm arm-r" d="M49 42 L49 54 L42 62" />
      <path className="arm arm-up" d="M49 42 L56 30 L58 16" />
      <circle className="hand-up" cx="58" cy="14" r="3" />
    </g>
  );
}

/** A standing character: in the door queue or at a meeting table.
 * viewBox 40×64, anchored at (20, 60). */
export function StandingFigure({ clipboard }: { clipboard?: boolean }) {
  return (
    <svg className="game-standing" width="40" height="64" viewBox="0 0 40 64" aria-hidden="true" focusable="false">
      <ellipse className="shadow" cx="20" cy="60" rx="11" ry="4" />
      <g className="figure">
        <rect className="leg" x="14" y="44" width="5" height="15" rx="2" />
        <rect className="leg" x="21" y="44" width="5" height="15" rx="2" />
        <path className="torso" d="M11 46 L11 28 Q20 21 29 28 L29 46 Z" />
        <circle className="head" cx="20" cy="14" r="7.5" />
        <path className="hair" d="M12.6 12 Q14 5 20 5 Q26 5 27.4 12 Q24 8.6 20 9 Q16 8.6 12.6 12 Z" />
        {clipboard ? (
          <>
            <path className="arm" d="M12 29 L10 38 L16 40" />
            <rect className="clipboard" x="14" y="33" width="10" height="12" rx="1.5" />
            <path className="arm" d="M28 29 L29 38 L24 40" />
          </>
        ) : (
          <>
            <path className="arm" d="M12 29 L10 43" />
            <path className="arm" d="M28 29 L30 43" />
          </>
        )}
      </g>
    </svg>
  );
}

/** A round meeting table, viewBox 96×56 anchored at (48, 30). */
export function TableSvg() {
  return (
    <svg className="game-table" width="96" height="56" viewBox="0 0 96 56" aria-hidden="true" focusable="false">
      <ellipse className="shadow" cx="48" cy="40" rx="40" ry="14" />
      <rect className="table-leg" x="44" y="28" width="8" height="16" />
      <ellipse className="table-side" cx="48" cy="28" rx="38" ry="15" />
      <ellipse className="table-top" cx="48" cy="24" rx="38" ry="15" />
      <rect className="table-paper" x="36" y="18" width="12" height="8" rx="1" transform="rotate(-12 42 22)" />
      <rect className="table-paper" x="50" y="21" width="12" height="8" rx="1" transform="rotate(8 56 25)" />
    </svg>
  );
}
