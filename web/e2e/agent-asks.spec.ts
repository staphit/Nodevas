import { type Locator, type Page } from "@playwright/test";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { nodevasBinary } from "./database";
import { expect, freshProject, test } from "./fixture";

// Agent interaction against the real binary: an agent raises its hand
// (ask_user: approval, question, choice) or relays a tool-permission prompt
// (approve_tool: allow / allow for this run / deny), a person answers in the
// web UI, and the answer reaches the live agent.
//
// The agent is real too: `nodevas mcp`, the very sidecar Claude Code,
// Antigravity and OpenCode start, spoken to over stdio JSON-RPC exactly as an
// agent CLI speaks to it. Its tool call blocks on the hub's long poll until
// the person answers, so every test reads back what the agent was told.
//
// Two things the MCP tools cannot do are done on the HTTP route they post to
// (POST /api/agents/asks, the same body, as an agent's request): ask with a
// short timeoutSeconds (the tools always wait their kind's full limit), for
// the countdown and the expired state.
//
// Only the runner list is answered here: these servers run without a runner
// (--no-runner), and a runner's settings are what the relay row shows.

interface Ask {
  id: string;
  kind?: string;
  status: string;
  sessionName: string;
  question: string;
  tool?: string;
  toolInput?: string;
  answer?: string;
  note?: string;
  expiresAt?: number;
  createdAt: number;
}

interface ToolResult {
  content?: { type: string; text?: string }[];
  structuredContent?: Record<string, unknown>;
  isError?: boolean;
}

/**
 * One agent session: `nodevas mcp` against the worker's server, as an agent
 * CLI starts it, and a minimal MCP client on its stdio.
 */
class McpAgent {
  private nextId = 1;
  private buffer = "";
  private pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private stderr = "";

  private constructor(
    private readonly child: ChildProcessWithoutNullStreams,
    private readonly home: string,
    readonly name: string,
  ) {
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => this.read(chunk));
    child.stderr.on("data", (chunk: Buffer) => {
      this.stderr += chunk.toString();
    });
    child.on("exit", (code) => {
      for (const { reject } of this.pending.values()) {
        reject(new Error(`nodevas mcp exited (${code}): ${this.stderr}`));
      }
      this.pending.clear();
    });
  }

  static async start(baseURL: string, project: string, name: string): Promise<McpAgent> {
    // Its own empty home: the sidecar never reads the developer's config.
    const home = mkdtempSync(join(tmpdir(), "nodevas-e2e-agent-"));
    const env: NodeJS.ProcessEnv = {
      ...process.env,
      APPDATA: home,
      LOCALAPPDATA: home,
      XDG_CONFIG_HOME: home,
      HOME: home,
      USERPROFILE: home,
      // Who the sidecar says it is: a Claude Code session of its own, never
      // the session this test happens to run in.
      CLAUDE_CODE_SESSION_ID: randomUUID(),
    };
    for (const key of [
      "NODEVAS_RUN_MCP",
      "NODEVAS_MODEL",
      "CODEX_THREAD_ID",
      "OPENCODE_SESSION_ID",
      "GEMINI_SESSION_ID",
      "COPILOT_AGENT_SESSION_ID",
      "GROK_SESSION_ID",
      "NODEVAS_SESSION_ID",
    ]) {
      delete env[key];
    }
    const child = spawn(
      nodevasBinary(),
      [
        "mcp",
        "--server",
        baseURL,
        "--project",
        project,
        "--actor",
        `mcp:${name}`,
        "--session-name",
        name,
        "--model",
        "sonnet",
      ],
      { stdio: ["pipe", "pipe", "pipe"], env },
    );
    const agent = new McpAgent(child, home, name);
    await agent.request("initialize", {
      protocolVersion: "2025-06-18",
      capabilities: {},
      clientInfo: { name: "nodevas-e2e", version: "1.0.0" },
    });
    agent.send({ jsonrpc: "2.0", method: "notifications/initialized" });
    return agent;
  }

  private read(chunk: string) {
    this.buffer += chunk;
    let at: number;
    while ((at = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, at).trim();
      this.buffer = this.buffer.slice(at + 1);
      if (!line) continue;
      const message = JSON.parse(line) as { id?: number; result?: unknown; error?: { message: string } };
      if (typeof message.id !== "number") continue;
      const waiter = this.pending.get(message.id);
      if (!waiter) continue;
      this.pending.delete(message.id);
      if (message.error) waiter.reject(new Error(message.error.message));
      else waiter.resolve(message.result);
    }
  }

  private send(message: Record<string, unknown>) {
    this.child.stdin.write(`${JSON.stringify(message)}\n`);
  }

  private request(method: string, params: Record<string, unknown>): Promise<unknown> {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.send({ jsonrpc: "2.0", id, method, params });
    });
  }

  /** Calls a tool; resolves when the hub has the person's answer. */
  call(name: string, args: Record<string, unknown>): Promise<ToolResult> {
    return this.request("tools/call", { name, arguments: args }) as Promise<ToolResult>;
  }

  /** ask_user, resolving to what the agent is told. */
  async askUser(args: Record<string, unknown>): Promise<{ status: string; answer?: string; note?: string }> {
    const result = await this.call("ask_user", args);
    expect(result.isError, JSON.stringify(result)).toBeFalsy();
    if (result.structuredContent) return result.structuredContent as { status: string };
    return JSON.parse(result.content?.[0]?.text ?? "{}");
  }

  /** approve_tool as Claude Code's --permission-prompt-tool calls it,
   * resolving to the reply the CLI parses. */
  async approveTool(toolName: string, input: Record<string, unknown>) {
    const result = await this.call("approve_tool", { tool_name: toolName, input, tool_use_id: randomUUID() });
    expect(result.content).toHaveLength(1);
    return JSON.parse(result.content![0].text!) as {
      behavior: "allow" | "deny";
      updatedInput?: Record<string, unknown>;
      message?: string;
    };
  }

  async stop() {
    this.child.stdin.end();
    this.child.kill();
    await new Promise((resolve) => {
      if (this.child.exitCode !== null) return resolve(undefined);
      this.child.once("exit", resolve);
      setTimeout(resolve, 5_000);
    });
    try {
      rmSync(this.home, { recursive: true, force: true });
    } catch {
      /* best effort */
    }
  }
}

