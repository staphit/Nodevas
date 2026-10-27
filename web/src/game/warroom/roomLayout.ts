// Where things stand inside one war-room room, in grid units (a point
// (col, row) on the room's floor, projected with isoProject). Pure, so the
// arrangement is testable without rendering:
//
//   back wall ── door (right wall, near the corner): offered runs queue here
//   desks, three to a row: everyone at work, failed, waiting on CI, idle
//   meeting tables below the desks: a review pipeline's reviewers
//
// A room has at least as many desks as its runner has agents installed, so
// an idle runner still reads as a room with empty desks.

import type { Actor, Meeting, Room } from "../world";

export const ROOM_COLS = 8;
const DESKS_PER_ROW = 3;
const DESK_PITCH_COL = 2.6;
const DESK_PITCH_ROW = 2.6;
const FIRST_DESK = { col: 1.4, row: 2.4 };
const TABLE_DEPTH = 3.6;

export interface Seat {
  actorId: string;
  col: number;
  row: number;
  kind: "desk" | "queue" | "meeting";
}

export interface EmptyDesk {
  /** The installed agent kind the desk is kept for, "" when none. */
  agent: string;
  col: number;
  row: number;
}

export interface TablePlace {
  pipelineId: string;
  col: number;
  row: number;
}

export interface RoomLayout {
  cols: number;
  rows: number;
  seats: Seat[];
  emptyDesks: EmptyDesk[];
  tables: TablePlace[];
  /** The door's span along the right wall (row 0), in cols. */
  door: { from: number; to: number };
}

/** Seats around a table at (col, row): four sides first, then corners. */
const AROUND: readonly [number, number][] = [
  [-1.5, 0],
  [1.5, 0],
  [0, 1.4],
  [0, -1.4],
  [-1.2, -1.2],
  [1.2, 1.2],
  [1.2, -1.2],
  [-1.2, 1.2],
];

export function layoutRoom(
  room: Pick<Room, "actorIds" | "agentKinds">,
  actorsById: ReadonlyMap<string, Pick<Actor, "pose" | "agent">>,
  meetings: Pick<Meeting, "pipelineId" | "actorIds">[],
): RoomLayout {
  const inMeeting = new Set(meetings.flatMap((meeting) => meeting.actorIds));
  const queue: string[] = [];
  const desks: string[] = [];
  for (const id of room.actorIds) {
    const actor = actorsById.get(id);
    if (!actor || inMeeting.has(id)) continue;
    if (actor.pose === "awaiting-accept") queue.push(id);
    else desks.push(id);
  }

  // Empty desks for installed agents nobody of that kind is sitting at.
  const seatedKinds = new Map<string, number>();
  for (const id of desks) {
    const agent = actorsById.get(id)?.agent ?? "";
    seatedKinds.set(agent, (seatedKinds.get(agent) ?? 0) + 1);
  }
  const spare: string[] = [];
  for (const kind of room.agentKinds) {
    const left = seatedKinds.get(kind) ?? 0;
    if (left > 0) seatedKinds.set(kind, left - 1);
    else spare.push(kind);
  }
  if (desks.length + spare.length === 0 && meetings.length === 0 && queue.length === 0) spare.push("");

  const deskAt = (i: number) => ({
    col: FIRST_DESK.col + (i % DESKS_PER_ROW) * DESK_PITCH_COL,
    row: FIRST_DESK.row + Math.floor(i / DESKS_PER_ROW) * DESK_PITCH_ROW,
  });
  const seats: Seat[] = desks.map((actorId, i) => ({ actorId, kind: "desk", ...deskAt(i) }));
  const emptyDesks: EmptyDesk[] = spare.map((agent, i) => ({ agent, ...deskAt(desks.length + i) }));
  const deskRows = Math.ceil((desks.length + spare.length) / DESKS_PER_ROW);

  const cols = ROOM_COLS;
  const door = { from: cols - 2.4, to: cols - 1.2 };
  queue.forEach((actorId, i) => {
    seats.push({ actorId, kind: "queue", col: door.to - 0.5 - i * 0.9, row: 0.8 });
  });

  const tables: TablePlace[] = [];
  // `next` is the first free row below what is placed so far.
  let next = deskRows ? FIRST_DESK.row + (deskRows - 1) * DESK_PITCH_ROW + 1 : 1.6;
  for (const meeting of meetings) {
    const table = { pipelineId: meeting.pipelineId, col: cols / 2, row: next + 2.2 };
    tables.push(table);
    meeting.actorIds.forEach((actorId, i) => {
      const [dc, dr] = AROUND[i % AROUND.length];
      const ring = 1 + Math.floor(i / AROUND.length) * 0.6;
      seats.push({ actorId, kind: "meeting", col: table.col + dc * ring, row: table.row + dr * ring });
    });
    next += TABLE_DEPTH;
  }

  // Deep enough for the front-most thing placed, plus a strip of floor.
  const deepest = Math.max(
    0,
    ...seats.map((seat) => seat.row),
    ...emptyDesks.map((desk) => desk.row),
    ...tables.map((table) => table.row),
  );
  const rows = Math.max(4, Math.ceil(deepest + 1.2));
  return { cols, rows, seats, emptyDesks, tables, door };
}
