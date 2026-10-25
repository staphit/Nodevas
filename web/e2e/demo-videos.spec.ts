/**
 * Not a test: the README's feature videos.
 *
 * Each test drives the real app through one feature while Playwright records
 * the page (see playwright.demo.config.ts). The hub routes a runner would feed
 * — runners, runs, autopilot, usage, agents, the second factor — are answered
 * here, and pushes are injected into the app's real WebSocket, so a run can be
 * seen starting, streaming and finishing without a runner or an agent.
 *
 * Playwright's recording has no mouse pointer, so a drawn pointer follows the
 * mouse, and a caption names each step. Run it with:
 *
 *   npm run demo:videos
 */

import { test, expect, type Locator, type Page, type WebSocketRoute } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const QR = readFileSync(join(import.meta.dirname, "demo-assets", "totp-qr.txt"), "utf8").trim();

// ---------------------------------------------------------------------------
// Staging: the pointer, captions, pacing.

async function stage(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("vised-tour-prompt", "declined");
    localStorage.setItem("vised-language", "en");
    const install = () => {
      if (document.getElementById("demo-pointer")) return;
      const style = document.createElement("style");
      style.textContent = `
        #demo-pointer { position: fixed; left: 0; top: 0; width: 22px; height: 22px; z-index: 2147483647;
          pointer-events: none; transform: translate(-100px, -100px); transition: transform 40ms linear; }
        #demo-pointer svg { filter: drop-shadow(0 1px 2px rgba(0,0,0,.45)); }
        #demo-pointer.down::after { content: ""; position: absolute; left: -9px; top: -9px; width: 18px; height: 18px;
          border-radius: 50%; background: rgba(20, 184, 166, .45); }
        #demo-caption { position: fixed; left: 50%; bottom: 22px; transform: translateX(-50%); z-index: 2147483646;
          pointer-events: none; max-width: 80%; padding: 10px 18px; border-radius: 10px; opacity: 0;
          background: rgba(15, 23, 42, .88); color: #f8fafc; font: 600 17px/1.35 system-ui, sans-serif;
          box-shadow: 0 6px 24px rgba(0,0,0,.25); transition: opacity 200ms ease; text-align: center; }
        #demo-caption.on { opacity: 1; }`;
      document.head.appendChild(style);
      const pointer = document.createElement("div");
      pointer.id = "demo-pointer";
      pointer.innerHTML =
        '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2l14 8.5-6.2 1.3 3.6 6.9-2.6 1.4-3.6-6.9L3 17z" fill="#fff" stroke="#0f172a" stroke-width="1.4" stroke-linejoin="round"/></svg>';
      document.body.appendChild(pointer);
      const caption = document.createElement("div");
      caption.id = "demo-caption";
      document.body.appendChild(caption);
      window.addEventListener("mousemove", (event) => {
        pointer.style.transform = `translate(${event.clientX - 3}px, ${event.clientY - 2}px)`;
      }, true);
      window.addEventListener("mousedown", () => pointer.classList.add("down"), true);
      window.addEventListener("mouseup", () => setTimeout(() => pointer.classList.remove("down"), 120), true);
    };
    if (document.body) install();
    else document.addEventListener("DOMContentLoaded", install);
  });
}

async function caption(page: Page, text: string) {
  await page.evaluate((value) => {
    const element = document.getElementById("demo-caption");
    if (!element) return;
    element.textContent = value;
    element.classList.toggle("on", value !== "");
  }, text);
}

const pause = (page: Page, ms: number) => page.waitForTimeout(ms);

/** Moves the drawn pointer to the element the way a hand would, then clicks. */
async function glide(page: Page, target: Locator, options: { click?: boolean } = {}) {
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  if (!box) throw new Error("demo: target has no box");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 18 });
  await pause(page, 180);
  if (options.click !== false) {
    await page.mouse.down();
    await page.mouse.up();
  }
}

/** Scrolls the element into view the way a person would, smoothly. */
async function reveal(target: Locator, block: ScrollLogicalPosition = "start") {
  await target.evaluate(
    (element, where) => element.scrollIntoView({ behavior: "smooth", block: where }),
    block,
  );
}

async function typeSlowly(page: Page, target: Locator, text: string) {
  await glide(page, target);
  await target.pressSequentially(text, { delay: 55 });
}

/** Forwards the app's live socket to the real server and hands back a way to
 * push hub events into the page. */
async function liveSocket(page: Page) {
  let socket: WebSocketRoute | null = null;
  await page.routeWebSocket(/\/ws$/, (ws) => {
    ws.connectToServer();
    socket = ws;
  });
  return (event: Record<string, unknown>) => {
    socket?.send(JSON.stringify(event));
  };
}

