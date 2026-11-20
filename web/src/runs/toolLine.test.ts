import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { formatToolSummary, summarizeTool } from "./toolLine";

const tEn = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);
const tZh = (key: string, values?: Record<string, string | number>) => translate(key, "zh-TW", values);

describe("summarizeTool", () => {
  describe("stored names per Go adapter parser", () => {
    it("summarizes Claude stored tool names (claude.go:457-464)", () => {
      // claude.go:460 stores block.Name verbatim: Bash, Read, Write, Edit, Grep, Glob, WebFetch, WebSearch, TodoWrite, Task
      const bash = summarizeTool(
        { tool: "Bash", input: JSON.stringify({ command: "npm test\necho done" }) },
        { isError: false, text: "ok" },
      );
      expect(bash.verb).toBe("run");
      expect(bash.target).toBe("npm test");
      expect(bash.state).toBe("ok");
      expect(formatToolSummary(tEn, bash)).toBe("Run npm test");
      expect(formatToolSummary(tZh, bash)).toBe("執行 npm test");

      const read = summarizeTool(
        { tool: "Read", input: JSON.stringify({ file_path: "/work/wt/web/src/app.tsx" }) },
        undefined,
        { worktree: "/work/wt" },
      );
      expect(read.verb).toBe("read");
      expect(read.target).toBe("web/src/app.tsx");
      expect(read.state).toBe("running");

      const edit = summarizeTool(
        { tool: "Edit", input: JSON.stringify({ file_path: "/a/b/c/d.ts" }) },
        { isError: false, text: "" },
      );
      expect(edit.verb).toBe("edit");
      expect(edit.target).toBe("c/d.ts");

      const write = summarizeTool(
        { tool: "Write", input: JSON.stringify({ file_path: "/x/y/new.ts" }) },
        { isError: false, text: "" },
      );
      expect(write.verb).toBe("write");
      expect(write.target).toBe("y/new.ts");

      const grep = summarizeTool(
        { tool: "Grep", input: JSON.stringify({ pattern: "RunDialog" }) },
        { isError: false, text: "" },
      );
      expect(grep.verb).toBe("search");
      expect(grep.target).toBe("RunDialog");

      const todo = summarizeTool({ tool: "TodoWrite", input: "{}" }, { isError: false, text: "" });
      expect(todo.verb).toBe("todo");
      expect(formatToolSummary(tEn, todo)).toBe("Update the to-do list");

      const agent = summarizeTool(
        { tool: "Task", input: JSON.stringify({ description: "Explore store\nmore details" }) },
        undefined,
      );
      expect(agent.verb).toBe("agent");
      expect(agent.target).toBe("Explore store");
      expect(formatToolSummary(tEn, agent)).toBe("Send a helper: Explore store");
    });

    it("summarizes Codex stored tool names (codex.go:322-339)", () => {
      // codex.go:326 stores command_execution as "shell" with plain string input
      const shell = summarizeTool(
        { tool: "shell", input: "go test ./internal/runs/..." },
        { isError: false, text: "PASS" },
      );
      expect(shell.verb).toBe("run");
      expect(shell.target).toBe("go test ./internal/runs/...");
      expect(shell.state).toBe("ok");

      // codex.go:356 declined status marks result failed
      const declined = summarizeTool(
        { tool: "shell", input: "rm -rf /tmp/x" },
        { isError: true, text: "(declined by the sandbox policy)" },
      );
      expect(declined.state).toBe("failed");

      // codex.go:337 stores web_search with plain string query input
      const web = summarizeTool(
        { tool: "web_search", input: "codex exec json" },
        { isError: false, text: "searched: codex exec json" },
      );
      expect(web.verb).toBe("web");
      expect(web.target).toBe("codex exec json");
      expect(formatToolSummary(tEn, web)).toBe("Look up codex exec json");

      // codex.go:329 stores file_change as "apply_patch" with "<kind> <path>" lines
      const onePatch = summarizeTool(
        { tool: "apply_patch", input: "update /repo/web/src/App.tsx" },
        { isError: false, text: "" },
      );
      expect(onePatch.verb).toBe("edit");
      expect(onePatch.target).toBe("src/App.tsx");
      expect(formatToolSummary(tEn, onePatch)).toBe("Edit src/App.tsx");

      const multiPatch = summarizeTool(
        {
          tool: "apply_patch",
          input: "update /repo/a/first.ts\nadd /repo/b/second.ts\ndelete /repo/c/third.ts",
        },
        { isError: false, text: "" },
      );
      expect(multiPatch.verb).toBe("edit");
      expect(multiPatch.files).toEqual({ first: "a/first.ts", moreCount: 2 });
      expect(formatToolSummary(tEn, multiPatch)).toBe("Edit a/first.ts and 2 more");
      expect(formatToolSummary(tZh, multiPatch)).toBe("修改 a/first.ts 等 2 個檔案");

      // codex.go:334 stores mcp_tool_call as "mcp__<server>__<tool>"
      const mcp = summarizeTool(
        { tool: "mcp__nodevas__claim_task", input: "{}" },
        { isError: false, text: "" },
      );
      expect(mcp.verb).toBe("mcp");
      expect(formatToolSummary(tEn, mcp)).toBe("Take the task");
    });

    it("summarizes Gemini stored tool names including bare board tools (gemini.go:292)", () => {
      // gemini.go:292 stores msg.ToolName bare: claim_task, get_node, set_node_status, release_task, check_inbox, send_message, run_shell_command, google_web_search
      expect(formatToolSummary(tEn, summarizeTool({ tool: "claim_task", input: "{}" }))).toBe("Take the task");
      expect(formatToolSummary(tEn, summarizeTool({ tool: "get_node", input: "{}" }))).toBe("Read the task");
      expect(formatToolSummary(tEn, summarizeTool({ tool: "set_node_status", input: "{}" }))).toBe("Report status");

      const inbox = summarizeTool({ tool: "check_inbox", input: "{}" }, { isError: false, text: "" });
      expect(inbox.verb).toBe("mcp");
      expect(inbox.boardServer).toBe(true);
      expect(formatToolSummary(tEn, inbox)).toBe("Use check_inbox from the board");
      expect(formatToolSummary(tZh, inbox)).toBe("使用 看板 的 check_inbox");

      const shell = summarizeTool(
        { tool: "run_shell_command", input: JSON.stringify({ command: "git status" }) },
        { isError: false, text: "" },
      );
      expect(shell.verb).toBe("run");
      expect(shell.target).toBe("git status");

      const web = summarizeTool(
        { tool: "google_web_search", input: JSON.stringify({ query: "vitest timers" }) },
        { isError: false, text: "" },
      );
      expect(web.verb).toBe("web");
      expect(web.target).toBe("vitest timers");
    });

    it("summarizes OpenCode stored tool names (opencode.go:381-393)", () => {
      // opencode.go:386 maps nodevas_* -> mcp__nodevas__*, and emits call + result together
      const board = summarizeTool(
        { tool: "mcp__nodevas__send_message", input: "{}" },
        { isError: false, text: "sent" },
      );
      expect(board.verb).toBe("mcp");
      expect(board.state).toBe("ok");
      expect(formatToolSummary(tEn, board)).toBe("Use send_message from the board");

      const strReplace = summarizeTool(
        { tool: "str_replace_editor", input: JSON.stringify({ path: "src/main.ts" }) },
        { isError: false, text: "ok" },
      );
      expect(strReplace.verb).toBe("edit");
      expect(strReplace.target).toBe("src/main.ts");
    });

    it("summarizes Antigravity stored tool names (antigravity.go:449, 485-497)", () => {
      // antigravity.go:485 maps call_mcp_tool -> mcp__<server>__<tool>; native tools stay e.g. run_command, view_file, replace_file_content, find_by_name
      const runCmd = summarizeTool(
        { tool: "run_command", input: JSON.stringify({ CommandLine: "npx vitest run" }) },
        undefined,
      );
      expect(runCmd.verb).toBe("run");
      expect(runCmd.target).toBe("npx vitest run");
      expect(runCmd.state).toBe("running");

      const view = summarizeTool(
        { tool: "view_file", input: JSON.stringify({ AbsolutePath: "/repo/a/b/file.go" }) },
        { isError: false, text: "package main" },
      );
      expect(view.verb).toBe("read");
      expect(view.target).toBe("b/file.go");

      const find = summarizeTool(
        { tool: "find_by_name", input: JSON.stringify({ Query: "*.test.ts" }) },
        { isError: false, text: "" },
      );
      expect(find.verb).toBe("search");
      expect(find.target).toBe("*.test.ts");
    });
  });

  describe("rule order and edge cases", () => {
    it("matches mcp__ before run so mcp__terminal__x is not 'run'", () => {
      const summary = summarizeTool(
        { tool: "mcp__terminal__exec_command", input: JSON.stringify({ command: "ls" }) },
        { isError: false, text: "" },
      );
      expect(summary.verb).toBe("mcp");
      expect(formatToolSummary(tEn, summary)).toBe("Use exec_command from terminal");
    });

    it("unwraps ['bash', '-lc', X] command arrays and joins other arrays", () => {
      const unwrapped = summarizeTool({
        tool: "exec_command",
        input: JSON.stringify({ command: ["bash", "-lc", "npm run build && npm test"] }),
      });
      expect(unwrapped.verb).toBe("run");
      expect(unwrapped.target).toBe("npm run build && npm test");

      const joined = summarizeTool({
        tool: "local_shell",
        input: JSON.stringify({ cmd: ["git", "diff", "--stat"] }),
      });
      expect(joined.target).toBe("git diff --stat");
    });

    it("extracts keys from truncated mid-JSON input via regex fallback", () => {
      const cutCmd = '{"command": "npm run check:api-imports", "extra": "zzzzzz';
      const runSummary = summarizeTool({ tool: "Bash", input: cutCmd });
      expect(runSummary.verb).toBe("run");
      expect(runSummary.target).toBe("npm run check:api-imports");

      const cutFile = '{"file_path": "/work/proj/web/src/runs/toolLine.ts", "content": "very long truncated';
      const writeSummary = summarizeTool({ tool: "write_file", input: cutFile });
      expect(writeSummary.verb).toBe("write");
      expect(writeSummary.target).toBe("runs/toolLine.ts");
    });

    it("cuts long commands to first line and 80 characters", () => {
      const longLine = "echo " + "a".repeat(120) + "\nsecond line";
      const summary = summarizeTool({ tool: "bash", input: JSON.stringify({ command: longLine }) });
      expect(summary.target.length).toBeLessThanOrEqual(80);
      expect(summary.target).not.toContain("\n");
      expect(summary.target.endsWith("…")).toBe(true);
    });

    it("falls back to other for unknown tool names", () => {
      const summary = summarizeTool({ tool: "custom_gizmo", input: "{}" }, { isError: false, text: "" });
      expect(summary.verb).toBe("other");
      expect(formatToolSummary(tEn, summary)).toBe("custom_gizmo");
    });
  });
});
