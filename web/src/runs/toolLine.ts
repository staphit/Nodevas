import type { RunEvent } from "../api";

export type ToolVerb =
  | "mcp"
  | "run"
  | "write"
  | "edit"
  | "read"
  | "search"
  | "web"
  | "todo"
  | "agent"
  | "other";

export type ToolState = "running" | "ok" | "failed";

export interface ToolSummary {
  verb: ToolVerb;
  target: string;
  state: ToolState;
  toolName: string;
  server?: string;
  mcpTool?: string;
  boardServer?: boolean;
  files?: { first: string; moreCount: number };
}

export interface SummarizeToolOptions {
  worktree?: string;
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

const MAX_COMMAND_CHARS = 80;

const BOARD_TOOL_ALIASES = new Set([
  "claim_task",
  "get_node",
  "set_node_status",
  "release_task",
  "check_inbox",
  "send_message",
]);

const RUN_NAMES = new Set([
  "bash",
  "shell",
  "run_shell_command",
  "run_command",
  "command",
  "terminal",
  "exec_command",
  "local_shell",
]);

const WRITE_NAMES = new Set(["write", "write_file", "create_file"]);
const EDIT_NAMES = new Set([
  "edit",
  "multiedit",
  "replace",
  "replace_file_content",
  "patch",
  "notebookedit",
]);
const READ_NAMES = new Set(["read", "read_file", "view_file", "view", "notebookread", "cat"]);
const SEARCH_NAMES = new Set(["grep", "glob", "find", "ls", "codebase_search", "find_by_name"]);
const WEB_NAMES = new Set([
  "webfetch",
  "web_fetch",
  "fetch",
  "websearch",
  "web_search",
  "google_web_search",
]);
const TODO_NAMES = new Set(["todowrite", "write_todos", "update_plan", "plan"]);
const AGENT_NAMES = new Set(["task", "subagent", "agent"]);

const COMMAND_KEYS = ["command", "cmd", "CommandLine", "commandLine"] as const;
const FILE_KEYS = [
  "file_path",
  "filePath",
  "path",
  "Path",
  "TargetFile",
  "AbsolutePath",
  "notebook_path",
  "file",
  "filename",
] as const;
const SEARCH_KEYS = ["pattern", "query", "Query", "glob", "regex", "path"] as const;
const WEB_KEYS = ["url", "Url", "query", "Query"] as const;
const AGENT_KEYS = ["description", "prompt"] as const;

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function unescapeJsonString(raw: string): string {
  try {
    return JSON.parse(`"${raw}"`) as string;
  } catch {
    return raw
      .replace(/\\n/g, "\n")
      .replace(/\\r/g, "\r")
      .replace(/\\t/g, "\t")
      .replace(/\\"/g, '"')
      .replace(/\\\\/g, "\\");
  }
}

function parseJsonObject(input: string): Record<string, unknown> | null {
  const trimmed = input.trim();
  if (!trimmed.startsWith("{")) return null;
  try {
    const parsed = JSON.parse(trimmed);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    /* truncated or non-JSON input falls back to regex */
  }
  return null;
}

function extractTruncatedJsonValue(input: string, keys: readonly string[]): string {
  for (const key of keys) {
    const pattern = new RegExp(`"${escapeRegex(key)}"\\s*:\\s*"((?:[^"\\\\]|\\\\.){1,300})`);
    const match = pattern.exec(input);
    if (match?.[1]) return unescapeJsonString(match[1]);
  }
  return "";
}

function extractCommandValue(input: string): string {
  const trimmed = input.trim();
  if (!trimmed) return "";
  const obj = parseJsonObject(trimmed);
  if (obj) {
    for (const key of COMMAND_KEYS) {
      const value = obj[key];
      if (typeof value === "string" && value.trim()) return value;
      if (Array.isArray(value) && value.length > 0) {
        const parts = value.map((item) => String(item));
        if (
          parts.length === 3 &&
          (parts[0] === "bash" || parts[0] === "sh" || parts[0] === "zsh") &&
          (parts[1] === "-lc" || parts[1] === "-c")
        ) {
          return parts[2];
        }
        return parts.join(" ");
      }
    }
    return "";
  }
  if (trimmed.startsWith("{")) {
    return extractTruncatedJsonValue(trimmed, COMMAND_KEYS);
  }
  // Plain string input (e.g. Codex shell tool).
  return trimmed;
}

function extractStringKey(
  input: string,
  keys: readonly string[],
  allowPlainString = false,
): string {
  const trimmed = input.trim();
  if (!trimmed) return "";
  const obj = parseJsonObject(trimmed);
  if (obj) {
    for (const key of keys) {
      const value = obj[key];
      if (typeof value === "string" && value.trim()) return value;
    }
    return "";
  }
  if (trimmed.startsWith("{")) {
    return extractTruncatedJsonValue(trimmed, keys);
  }
  return allowPlainString ? trimmed : "";
}

function firstLineCut(text: string, max = MAX_COMMAND_CHARS): string {
  const first =
    text
      .replace(/\r\n/g, "\n")
      .split("\n")
      .map((line) => line.trim())
      .find(Boolean) ?? "";
  if (first.length <= max) return first;
  return `${first.slice(0, max - 1)}…`;
}

function formatPath(raw: string, worktree?: string): string {
  const normalized = raw.trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (!normalized) return "";
  if (worktree) {
    const wt = worktree.trim().replace(/\\/g, "/").replace(/\/+$/, "");
    if (wt && normalized === wt) return ".";
    if (wt && normalized.startsWith(`${wt}/`)) {
      return normalized.slice(wt.length + 1);
    }
  }
  const segments = normalized.split("/").filter(Boolean);
  if (segments.length <= 2) return segments.join("/");
  return segments.slice(-2).join("/");
}

function parseApplyPatchPaths(input: string): string[] {
  const paths: string[] = [];
  for (const rawLine of input.replace(/\r\n/g, "\n").split("\n")) {
    const line = rawLine.trim();
    if (!line) continue;
    const space = line.indexOf(" ");
    const candidate = space >= 0 ? line.slice(space + 1).trim() : line;
    if (candidate) paths.push(candidate);
  }
  return paths;
}

function deriveToolState(result?: Pick<RunEvent, "isError" | "text">): ToolState {
  if (!result) return "running";
  if (result.isError) return "failed";
  const text = (result.text ?? "").trim();
  if (/^\(?declined\b/i.test(text)) return "failed";
  return "ok";
}

/**
 * Pure one-line tool summary (§5). Rule order is fixed: `mcp__` and bare board
 * aliases are matched first so `mcp__terminal__*` reads as MCP, never as run.
 */
export function summarizeTool(
  call: Pick<RunEvent, "tool" | "input">,
  result?: Pick<RunEvent, "isError" | "text">,
  options?: SummarizeToolOptions,
): ToolSummary {
  const rawName = (call.tool ?? "").trim();
  const name = rawName.toLowerCase();
  const input = call.input ?? "";
  const state = deriveToolState(result);
  const worktree = options?.worktree;

  // 1. MCP and bare board tool names first.
  if (name.startsWith("mcp__")) {
    const rest = name.slice(5);
    const sep = rest.indexOf("__");
    const server = sep >= 0 ? rest.slice(0, sep) : rest;
    const mcpTool = sep >= 0 ? rest.slice(sep + 2) : "";
    const boardServer = server === "nodevas";
    return {
      verb: "mcp",
      target: mcpTool || rawName,
      state,
      toolName: rawName,
      server,
      mcpTool: mcpTool || rawName,
      boardServer,
    };
  }
  if (BOARD_TOOL_ALIASES.has(name)) {
    return {
      verb: "mcp",
      target: name,
      state,
      toolName: rawName,
      server: "nodevas",
      mcpTool: name,
      boardServer: true,
    };
  }

  // 2. Shell / command execution.
  if (RUN_NAMES.has(name) || name.includes("command") || name.includes("terminal")) {
    return {
      verb: "run",
      target: firstLineCut(extractCommandValue(input)),
      state,
      toolName: rawName,
    };
  }

  // 3. File write.
  if (WRITE_NAMES.has(name)) {
    return {
      verb: "write",
      target: formatPath(extractStringKey(input, FILE_KEYS), worktree),
      state,
      toolName: rawName,
    };
  }

  // 4. Codex apply_patch (lines of "<kind> <path>").
  if (name === "apply_patch") {
    const paths = parseApplyPatchPaths(input).map((p) => formatPath(p, worktree)).filter(Boolean);
    if (paths.length > 1) {
      return {
        verb: "edit",
        target: paths[0],
        files: { first: paths[0], moreCount: paths.length - 1 },
        state,
        toolName: rawName,
      };
    }
    return {
      verb: "edit",
      target: paths[0] ?? "",
      state,
      toolName: rawName,
    };
  }

  // 5. File edit.
  if (EDIT_NAMES.has(name) || name.startsWith("str_replace")) {
    return {
      verb: "edit",
      target: formatPath(extractStringKey(input, FILE_KEYS), worktree),
      state,
      toolName: rawName,
    };
  }

  // 6. File read.
  if (READ_NAMES.has(name)) {
    return {
      verb: "read",
      target: formatPath(extractStringKey(input, FILE_KEYS), worktree),
      state,
      toolName: rawName,
    };
  }

  // 7. Search / list.
  if (SEARCH_NAMES.has(name) || name.startsWith("search") || name.startsWith("list")) {
    return {
      verb: "search",
      target: firstLineCut(extractStringKey(input, SEARCH_KEYS)),
      state,
      toolName: rawName,
    };
  }

  // 8. Web fetch / search (Codex web_search passes plain string).
  if (WEB_NAMES.has(name)) {
    return {
      verb: "web",
      target: firstLineCut(extractStringKey(input, WEB_KEYS, true)),
      state,
      toolName: rawName,
    };
  }

  // 9. Todo / plan updates.
  if (TODO_NAMES.has(name) || name.includes("todo")) {
    return {
      verb: "todo",
      target: "",
      state,
      toolName: rawName,
    };
  }

  // 10. Subagent dispatch.
  if (AGENT_NAMES.has(name)) {
    return {
      verb: "agent",
      target: firstLineCut(extractStringKey(input, AGENT_KEYS)),
      state,
      toolName: rawName,
    };
  }

  // 11. Fallback.
  return {
    verb: "other",
    target: rawName,
    state,
    toolName: rawName,
  };
}

/** Formats a ToolSummary into its localized plain-language line. */
export function formatToolSummary(t: Translate, summary: ToolSummary): string {
  switch (summary.verb) {
    case "mcp": {
      const tool = summary.mcpTool || summary.target;
      if (summary.boardServer) {
        if (tool === "claim_task") return t("runDialog.tool.claim");
        if (tool === "get_node") return t("runDialog.tool.getNode");
        if (tool === "set_node_status") return t("runDialog.tool.setStatus");
        return t("runDialog.tool.mcp", { server: t("runDialog.tool.board"), tool });
      }
      return t("runDialog.tool.mcp", { server: summary.server || "", tool });
    }
    case "run":
      return t("runDialog.tool.run", { target: summary.target }).trim();
    case "write":
      return t("runDialog.tool.write", { target: summary.target }).trim();
    case "edit": {
      const target = summary.files
        ? t("runDialog.tool.files", { first: summary.files.first, count: summary.files.moreCount })
        : summary.target;
      return t("runDialog.tool.edit", { target }).trim();
    }
    case "read":
      return t("runDialog.tool.read", { target: summary.target }).trim();
    case "search":
      return t("runDialog.tool.search", { target: summary.target }).trim();
    case "web":
      return t("runDialog.tool.web", { target: summary.target }).trim();
    case "todo":
      return t("runDialog.tool.todo");
    case "agent":
      return t("runDialog.tool.agent", { target: summary.target }).trim();
    case "other":
    default:
      return t("runDialog.tool.other", { tool: summary.toolName || summary.target });
  }
}
