import { afterEach, describe, expect, it } from "vitest";

import { noteServerDate, resetServerClock } from "../api/serverClock";
import { ACTIVITY_STALE_MS, SHELL_STALE_MS, activityOf, classifyTool, type Activity } from "./activity";

describe("classifyTool", () => {
  const cases: [string, string, Activity | null][] = [
    // Claude Code
    ["claude", "Read", "read"],
    ["claude", "Edit", "edit"],
    ["claude", "MultiEdit", "edit"],
    ["claude", "Write", "edit"],
    ["claude", "NotebookEdit", "edit"],
    ["claude", "Bash", "shell"],
    ["claude", "BashOutput", "shell"],
    ["claude", "KillShell", "shell"],
    ["claude", "Grep", "search"],
    ["claude", "Glob", "search"],
    ["claude", "LS", "search"],
    ["claude", "WebFetch", "web"],
    ["claude", "WebSearch", "web"],
    ["claude", "TodoWrite", "think"],
    ["claude", "ExitPlanMode", "think"],
    ["claude", "Task", null],
    ["claude", "Agent", null],
    // Codex
    ["codex", "shell", "shell"],
    ["codex", "local_shell", "shell"],
    ["codex", "exec_command", "shell"],
    ["codex", "apply_patch", "edit"],
    ["codex", "web_search", "web"],
    ["codex", "read_file", "read"],
    ["codex", "update_plan", "think"],
    // Gemini CLI
    ["gemini", "read_file", "read"],
    ["gemini", "read_many_files", "read"],
    ["gemini", "write_file", "edit"],
    ["gemini", "replace", "edit"],
    ["gemini", "run_shell_command", "shell"],
    ["gemini", "search_file_content", "search"],
    ["gemini", "glob", "search"],
    ["gemini", "list_directory", "search"],
    ["gemini", "google_web_search", "web"],
    ["gemini", "web_fetch", "web"],
    ["gemini", "save_memory", null],
    // OpenCode
    ["opencode", "read", "read"],
    ["opencode", "edit", "edit"],
    ["opencode", "write", "edit"],
    ["opencode", "patch", "edit"],
    ["opencode", "bash", "shell"],
    ["opencode", "grep", "search"],
    ["opencode", "glob", "search"],
    ["opencode", "list", "search"],
    ["opencode", "webfetch", "web"],
    ["opencode", "todowrite", "think"],
    ["opencode", "todoread", "think"],
    // Antigravity
    ["antigravity", "view_file", "read"],
    ["antigravity", "view_file_outline", "read"],
    ["antigravity", "view_code_item", "read"],
    ["antigravity", "edit_file", "edit"],
    ["antigravity", "replace_file_content", "edit"],
    ["antigravity", "multi_replace_file_content", "edit"],
    ["antigravity", "write_to_file", "edit"],
    ["antigravity", "run_command", "shell"],
    ["antigravity", "command_status", "shell"],
    ["antigravity", "send_command_input", "shell"],
    ["antigravity", "grep_search", "search"],
    ["antigravity", "find_by_name", "search"],
    ["antigravity", "codebase_search", "search"],
    ["antigravity", "list_dir", "search"],
    ["antigravity", "read_url_content", "web"],
    ["antigravity", "search_web", "web"],
    ["antigravity", "browser_navigate", "web"],
    // Thinking, whichever agent
    ["any", "thinking", "think"],
    // Case does not matter
    ["any", "READ", "read"],
    ["any", "run_SHELL_command", "shell"],
    // MCP tools: the trailing tool name's words
    ["mcp", "mcp__github__get_file_contents", "read"],
    ["mcp", "mcp__fs__readFile", "read"],
    ["mcp", "mcp__notes__view_page", "read"],
    ["mcp", "mcp__fs__write_file", "edit"],
    ["mcp", "mcp__github__create_or_update_file", null],
    ["mcp", "mcp__code__apply_patch", "edit"],
    ["mcp", "mcp__docs__replace_text", "edit"],
    ["mcp", "mcp__codebase-memory-mcp__search_graph", "search"],
    ["mcp", "mcp__db__query", "search"],
    ["mcp", "mcp__fs__find_files", "search"],
    ["mcp", "mcp__chrome-devtools__navigate_page", "web"],
    ["mcp", "mcp__web__fetch", "web"],
    ["mcp", "mcp__shell__exec", "shell"],
    ["mcp", "mcp__term__run_terminal_cmd", "shell"],
    ["mcp", "mcp__slack__thread_reply", null],
    // A browser or web-search server is the web whatever its tool is called.
    ["mcp", "mcp__chrome-devtools__click", "web"],
    ["mcp", "mcp__chrome-devtools__list_pages", "web"],
    ["mcp", "mcp__chrome-devtools__list_network_requests", "web"],
    ["mcp", "mcp__claude-in-chrome__read_page", "web"],
    ["mcp", "mcp__claude-in-chrome__get_page_text", "web"],
    ["mcp", "mcp__playwright__browser_snapshot", "web"],
    ["mcp", "mcp__exa__web_search_exa", "web"],
    ["mcp", "mcp__brave-search__brave_web_search", "web"],
    ["mcp", "mcp__tavily__tavily_search", "web"],
    ["mcp", "mcp__fetch__fetch", "web"],
    // Web words come before reading and searching ones.
    ["mcp", "mcp__tools__search_web", "web"],
    ["mcp", "mcp__tools__get_url", "web"],
    // The agent network's own calls are coordination: thinking out loud.
    ["mcp", "mcp__nodevas__get_node", "think"],
    ["mcp", "mcp__nodevas__list_team", "think"],
    // Unknown, empty
    ["any", "frobnicate", null],
    ["any", "", null],
    ["any", "  ", null],
  ];
  it.each(cases)("%s %s → %s", (_agent, name, activity) => {
    expect(classifyTool(name)).toBe(activity);
  });

  it("never classifies an unknown tool by keyword outside MCP", () => {
    expect(classifyTool("read_the_room")).toBeNull();
  });
});

