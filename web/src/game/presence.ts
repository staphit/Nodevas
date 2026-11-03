// Whether an agent session is still there, judged against a clock. The hub
// computes `online` only when someone asks (internal/agents/registry.go,
// pruneExpiredLocked) and sends no event when a session times out, so a view
// that stays open must age sessions itself: a session is online while its
// last heartbeat is younger than the hub's SessionTTL.

import type { AgentSession } from "../api/agents";

/** internal/agents/registry.go SessionTTL (90s). */
export const SESSION_TTL_MS = 90_000;

/** How often a mounted war room re-derives presence. A third of the TTL: a
 * session that went quiet shows offline at most 30s late. */
export const PRESENCE_TICK_MS = 30_000;

/** How long a sent message shows as a speech bubble. */
export const BUBBLE_MS = 10_000;

/** Whether the session is online at `now`: the hub said so when it listed
 * it, and its last heartbeat is still inside the TTL. Exactly at the TTL it
 * is offline, as on the hub (lastSeen + TTL < now keeps it). */
export function sessionOnline(
  session: Pick<AgentSession, "online" | "lastSeen">,
  now: number,
  ttl = SESSION_TTL_MS,
): boolean {
  if (!session.online) return false;
  if (!Number.isFinite(session.lastSeen) || session.lastSeen <= 0) return true;
  return now - session.lastSeen < ttl;
}

/** The presence tick `now` falls in: the start of its PRESENCE_TICK_MS
 * window. Memoising on it rather than on `now` rebuilds the world once per
 * tick, not on every render. */
export function presenceTick(now: number, step = PRESENCE_TICK_MS): number {
  if (!Number.isFinite(now) || step <= 0) return 0;
  return Math.floor(now / step) * step;
}

/** Milliseconds from `now` to the next tick, for a setTimeout that keeps the
 * view's clock aligned to the ticks (never 0: at a boundary, a whole step). */
export function msUntilNextTick(now: number, step = PRESENCE_TICK_MS): number {
  if (!Number.isFinite(now) || step <= 0) return step > 0 ? step : PRESENCE_TICK_MS;
  return presenceTick(now, step) + step - now;
}

/** When the session will show offline if no heartbeat comes, or null when it
 * already does. A view can wake exactly then instead of polling. */
export function sessionExpiresAt(
  session: Pick<AgentSession, "online" | "lastSeen">,
  now: number,
  ttl = SESSION_TTL_MS,
): number | null {
  if (!sessionOnline(session, now, ttl)) return null;
  if (!Number.isFinite(session.lastSeen) || session.lastSeen <= 0) return null;
  return session.lastSeen + ttl;
}

/** Whether a message sent at `createdAt` still shows as a bubble at `now`. */
export function bubbleVisible(createdAt: number, now: number, window = BUBBLE_MS): boolean {
  return Number.isFinite(createdAt) && createdAt <= now && now - createdAt < window;
}