let agents: McpAgent[] = [];

test.afterEach(async () => {
  await Promise.all(agents.map((agent) => agent.stop()));
  agents = [];
});

async function startAgent(page: Page, project: string, name = "e2e-claude") {
  const agent = await McpAgent.start(new URL(page.url()).origin, project, name);
  agents.push(agent);
  return agent;
}

/** The pending ask the hub holds that matches, once it holds one. */
async function pendingAsk(page: Page, match: (ask: Ask) => boolean): Promise<Ask> {
  let found: Ask | undefined;
  await expect
    .poll(
      async () => {
        const res = await page.request.get("/api/agents/asks?status=pending");
        const { asks } = (await res.json()) as { asks: Ask[] };
        found = asks.find(match);
        return Boolean(found);
      },
      { timeout: 15_000 },
    )
    .toBe(true);
  return found!;
}

/** An ask posted on the route ask_user and approve_tool post to, as the
 * agent's request, with a timeout of its own. Resolves when it settles. */
async function askOverHttp(page: Page, body: Record<string, unknown>): Promise<Ask> {
  const res = await fetch(`${new URL(page.url()).origin}/api/agents/asks`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Nodevas-Agent-Role": "worker" },
    body: JSON.stringify({
      sessionId: "claude:e2e-timer",
      sessionName: "e2e-timer",
      actor: "mcp:e2e-timer",
      agent: "claude",
      ...body,
    }),
  });
  expect(res.ok, await res.clone().text()).toBeTruthy();
  return ((await res.json()) as { ask: Ask }).ask;
}

/** Switches the UI language for this page and reloads into it. */
async function useLanguage(page: Page, language: "en" | "zh-TW", extra: Record<string, string> = {}) {
  await page.addInitScript(
    ({ language, extra }) => {
      localStorage.setItem("vised-language", language);
      for (const [key, value] of Object.entries(extra)) localStorage.setItem(key, value);
    },
    { language, extra },
  );
  await page.reload();
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 15_000 });
}

const cardOf = (page: Page, ask: Pick<Ask, "id">): Locator => page.locator(`[data-ask-id="${ask.id}"]`).first();