describe("activityOf", () => {
  const now = Date.parse("2026-11-05T12:00:00Z");
  const iso = (ms: number) => new Date(now - ms).toISOString();

  it("reads an RFC 3339 time or Unix milliseconds", () => {
    expect(activityOf("Read", iso(5_000), now)).toBe("read");
    expect(activityOf("Bash", now - 5_000, now)).toBe("shell");
    expect(activityOf("thinking", "2026-11-05T11:59:58.123+00:00", now)).toBe("think");
  });

  it("shows nothing once the tool is older than ACTIVITY_STALE_MS", () => {
    expect(ACTIVITY_STALE_MS).toBe(120_000);
    expect(activityOf("Read", iso(ACTIVITY_STALE_MS), now)).toBe("read");
    expect(activityOf("Read", iso(ACTIVITY_STALE_MS + 1), now)).toBeUndefined();
    expect(activityOf("Read", now - ACTIVITY_STALE_MS - 1, now)).toBeUndefined();
  });

  it("trusts a time a little ahead of the clock (another machine's)", () => {
    expect(activityOf("Edit", iso(-3_000), now)).toBe("edit");
  });

  it("does not trust a time implausibly far ahead to stay fresh", () => {
    expect(activityOf("Edit", iso(-(ACTIVITY_STALE_MS + 1)), now)).toBeUndefined();
  });

  it("keeps a shell call current far longer: a build or a test suite runs for minutes", () => {
    expect(SHELL_STALE_MS).toBeGreaterThanOrEqual(10 * 60_000);
    expect(activityOf("Bash", iso(5 * 60_000), now)).toBe("shell");
    expect(activityOf("run_shell_command", iso(SHELL_STALE_MS), now)).toBe("shell");
    expect(activityOf("Bash", iso(SHELL_STALE_MS + 1), now)).toBeUndefined();
    expect(activityOf("Read", iso(5 * 60_000), now)).toBeUndefined();
  });

  describe("against the hub's clock, not the browser's", () => {
    afterEach(() => resetServerClock());

    it("a browser 5 minutes ahead of the hub still shows a tool the hub stamped 10 s ago", () => {
      const hubNow = now - 5 * 60_000;
      noteServerDate(new Date(hubNow).toUTCString(), now);
      expect(activityOf("Read", new Date(hubNow - 10_000).toISOString(), now)).toBe("read");
      expect(activityOf("Read", hubNow - 10_000, now)).toBe("read");
    });

    it("a browser 5 minutes behind the hub lets a tool go stale on time", () => {
      const hubNow = now + 5 * 60_000;
      noteServerDate(new Date(hubNow).toUTCString(), now);
      expect(activityOf("Read", new Date(hubNow - 10_000).toISOString(), now)).toBe("read");
      expect(activityOf("Read", new Date(hubNow - ACTIVITY_STALE_MS - 5_000).toISOString(), now)).toBeUndefined();
    });

    it("ignores a missing or broken Date header, and a second's rounding", () => {
      noteServerDate(null, now);
      noteServerDate("not a date", now);
      noteServerDate(new Date(now - 900).toUTCString(), now);
      expect(activityOf("Read", iso(ACTIVITY_STALE_MS), now)).toBe("read");
      expect(activityOf("Read", iso(ACTIVITY_STALE_MS + 1), now)).toBeUndefined();
    });
  });

  it("shows nothing without a tool, a time, or a known tool", () => {
    expect(activityOf(undefined, iso(1_000), now)).toBeUndefined();
    expect(activityOf("Read", undefined, now)).toBeUndefined();
    expect(activityOf("Read", "not a time", now)).toBeUndefined();
    expect(activityOf("Read", 0, now)).toBeUndefined();
    expect(activityOf("Read", Number.NaN, now)).toBeUndefined();
    expect(activityOf("frobnicate", iso(1_000), now)).toBeUndefined();
  });
});
