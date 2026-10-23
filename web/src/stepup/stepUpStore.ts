import { create } from "zustand";
import { api, type StepUpStatus } from "../api";

/**
 * Step-up (W6): a signed-in browser re-proves who it is with a fresh email
 * passcode, and for a few minutes after that a runner may auto-accept its
 * dispatches. This holds the hub's answer to GET /api/auth/step-up.
 */
export interface StepUpStoreState {
  /** undefined until fetched; null when the hub could not say (an older hub
   * without step-up, or a failed request). */
  status: StepUpStatus | null | undefined;
  loading: boolean;

  /** Fetches the status (each dialog open asks afresh). */
  load: () => Promise<void>;
  /** Mails a passcode; throws the API error so the dialog can word it. */
  requestCode: () => Promise<void>;
  /** Checks a passcode; throws on a wrong code (400/401) or 429. */
  verify: (code: string) => Promise<StepUpStatus>;
  reset: () => void;
}

/** Whether a status is stepped up right now: the hub said so and its window
 * has not run out since. */
export function stepUpActive(status: StepUpStatus | null | undefined, now = Date.now()): boolean {
  if (!status?.active) return false;
  if (!status.until) return true;
  const until = Date.parse(status.until);
  return !Number.isFinite(until) || until > now;
}

// Re-asks when an active window ends, so "verified until 14:05" does not
// linger past 14:05 in an open dialog.
let expiryTimer: ReturnType<typeof setTimeout> | null = null;
// setTimeout's longest delay; a window past it is re-checked then.
const MAX_DELAY = 2_147_483_000;

function scheduleExpiry(status: StepUpStatus | null | undefined, reload: () => void) {
  if (expiryTimer !== null) clearTimeout(expiryTimer);
  expiryTimer = null;
  if (!status?.active || !status.until) return;
  const until = Date.parse(status.until);
  if (!Number.isFinite(until)) return;
  const delay = Math.min(MAX_DELAY, Math.max(0, until - Date.now()) + 500);
  expiryTimer = setTimeout(() => {
    expiryTimer = null;
    reload();
  }, delay);
}

let generation = 0;

export const useStepUp = create<StepUpStoreState>((set, get) => {
  const hold = (status: StepUpStatus | null) => {
    set({ status });
    scheduleExpiry(status, () => void get().load());
  };
  return {
    status: undefined,
    loading: false,

    load: async () => {
      const gen = ++generation;
      set({ loading: true });
      try {
        const status = await api.getStepUp();
        if (gen === generation) hold(status);
      } catch {
        // Not knowing is not an error worth a banner: the dialogs then say
        // what happens without a step-up.
        if (gen === generation) hold(null);
      } finally {
        if (gen === generation) set({ loading: false });
      }
    },

    requestCode: () => api.requestStepUp(),

    verify: async (code) => {
      const status = await api.verifyStepUp(code);
      generation++;
      hold(status);
      set({ loading: false });
      return status;
    },

    reset: () => {
      generation++;
      if (expiryTimer !== null) clearTimeout(expiryTimer);
      expiryTimer = null;
      set({ status: undefined, loading: false });
    },
  };
});