async function openBoard(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("Add rate limiting middleware").first()).toBeVisible();
  // Give the graph most of the height: the timeline is not what these show.
  const split = page.getByRole("separator", { name: "Resize graph and timeline height" });
  const box = await split.boundingBox().catch(() => null);
  if (box) {
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2, 700, { steps: 4 });
    await page.mouse.up();
  }
  const fit = page.getByRole("button", { name: "Fit all" }).first();
  if (await fit.isVisible().catch(() => false)) await fit.click();
  await page.mouse.move(640, 420);
  await pause(page, 600);
}

async function nodeIds(page: Page): Promise<Record<string, string>> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Record<string, string> = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

async function openNode(page: Page, title: string) {
  await glide(page, page.getByText(title).first());
  const drawer = page.getByRole("dialog", { name: "Node editor" });
  await expect(drawer).toBeVisible();
  // Widen the drawer so run activity and the autopilot grid have room.
  const handle = page.getByRole("separator", { name: "Resize information panel" });
  const box = await handle.boundingBox().catch(() => null);
  if (box && box.x > 640) {
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 10 });
    await page.mouse.down();
    await page.mouse.move(640, box.y + box.height / 2, { steps: 12 });
    await page.mouse.up();
  }
  return drawer;
}

// ---------------------------------------------------------------------------
// Hub fixtures.

const now = () => new Date().toISOString();

function runnerFor(ownerId: string) {
  return {
    id: "r1",
    name: "desktop",
    ownerId,
    ownerName: "",
    createdAt: "2026-10-20T00:00:00Z",
    lastSeenAt: now(),
    online: true,
    info: {
      version: "1.4.0",
      hostname: "PC-01",
      os: "windows",
      arch: "amd64",
      capabilities: ["headless", "interactive"],
      agents: [
        {
          kind: "opencode",
          installed: true,
          version: "1.18.23",
          models: [
            { id: "default", label: "CLI default", default: true },
            { id: "opencode/qwen3-coder", label: "qwen3-coder" },
          ],
        },
        {
          kind: "claude",
          installed: true,
          version: "2.1.0 (Claude Code)",
          models: [
            { id: "default", label: "CLI default", default: true },
            { id: "opus", label: "Opus" },
          ],
        },
        { kind: "codex", installed: true, version: "0.40.0", models: [{ id: "gpt-5.5", default: true }] },
        { kind: "antigravity", installed: true, version: "1.4.7", models: [{ id: "default", default: true }] },
      ],
      repos: [{ id: "repo1", name: "public-api", path: "C:\\work\\public-api" }],
    },
  };
}

const runBase = {
  id: "run1",
  project: "public-api",
  runnerId: "r1",
  runnerName: "desktop",
  agent: "opencode",
  model: "opencode/qwen3-coder",
  repoId: "repo1",
  repoName: "public-api",
  base: "main",
  branch: "agent/add-rate-limiting-run1",
  prompt: "",
  exitCode: 0,
  createdBy: "u1",
  createdByName: "patrick",
};

// What the runner's ACP adapter (internal/runner/adapter/acp.go) reports for
// an OpenCode run: the handshake, the agent's reading and editing, and the
// one shell command it asked for and was refused, since nobody is at a
// headless run to say yes.
const runEvents = [
  { type: "log", text: "worktree created at C:\\runner\\worktrees\\run1 on agent/add-rate-limiting-run1" },
  { type: "log", text: "ACP: opencode 1.18.23, protocol version 1" },
  { type: "log", text: "opencode session started ses_3f2a9c41d7" },
  { type: "thinking", text: "The /api routes are registered in internal/server/routes.go. Read it before adding the limiter." },
  { type: "tool_call", tool: "read", toolId: "t1", input: JSON.stringify({ filePath: "internal/server/routes.go" }) },
  { type: "tool_result", toolId: "t1", text: "package server\n\nfunc (s *Server) routes() {\n\ts.mux.Handle(\"/api/\", s.api())\n}" },
  { type: "tool_call", tool: "edit", toolId: "t2", input: JSON.stringify({ filePath: "internal/server/routes.go", oldString: "s.api()", newString: "s.limit(s.api())" }) },
  { type: "tool_result", toolId: "t2", text: "Edit applied successfully." },
  { type: "tool_call", tool: "write", toolId: "t3", input: JSON.stringify({ filePath: "internal/server/limit.go" }) },
  { type: "tool_result", toolId: "t3", text: "Wrote 48 lines." },
  { type: "log", text: "opencode asked to run go test ./internal/server/...; the runner refused (headless: nobody to ask)" },
  { type: "text", text: "Added a per-key token bucket in front of every /api route. Over the limit, a request gets 429 with Retry-After.\n\nThe runner did not let me run the tests; CI will." },
  { type: "usage", usage: { inputTokens: 38211, outputTokens: 2904, cacheReadTokens: 12416, costUsd: 0 } },
].map((event, index) => ({ seq: index + 1, at: now(), ...event }));

