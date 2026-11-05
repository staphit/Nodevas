// The hub's clock as this browser estimates it, from the Date header of the
// hub's answers. Times the hub stamps (a run's or a session's last tool) are
// judged against it, not against this machine's clock, which may be minutes
// off the hub's.

/** Below this the header's whole seconds and the trip's latency are all the
 * difference there is: the clocks agree. */
const AGREE_MS = 2_000;

let offset = 0;

/** Notes a Date header from the hub, received at `receivedAt` (this
 * browser's clock). A missing or unreadable header changes nothing. */
export function noteServerDate(header: string | null | undefined, receivedAt = Date.now()): void {
  if (!header) return;
  const server = Date.parse(header);
  if (!Number.isFinite(server)) return;
  // The header is whole seconds, rounded down: the hub's clock was up to a
  // second past it.
  const next = server + 500 - receivedAt;
  offset = Math.abs(next) < AGREE_MS ? 0 : next;
}

/** How far the hub's clock is ahead of this browser's, in ms (negative when
 * behind). */
export function serverClockOffset(): number {
  return offset;
}

/** For tests: the clocks agree again. */
export function resetServerClock(): void {
  offset = 0;
}
