import type { CommandResult } from "../../store";

/**
 * How every settings editor talks back to the dialog frame.
 *
 * The banners belong to the frame so that switching tabs can wipe them; the
 * editors only ever say what happened.
 */
export interface SettingsNotify {
  onError(message: string | null): void;
  onNotice(message: string | null): void;
}

/**
 * Runs one workflow command and reports it.
 *
 * Both banners are cleared before the command runs, otherwise a stale success
 * would sit next to a fresh failure.
 */
export async function runSettingsCommand(
  notify: SettingsNotify,
  command: Promise<CommandResult>,
  done?: string,
): Promise<boolean> {
  notify.onError(null);
  notify.onNotice(null);
  const result = await command;
  if (!result.ok) {
    notify.onError(result.message);
    return false;
  }
  if (done) notify.onNotice(done);
  return true;
}