const patch = [
  "diff --git a/internal/server/limit.go b/internal/server/limit.go",
  "new file mode 100644",
  "index 0000000..9c1e2f4",
  "--- /dev/null",
  "+++ b/internal/server/limit.go",
  "@@ -0,0 +1,12 @@",
  "+package server",
  "+",
  "+// limit answers 429 once a key has spent its bucket for the window.",
  "+func (s *Server) limit(next http.Handler) http.Handler {",
  "+\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {",
  "+\t\tif wait, ok := s.quota.Take(apiKey(r)); !ok {",
  "+\t\t\tw.Header().Set(\"Retry-After\", strconv.Itoa(int(wait.Seconds())))",
  "+\t\t\thttp.Error(w, \"rate limited\", http.StatusTooManyRequests)",
  "+\t\t\treturn",
  "+\t\t}",
  "+\t\tnext.ServeHTTP(w, r)",
  "+\t})",
  "+}",
  "diff --git a/internal/server/routes.go b/internal/server/routes.go",
  "index 3b18e51..a9c2f04 100644",
  "--- a/internal/server/routes.go",
  "+++ b/internal/server/routes.go",
  "@@ -1,5 +1,5 @@ package server",
  " package server",
  " ",
  " func (s *Server) routes() {",
  "-\ts.mux.Handle(\"/api/\", s.api())",
  "+\ts.mux.Handle(\"/api/\", s.limit(s.api()))",
  " }",
  "",
].join("\n");

const changes = {
  commit: "c0ffee1",
  base: "main",
  files: [
    { path: "internal/server/limit.go", status: "added", additions: 13, deletions: 0 },
    { path: "internal/server/routes.go", status: "modified", additions: 1, deletions: 1 },
  ],
  additions: 14,
  deletions: 1,
  patch,
};

// ---------------------------------------------------------------------------

test.beforeEach(async ({ page }) => {
  await stage(page);
  // demo-serve.mjs seeds one project; make sure it is the one on screen.
  const projects = await (await page.request.get("/api/projects")).json();
  if (projects.active !== "public-api") {
    const open = await page.request.post("/api/projects/open", { data: { name: "public-api" } });
    expect(open.ok()).toBeTruthy();
  }
});

