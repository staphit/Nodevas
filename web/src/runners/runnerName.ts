/**
 * The name `nodevas serve` registers its own runner under
 * (runnerwire.LocalRunnerName). It is a fixed id, not a name someone typed,
 * so the web says it in the reader's language.
 */
export const LOCAL_RUNNER_NAME = "this machine";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** A runner's name as shown: the hub's own runner translated, any other as it is. */
export function runnerDisplayName(t: Translate, name: string | undefined): string {
  if (name === LOCAL_RUNNER_NAME) return t("runner.local");
  return name ?? "";
}
