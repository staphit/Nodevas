import { describe, expect, it } from "vitest";
import { addLocalDays, CARD_H, CARD_W, type Col } from "../canvas/geometry";
import type { TimelineRowItem } from "../timeline/TimelineGrid";
import type { GraphNode } from "../../types";
import {
  nearestFreeGraphCell,
  openingScroll,
  timelineNodeColumnWidth,
  timelineOpeningScroll,
} from "./boardGeometry";

function card(id: string, index: number, row: number): Col {
  return { node: { id, title: id } as GraphNode, index, row, width: CARD_W, height: CARD_H };
}

// "新增節點" from the toolbar anchors at the middle of the view. Pressing it
// five times without touching the board put five cards on exactly the same
// cell, each hiding the one before: the board looked as if four of them had
// never been made.
describe("nearestFreeGraphCell", () => {
  it("keeps the anchor when nothing is there", () => {
    expect(nearestFreeGraphCell({ col: 4, row: 3 }, [card("a", 0, 0)])).toEqual({ col: 4, row: 3 });
  });

  it("moves off a card that already sits on the anchor, to a neighbouring cell", () => {
    const taken = [card("a", 4, 3)];
    const cell = nearestFreeGraphCell({ col: 4, row: 3 }, taken);
    expect(cell).not.toEqual({ col: 4, row: 3 });
    expect(Math.abs(cell.col - 4) + Math.abs(cell.row - 3)).toBe(1);
  });

  it("gives five cards made at one anchor five different places", () => {
    const cols: Col[] = [];
    for (let i = 0; i < 5; i++) {
      const cell = nearestFreeGraphCell({ col: 4, row: 3 }, cols);
      cols.push(card(`n${i}`, cell.col, cell.row));
    }
    const keys = new Set(cols.map((c) => `${c.index},${c.row}`));
    expect(keys.size).toBe(5);
  });

  it("treats a card nudged half a row off the anchor as still in the way", () => {
    const cell = nearestFreeGraphCell({ col: 4, row: 3 }, [card("a", 4, 3.4)]);
    expect(cell).not.toEqual({ col: 4, row: 3 });
  });

  it("never leaves the board", () => {
    const cell = nearestFreeGraphCell({ col: 0, row: 0 }, [card("a", 0, 0)]);
    expect(cell.col).toBeGreaterThanOrEqual(0);
    expect(cell.row).toBeGreaterThanOrEqual(0);
  });
});

