import type { Actor, AgentAsk } from "../api";
import { canManageSession } from "./agentStore";

/** The note on a permission prompt the hub allowed without asking, on an
 * earlier "allow for this run" (agents.AllowedForRunNote). */
export const ALLOWED_FOR_RUN_NOTE = "allowed for this run";

/** How long a settled ask stays in Agent chat after it was decided. */
export const SETTLED_ASK_WINDOW_MS = 10 * 60 * 1000;

/** At most this many settled asks are shown under the pending ones. */
export const MAX_SETTLED_ASKS = 5;

/** A permission prompt the hub allowed on its own, which nobody answered. */
export function isAutoAllowedAsk(ask: Pick<AgentAsk, "kind" | "note">): boolean {
  return ask.kind === "permission" && ask.note === ALLOWED_FOR_RUN_NOTE;
}

/**
 * The asks Agent chat shows: every pending one, then the few the viewer could
 * answer that were settled in the last minutes, newest first. Prompts the hub
 * allowed on its own are not answers, so they never take those places.
 */
export function drawerAsks(asks: AgentAsk[], viewer: Actor | null, now: number): AgentAsk[] {
  const pending = asks.filter((a) => a.status === "pending");
  const settled = asks
    .filter(
      (a) =>
        a.status !== "pending" &&
        !isAutoAllowedAsk(a) &&
        canManageSession(a, viewer) &&
        (a.decidedAt ?? a.createdAt) >= now - SETTLED_ASK_WINDOW_MS,
    )
    .sort((a, b) => (b.decidedAt ?? b.createdAt) - (a.decidedAt ?? a.createdAt))
    .slice(0, MAX_SETTLED_ASKS);
  return [...pending, ...settled];
}