test("approval: approve and deny reach the agent; settled cards stay with their result (en)", async ({ page }) => {
  const project = await freshProject(page, ["Deploy"]);
  await useLanguage(page, "en");
  const agent = await startAgent(page, project);

  const approved = agent.askUser({ question: "Deploy to staging first?" });
  const first = await pendingAsk(page, (ask) => ask.question === "Deploy to staging first?");
  expect(first.kind ?? "approval").toBe("approval");
  // The panel opens itself on the new ask.
  const panel = page.locator("#agent-network-panel");
  await expect(panel).toBeVisible({ timeout: 15_000 });
  const section = panel.getByRole("region", { name: "Pending human approvals" });
  const card = cardOf(page, first);
  await expect(section.locator(`[data-ask-id="${first.id}"]`)).toBeVisible();
  await expect(card.getByText("e2e-claude asks for approval")).toBeVisible();
  await expect(card).toContainText("Deploy to staging first?");
  await expect(card.getByText(/^Expires in \d+:\d\d$/)).toBeVisible();
  await card.getByRole("button", { name: /^Approve/ }).click();
  expect((await approved).status).toBe("approved");
  await expect(card.locator(".agent-ask-status-approved")).toHaveText("Approved");
  await expect(card.getByRole("button")).toHaveCount(0);

  const denied = agent.askUser({ question: "Drop the legacy table?" });
  const second = await pendingAsk(page, (ask) => ask.question === "Drop the legacy table?");
  const secondCard = cardOf(page, second);
  await expect(secondCard).toBeVisible();
  await secondCard.getByPlaceholder("Optional note for the agent…").fill("not before the backup");
  await secondCard.getByRole("button", { name: /^Deny/ }).click();
  const told = await denied;
  expect(told.status).toBe("denied");
  expect(told.note).toBe("not before the backup");
  await expect(secondCard.locator(".agent-ask-status-denied")).toHaveText("Denied");
  await expect(secondCard.getByText("Note: not before the backup")).toBeVisible();
  // The first one is still there with its result.
  await expect(card.locator(".agent-ask-status-approved")).toHaveText("Approved");
});

test("question and choice: send a text answer, 不回答, pick an option (zh-TW)", async ({ page }) => {
  const project = await freshProject(page, ["設計稿"]);
  const agent = await startAgent(page, project);
  const panel = page.locator("#agent-network-panel");

  // A question: the textarea's answer goes back as the tool's answer.
  const answered = agent.askUser({ kind: "question", question: "要用哪個分支？" });
  const question = await pendingAsk(page, (ask) => ask.question === "要用哪個分支？");
  expect(question.kind).toBe("question");
  await expect(panel).toBeVisible({ timeout: 15_000 });
  const card = cardOf(page, question);
  await expect(card.getByText("e2e-claude 提出問題")).toBeVisible();
  const send = card.getByRole("button", { name: "送出" });
  await expect(send).toBeDisabled();
  await card.getByPlaceholder("你的回答…").fill("  feat/ask-e2e  ");
  await send.click();
  const told = await answered;
  expect(told.status).toBe("answered");
  expect(told.answer).toBe("feat/ask-e2e");
  await expect(card.locator(".agent-ask-status-answered")).toHaveText("已回答");
  await expect(card.getByText("回答：feat/ask-e2e")).toBeVisible();

  // Another question, dismissed: the agent is told nobody will answer.
  const dismissed = agent.askUser({ kind: "question", question: "週末要部署嗎？" });
  const second = await pendingAsk(page, (ask) => ask.question === "週末要部署嗎？");
  const secondCard = cardOf(page, second);
  await secondCard.getByRole("button", { name: "不回答" }).click();
  expect((await dismissed).status).toBe("denied");
  await expect(secondCard.locator(".agent-ask-status-denied")).toHaveText("已拒絕");

  // A choice: one button per option; the one clicked is the answer.
  const chosen = agent.askUser({ kind: "choice", question: "主色用哪個？", options: ["藍色", "綠色", "橘色"] });
  const choice = await pendingAsk(page, (ask) => ask.question === "主色用哪個？");
  expect(choice.kind).toBe("choice");
  const choiceCard = cardOf(page, choice);
  await expect(choiceCard.getByText("e2e-claude 請你選擇")).toBeVisible();
  for (const option of ["藍色", "綠色", "橘色", "不回答"]) {
    await expect(choiceCard.getByRole("button", { name: option, exact: true })).toBeVisible();
  }
  await choiceCard.getByRole("button", { name: "綠色", exact: true }).click();
  const picked = await chosen;
  expect(picked.status).toBe("answered");
  expect(picked.answer).toBe("綠色");
  await expect(choiceCard.getByText("回答：綠色")).toBeVisible();

  // All three stay, newest first after the pending ones, with their results.
  for (const ask of [question, second, choice]) await expect(cardOf(page, ask)).toBeVisible();
});