// The timeline's range starts two weeks before today (more with older
// records), so a board left at scroll 0 opened on a fortnight of empty rows,
// with today's plan some 1,900px further down.
describe("timelineOpeningScroll", () => {
  const start = new Date(2026, 9, 15);
  const days = Array.from({ length: 21 }, (_, i) => addLocalDays(start, i));
  const dayItems = (): TimelineRowItem[] =>
    days.map((date, dayIndex) => ({ kind: "day", date, dayIndex }));
  const base = {
    todayKey: "2026-10-28",
    daySize: 100,
    gapSize: 20,
    viewport: { width: 1000, height: 500 },
    zoom: 1,
  };

  it("opens a vertical timeline on today", () => {
    const scroll = timelineOpeningScroll({
      ...base,
      days,
      rowItems: dayItems(),
      contentDays: new Set(["2026-10-28", "2026-10-30"]),
      orientation: "vertical",
    });
    // 10/28 is the 14th day of the range.
    expect(scroll).toEqual({ left: 0, top: 1300 });
  });

  it("opens a horizontal timeline on today, scaled by the zoom", () => {
    const scroll = timelineOpeningScroll({
      ...base,
      zoom: 0.5,
      days,
      rowItems: dayItems(),
      contentDays: new Set(),
      orientation: "horizontal",
    });
    expect(scroll).toEqual({ left: 650, top: 0 });
  });

  it("opens on the first planned day when nothing planned is in view from today", () => {
    const scroll = timelineOpeningScroll({
      ...base,
      days,
      rowItems: dayItems(),
      contentDays: new Set(["2026-10-16", "2026-11-03"]),
      orientation: "vertical",
    });
    expect(scroll.top).toBe(1900);
  });

  it("opens on the latest record when everything is behind today", () => {
    const scroll = timelineOpeningScroll({
      ...base,
      days,
      rowItems: dayItems(),
      contentDays: new Set(["2026-10-16", "2026-10-20"]),
      orientation: "vertical",
    });
    expect(scroll.top).toBe(500);
  });

  it("counts collapsed runs of empty days at the gap's size", () => {
    // 10/15 and 10/27 kept, 10/16–10/26 folded into one gap, today kept.
    const rowItems: TimelineRowItem[] = [
      { kind: "day", date: days[0], dayIndex: 0 },
      { kind: "gap", count: 11 },
      { kind: "day", date: days[12], dayIndex: 12 },
      { kind: "day", date: days[13], dayIndex: 13 },
      { kind: "gap", count: 7 },
    ];
    const scroll = timelineOpeningScroll({
      ...base,
      days,
      rowItems,
      contentDays: new Set(["2026-10-15", "2026-10-27", "2026-10-28"]),
      orientation: "vertical",
    });
    expect(scroll.top).toBe(100 + 20 + 100);
  });

  // Daily work schedule at 150% text, explorer 520: the timeline opened on
  // 10/29's row, but today's plans were in lanes behind the date ruler and
  // the lanes in view were empty. The lane axis opens on them too.
  describe("along the lanes", () => {
    // Six lanes of 300px; the date ruler (or header) takes 56px of the view.
    const lanes = (planned: Record<number, string[]>) =>
      Array.from({ length: 6 }, (_, index) => new Set(planned[index] ?? []));
    const laneBase = { ...base, laneSize: 300, laneInset: 56, viewport: { width: 1000, height: 700 } };

    it("brings today's lanes into view when they lie past it", () => {
      const scroll = timelineOpeningScroll({
        ...laneBase,
        days,
        rowItems: dayItems(),
        contentDays: new Set(["2026-10-28"]),
        lanes: lanes({ 3: ["2026-10-28"], 4: ["2026-10-28"], 0: ["2026-10-20"] }),
        orientation: "vertical",
      });
      expect(scroll.top).toBe(1300);
      // Lanes 3 and 4 (900-1500) in a 944px span.
      expect(scroll.left).toBeLessThanOrEqual(900);
      expect(scroll.left + 1000 - 56).toBeGreaterThanOrEqual(1500);
    });

    it("leaves lanes already in view where they are", () => {
      const scroll = timelineOpeningScroll({
        ...laneBase,
        days,
        rowItems: dayItems(),
        contentDays: new Set(["2026-10-28"]),
        lanes: lanes({ 1: ["2026-10-28"] }),
        orientation: "vertical",
      });
      expect(scroll.left).toBe(0);
    });

    it("starts at the first of today's lanes when they do not all fit", () => {
      const scroll = timelineOpeningScroll({
        ...laneBase,
        zoom: 0.5,
        days,
        rowItems: dayItems(),
        contentDays: new Set(["2026-10-28"]),
        lanes: lanes({ 2: ["2026-10-28"], 5: ["2026-10-28"] }),
        viewport: { width: 1000, height: 300 },
        orientation: "horizontal",
      });
      // Horizontal: lanes run down the page; 600px of lanes at 50% is 300px.
      expect(scroll.top).toBe(300);
      expect(scroll.left).toBe(650);
    });
  });
});

