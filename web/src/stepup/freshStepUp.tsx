import { useCallback, useRef, useState } from "react";
import { AuthError } from "../api";
import { StepUpDialog } from "./StepUpDialog";
import { stepUpActive, useStepUp } from "./stepUpStore";

/**
 * Runs an action that needs a fresh step-up: straight away when this browser
 * is verified (or the hub has no step-up, or has not said yet), otherwise
 * after the step-up dialog closes verified. `ask` forces the dialog, for when
 * the hub refused anyway. The same flow as linking a device
 * (runners/LinkDevice.tsx).
 */
export function useFreshStepUp() {
  const [open, setOpen] = useState(false);
  const pending = useRef<(() => void) | null>(null);
  const ask = useCallback((action: () => void) => {
    pending.current = action;
    setOpen(true);
  }, []);
  const run = useCallback(
    (action: () => void) => {
      const status = useStepUp.getState().status;
      if (status?.required && !stepUpActive(status)) ask(action);
      else action();
    },
    [ask],
  );
  const close = useCallback(() => {
    setOpen(false);
    const action = pending.current;
    pending.current = null;
    if (action && stepUpActive(useStepUp.getState().status)) action();
  }, []);
  return { run, ask, open, dialog: open ? <StepUpDialog onClose={close} /> : null };
}

/** A 403 that a fresh step-up would cure: true when, asked again, the hub
 * says this browser is not stepped up. req() keeps a 403's status but not its
 * code, so the hub is asked rather than the error read. */
export async function needsStepUp(failure: unknown): Promise<boolean> {
  if (!(failure instanceof AuthError) || failure.status !== 403) return false;
  await useStepUp.getState().load();
  const status = useStepUp.getState().status;
  return Boolean(status?.required) && !stepUpActive(status);
}
