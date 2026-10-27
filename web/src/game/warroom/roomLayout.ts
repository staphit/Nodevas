// Who sits where in one war-room room. Pure, so the arrangement is testable
// without rendering:
//
//   a desk per slot the runner has (its maxConcurrent), more if more runs
//   are seated than it takes at once; everyone at work, failed, asleep on a
//   usage limit, waiting on CI or idle sits at one, in the order they came;
//   offered and queued runs wait at the door, after the desks.
//
// An idle runner still reads as a room with its free desks.

import type { Actor, Room } from "../world";

export interface RoomLayout {
  /** One entry per desk: the actor sitting there, null for a free desk. */
  desks: (string | null)[];
  /** Actors waiting at the door to be let in. */
  queue: string[];
}

export function layoutRoom(
  room: Pick<Room, "actorIds" | "kind" | "slots" | "online">,
  actorsById: ReadonlyMap<string, Pick<Actor, "pose">>,
): RoomLayout {
  const seated: string[] = [];
  const queue: string[] = [];
  for (const id of room.actorIds) {
    const actor = actorsById.get(id);
    if (!actor) continue;
    if (actor.pose === "awaiting-accept") queue.push(id);
    else seated.push(id);
  }
  // A runner we know has at least one desk; the terminal room and a runner
  // we cannot list have as many as are sitting.
  const known = room.kind === "runner" && room.online !== null;
  const slots = known ? Math.max(1, room.slots ?? 1) : 0;
  const desks: (string | null)[] = [...seated];
  while (desks.length < slots) desks.push(null);
  return { desks, queue };
}