// A template board with no cards, only its four quadrant groups: 1,260px
// wide, which fits a 1,280px view, but not with 72px of room on each side, so
// the opening started at the left edge and cut the right-hand quadrants.
describe("openingScroll", () => {
  const rect = (left: number, top: number, width: number, height: number) => ({
    left,
    top,
    right: left + width,
    bottom: top + height,
  });

  it("centres content that fits the view, however little room it leaves", () => {
    const quadrants = [
      rect(1697, 933, 620, 380),
      rect(2337, 933, 620, 380),
      rect(1697, 1333, 620, 380),
      rect(2337, 1333, 620, 380),
    ];
    const scroll = openingScroll(quadrants, 1, { width: 1280, height: 700 });
    expect(scroll.left).toBeLessThanOrEqual(1697);
    expect(scroll.left + 1280).toBeGreaterThanOrEqual(2957);
  });

  // The minimap floats over the view's bottom-right corner. Daily work
  // schedule in a 773x665 pane (explorer 480) opened centred with 'Weekly
  // review' under it, though the cards left room to sit clear of it.
  describe("with the minimap's corner to keep clear", () => {
    const viewport = { width: 773, height: 665 };
    const corner = { width: 126, height: 145 };
    // The six cards' layout at 100%, in board pixels.
    const cards = [
      rect(2000, 1000, 152, 68),
      rect(2103, 1225, 152, 68),
      rect(2255, 1093, 152, 68),
      rect(2273, 1394, 152, 68),
      rect(2514, 1231, 152, 68),
      rect(2578, 1450, 152, 68),
    ];
    const onScreen = (scroll: { left: number; top: number }, zoom = 1) =>
      cards.map((card) => ({
        left: card.left * zoom - scroll.left,
        right: card.right * zoom - scroll.left,
        top: card.top * zoom - scroll.top,
        bottom: card.bottom * zoom - scroll.top,
      }));

    it("moves content that fits off the corner, keeping all of it in view", () => {
      const scroll = openingScroll(cards, 1, viewport, corner);
      for (const card of onScreen(scroll)) {
        expect(card.left).toBeGreaterThanOrEqual(0);
        expect(card.top).toBeGreaterThanOrEqual(0);
        expect(card.right).toBeLessThanOrEqual(viewport.width);
        expect(card.bottom).toBeLessThanOrEqual(viewport.height);
        const underCorner =
          card.right > viewport.width - corner.width &&
          card.bottom > viewport.height - corner.height;
        expect(underCorner).toBe(false);
      }
    });

    it("stays centred when the content is clear of the corner already", () => {
      const clear = openingScroll(cards, 1, viewport, { width: 20, height: 20 });
      expect(clear).toEqual(openingScroll(cards, 1, viewport));
    });

    // 150% text, explorer 520: the cards fit the view with 3px to spare
    // across and nothing places them all clear of the map, but sliding them
    // up leaves only the foot of 'Weekly review' under it, not its title.
    it("covers as little of the content as it can when no room keeps it clear", () => {
      const tight = { width: 733, height: 647 };
      const map = { width: 120, height: 141 };
      const coveredAt = (scroll: { left: number; top: number }) =>
        onScreen(scroll)
          .map(
            (card) =>
              Math.max(0, card.right - Math.max(card.left, tight.width - map.width)) *
              Math.max(0, card.bottom - Math.max(card.top, tight.height - map.height)),
          )
          .reduce((sum, area) => sum + area, 0);
      const plain = openingScroll(cards, 1, tight);
      const scroll = openingScroll(cards, 1, tight, map);
      expect(coveredAt(scroll)).toBeLessThan(coveredAt(plain) / 4);
      for (const card of onScreen(scroll)) {
        expect(card.top).toBeGreaterThanOrEqual(0);
        expect(card.right).toBeLessThanOrEqual(tight.width);
      }
    });
  });
});

describe("timelineNodeColumnWidth", () => {
  it("is a date cell wide with no drawer over the board", () => {
    expect(timelineNodeColumnWidth({ cellWidth: 320, viewportWidth: 930, cover: 0, zoom: 1 })).toBe(320);
  });

  it("is a third of what an open drawer leaves, in board pixels", () => {
    expect(timelineNodeColumnWidth({ cellWidth: 320, viewportWidth: 930, cover: 570, zoom: 1 })).toBe(120);
    expect(timelineNodeColumnWidth({ cellWidth: 320, viewportWidth: 930, cover: 570, zoom: 0.75 })).toBe(160);
  });

  it("never narrows under 112px nor widens past a cell", () => {
    expect(timelineNodeColumnWidth({ cellWidth: 320, viewportWidth: 930, cover: 900, zoom: 1 })).toBe(112);
    expect(timelineNodeColumnWidth({ cellWidth: 200, viewportWidth: 1900, cover: 580, zoom: 1 })).toBe(200);
  });
});