test("agent-run", async ({ page }) => {
  const push = await liveSocket(page);
  const status = await (await page.request.get("/api/auth/status")).json();
  const owner = status.actor?.id ?? "";
  const ids = await nodeIds(page);
  const nodeId = ids["Add rate limiting middleware"];

  type Phase = "none" | "offered" | "running" | "done" | "merging" | "merged";
  let phase: Phase = "none";
  let shown = 0;
  const run = () => {
    const base = { ...runBase, nodeId, createdAt: now() };
    switch (phase) {
      case "offered":
        return { ...base, status: "offered" };
      case "running":
        return { ...base, status: "running" };
      default:
        return {
          ...base,
          status: "succeeded",
          summary: "Added a per-key token bucket in front of /api.",
          finishedAt: now(),
          commit: "c0ffee1",
          filesChanged: 2,
          additions: 14,
          deletions: 1,
          prUrl: "https://github.com/example/public-api/pull/42",
          prNumber: 42,
          prState: phase === "merged" ? "merged" : "open",
          prHeadSha: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
          ciStatus: "success",
          usage: { inputTokens: 38211, outputTokens: 2904, cacheReadTokens: 12416, costUsd: 0 },
          ...(phase === "merging"
            ? { merge: { id: "m1", runId: "run1", headSha: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", status: "pending", requestedBy: "patrick", createdAt: now() } }
            : {}),
        };
    }
  };
  const list = () => (phase === "none" ? [] : [run()]);

  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [runnerFor(owner)] } }));
  await page.route(/\/api\/runs\/route\?/, (route) => route.fulfill({ status: 404, json: { error: "no rule" } }));
  await page.route(/\/api\/runs\/routing/, (route) => route.fulfill({ json: { rules: [] } }));
  await page.route(/\/api\/runs\/budget/, (route) => route.fulfill({ json: { limit: "", used: {} } }));
  await page.route(/\/api\/runs$/, async (route) => {
    if (route.request().method() === "POST") {
      phase = "offered";
      return route.fulfill({ status: 201, json: run() });
    }
    return route.fulfill({ json: { runs: list() } });
  });
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: list() } }));
  await page.route(/\/api\/runs\/run1$/, (route) => route.fulfill({ json: run() }));
  await page.route(/\/api\/runs\/run1\/events/, (route) => {
    const after = Number(new URL(route.request().url()).searchParams.get("after") ?? 0);
    return route.fulfill({ json: { events: runEvents.slice(after, shown), more: false } });
  });
  await page.route(/\/api\/runs\/run1\/changes/, (route) => route.fulfill({ json: { changes } }));
  await page.route(/\/api\/runs\/run1\/merge$/, (route) => {
    phase = "merging";
    return route.fulfill({ status: 202, json: run() });
  });
  const changed = () => push({ type: "runs-changed", id: nodeId });

  await openBoard(page);
  await caption(page, "Each node is a task you can hand to an agent on your own PC");
  const drawer = await openNode(page, "Add rate limiting middleware");
  await pause(page, 900);
  await glide(page, drawer.getByRole("tab", { name: "Runs" }));
  await pause(page, 300);
  await reveal(drawer.getByRole("button", { name: "Dispatch to agent…" }), "center");
  await pause(page, 600);

  await caption(page, "Pick a runner, an agent and a model");
  await glide(page, drawer.getByRole("button", { name: "Dispatch to agent…" }));
  const dialog = page.getByRole("dialog", { name: "Dispatch to agent" });
  await expect(dialog).toBeVisible();
  await pause(page, 700);
  const agent = dialog.getByRole("combobox", { name: "Agent", exact: true });
  await glide(page, agent, { click: false });
  await agent.selectOption("opencode");
  await pause(page, 500);
  const model = dialog.getByRole("combobox", { name: "Model", exact: true });
  await glide(page, model, { click: false });
  await model.selectOption("opencode/qwen3-coder");
  await pause(page, 500);
  const extra = dialog.getByRole("textbox", { name: /Extra instructions/ });
  if (await extra.isVisible().catch(() => false)) {
    await typeSlowly(page, extra, "Return 429 with Retry-After.");
  }
  await pause(page, 500);
  await glide(page, dialog.getByRole("button", { name: "Dispatch", exact: true }));
  await expect(dialog).toBeHidden();
  changed();

  await caption(page, "Nothing starts until you accept it at the runner");
  await pause(page, 2200);
  phase = "running";
  changed();
  await caption(page, "OpenCode over ACP: every step streams to the node");
  const activity = drawer.getByRole("list", { name: "Run activity" });
  await expect(activity).toBeVisible();
  await reveal(drawer.getByRole("region", { name: "Run details" }));
  for (let i = 1; i <= runEvents.length; i++) {
    shown = i;
    push({ type: "run-events", id: "run1" });
    await pause(page, 150);
    await reveal(activity.locator(":scope > *").last(), "end");
    await pause(page, i === 11 ? 1800 : 750);
    if (i === 11) await caption(page, "Shell commands are refused: nobody is there to approve them");
  }
  await pause(page, 1200);

  phase = "done";
  changed();
  await caption(page, "The runner commits, pushes and opens a pull request");
  await pause(page, 300);
  await reveal(drawer.getByRole("region", { name: "Run details" }));
  await pause(page, 2400);

  await caption(page, "Read the diff before you merge");
  await glide(page, drawer.getByRole("button", { name: "View changes" }));
  const diff = page.getByRole("dialog", { name: "Changes" });
  await expect(diff).toBeVisible();
  await pause(page, 2000);
  await glide(page, diff.getByRole("navigation").getByRole("button").nth(1));
  await pause(page, 1600);

  const merge = diff.getByRole("button", { name: "Merge PR #42" });
  await glide(page, merge);
  const confirm = page.getByRole("alertdialog", { name: "Merge PR #42?" });
  await expect(confirm).toBeVisible();
  await pause(page, 1600);
  await glide(page, confirm.getByRole("button", { name: "Merge PR #42" }));
  changed();
  await pause(page, 1500);
  phase = "merged";
  changed();
  await caption(page, "");
  await pause(page, 1500);
});

