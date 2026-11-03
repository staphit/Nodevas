// Where in the building a character is. One ordered decision, the first rule
// that matches wins:
//
//   asked the viewer something      → the meeting room (a hand up at the table)
//   a plan (or replan) run going    → the meeting room (planning is a meeting)
//   asleep on a usage limit         → the lounge (zzz on the sofa)
//   offered, not yet accepted       → the entrance (waiting at the door)
//   a session with nothing to do    → the lounge
//   anything else                   → the work area, at a desk
//
// A run whose approved merge is pending is "awaiting-accept" too, but it is
// not at the door: it stays at its desk, its screen on the CI hourglass.

import { runFinal, type Run } from "../api/runs";
import type { Actor, Area } from "./world";

export function areaOf(
  actor: Pick<Actor, "pose" | "kind" | "nodeId">,
  run?: Pick<Run, "kind" | "status"> | null,
): Area {
  if (actor.pose === "asking-user") return "meeting";
  if (run?.kind === "plan" && !runFinal(run.status)) return "meeting";
  if (actor.pose === "limit") return "lounge";
  if (actor.pose === "awaiting-accept" && (run?.status === "queued" || run?.status === "offered")) return "entrance";
  if (actor.kind === "session" && (actor.pose === "idle" || actor.pose === "offline") && !actor.nodeId) return "lounge";
  return "work";
}
