import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { cardDetailAt } from "./GraphCard";
import {
  CARD_GAP_X,
  CARD_HANDLES,
  CARD_MAX_H,
  CARD_MAX_W,
  CARD_MIN_H,
  CARD_MIN_W,
  CARD_W,
  connectPortTop,
} from "./geometry";

// jsdom applies no CSS, so this reads the contract from the stylesheets. The
// connect dot has to look nothing like the resize squares beside it — a
// beginner dragged the square on the right edge to draw a line and resized
// the card instead — and it has to be there when the card is pointed at or
// selected, and gone while a card is being moved or resized.
const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");
const board = stripComments(readFileSync(resolve(process.cwd(), "src/styles/04-board.css"), "utf8"));
const shapes = stripComments(
  readFileSync(resolve(process.cwd(), "src/styles/08-card-shapes.css"), "utf8"),
);

/** Top-level rules as [selectors, body]; rules inside @media are left out. */
function rules(css: string): Array<[string[], string]> {
  const found: Array<[string[], string]> = [];
  for (const match of css.matchAll(/^([.:#\w][^{}@]*)\{([^}]*)\}/gm)) {
    found.push([match[1].split(",").map((part) => part.trim()), match[2]]);
  }
  return found;
}

/** The rules inside every `@media (pointer: coarse)` block, as top-level rules. */
const coarseOf = (css: string) =>
  [...css.matchAll(/@media \(pointer: coarse\) \{([\s\S]*?\n)\}/g)]
    .map((match) => match[1].replace(/^ {2}/gm, ""))
    .join("\n");

const bodyIn = (css: string, selector: string) =>
  rules(css)
    .filter(([selectors]) => selectors.includes(selector))
    .map(([, body]) => body)
    .join(";");

const bodyOf = (selector: string) => bodyIn(board, selector);

const selectorsWith = (declaration: RegExp) =>
  rules(board)
    .filter(([, body]) => declaration.test(body))
    .flatMap(([selectors]) => selectors);

/** The last value a body gives a property, as written. */
function valueOf(body: string, property: string): string {
  const all = [...body.matchAll(new RegExp(`(?:^|[;\\s{])${property}:\\s*([^;]+)`, "g"))];
  const value = all.at(-1)?.[1].trim();
  if (value === undefined) throw new Error(`no ${property} in ${body}`);
  return value;
}

/**
 * A length in board pixels at a view scale: either a plain `Npx`, or
 * `min(calc(Npx / var(--view-scale, 1)), Cpx)`, the on-screen size held
 * against the zoom up to a cap. Anything else fails the test: the geometry
 * below has to be able to read every size it checks.
 */
function lengthAt(value: string, scale: number): number {
  const plain = /^(\d+(?:\.\d+)?)px$/.exec(value);
  if (plain) return Number(plain[1]);
  const held = /^min\(calc\((\d+(?:\.\d+)?)px \/ var\(--view-scale, 1\)\),\s*(\d+(?:\.\d+)?)px\)$/.exec(value);
  if (held) return Math.min(Number(held[1]) / scale, Number(held[2]));
  throw new Error(`unreadable length: ${value}`);
}

/** How far the box's right side is from the card's right edge (out is +). */
function rightSideOf(translate: string): number {
  const out = /^calc\((\d+)px - 100%\)\s+-50%$/.exec(translate);
  if (out) return Number(out[1]);
  const inward = /^calc\(-100% - (\d+)px\)\s+-50%$/.exec(translate);
  if (inward) return -Number(inward[1]);
  throw new Error(`unreadable translate: ${translate}`);
}

interface Rect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** Shared area, not a shared side: rects that only touch do not overlap. */
const overlaps = (a: Rect, b: Rect) =>
  a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;

const square = (cx: number, cy: number, size: number): Rect => ({
  left: cx - size / 2,
  top: cy - size / 2,
  right: cx + size / 2,
  bottom: cy + size / 2,
});

type Pointer = "fine" | "coarse";

/** What the stylesheets say for one pointer, the later rule winning. */
function sheetFor(pointer: Pointer) {
  const pick = (selector: string) =>
    [bodyOf(selector), pointer === "coarse" ? bodyIn(coarseOf(board), selector) : ""]
      .filter(Boolean)
      .join(";");
  const handle = [
    bodyIn(shapes, ".col-card-handle"),
    pointer === "coarse" ? bodyIn(coarseOf(shapes), ".col-card-handle") : "",
  ]
    .filter(Boolean)
    .join(";");
  return {
    box: pick(".col-card-connect"),
    dot: pick(".col-card-connect::after"),
    minimalBox: pick('.board-inner[data-lod="minimal"] .col-card-connect'),
    minimalDot: pick('.board-inner[data-lod="minimal"] .col-card-connect::after'),
    selectedMeta: pick(".col-card.selected .col-card-meta"),
    handle: lengthAt(valueOf(handle, "width"), 1),
  };
}

/**
 * The connect dot's box and drawn disc on a card of width w and height h at a
 * view scale, in board pixels from the card's top-left corner: the left/top
 * GraphCard gives it, then the sheet's size and translate.
 */
function placed(pointer: Pointer, scale: number, w: number, h: number) {
  const sheet = sheetFor(pointer);
  const minimal = cardDetailAt(scale) === "minimal";
  const box = minimal ? sheet.minimalBox : sheet.box;
  const dot = minimal ? sheet.minimalDot : sheet.dot;
  const read = (body: string, fallback: string, property: string) =>
    lengthAt(valueOf(body || fallback, property), scale);
  const boxW = minimal && /width:/.test(box) ? read(box, "", "width") : read(sheet.box, "", "width");
  const boxH = minimal && /height:/.test(box) ? read(box, "", "height") : read(sheet.box, "", "height");
  const dotW = minimal && /width:/.test(dot) ? read(dot, "", "width") : read(sheet.dot, "", "width");
  const dotH = minimal && /height:/.test(dot) ? read(dot, "", "height") : read(sheet.dot, "", "height");
  const translate = valueOf(minimal && /translate:/.test(box) ? box : sheet.box, "translate");
  const right = w + rightSideOf(translate);
  const cy = connectPortTop(h, minimal);
  const hit: Rect = { left: right - boxW, top: cy - boxH / 2, right, bottom: cy + boxH / 2 };
  // The disc is drawn in the middle of the box.
  const cx = right - boxW / 2;
  const disc = { left: cx - dotW / 2, top: cy - dotH / 2, right: cx + dotW / 2, bottom: cy + dotH / 2 };
  return { hit, disc, dotW, dotH, boxW, minimal, sheet };
}

// Every zoom the toolbar allows, times a node scale, lands somewhere in here:
// both sides of each level-of-detail boundary, and far past the minimal one.
const SCALES = [2, 1.2, 1, 0.8, 0.79, 0.6, 0.59, 0.5, 0.35, 0.2];
const POINTERS: Pointer[] = ["fine", "coarse"];
const HEIGHTS = [CARD_MIN_H, 68, CARD_MAX_H];
const WIDTHS = [CARD_MIN_W, CARD_W];

describe("card connect dot", () => {
  it("is a round dot in the accent colour, where the resize handles are squares", () => {
    const dot = bodyOf(".col-card-connect::after");
    expect(dot).toMatch(/border-radius:\s*50%/);
    expect(dot).toMatch(/background:\s*var\(--accent\)/);
    expect(shapes).toMatch(/\.col-card-handle\s*\{[^}]*border-radius:\s*3px/);
  });

  it("is a box the dot is drawn in, with the box as the place to press", () => {
    const box = bodyOf(".col-card-connect");
    expect(box).toMatch(/cursor:\s*crosshair/);
    expect(box).toMatch(/touch-action:\s*none/);
    for (const pointer of POINTERS) {
      for (const scale of SCALES) {
        const { hit, disc } = placed(pointer, scale, CARD_W, CARD_MIN_H);
        expect(disc.left, `${pointer} ${scale}`).toBeGreaterThanOrEqual(hit.left);
        expect(disc.right, `${pointer} ${scale}`).toBeLessThanOrEqual(hit.right);
        expect(disc.top, `${pointer} ${scale}`).toBeGreaterThanOrEqual(hit.top);
        expect(disc.bottom, `${pointer} ${scale}`).toBeLessThanOrEqual(hit.bottom);
      }
    }
  });

  // Cards on the default grid are 12px apart, and a selected card's left
  // squares reach into the gap from the other side. A dot reaching 10–24px
  // out of its card covered the next card's left edge and its west square:
  // with one card selected, pressing its neighbour started a line from it.
  it("stays within its card and half the gap to the next one, at every zoom", () => {
    for (const pointer of POINTERS) {
      for (const scale of SCALES) {
        for (const w of WIDTHS) {
          for (const h of HEIGHTS) {
            const where = `${pointer} at ${scale}, ${w}×${h}`;
            const { hit, disc } = placed(pointer, scale, w, h);
            for (const rect of [hit, disc]) {
              expect(rect.right, where).toBeLessThanOrEqual(w + CARD_GAP_X / 2);
              expect(rect.left, where).toBeGreaterThanOrEqual(0);
              expect(rect.top, where).toBeGreaterThanOrEqual(0);
              expect(rect.bottom, where).toBeLessThanOrEqual(h);
            }
          }
        }
      }
    }
  });

  it("leaves the next card's squares alone where they are drawn", () => {
    for (const pointer of POINTERS) {
      for (const scale of SCALES) {
        const { hit, disc, minimal, sheet } = placed(pointer, scale, CARD_W, CARD_MIN_H);
        if (minimal) continue;
        // The neighbour's left squares, the same height as this card or not.
        const left = CARD_W + CARD_GAP_X;
        for (const y of [0, CARD_MIN_H / 2, CARD_MIN_H, hit.top, hit.bottom]) {
          const theirs = square(left, y, sheet.handle);
          expect(overlaps(hit, theirs), `${pointer} at ${scale}, y ${y}`).toBe(false);
          expect(overlaps(disc, theirs), `${pointer} at ${scale}, y ${y}`).toBe(false);
        }
      }
    }
  });

  // The squares are drawn on a selected card at every level but the minimal
  // one. The dot covering the east square, or the corner one below it, would
  // put the resize trap the dot is there to remove right under it.
  it("never covers its own card's resize squares, down to the shortest card", () => {
    for (const pointer of POINTERS) {
      for (const scale of SCALES) {
        for (const w of WIDTHS) {
          for (const h of HEIGHTS) {
            const { hit, disc, minimal, sheet } = placed(pointer, scale, w, h);
            if (minimal) continue;
            for (const handle of CARD_HANDLES) {
              const theirs = square((w * (handle.dirX + 1)) / 2, (h * (handle.dirY + 1)) / 2, sheet.handle);
              const where = `${pointer} at ${scale}, ${w}×${h}, ${handle.name}`;
              expect(overlaps(hit, theirs), where).toBe(false);
              expect(overlaps(disc, theirs), where).toBe(false);
            }
          }
        }
      }
    }
  });

  // The marker chip ends where the card's content does, 8px of padding and a
  // 1px border in from the right edge, on the second line (GraphCard.test's
  // width check counts on that line's whole width). That line is not drawn
  // at a reduced level of detail.
  it("leaves the marker chip uncovered", () => {
    const card = bodyOf(".col-card");
    expect(valueOf(card, "padding")).toBe("5px 8px");
    expect(valueOf(card, "border")).toMatch(/^1px solid/);
    const contentRight = CARD_W - 1 - 8;
    for (const pointer of POINTERS) {
      const { hit, disc, boxW, sheet } = placed(pointer, 1, CARD_W, CARD_MIN_H);
      // What the second line gives up on the right while the dot is there.
      const reserve = sheet.selectedMeta ? lengthAt(valueOf(sheet.selectedMeta, "padding-right"), 1) : 0;
      if (pointer === "coarse") expect(reserve).toBeGreaterThanOrEqual(boxW);
      for (const rect of [hit, disc]) {
        expect(rect.left, pointer).toBeGreaterThanOrEqual(contentRight - reserve);
      }
    }
  });

  // Under a finger the dot sits inside the card, over where the marker chip
  // ends, and only a selected card's second line makes room for it. Hover
  // (a pen or a touch screen with a mouse beside it) and a connect-mode drag
  // from a card that was never selected lit the dot on any card, over the
  // chip. So under a coarse pointer a card that is not selected never shows
  // it, whatever else would: the rule doing that has to come after, and be
  // at least as specific as, every rule that shows the dot from its card.
  it("shows under a finger only on a selected card, the one that made room", () => {
    const specificity = (selector: string) =>
      (selector.replace(/:not\(([^)]*)\)/g, "$1").replace(/::[\w-]+/g, "").match(/[.:][\w-]+/g) ?? [])
        .length;
    const coarse = coarseOf(board);
    const hiding = rules(coarse)
      .filter(([, body]) => /opacity:\s*0(?![.\d])/.test(body) && /visibility:\s*hidden/.test(body))
      .flatMap(([selectors]) => selectors);
    // A rule naming the ⋯ only matches a dot with the ⋯ before it, which is
    // the dot the second hiding rule is for.
    for (const [via, tail, pastMenu] of [
      ["direct", " + .col-card-connect", false],
      ["past the ⋯", " + .col-card-more + .col-card-connect", true],
    ] as const) {
      const hide = `.col-card:not(.selected)${tail}`;
      expect(hiding, via).toContain(hide);
      const shows = selectorsWith(/opacity:\s*1/).filter(
        (selector) =>
          selector.endsWith(" + .col-card-connect") &&
          selector.includes(".col-card-more") === pastMenu &&
          !selector.includes(".selected"),
      );
      expect(shows.length, via).toBeGreaterThan(0);
      for (const show of shows) {
        expect(specificity(hide), show).toBeGreaterThanOrEqual(specificity(show));
        expect(board.lastIndexOf(show), show).toBeLessThan(board.indexOf(hide));
      }
    }
    // A dot going away under a finger goes at once: the lingering that lets a
    // mouse cross a shaped card's clipped edge would leave a press target
    // nobody can see inside the card, where the next tap on it would land.
    expect(bodyIn(coarse, ".col-card:not(.selected) + .col-card-connect")).toMatch(
      /transition-delay:\s*0s/,
    );
    // And the room is made on the selected card's second line.
    expect(bodyIn(coarse, ".col-card.selected .col-card-meta")).toMatch(/padding-right:\s*\d+px/);
  });

  // A shaped card is clipped, and below its middle its outline falls short
  // of the right edge, so the pointer crosses a strip over neither the card
  // nor the dot. The dot must stay where it can be pointed at long enough to
  // be reached across that strip; pointer-events: none would end at once.
  it("can be reached from a shaped card whose outline falls short of the edge", () => {
    const hidden = bodyOf(".col-card-connect");
    expect(hidden).not.toMatch(/pointer-events:\s*none/);
    expect(hidden).toMatch(/visibility:\s*hidden/);
    const linger = Number(/visibility 0s linear (\d+)ms/.exec(valueOf(hidden, "transition"))?.[1]);
    expect(linger).toBeGreaterThanOrEqual(250);
    expect(bodyOf(".col-card-connect:hover")).toMatch(/visibility:\s*visible/);
    expect(bodyOf(".col-card-connect:hover")).toMatch(/transition-delay:\s*0s/);

    // Where each outline is on a row, from 08-card-shapes.css.
    const rounded = (r: number) => (w: number, h: number, y: number) => {
      const radius = Math.min(r, w / 2, h / 2);
      const dy = Math.max(0, radius - y, y - (h - radius));
      return w - radius + Math.sqrt(Math.max(0, radius * radius - dy * dy));
    };
    const outlines: Record<string, (w: number, h: number, y: number) => number> = {
      round: rounded(14),
      pill: rounded(999),
      ellipse: (w, h, y) => w / 2 + (w / 2) * Math.sqrt(Math.max(0, 1 - ((y - h / 2) / (h / 2)) ** 2)),
      diamond: (w, h, y) => w - (w / 2) * (Math.abs(y - h / 2) / (h / 2)),
      hexagon: (w, h, y) => w - 0.14 * w * (Math.abs(y - h / 2) / (h / 2)),
    };
    expect(shapes).toMatch(/shape-diamond[^}]*polygon\(50% 0, 100% 50%, 50% 100%, 0 50%\)/);
    expect(shapes).toMatch(/shape-hexagon[^}]*polygon\(14% 0, 86% 0, 100% 50%, 86% 100%, 14% 100%, 0 50%\)/);
    for (const [shape, outline] of Object.entries(outlines)) {
      for (const pointer of POINTERS) {
        for (const w of [CARD_MIN_W, CARD_W, CARD_MAX_W]) {
          for (const h of HEIGHTS) {
            const { hit } = placed(pointer, 1, w, h);
            // The narrowest strip on any row the box spans.
            let strip = Infinity;
            for (let y = hit.top; y <= hit.bottom; y += 0.5) {
              strip = Math.min(strip, Math.max(0, hit.left - outline(w, h, y)));
            }
            // A hand moving at a slow 0.3px per millisecond still gets there.
            expect(strip / linger, `${shape}, ${pointer}, ${w}×${h}`).toBeLessThanOrEqual(0.3);
          }
        }
      }
    }
  });

  it("is placed by the translate property, never a transform", () => {
    for (const body of [
      bodyOf(".col-card-connect"),
      bodyIn(coarseOf(board), ".col-card-connect"),
      bodyOf('.board-inner[data-lod="minimal"] .col-card-connect'),
    ]) {
      expect(body).not.toMatch(/(^|[;\s])transform\s*:/);
      expect(body).toMatch(/(^|[;\s])translate\s*:/);
    }
  });

  // Each sits where the other could not, so neither needs to win a press.
  it("does not rise above the resize frames", () => {
    const zIndex = (body: string) => Number(/z-index:\s*(\d+)/.exec(body)?.[1]);
    const frame = /\.card-resize-frame\s*\{([^}]*)\}/.exec(shapes)?.[1] ?? "";
    expect(zIndex(bodyOf(".col-card-connect"))).toBeLessThan(zIndex(frame));
    expect(board).not.toMatch(/\.col-card\.selected \+ (\.col-card-more \+ )?\.col-card-connect\s*\{/);
  });

  it("shows on hover and on selection, with or without the ⋯ button between", () => {
    const shown = selectorsWith(/opacity:\s*1/);
    for (const selector of [
      ".col-card:hover + .col-card-connect",
      ".col-card:hover + .col-card-more + .col-card-connect",
      ".col-card.selected + .col-card-connect",
      ".col-card.selected + .col-card-more + .col-card-connect",
      ".col-card.connection-source + .col-card-more + .col-card-connect",
      ".col-card-connect:hover",
    ]) {
      expect(shown, selector).toContain(selector);
    }
    // `~` would light the dot of every card drawn after the one pointed at.
    expect(board).not.toMatch(/~\s*\.col-card-connect/);
  });

  it("goes while a card is dragged or resized", () => {
    const hidden = selectorsWith(/display:\s*none/);
    for (const selector of [
      ".board-inner.dragging .col-card-connect",
      ".col-card.resizing + .col-card-connect",
      ".col-card.resizing + .col-card-more + .col-card-connect",
    ]) {
      expect(hidden, selector).toContain(selector);
    }
  });

  // The board shrinks by CSS zoom; a phone's board opens at the minimal
  // level, where the dot, shrunk with the cards, would be a few pixels across.
  it("holds its on-screen size at the minimal level until it fills the card", () => {
    for (const pointer of POINTERS) {
      const atFull = placed(pointer, 1, CARD_W, CARD_MIN_H);
      const out = placed(pointer, 0.5, CARD_W, CARD_MIN_H);
      expect(out.minimal).toBe(true);
      // On screen the dot is its board size times the scale.
      expect(out.dotW * 0.5, pointer).toBeGreaterThanOrEqual(atFull.dotW);
      // Far enough out the cap wins, and the dot shrinks with the card.
      const far = placed(pointer, 0.2, CARD_W, CARD_MIN_H);
      expect(far.hit.bottom - far.hit.top, pointer).toBeLessThanOrEqual(CARD_MIN_H);
    }
  });

  it("is larger under a finger", () => {
    const fine = placed("fine", 1, CARD_W, CARD_MIN_H);
    const coarse = placed("coarse", 1, CARD_W, CARD_MIN_H);
    expect(coarse.boxW).toBeGreaterThanOrEqual(24);
    expect(coarse.boxW).toBeGreaterThan(fine.boxW);
    expect(coarse.dotW).toBeGreaterThan(fine.dotW);
  });
});