test("autopilot", async ({ page }) => {
  const push = await liveSocket(page);
  const ids = await nodeIds(page);
  const goal = ids["Ship the rate-limited public API"];
  const at = now();
  const done = (name: string, extra = {}) => ({ stage: name, status: "success", at, ...extra });
  const pipeline = ["queued", "implement", "commit", "push", "pr", "ci"];
  const reviewers = [
    { agent: "codex", model: "gpt-5.5" },
    { agent: "claude", model: "opus" },
  ];
  const approve = (round: number) => reviewers.map((r) => ({ ...r, verdict: "approve", round }));
  type Task = Record<string, unknown>;
  const task = (title: string, upstream: string[], state: string, stage: string, stages: unknown[], extra: Task = {}): Task => ({
    nodeId: ids[title],
    title,
    state,
    stage,
    try: stages.length ? 1 : 0,
    upstream: upstream.map((u) => ids[u]),
    stages,
    updatedAt: at,
    ...extra,
  });
  const merged = (n: number) => ({ prUrl: `https://github.com/example/public-api/pull/${n}`, ciStatus: "success", votes: approve(1) });

  const steps: Task[][] = [
    [
      task("Design the quota table", [], "running", "implement", [done("queued"), { stage: "implement", status: "running", at }]),
      task("Add rate limiting middleware", ["Design the quota table"], "none", "", []),
      task("Show quota in the dashboard", ["Add rate limiting middleware"], "none", "", []),
      task("Document the limits", ["Add rate limiting middleware"], "none", "", []),
    ],
    [
      task("Design the quota table", [], "success", "done", [...pipeline.map((s) => done(s)), done("review", { round: 1 }), done("merge"), done("done")], merged(51)),
      task("Add rate limiting middleware", ["Design the quota table"], "waiting_review", "review", [...pipeline.map((s) => done(s)), { stage: "review", status: "running", at, round: 1 }], { prUrl: "https://github.com/example/public-api/pull/52", ciStatus: "success" }),
      task("Show quota in the dashboard", ["Add rate limiting middleware"], "none", "", []),
      task("Document the limits", ["Add rate limiting middleware"], "none", "", []),
    ],
    [
      task("Design the quota table", [], "success", "done", [...pipeline.map((s) => done(s)), done("review", { round: 1 }), done("merge"), done("done")], merged(51)),
      task("Add rate limiting middleware", ["Design the quota table"], "waiting_human", "human_review", [...pipeline.map((s) => done(s)), { stage: "review", status: "failed", at, round: 1 }, done("revise", { round: 1 }), done("review", { round: 2 }), { stage: "human_review", status: "waiting", at, round: 2 }], { prUrl: "https://github.com/example/public-api/pull/52", ciStatus: "success", votes: [{ ...reviewers[0], verdict: "changes", round: 1 }, { ...reviewers[1], verdict: "approve", round: 1 }, ...approve(2)] }),
      task("Show quota in the dashboard", ["Add rate limiting middleware"], "none", "", []),
      task("Document the limits", ["Add rate limiting middleware"], "none", "", []),
    ],
  ];
  let step = 0;
  const view = () => {
    const tasks = steps[step];
    const count = (state: string) => tasks.filter((t) => t.state === state).length;
    return {
      run: {
        id: "ap1",
        project: "public-api",
        goalNodeId: goal,
        status: "running",
        policy: {
          implement: [{ agent: "opencode", model: "opencode/qwen3-coder" }],
          review: { mode: "agents+human", reviewers, quorum: 2 },
          maxParallel: 2,
          maxRounds: 2,
          autoMerge: true,
        },
        counts: {
          total: tasks.length,
          success: count("success"),
          running: count("running") + count("waiting_review"),
          waiting: count("waiting_human"),
          failed: 0,
          pending: count("none"),
          skipped: 0,
        },
        createdBy: "u1",
        createdByName: "patrick",
        createdAt: at,
        updatedAt: now(),
      },
      tasks,
    };
  };
  await page.route(/\/api\/autopilot(\?|\/)/, (route) => route.fulfill({ json: view() }));
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [] } }));
  const advance = () => push({ type: "autopilot-changed", id: "ap1", goalNodeId: goal });

  await openBoard(page);
  await caption(page, "Autopilot: a goal, broken into a graph of tasks");
  await pause(page, 1600);
  const drawer = await openNode(page, "Ship the rate-limited public API");
  await pause(page, 600);
  await glide(page, drawer.getByRole("tab", { name: "Autopilot" }));
  await expect(drawer.getByRole("table")).toBeVisible();
  await reveal(drawer.getByRole("table"), "end");
  await caption(page, "Each task runs once its dependencies are done");
  await pause(page, 3000);
  step = 1;
  advance();
  await caption(page, "Implement → CI → review by other models");
  await pause(page, 3200);
  step = 2;
  advance();
  await caption(page, "Requested changes get a revise run, then it waits for you");
  await pause(page, 2500);
  const table = drawer.getByRole("table");
  await glide(page, table.getByRole("row").nth(2), { click: false });
  await pause(page, 2600);
  await caption(page, "");
  await pause(page, 800);
});

