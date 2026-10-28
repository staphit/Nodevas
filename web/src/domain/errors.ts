/** Domain failures [A-04]. Thrown by reducers, converted to `CommandResult`. */

import { translate } from "../i18n";

export type CommandErrorCode =
  | "not-found"
  | "invalid"
  | "duplicate"
  | "unsupported";

export class CommandError extends Error {
  readonly code: CommandErrorCode;
  constructor(code: CommandErrorCode, message: string) {
    super(message);
    this.name = "CommandError";
    this.code = code;
  }
}

/** `what` is already in the reader's language (a `domain.subject.*` message). */
export function notFound(what: string): CommandError {
  return new CommandError("not-found", translate("domain.error.notFound", undefined, { what }));
}

export function invalid(message: string): CommandError {
  return new CommandError("invalid", message);
}
