/** Domain failures [A-04]. Thrown by reducers, converted to `CommandResult`. */

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

export function notFound(what: string): CommandError {
  return new CommandError("not-found", `${what}不存在或已被刪除。`);
}

export function invalid(message: string): CommandError {
  return new CommandError("invalid", message);
}