test("usage", async ({ page }) => {
  const bucket = (input: number, output: number, cache: number, cost: number, extra = {}) => ({
    inputTokens: input,
    outputTokens: output,
    cacheReadTokens: cache,
    cacheWriteTokens: 0,
    totalTokens: input + output + cache,
    costUsd: cost,
    reportedCostUsd: 0,
    estimatedCostUsd: cost,
    unpricedTokens: 0,
    runs: 0,
    sessions: 0,
    ...extra,
  });
  const byDay = Array.from({ length: 30 }, (_, i) => {
    const date = new Date(Date.UTC(2026, 8, 26 + i));
    const wave = Math.round(40_000 + 30_000 * Math.sin(i / 3) + (i % 4) * 6_000);
    return { key: date.toISOString().slice(0, 10), ...bucket(wave * 3, wave, wave * 6, wave / 40_000) };
  });
  const report = {
    days: 30,
    since: "2026-09-26T00:00:00Z",
    currency: "USD",
    pricesAsOf: "2026-10-06",
    totals: bucket(3_600_000, 1_200_000, 7_200_000, 42.37, { runs: 14, sessions: 6 }),
    byAgent: [
      { key: "claude", ...bucket(2_400_000, 900_000, 6_000_000, 31.2, { runs: 8, sessions: 4 }) },
      { key: "codex", ...bucket(900_000, 250_000, 1_000_000, 9.1, { runs: 4, sessions: 1 }) },
      { key: "opencode", ...bucket(300_000, 50_000, 200_000, 2.07, { runs: 2, sessions: 1 }) },
    ],
    byModel: [
      { key: "claude-opus-5-5", agent: "claude", ...bucket(2_000_000, 800_000, 5_000_000, 29) },
      { key: "claude-haiku-5-5", agent: "claude", ...bucket(400_000, 100_000, 1_000_000, 2.2) },
      { key: "gpt-5.5", agent: "codex", ...bucket(900_000, 250_000, 1_000_000, 9.1) },
      { key: "qwen3-coder", agent: "opencode", ...bucket(300_000, 50_000, 200_000, 2.07) },
    ],
    byDay,
    sessions: [
      {
        id: "claude:abc",
        source: "session",
        name: "public-api-claude",
        actorName: "patrick",
        agent: "claude",
        model: "claude-opus-5-5",
        models: ["claude-opus-5-5", "claude-haiku-5-5"],
        nodeId: "",
        runId: "",
        firstAt: "2026-10-24T01:00:00Z",
        lastAt: "2026-10-24T09:00:00Z",
        ...bucket(500_000, 90_000, 2_000_000, 6.4),
      },
      {
        id: "run-1",
        source: "run",
        name: "Add rate limiting middleware",
        actorName: "",
        agent: "opencode",
        model: "qwen3-coder",
        models: [],
        nodeId: "limiter",
        runId: "run-1",
        firstAt: "2026-10-23T01:00:00Z",
        lastAt: "2026-10-23T02:00:00Z",
        ...bucket(90_000, 12_000, 40_000, 0.82),
      },
    ],
  };
  const prices = {
    asOf: "2026-10-06",
    prices: [
      { model: "claude-opus-5-5", inputPerMTok: 4, outputPerMTok: 20, cacheReadPerMTok: 0.2, cacheWritePerMTok: 5, source: "builtin" },
      { model: "claude-haiku-5-5", inputPerMTok: 0.1, outputPerMTok: 0.5, cacheReadPerMTok: 0.01, cacheWritePerMTok: 0.125, source: "builtin" },
      { model: "gpt-5.5", inputPerMTok: 1.25, outputPerMTok: 10, cacheReadPerMTok: 0.125, cacheWritePerMTok: 0, source: "builtin" },
    ],
  };
  await page.route(/\/api\/usage\/prices/, (route) => route.fulfill({ json: prices }));
  await page.route(/\/api\/usage\?/, (route) => route.fulfill({ json: report }));
  await page.route((url) => url.pathname.startsWith("/api/agents"), (route) =>
    route.fulfill({ json: { sessions: [], users: [], messages: [], asks: [], unreadCount: 0 } }),
  );

  await openBoard(page);
  await caption(page, "Token usage across runs and agent sessions");
  await glide(page, page.getByRole("button", { name: "Open Agent Network" }));
  const panel = page.getByRole("dialog", { name: "Agent Network" });
  await expect(panel).toBeVisible();
  await pause(page, 500);
  await glide(page, panel.getByRole("button", { name: "Usage" }));
  const dialog = page.getByRole("dialog", { name: "Usage" });
  await expect(dialog).toBeVisible();
  await pause(page, 1800);
  await caption(page, "By day, agent and model, as API-equivalent estimates");
  const bars = dialog.locator(".usage-chart-plot .usage-hit");
  for (const index of [8, 14, 21, 27]) {
    await glide(page, bars.nth(index), { click: false });
    await pause(page, 650);
  }
  await pause(page, 600);
  await glide(page, dialog.getByRole("tab", { name: "Sessions" }));
  await pause(page, 2000);
  await caption(page, "Prices are editable when a model is missing");
  await glide(page, dialog.getByRole("tab", { name: "Prices" }));
  await pause(page, 2200);
  await caption(page, "");
  await pause(page, 600);
});

