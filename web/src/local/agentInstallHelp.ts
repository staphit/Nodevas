// Where to get each agent, for someone who has none yet (single-user
// edition): the official page and who makes it. Nothing is downloaded or run
// from here; the link opens in the person's own browser (the desktop app
// hands a new tab to the OS browser). The pages' hosts are pinned in a test.

/** The agent kinds a runner looks for, in its order (runner.Kinds). */
export const AGENT_KINDS = ["claude", "codex", "gemini", "opencode", "antigravity"] as const;
export type AgentKind = (typeof AGENT_KINDS)[number];

export interface AgentInstallHelp {
  /** The official page that says how to install it. */
  url: string;
  /** Who makes it, for "may need a paid plan"; "" when it is free to use. */
  vendor: string;
}

export const AGENT_INSTALL_HELP: Record<AgentKind, AgentInstallHelp> = {
  claude: { url: "https://code.claude.com/docs/en/setup", vendor: "Anthropic" },
  codex: { url: "https://developers.openai.com/codex/cli", vendor: "OpenAI" },
  gemini: { url: "https://github.com/google-gemini/gemini-cli", vendor: "Google" },
  opencode: { url: "https://opencode.ai/docs/", vendor: "" },
  antigravity: { url: "https://antigravity.google/", vendor: "Google" },
};

/** How an outside link opens: a new tab that cannot reach back to this page. */
export const EXTERNAL_LINK = { target: "_blank", rel: "noopener noreferrer" } as const;

/** The install help for `kind`, or undefined for a kind this app does not know. */
export function installHelp(kind: string): AgentInstallHelp | undefined {
  return Object.hasOwn(AGENT_INSTALL_HELP, kind) ? AGENT_INSTALL_HELP[kind as AgentKind] : undefined;
}

/** The program file the runner looks for (adapter.Binary): the kind's own
 * name, except Antigravity's "agy". */
export function agentProgram(kind: string): string {
  return kind === "antigravity" ? "agy" : kind;
}