test("permission: tool, collapsible input, Allow / Allow for this run / Deny and keys 1/2/3 (zh-TW)", async ({ page }) => {
  const project = await freshProject(page, ["設計稿"]);
  const agent = await startAgent(page, project);
  const panel = page.locator("#agent-network-panel");

  /** Relays one prompt and returns its card and what the CLI will be told. */
  const relay = async (tool: string, input: Record<string, unknown>) => {
    const reply = agent.approveTool(tool, input);
    const ask = await pendingAsk(page, (a) => a.kind === "permission" && a.toolInput === JSON.stringify(input));
    await expect(panel).toBeVisible({ timeout: 15_000 });
    const card = cardOf(page, ask);
    await expect(card).toBeVisible();
    return { ask, card, reply };
  };

  // Allow: the card names the tool, in the person's language — the hub's
  // own English question ("Allow Bash?") never shows.
  const bash = await relay("Bash", { command: "npm test -- --run agents" });
  expect(bash.ask.question).toBe("Allow Bash?");
  await expect(bash.card.getByText("e2e-claude 請求工具權限")).toBeVisible();
  await expect(bash.card.locator(".copilot-permission-summary")).toHaveText("e2e-claude 想要執行 Bash");
  await expect(bash.card.locator(".copilot-badge", { hasText: /^Bash$/ })).toBeVisible();
  await expect(bash.card).not.toContainText("Allow Bash?");
  await expect(bash.card).not.toContainText("wants to run");
  // The input is folded until asked for.
  const input = bash.card.locator("details pre");
  await expect(input).toBeHidden();
  await bash.card.getByText("輸入", { exact: true }).click();
  await expect(input).toBeVisible();
  await expect(input).toHaveText('{"command":"npm test -- --run agents"}');
  await bash.card.getByText("輸入", { exact: true }).click();
  await expect(input).toBeHidden();
  await bash.card.getByRole("button", { name: /^允許/ }).click();
  expect(await bash.reply).toEqual({ behavior: "allow", updatedInput: { command: "npm test -- --run agents" } });
  await expect(bash.card.locator(".agent-ask-status-approved")).toHaveText("已允許");
  await expect(bash.card.getByRole("button")).toHaveCount(0);

  // Allow for this run.
  const edit = await relay("Edit", { file_path: "web/src/a.ts", old_string: "a", new_string: "b" });
  await edit.card.getByRole("button", { name: /^本次執行一律允許/ }).click();
  expect((await edit.reply).behavior).toBe("allow");
  await expect(edit.card.locator(".agent-ask-status-approved")).toHaveText("本次執行一律允許");

  // Deny: the CLI is told who said no.
  const rm = await relay("Bash", { command: "rm -rf build" });
  await rm.card.getByRole("button", { name: /^拒絕/ }).click();
  const no = await rm.reply;
  expect(no.behavior).toBe("deny");
  expect(no.message).toBeTruthy();
  await expect(rm.card.locator(".agent-ask-status-denied")).toHaveText("已拒絕");

  // The keys answer once the card has focus: 1 allow, 2 allow for this run,
  // 3 deny.
  const keyed: [string, string, "allow" | "deny", string][] = [
    ["1", "Read", "allow", "已允許"],
    ["2", "Write", "allow", "本次執行一律允許"],
    ["3", "WebFetch", "deny", "已拒絕"],
  ];
  for (const [key, tool, behavior, badge] of keyed) {
    const prompt = await relay(tool, { key });
    await prompt.card.focus();
    await page.keyboard.press(key);
    expect((await prompt.reply).behavior).toBe(behavior);
    await expect(prompt.card.locator(".agent-ask-result .copilot-badge")).toHaveText(badge);
  }

  // Every settled prompt is still listed (the panel keeps the last five).
  await expect(panel.locator(".agent-ask-kind-permission")).toHaveCount(5);
});

