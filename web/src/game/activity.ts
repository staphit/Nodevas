// What a working agent is doing right now, from the last tool it called (or
// its thinking), so the desk can show reading, typing, a terminal… without
// asking any model: a fixed table from tool names to a handful of
// activities. A tool too old to trust shows no activity (the plain pose).

import { serverClockOffset } from "../api/serverClock";

/** Every activity a desk can draw; the order is the drawing's, not a
 * precedence. */
export const ACTIVITIES = ["read", "edit", "shell", "search", "web", "think"] as const;

export type Activity = (typeof ACTIVITIES)[number];

/** How long the last tool still says what the agent is doing: an agent
 * that called nothing for longer is drawn in its plain pose. */
export const ACTIVITY_STALE_MS = 120_000;

/** ACTIVITY_STALE_MS for a shell call. A tool call is reported when it
 * starts and nothing more until its result, and a build or a test suite runs
 * for minutes: the terminal stays up that long. */
export const SHELL_STALE_MS = 15 * 60_000;

/** Every agent's built-in tools, lower-cased: Claude Code, Codex, Gemini CLI,
 * OpenCode and Antigravity. "thinking" is the hub's name for a thinking
 * event (runnerwire.RunEvent type thinking). A tool not listed (a
 * sub-agent, memory) has no activity. */
const TOOLS: Readonly<Record<string, Activity>> = {
  thinking: "think",

  // Reading a file.
  read: "read",
  read_file: "read",
  read_many_files: "read",
  view_file: "read",
  view_file_outline: "read",
  view_code_item: "read",
  notebookread: "read",

  // Changing one.
  edit: "edit",
  multiedit: "edit",
  write: "edit",
  notebookedit: "edit",
  apply_patch: "edit",
  patch: "edit",
  write_file: "edit",
  replace: "edit",
  edit_file: "edit",
  write_to_file: "edit",
  replace_file_content: "edit",
  multi_replace_file_content: "edit",

  // A terminal.
  bash: "shell",
  bashoutput: "shell",
  killshell: "shell",
  killbash: "shell",
  shell: "shell",
  local_shell: "shell",
  exec_command: "shell",
  write_stdin: "shell",
  run_shell_command: "shell",
  run_command: "shell",
  command_status: "shell",
  send_command_input: "shell",

  // Looking for something in the code.
  grep: "search",
  glob: "search",
  ls: "search",
  list: "search",
  search_file_content: "search",
  list_directory: "search",
  grep_search: "search",
  find_by_name: "search",
  codebase_search: "search",
  list_dir: "search",

  // The web.
  webfetch: "web",
  websearch: "web",
  web_search: "web",
  web_fetch: "web",
  google_web_search: "web",
  read_url_content: "web",
  search_web: "web",

  // Planning: the to-do list is thinking out loud.
  todowrite: "think",
  todoread: "think",
  exitplanmode: "think",
  update_plan: "think",
};

/** An MCP tool's words, in precedence order: the first group with a word in
 * the tool's name wins ("search_web" and "get_url" are the web, "get_file"
 * reads, "list_files" searches). */
const MCP_WORDS: ReadonlyArray<[Activity, ReadonlySet<string>]> = [
  ["web", new Set(["web", "url", "http", "https", "browser", "browse", "navigate"])],
  ["read", new Set(["read", "get", "view", "cat", "open"])],
  ["edit", new Set(["write", "edit", "patch", "replace"])],
  ["search", new Set(["search", "grep", "find", "query", "list", "lookup"])],
  ["web", new Set(["fetch"])],
  ["shell", new Set(["run", "exec", "execute", "shell", "command", "cmd", "terminal", "bash"])],
];

/** MCP servers whose every tool is one activity, by a word of the server's
 * name: a browser or a web search is the web however its tools are named
 * (chrome-devtools' list_pages, claude-in-chrome's read_page, exa's
 * web_search_exa); the agent network's own calls are coordination. */
const MCP_SERVERS: Readonly<Record<string, Activity>> = {
  chrome: "web",
  devtools: "web",
  browser: "web",
  browserbase: "web",
  playwright: "web",
  puppeteer: "web",
  exa: "web",
  brave: "web",
  tavily: "web",
  fetch: "web",
  firecrawl: "web",
  perplexity: "web",
  kagi: "web",
  serper: "web",
  duckduckgo: "web",
  websearch: "web",
  web: "web",
  nodevas: "think",
};

/** "getFileContents" / "get_file-contents" → ["get", "file", "contents"]. */
function words(name: string): string[] {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

/** The activity a tool name stands for, or null when it stands for none the
 * desk draws. Case does not matter. An MCP tool (mcp__<server>__<tool>) is
 * read by the words of its tool name. */
export function classifyTool(name: string): Activity | null {
  const trimmed = name.trim();
  if (!trimmed) return null;
  const lower = trimmed.toLowerCase();
  const known = TOOLS[lower];
  if (known) return known;
  // Antigravity drives a browser through browser_* tools.
  if (lower.startsWith("browser_")) return "web";
  if (!lower.startsWith("mcp__")) return null;
  const cut = trimmed.lastIndexOf("__");
  for (const word of words(trimmed.slice(5, cut))) {
    const server = MCP_SERVERS[word];
    if (server) return server;
  }
  const tool = trimmed.slice(cut + 2);
  const found = words(tool);
  for (const [activity, group] of MCP_WORDS) {
    if (found.some((word) => group.has(word))) return activity;
  }
  return null;
}

/** When the last tool was called, in ms: RFC 3339 (a run's lastToolAt) or
 * Unix milliseconds (a session's). NaN when unknown. */
function toolTime(at: string | number | undefined): number {
  if (typeof at === "number") return at > 0 ? at : NaN;
  if (!at) return NaN;
  return Date.parse(at);
}

/** What the agent is doing at `now` (this browser's clock): its last tool's
 * activity, unless the tool is unknown, has no time, or is older than
 * ACTIVITY_STALE_MS (SHELL_STALE_MS for a shell call). The time is the
 * hub's, so it is judged against the hub's clock (serverClock). A time a
 * little ahead still counts; one further ahead than a whole window is not
 * trusted. */
export function activityOf(
  lastTool: string | undefined,
  lastToolAt: string | number | undefined,
  now: number,
): Activity | undefined {
  if (!lastTool) return undefined;
  const at = toolTime(lastToolAt);
  if (!Number.isFinite(at)) return undefined;
  const activity = classifyTool(lastTool);
  if (!activity) return undefined;
  const age = now + serverClockOffset() - at;
  const window = activity === "shell" ? SHELL_STALE_MS : ACTIVITY_STALE_MS;
  if (age > window || age < -ACTIVITY_STALE_MS) return undefined;
  return activity;
}
