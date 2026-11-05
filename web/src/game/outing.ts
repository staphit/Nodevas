// Where an agent goes while its run waits for a usage limit to reset: out of
// the office for a hike, up a stream, fishing or camping, picked from the
// run id so the same run always goes on the same outing, and as far along it
// as the wait is (createdAt → retryAt), in OUTING_STEPS whole steps so the
// drawing changes only a few times per wait.

export const OUTING_KINDS = ["hike", "stream", "fish", "camp"] as const;

export type OutingKind = (typeof OUTING_KINDS)[number];

/** Steps along an outing: 0 just left, OUTING_STEPS - 1 on the way back. */
export const OUTING_STEPS = 8;

export interface Outing {
  kind: OutingKind;
  /** 0 … OUTING_STEPS - 1. */
  step: number;
  /** When the limit resets (Run.retryAt), RFC 3339. */
  returnsAt: string;
  /** Whole minutes from the world's clock to returnsAt, rounded up, never
   * below 0 (for the sentence: "back in about N minutes"). */
  minutesLeft: number;
}

/** 32-bit FNV-1a over the string's UTF-16 code units (a run id is ASCII):
 * small, stable and the same in every browser. */
export function fnv1a32(text: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash >>> 0;
}

/** The outing a run goes on: the same one for the same run id. */
export function outingKindOf(runId: string): OutingKind {
  return OUTING_KINDS[fnv1a32(runId) % OUTING_KINDS.length];
}

/** The run's outing at `now`, or undefined when it has no reset time. The
 * wait began at createdAt (a limit-wait run is queued when its predecessor
 * hits the limit); a wait whose start is unknown has just begun, one whose
 * reset is not after its start is on the way back. */
export function outingOf(
  run: { id: string; createdAt: string; retryAt?: string },
  now: number,
): Outing | undefined {
  if (!run.retryAt) return undefined;
  const end = Date.parse(run.retryAt);
  if (!Number.isFinite(end)) return undefined;
  const begin = Date.parse(run.createdAt);
  let progress: number;
  if (!Number.isFinite(begin)) progress = 0;
  else if (end <= begin) progress = 1;
  else progress = (now - begin) / (end - begin);
  progress = Math.min(1, Math.max(0, progress));
  const step = Math.min(OUTING_STEPS - 1, Math.max(0, Math.floor(progress * OUTING_STEPS)));
  return {
    kind: outingKindOf(run.id),
    step,
    returnsAt: run.retryAt,
    minutesLeft: Math.max(0, Math.ceil((end - now) / 60_000)),
  };
}