test("countdown follows expiresAt; an expired ask stays, marked expired (en)", async ({ page }) => {
  test.setTimeout(60_000);
  await freshProject(page, ["Deploy"]);
  await useLanguage(page, "en");
  const panel = page.locator("#agent-network-panel");

  // A question that waits 75 s, not the kind's 10 minutes.
  const longer = askOverHttp(page, { kind: "question", question: "Which region?", timeoutSeconds: 75 });
  const question = await pendingAsk(page, (ask) => ask.question === "Which region?");
  expect(question.expiresAt! - question.createdAt).toBe(75_000);
  await expect(panel).toBeVisible({ timeout: 15_000 });
  await expect(cardOf(page, question).locator(".agent-ask-countdown")).toHaveText(/^Expires in 1:(0\d|1[0-5])$/);

  // A permission prompt that waits 6 s, not the kind's 5 minutes; it runs
  // out unanswered.
  const expiring = askOverHttp(page, {
    kind: "permission",
    tool: "Bash",
    toolInput: '{"command":"make deploy"}',
    question: "Allow Bash?",
    timeoutSeconds: 6,
  });
  const prompt = await pendingAsk(page, (ask) => ask.kind === "permission" && ask.tool === "Bash");
  expect(prompt.expiresAt! - prompt.createdAt).toBe(6_000);
  const card = cardOf(page, prompt);
  await expect(card.locator(".agent-ask-countdown")).toHaveText(/^Expires in 0:0[0-6]$/);
  await expect(card.getByRole("button", { name: /^Allow/ }).first()).toBeVisible();
  expect((await expiring).status).toBe("expired");
  // Settled, it stays: marked expired, nothing left to press, no countdown.
  await expect(card.locator(".agent-ask-status-expired")).toHaveText("Expired", { timeout: 10_000 });
  await expect(card.getByRole("button")).toHaveCount(0);
  await expect(card.locator(".agent-ask-countdown")).toHaveCount(0);
  await expect(card).toContainText("e2e-timer wants to run Bash");

  // The question is still waiting; answer it so nothing is left hanging.
  await cardOf(page, question).getByPlaceholder("Your answer…").fill("eu-west");
  await cardOf(page, question).getByRole("button", { name: "Send" }).click();
  expect((await longer).answer).toBe("eu-west");
});

test("Station: Waiting on you and the war room list a permission ask, each with its own dialog (en)", async ({ page }) => {
  const project = await freshProject(page, ["Deploy"]);
  await useLanguage(page, "en", { "vised-project-mode": "station" });
  const agent = await startAgent(page, project);

  const reply = agent.approveTool("Bash", { command: "git push origin feat/ask-e2e" });
  const ask = await pendingAsk(page, (a) => a.kind === "permission");

  // Waiting on you: who wants to run what, and the input under it.
  const waiting = page.getByRole("region", { name: "Waiting on you" });
  const row = waiting.locator(".station-tile, li, [role=listitem]").filter({ hasText: "e2e-claude wants to run Bash" }).first();
  await expect(waiting.getByText("e2e-claude wants to run Bash")).toBeVisible({ timeout: 15_000 });
  await expect(waiting).toContainText("git push origin feat/ask-e2e");
  await expect(waiting).not.toContainText("Allow Bash?");
  await row.getByRole("button", { name: "Answer" }).click();
  const dialog = page.getByRole("dialog", { name: "Review permission" });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator(`[data-ask-id="${ask.id}"]`)).toContainText("e2e-claude wants to run Bash");
  await expect(dialog.getByRole("button", { name: /^Allow for this run/ })).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toHaveCount(0);

  // The war room: the agent sits in the meeting room with its hand up.
  const room = page.getByRole("region", { name: "War room" });
  const asking = room.getByRole("button", { name: /waiting for your answer/ });
  await expect(asking).toBeVisible({ timeout: 15_000 });
  await expect(asking).toHaveAccessibleName(/e2e-claude wants to run Bash/);
  await expect(asking).not.toHaveAccessibleName(/Allow Bash\?/);
  await asking.click();
  const item = page.getByRole("menuitem", { name: /^(Review permission|Answer question)$/ });
  await expect(item).toBeVisible();
  await item.click();
  const fromRoom = page.getByRole("dialog", { name: "Review permission" });
  await expect(fromRoom).toBeVisible();
  await fromRoom.getByRole("button", { name: /^Allow/ }).first().click();
  await expect(fromRoom).toHaveCount(0);
  expect((await reply).behavior).toBe("allow");
  await expect(waiting.getByText("e2e-claude wants to run Bash")).toHaveCount(0);
});