test("agent-network", async ({ page }) => {
  const session = (name: string, agent: string, model: string, doing: string, extra = {}) => ({
    id: `${agent}:${name}`,
    project: "public-api",
    actorId: "",
    actorName: "patrick",
    name,
    agent,
    role: "worker",
    repo: "public-api",
    branch: `feat/${name}`,
    state: "busy",
    since: Date.now() - 600_000,
    doing,
    lastSeen: Date.now(),
    online: true,
    inbound: "open",
    model,
    ...extra,
  });
  const sessions = [
    session("limiter-claude", "claude", "claude-opus-5-5", "Wiring the token bucket into the /api router", { inbound: "hold" }),
    session("docs-codex", "codex", "gpt-5.5", "Writing the rate-limit section of the API docs"),
    session("dashboard-opencode", "opencode", "qwen3-coder", "Adding the quota meter to the keys page", { state: "idle" }),
  ];
  let heldOpen = true;
  const held = {
    id: "h1",
    project: "public-api",
    from: "patrick/docs-codex",
    senderActor: "patrick",
    senderSession: "docs-codex",
    senderAgent: "codex",
    to: "limiter-claude",
    body: "What does the 429 body look like? I'll document the exact shape and the Retry-After header.",
    createdAt: Date.now() - 60_000,
    status: "held",
    files: [],
  };
  const delivered = {
    ...held,
    id: "d1",
    from: "patrick/limiter-claude",
    senderSession: "limiter-claude",
    senderAgent: "claude",
    to: "dashboard-opencode",
    body: "The quota endpoint is GET /api/keys/{id}/quota → {limit, used, resetAt}.",
    status: "delivered",
    createdAt: Date.now() - 300_000,
  };
  await page.route((url) => url.pathname.startsWith("/api/agents"), async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "POST") {
      if (url.pathname.endsWith("/approve")) {
        heldOpen = false;
        return route.fulfill({ json: { ...held, status: "delivered" } });
      }
      return route.fulfill({ json: { ok: true } });
    }
    if (url.pathname === "/api/agents") return route.fulfill({ json: { sessions, users: [], unreadCount: 0 } });
    if (url.pathname === "/api/agents/messages") {
      const wantHeld = url.searchParams.get("status") === "held";
      const all = heldOpen ? [held, delivered] : [{ ...held, status: "delivered" }, delivered];
      return route.fulfill({ json: { messages: wantHeld ? (heldOpen ? [held] : []) : all } });
    }
    if (url.pathname === "/api/agents/asks") return route.fulfill({ json: { asks: [] } });
    return route.fulfill({ json: {} });
  });

  await openBoard(page);
  await caption(page, "Agent network: see who else is working");
  const fab = page.getByRole("button", { name: "Open Agent Network" });
  await glide(page, fab);
  const panel = page.getByRole("dialog", { name: "Agent Network" });
  await expect(panel).toBeVisible();
  await pause(page, 2400);
  await caption(page, "Agents message each other; you can hold a session's inbox");
  const heldSection = panel.getByRole("region", { name: /Held messages/ });
  if (await heldSection.isVisible().catch(() => false)) {
    await glide(page, heldSection, { click: false });
    await pause(page, 1800);
    await glide(page, heldSection.getByRole("button", { name: "Approve" }));
  }
  await pause(page, 2000);
  await caption(page, "");
  await pause(page, 800);
});

