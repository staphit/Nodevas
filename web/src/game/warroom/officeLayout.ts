// Where everyone sits in the current project's office. Pure, so the
// arrangement is testable without rendering:
//
//   the work area: a desk per agent at work plus the free desks the
//   runners have room for, at least two in all (an empty office still reads
//   as one); the meeting room: a seat per agent in a meeting, then what lies on
//   the table for the viewer, never fewer than two seats (an empty room still
//   shows its table); the entrance: who waits at the door.

import type { Actor, MeetingItem, ProjectOffice } from "../world";

export type MeetingCell = { actorId: string } | { itemId: string } | { empty: true };

export interface OfficeLayout {
  /** One entry per desk: the actor sitting there, null for a free desk. */
  desks: (string | null)[];
  meetingCells: MeetingCell[];
  entrance: string[];
}

/** Desks an office shows at least, and seats its meeting table has. */
export const MIN_DESKS = 2;
export const MIN_MEETING_SEATS = 2;

export function layoutOffice(
  office: Pick<ProjectOffice, "work" | "freeDesks" | "meeting" | "entrance"> & { items: Pick<MeetingItem, "id">[] },
  actorsById: ReadonlyMap<string, Pick<Actor, "id">>,
): OfficeLayout {
  const present = (id: string) => actorsById.has(id);
  const work = office.work.filter(present);
  const desks: (string | null)[] = [...work];
  // Free desks are the runners' free slots; only an office with fewer than
  // MIN_DESKS in all is padded, so the drawing never shows more empty desks
  // than the header's "free desks" plus that padding.
  const total = Math.max(MIN_DESKS, work.length + Math.max(0, office.freeDesks));
  while (desks.length < total) desks.push(null);
  const meetingCells: MeetingCell[] = [
    ...office.meeting.filter(present).map((actorId) => ({ actorId })),
    ...office.items.map((item) => ({ itemId: item.id })),
  ];
  while (meetingCells.length < MIN_MEETING_SEATS) meetingCells.push({ empty: true });
  return { desks, meetingCells, entrance: office.entrance.filter(present) };
}