test("runner details show the permission relay read-only, with the command to change it (zh-TW)", async ({ page }) => {
  const status = await (await page.request.get("/api/auth/status")).json();
  const runner = (id: string, name: string, permissionRelay: boolean) => ({
    id,
    name,
    ownerId: status.actor?.id ?? "",
    ownerName: "",
    createdAt: "2026-10-23T00:00:00Z",
    lastSeenAt: new Date().toISOString(),
    online: true,
    info: {
      version: "1.4.0",
      hostname: `${name}-host`,
      os: "windows",
      arch: "amd64",
      capabilities: ["headless"],
      agents: [{ kind: "claude", installed: true, version: "2.1.0 (Claude Code)", models: [] }],
      repos: [],
      permissionRelay,
    },
  });
  await page.route("**/api/runners", (route) =>
    route.fulfill({ json: { runners: [runner("r-on", "desktop", true), runner("r-off", "laptop", false)] } }),
  );
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [] } }));
  await freshProject(page, ["設計稿"]);
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("nodevas-open-runners")));

  for (const [id, name, state] of [
    ["r-on", "desktop", "開啟"],
    ["r-off", "laptop", "關閉"],
  ] as const) {
    await page.getByRole("button", { name: `${name} 的詳細資料` }).click();
    const relay = page.getByTestId(`runner-relay-${id}`);
    await expect(relay).toBeVisible();
    await expect(relay).toContainText(`轉發權限請求至 Nodevas：${state}`);
    await expect(relay.locator(".runner-policy-state")).toHaveText(state);
    // Read-only: nothing to toggle here; the command is what changes it.
    await expect(relay.getByRole("checkbox")).toHaveCount(0);
    await expect(relay.getByRole("switch")).toHaveCount(0);
    await expect(relay.getByRole("button")).toHaveCount(0);
    const command = relay.locator("code");
    await expect(command).toHaveText("nodevas runner set permissionRelay true");
    // Copyable: plain selectable text.
    expect(await command.evaluate((el) => getComputedStyle(el).userSelect)).not.toBe("none");
    await expect(relay).toContainText("僅適用於 Claude Code、Antigravity 與 OpenCode ACP 執行");
  }
});

test("a permission card fits a phone (390 px) without clipping (zh-TW)", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const project = await freshProject(page, ["設計稿"]);
  const agent = await startAgent(page, project, "e2e-claude-with-a-rather-long-session-name");
  const command = `npx playwright test e2e/agent-asks.spec.ts ${"--grep something-long ".repeat(8)}`;
  const reply = agent.approveTool("Bash", { command });
  const ask = await pendingAsk(page, (a) => a.kind === "permission");

  const card = cardOf(page, ask);
  // At phone width the panel may need opening by hand.
  if (!(await card.isVisible({ timeout: 10_000 }).catch(() => false))) {
    await page.keyboard.press("Control+Shift+A");
  }
  await expect(card).toBeVisible();
  await card.getByText("輸入", { exact: true }).click();
  await expect(card.locator("details pre")).toBeVisible();

  const inside = async (locator: Locator) => {
    const box = await locator.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390 + 1);
  };
  await inside(card);
  for (const name of [/^允許/, /^本次執行一律允許/, /^拒絕/]) {
    const button = card.getByRole("button", { name });
    await expect(button).toBeVisible();
    await inside(button);
  }
  // Nothing in the card pushes it wider than it is.
  expect(await card.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth),
  ).toBeLessThanOrEqual(0);

  await card.getByRole("button", { name: /^拒絕/ }).click();
  expect((await reply).behavior).toBe("deny");
});