test("two-factor-and-device-link", async ({ page }) => {
  // A hub with accounts: the board belongs to a signed-in person whose
  // second factor is set up here.
  const actor = { id: "u1", name: "patrick", email: "patrick@example.com", role: "admin" };
  let totp = { enabled: false, pending: false, recoveryCodesLeft: 0 } as Record<string, unknown>;
  let stepUp: Record<string, unknown> = { required: true, active: true, until: new Date(Date.now() + 600_000).toISOString(), method: "email" };
  await page.route(/\/api\/auth\/status/, (route) =>
    route.fulfill({ json: { mode: "accounts", authenticated: true, actor } }),
  );
  await page.route(/\/api\/auth\/totp$/, (route) => route.fulfill({ json: totp }));
  await page.route(/\/api\/auth\/totp\/begin/, (route) =>
    route.fulfill({
      json: {
        secret: "JBSWY3DPEHPK3PXPDEMOKEY7",
        uri: "otpauth://totp/Nodevas:patrick%40example.com?secret=JBSWY3DPEHPK3PXPDEMOKEY7&issuer=Nodevas",
        qr: QR,
        account: "patrick@example.com",
        issuer: "Nodevas",
        digits: 6,
        period: 30,
      },
    }),
  );
  await page.route(/\/api\/auth\/totp\/confirm/, (route) => {
    totp = { enabled: true, pending: false, enabledAt: now(), recoveryCodesLeft: 10 };
    // From here on, step-up asks the authenticator, not email.
    stepUp = { required: true, active: false, method: "totp" };
    return route.fulfill({
      json: {
        enabled: true,
        recoveryCodes: [
          "K7QM-4XTR-9PWD-2HNA", "B3VJ-8LCE-5RYU-6TMK", "W2ZP-7GHD-3NQX-9FRA", "M5TC-2KYW-8JEV-4PLD", "R9XH-6BNM-3TQZ-7CWE",
          "D4LU-9PSA-2MKX-5HJT", "F8NQ-3WEV-7RZC-2YBK", "H6TK-5JMD-9XLP-3QWA", "P2VE-8CNR-4HTY-6MZU", "Y7AW-3DKQ-5LXN-9BTR",
        ],
      },
    });
  });
  await page.route(/\/api\/auth\/step-up\/request/, (route) => route.fulfill({ json: { method: "totp" } }));
  await page.route(/\/api\/auth\/step-up$/, (route) => {
    if (route.request().method() === "POST") {
      stepUp = { required: true, active: true, until: new Date(Date.now() + 600_000).toISOString(), method: "totp" };
    }
    return route.fulfill({ json: stepUp });
  });
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [runnerFor(actor.id)] } }));
  await page.route(/\/api\/runners\/access-token/, (route) =>
    route.fulfill({ json: { clientId: "3f1c0e7a9b.access", set: true } }),
  );
  await page.route(/\/api\/runners\/links/, (route) => {
    if (stepUp.active !== true) {
      return route.fulfill({ status: 403, json: { error: "step-up required", code: "STEP_UP_REQUIRED" } });
    }
    return route.fulfill({
      json: {
        link: "nvlink1.eyJodWIiOiJodHRwczovL25vZGV2YXMuZXhhbXBsZS5jb20iLCJjb2RlIjoiZGVtbyJ9.demo-only-not-a-real-link",
        name: "laptop",
        hub: "https://nodevas.example.com",
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
        access: true,
      },
    });
  });

  await openBoard(page);
  await caption(page, "Two-factor authentication with an authenticator app");
  await glide(page, page.getByRole("button", { name: "Project settings" }).first());
  await pause(page, 600);
  await glide(page, page.getByRole("tab", { name: /Account security/ }).or(page.getByRole("button", { name: /Account security/ })).first());
  await pause(page, 1200);
  await glide(page, page.getByRole("button", { name: "Set up" }));
  await expect(page.getByRole("img", { name: "QR code for your authenticator app" })).toBeVisible();
  await caption(page, "Scan the QR code, then type the 6-digit code");
  await pause(page, 1800);
  await typeSlowly(page, page.getByLabel("6-digit code from the app"), "482913");
  await pause(page, 400);
  await glide(page, page.getByRole("button", { name: "Turn on" }));
  await caption(page, "Recovery codes are shown once");
  await pause(page, 2600);
  await glide(page, page.getByRole("button", { name: "I've saved them" }));
  await pause(page, 1200);
  await page.keyboard.press("Escape");
  await pause(page, 600);

  await caption(page, "Link a new runner with a one-time device link");
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("nodevas-open-runners")));
  const runners = page.getByRole("dialog", { name: "Runners" });
  await expect(runners).toBeVisible();
  await pause(page, 1200);
  await glide(page, runners.getByRole("button", { name: "Link a new device" }));
  const link = page.getByRole("dialog", { name: "Link a new device" });
  await expect(link).toBeVisible();
  await typeSlowly(page, link.getByLabel("Runner name"), "laptop");
  await pause(page, 400);
  await glide(page, link.getByRole("button", { name: "Create link" }));
  await caption(page, "Step-up now asks the authenticator, not email");
  const verify = page.getByRole("dialog", { name: "Verify it's you" });
  await expect(verify).toBeVisible();
  await pause(page, 1200);
  await typeSlowly(page, verify.getByLabel("Authenticator code"), "730164");
  await pause(page, 300);
  await glide(page, verify.getByRole("button", { name: "Verify" }));
  const done = verify.getByRole("button", { name: "Done" });
  await expect(done).toBeVisible();
  await pause(page, 900);
  await glide(page, done);
  await expect(link.getByText("Link for laptop")).toBeVisible();
  await caption(page, "Paste it into the new machine's nodevas: it works once");
  await pause(page, 3200);
  await caption(page, "");
  await pause(page, 800);
});
