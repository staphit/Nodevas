import { agentName } from "../game/labels";

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

/**
 * A runner's name for someone in Simple mode: the hub's own runner is this
 * computer ("This computer" as a label, "this computer" inside a sentence),
 * any other as runnerDisplayName says it. Outside Simple, runnerDisplayName.
 */
export function plainRunnerName(
  t: Translate,
  name: string | undefined,
  simple: boolean,
  inSentence = false,
): string {
  if (simple && name === LOCAL_RUNNER_NAME) return t(inSentence ? "local.machine" : "local.title");
  return runnerDisplayName(t, name);
}

/**
 * The label the runner gives its "pass no model flag" entry
 * (internal/runner/detect.go). A fixed English label, not a model's name, so
 * the web says it in the reader's language.
 */
export const CLI_DEFAULT_MODEL_LABEL = "CLI default";

/** A runner model's name as shown: the CLI-default entry translated, any other as the runner sent it. */
export function runnerModelLabel(t: Translate, model: { id: string; label?: string }): string {
  if (model.id === "default" && (!model.label || model.label === CLI_DEFAULT_MODEL_LABEL)) {
    return t("runners.cliDefault");
  }
  return model.label || model.id;
}

// Why the runner cannot use an agent CLI it found, as internal/runner spells
// it. The rule, path and command stay as sent so they can still be copied.
const ANTIGRAVITY_BLOCKED =
  /^antigravity: runs need "([^"]+)" in permissions\.allow of (\S+); run `([^`]+)`( \(the file does not parse\))?$/;
const OPENCODE_UNSUPPORTED = /^opencode: version (\d+) is not supported yet; runs need OpenCode 1\.x or 2\.x$/;
// The program file the person added for the agent is gone, and none is on
// PATH either (runner agentBinary).
const AGENT_PATH_GONE = /^([a-z]+): the program added for it is no longer there$/;

/** A runner agent's detection problem in the reader's language; any message the web does not know stays as sent. */
/** The agent problems whose words are a developer's (a settings file to
 * edit, a command to type, a version range), said plainly for someone who
 * does not use a terminal; "" for any other, which runnerAgentProblem says. */
export function agentProblemPlain(t: Translate, error: string, agent: string): string {
  if (ANTIGRAVITY_BLOCKED.test(error)) return t("local.scan.needsSetup", { agent });
  if (OPENCODE_UNSUPPORTED.test(error)) return t("local.scan.needsOtherVersion", { agent });
  return "";
}

export function runnerAgentProblem(t: Translate, error: string): string {
  const gone = AGENT_PATH_GONE.exec(error);
  if (gone) return t("local.agentPathGone", { agent: agentName(gone[1]) });
  const antigravity = ANTIGRAVITY_BLOCKED.exec(error);
  if (antigravity) {
    const [, rule, path, command, unparsable] = antigravity;
    return t(unparsable ? "runners.antigravityUnparsable" : "runners.antigravityBlocked", { rule, path, command });
  }
  const opencode = OPENCODE_UNSUPPORTED.exec(error);
  if (opencode) return t("runners.opencodeUnsupported", { major: opencode[1] });
  return error;
}
