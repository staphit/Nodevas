/**
 * Not a test: the README's feature videos.
 *
 * Each test drives the real app through one feature and records the page at
 * twice its pixels (see playwright.demo.config.ts). The hub routes a runner
 * would feed — runners, runs, autopilot, usage, agents, the second factor —
 * are answered here, and pushes are injected into the app's real WebSocket, so a run can be
 * seen starting, streaming and finishing without a runner or an agent.
 *
 * The page is only the footage. Alongside the frames the test writes a
 * film.json: a title card, where the camera looks (keyframes in viewport
 * pixels), the captions, and the cuts to cross-fade over. e2e/demo-encode.mjs
 * turns footage and film into the finished video: the app as a window on a
 * dark backdrop, the camera pushing in on what each step is about, and the
 * captions set below the window at output resolution. Real clicks stay real:
 * nothing on the page is transformed.
 *
 * Screenshots have no mouse pointer, so a drawn pointer follows the mouse,
 * ripples on a click and fades out when it is idle. Run it with:
 *
 *   npm run demo:videos
 */

import { test, expect, type Locator, type Page, type WebSocketRoute } from "@playwright/test";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

const QR = readFileSync(join(import.meta.dirname, "demo-assets", "totp-qr.txt"), "utf8").trim();

// ---------------------------------------------------------------------------
// The film: camera, captions and cuts, timed against the recording.

/** The viewport (playwright.demo.config.ts), in CSS pixels: 16:9, like the
 * camera. It is drawn at a device scale of 2, and recorded at that. */
const VW = 1440;
const VH = 810;
const ASPECT = VW / VH;

type Rect = { x: number; y: number; w: number; h: number };
type Film = {
  t0: number;
  viewport: { w: number; h: number };
  frames: string;
  title: { title: string; sub: string } | null;
  begin: number | null;
  end: number | null;
  camera: Array<Rect & { t: number; d: number }>;
  captions: Array<{ t: number; title: string; sub: string }>;
  cuts: Array<{ from: number; to: number }>;
  clicks: Array<{ t: number; x: number; y: number }>;
};

const FULL: Rect = { x: 0, y: 0, w: VW, h: VH };
let film: Film;
let cam: Rect = FULL;
let mouse = { x: VW / 2, y: VH / 2 };

/** Seconds since the recording started (the page was created just before). */
const clock = () => (Date.now() - film.t0) / 1000;
const pause = (page: Page, ms: number) => page.waitForTimeout(ms);
const clamp = (value: number, low: number, high: number) => Math.min(high, Math.max(low, value));
const ease = (k: number) => (k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2);

/** Where the finished video starts: the board is up, the title card is over it. */
async function begin(page: Page, title: string, sub: string) {
  await hidePointer(page);
  await pause(page, 300);
  film.title = { title, sub };
  film.begin = clock();
  cam = FULL;
  film.camera.push({ t: film.begin, d: 0, ...FULL });
}

/** A caption: a short headline and an optional line under it. "" clears. */
function say(title: string, sub = "") {
  film.captions.push({ t: clock(), title, sub });
}

async function boxesOf(target: Locator | Locator[] | Rect): Promise<Rect[]> {
  if (!Array.isArray(target) && "w" in target) return [target];
  const boxes: Rect[] = [];
  for (const locator of Array.isArray(target) ? target : [target]) {
    const box = await locator.boundingBox().catch(() => null);
    if (box) boxes.push({ x: box.x, y: box.y, w: box.width, h: box.height });
  }
  return boxes;
}

/** The camera rectangle that shows `area` with `pad` around it: 16:9, inside
 * the viewport, never tighter than `minW` pixels wide. */
function frameOf(area: Rect, pad: number, minW: number): Rect {
  let x = area.x - pad;
  let y = area.y - pad;
  let w = area.w + 2 * pad;
  let h = area.h + 2 * pad;
  if (w / h < ASPECT) {
    x -= (h * ASPECT - w) / 2;
    w = h * ASPECT;
  } else {
    y -= (w / ASPECT - h) / 2;
    h = w / ASPECT;
  }
  if (w < minW) {
    x -= (minW - w) / 2;
    y -= (minW / ASPECT - h) / 2;
    w = minW;
    h = minW / ASPECT;
  }
  if (w >= VW) return FULL;
  return { x: clamp(x, 0, VW - w), y: clamp(y, 0, VH - h), w, h };
}

/** pad and minW in CSS pixels; `within` clips the targets to a container
 * (content that overflows a scrolling panel is not on screen). */
type FocusOptions = { pad?: number; d?: number; hold?: number; minW?: number; within?: Locator };

/** Moves the camera onto the targets (their union), easing over `d` seconds,
 * and waits for it to arrive. */
async function focus(page: Page, target: Locator | Locator[] | Rect, options: FocusOptions = {}) {
  const { pad = 28, d = 0.8, hold = 0, minW = 560, within } = options;
  const outer = within ? (await boxesOf(within))[0] : FULL;
  const boxes = (await boxesOf(target))
    .map((b) => {
      const x = clamp(b.x, outer.x, outer.x + outer.w);
      const y = clamp(b.y, outer.y, outer.y + outer.h);
      return { x, y, w: clamp(b.x + b.w, outer.x, outer.x + outer.w) - x, h: clamp(b.y + b.h, outer.y, outer.y + outer.h) - y };
    })
    .filter((b) => b.w > 0 && b.h > 0);
  if (boxes.length === 0) throw new Error("demo: nothing to focus on");
  const left = Math.min(...boxes.map((b) => b.x));
  const top = Math.min(...boxes.map((b) => b.y));
  const right = Math.max(...boxes.map((b) => b.x + b.w));
  const bottom = Math.max(...boxes.map((b) => b.y + b.h));
  await setCamera(page, frameOf({ x: left, y: top, w: right - left, h: bottom - top }, pad, minW), d, hold);
}

async function wide(page: Page, d = 0.9, hold = 0) {
  await setCamera(page, FULL, d, hold);
}

async function setCamera(page: Page, rect: Rect, d: number, hold: number) {
  const round = (r: Rect) => ({ x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.w), h: Math.round(r.h) });
  cam = round(rect);
  film.camera.push({ t: clock(), d, ...cam });
  await pause(page, d * 1000 + hold);
}

/** A jump in the UI (a mode switch, a page change): the encoder leaves out
 * what `step` does after the click and cross-fades across it. */
async function cut(page: Page, step: () => Promise<void>) {
  await pause(page, 350);
  const from = clock();
  await step();
  await pause(page, 250);
  film.cuts.push({ from, to: clock() });
}

// ---------------------------------------------------------------------------
// The footage. Playwright's own video is in CSS pixels, too coarse to push in
// on, so the test records the page itself: screenshots at the device scale
// (2x), as fast as the browser hands them over, each stamped with the film's
// clock. The encoder plays them back at those times.

type Recorder = { stop: () => Promise<void> };

async function record(page: Page, dir: string): Promise<Recorder> {
  mkdirSync(dir, { recursive: true });
  const cdp = await page.context().newCDPSession(page);
  const frames: Array<{ file: string; t: number }> = [];
  let running = true;
  const loop = (async () => {
    while (running) {
      const before = clock();
      const shot = await cdp.send("Page.captureScreenshot", { format: "jpeg", quality: 92 }).catch(() => null);
      if (!shot) {
        await page.waitForTimeout(20).catch(() => {});
        continue;
      }
      const file = `${String(frames.length).padStart(5, "0")}.jpg`;
      frames.push({ file, t: (before + clock()) / 2 });
      await writeFile(join(dir, file), Buffer.from(shot.data, "base64"));
    }
  })();
  return {
    stop: async () => {
      running = false;
      await loop;
      await cdp.detach().catch(() => {});
      // ffconcat: each frame shows until the next; the first from zero.
      const lines = ["ffconcat version 1.0"];
      frames.forEach((frame, index) => {
        const next = frames[index + 1]?.t ?? frame.t + 0.04;
        const from = index === 0 ? 0 : frame.t;
        lines.push(`file ${frame.file}`, `duration ${(next - from).toFixed(4)}`);
      });
      if (frames.length > 0) lines.push(`file ${frames[frames.length - 1].file}`);
      writeFileSync(join(dir, "frames.ffconcat"), `${lines.join("\n")}\n`);
    },
  };
}

// ---------------------------------------------------------------------------
// The pointer.

async function stage(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("vised-tour-prompt", "declined");
    localStorage.setItem("vised-language", "en");
    localStorage.setItem("vised-theme", "dark");
    // Runners, runs, autopilot and usage are what these show.
    localStorage.setItem("vised-agent-workstation", "1");
    const install = () => {
      if (document.getElementById("demo-pointer")) return;
      const style = document.createElement("style");
      style.textContent = `
        #demo-pointer { position: fixed; left: 0; top: 0; width: 20px; height: 20px; z-index: 2147483647;
          pointer-events: none; opacity: 0; transform: translate(-100px, -100px); transition: opacity 280ms ease; }
        #demo-pointer.on { opacity: 1; }
        #demo-pointer svg { filter: drop-shadow(0 2px 3px rgba(0,0,0,.45)); }
        .demo-ripple { position: fixed; width: 40px; height: 40px; margin: -20px 0 0 -20px; border-radius: 50%;
          z-index: 2147483646; pointer-events: none; border: 2px solid rgba(255,255,255,.9);
          background: rgba(255,255,255,.14); animation: demo-ripple 560ms cubic-bezier(.2,.7,.3,1) forwards; }
        @keyframes demo-ripple { from { transform: scale(.25); opacity: .95; } to { transform: scale(1.35); opacity: 0; } }`;
      document.head.appendChild(style);
      const pointer = document.createElement("div");
      pointer.id = "demo-pointer";
      pointer.innerHTML =
        '<svg width="20" height="20" viewBox="0 0 22 22"><path d="M3 2l14 8.5-6.2 1.3 3.6 6.9-2.6 1.4-3.6-6.9L3 17z" fill="#fff" stroke="#111" stroke-width="1.3" stroke-linejoin="round"/></svg>';
      document.body.appendChild(pointer);
      let idle = 0;
      const wake = () => {
        pointer.classList.add("on");
        window.clearTimeout(idle);
        idle = window.setTimeout(() => pointer.classList.remove("on"), 1600);
      };
      window.addEventListener("mousemove", (event) => {
        pointer.style.transform = `translate(${event.clientX - 3}px, ${event.clientY - 2}px)`;
        wake();
      }, true);
      window.addEventListener("mousedown", (event) => {
        wake();
        const ripple = document.createElement("div");
        ripple.className = "demo-ripple";
        ripple.style.left = `${event.clientX}px`;
        ripple.style.top = `${event.clientY}px`;
        document.body.appendChild(ripple);
        window.setTimeout(() => ripple.remove(), 700);
      }, true);
      (window as unknown as { demoHidePointer: () => void }).demoHidePointer = () => {
        window.clearTimeout(idle);
        pointer.classList.remove("on");
      };
    };
    if (document.body) install();
    else document.addEventListener("DOMContentLoaded", install);
  });
}

async function hidePointer(page: Page) {
  await page.evaluate(() => (window as unknown as { demoHidePointer?: () => void }).demoHidePointer?.());
}

/** Moves the mouse the way a hand would: eased, never linear. */
async function moveTo(page: Page, x: number, y: number) {
  const from = { ...mouse };
  const distance = Math.hypot(x - from.x, y - from.y);
  const ms = clamp(320 + distance * 0.55, 420, 900);
  const start = Date.now();
  for (;;) {
    const k = Math.min(1, (Date.now() - start) / ms);
    const e = ease(k);
    await page.mouse.move(from.x + (x - from.x) * e, from.y + (y - from.y) * e);
    if (k >= 1) break;
    await page.waitForTimeout(14);
  }
  mouse = { x, y };
}

/** Frames the element if the camera is not already on it, then moves the
 * pointer to it (never both at once) and clicks. */
async function glide(page: Page, target: Locator, options: { click?: boolean } = {}) {
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  if (!box) throw new Error("demo: target has no box");
  const inset = 2;
  const inFrame =
    box.x >= cam.x + inset &&
    box.y >= cam.y + inset &&
    box.x + box.width <= cam.x + cam.w - inset &&
    box.y + box.height <= cam.y + cam.h - inset;
  if (!inFrame) await focus(page, target, { pad: 160, minW: 720 });
  await moveTo(page, box.x + box.width / 2, box.y + box.height / 2);
  await pause(page, 220);
  if (options.click !== false) await click(page);
}

async function click(page: Page) {
  film.clicks.push({ t: clock(), x: Math.round(mouse.x), y: Math.round(mouse.y) });
  await page.mouse.down();
  await page.mouse.up();
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
  await target.pressSequentially(text, { delay: 75 });
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

async function drag(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 6 });
  await page.mouse.up();
  mouse = to;
}

async function openBoard(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("Add rate limiting middleware").first()).toBeVisible();
  // Give the graph most of the height: the timeline is not what these show.
  const split = page.getByRole("separator", { name: "Resize graph and timeline height" });
  const box = await split.boundingBox().catch(() => null);
  if (box) {
    const x = box.x + box.width / 2;
    await drag(page, { x, y: box.y + box.height / 2 }, { x, y: VH - 110 });
  }
  // Fit all lives in the graph header's ⋯ menu.
  const more = page.getByRole("button", { name: "More graph tools" }).first();
  if (await more.isVisible().catch(() => false)) {
    await more.click();
    await page.getByRole("menuitem", { name: "Fit all" }).click();
  }
  await page.mouse.move(VW / 2, VH / 2);
  mouse = { x: VW / 2, y: VH / 2 };
  await pause(page, 900);
}

async function nodeIds(page: Page): Promise<Record<string, string>> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Record<string, string> = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

/** Opens the node's panel and widens it, out of shot. */
async function openNode(page: Page, title: string) {
  const node = page.getByText(title).first();
  await glide(page, node);
  const drawer = page.getByRole("dialog", { name: "Node editor" });
  await expect(drawer).toBeVisible();
  await pause(page, 400);
  // Widen the drawer so run activity and the autopilot grid have room.
  const handle = page.getByRole("separator", { name: "Resize information panel" });
  const box = await handle.boundingBox().catch(() => null);
  if (box && box.x > 620) {
    await cut(page, async () => {
      const y = box.y + box.height / 2;
      await drag(page, { x: box.x + box.width / 2, y }, { x: 620, y });
      await hidePointer(page);
      await pause(page, 300);
    });
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

let recorder: Recorder | null = null;

test.beforeEach(async ({ page }, testInfo) => {
  film = { t0: Date.now(), viewport: { w: VW, h: VH }, frames: "frames/frames.ffconcat", title: null, begin: null, end: null, camera: [], captions: [], cuts: [], clicks: [] };
  cam = FULL;
  mouse = { x: VW / 2, y: VH / 2 };
  await stage(page);
  const frames = testInfo.outputPath("frames");
  rmSync(frames, { recursive: true, force: true });
  recorder = await record(page, frames);
  // demo-serve.mjs seeds one project; make sure it is the one on screen.
  const projects = await (await page.request.get("/api/projects")).json();
  if (projects.active !== "public-api") {
    const open = await page.request.post("/api/projects/open", { data: { name: "public-api" } });
    expect(open.ok()).toBeTruthy();
  }
});

test.afterEach(async ({}, testInfo) => {
  film.end = clock();
  await recorder?.stop();
  recorder = null;
  writeFileSync(testInfo.outputPath("film.json"), JSON.stringify(film, null, 1));
});

/** A band of an element's box, top to bottom as fractions of its height. */
async function band(target: Locator, top: number, bottom: number): Promise<Rect> {
  const box = await target.boundingBox();
  if (!box) throw new Error("demo: target has no box");
  const y0 = Math.max(0, box.y);
  const y1 = Math.min(VH, box.y + box.height);
  return { x: box.x, y: y0 + (y1 - y0) * top, w: box.width, h: (y1 - y0) * (bottom - top) };
}

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
  await begin(page, "Agent runs", "Hand a task to a coding agent on your own PC.");
  say("Every node is a task", "Open one to hand it to an agent.");
  await pause(page, 1300);
  await focus(page, page.getByText("Add rate limiting middleware").first(), { pad: 140, minW: 760, hold: 700 });
  const drawer = await openNode(page, "Add rate limiting middleware");
  const runsTab = drawer.getByRole("tab", { name: "Runs" });
  await focus(page, [runsTab, drawer.getByRole("heading").first()], { pad: 40, minW: 760, hold: 300 });
  await glide(page, runsTab);
  await pause(page, 400);
  const dispatch = drawer.getByRole("button", { name: "Dispatch to agent…" });
  await reveal(dispatch, "center");
  await pause(page, 500);

  say("Pick who does the work", "A runner on your PC, an agent and a model.");
  await focus(page, dispatch, { pad: 160, minW: 720, hold: 400 });
  await glide(page, dispatch);
  const dialog = page.getByRole("dialog", { name: "Dispatch to agent" });
  await expect(dialog).toBeVisible();
  await pause(page, 300);
  await hidePointer(page);
  await focus(page, await band(dialog, 0, 0.42), { pad: 12, hold: 900 });
  const agent = dialog.getByRole("combobox", { name: "Agent", exact: true });
  await glide(page, agent, { click: false });
  await agent.selectOption("opencode");
  await pause(page, 600);
  const model = dialog.getByRole("combobox", { name: "Model", exact: true });
  await glide(page, model, { click: false });
  await model.selectOption("opencode/qwen3-coder");
  await pause(page, 600);
  const extra = dialog.getByRole("textbox", { name: /Extra instructions/ });
  await focus(page, await band(dialog, 0.55, 1), { pad: 12, hold: 300 });
  if (await extra.isVisible().catch(() => false)) {
    await typeSlowly(page, extra, "Return 429 with Retry-After.");
  }
  await pause(page, 500);
  await glide(page, dialog.getByRole("button", { name: "Dispatch", exact: true }));
  await expect(dialog).toBeHidden();
  changed();

  say("Nothing starts without you", "The run waits until you accept it at the runner.");
  await pause(page, 500);
  await hidePointer(page);
  const details = drawer.getByRole("region", { name: "Run details" });
  await reveal(details, "center");
  await pause(page, 600);
  await focus(page, details, { pad: 12, within: drawer, hold: 2600 });
  phase = "running";
  changed();
  const activity = drawer.getByRole("list", { name: "Run activity" });
  await expect(activity).toBeVisible();
  await reveal(drawer.getByRole("region", { name: "Run details" }));
  await pause(page, 500);
  say("Watch it work", "OpenCode's reads, edits and thoughts stream in live.");
  await focus(page, await band(drawer, 0.42, 1), { pad: 0, hold: 200 });
  for (let i = 1; i <= runEvents.length; i++) {
    shown = i;
    push({ type: "run-events", id: "run1" });
    await pause(page, 150);
    const latest = activity.locator(":scope > *").last();
    if (i === 11) {
      // The refused command: centre it and look closer.
      await reveal(latest, "center");
      await pause(page, 500);
      say("Risky commands are refused", "Nobody is at a headless run to approve them.");
      await focus(page, latest, { pad: 60, minW: 720, within: drawer, hold: 2600 });
      await focus(page, await band(drawer, 0.42, 1), { pad: 0, d: 0.7 });
      continue;
    }
    await reveal(latest, "end");
    await pause(page, 750);
  }
  await pause(page, 1200);

  phase = "done";
  changed();
  say("A pull request, ready to review", "The runner commits, pushes and opens it.");
  const viewChanges = drawer.getByRole("button", { name: "View changes" });
  await pause(page, 300);
  await reveal(viewChanges, "center");
  await pause(page, 700);
  await focus(page, [drawer.getByText("The agent finished."), viewChanges, drawer.getByRole("button", { name: "Merge PR #42" })], {
    pad: 24,
    within: drawer,
    hold: 2600,
  });

  say("Read the diff, then merge", "Without leaving the board.");
  const diff = page.getByRole("dialog", { name: "Changes" });
  await glide(page, viewChanges);
  await cut(page, async () => {
    await expect(diff.getByRole("navigation")).toBeVisible();
    const unified = diff.getByRole("button", { name: "Unified" });
    if (await unified.isVisible().catch(() => false)) await unified.click();
    await pause(page, 500);
    await hidePointer(page);
    const top = await band(diff, 0, 0.42);
    await focus(page, { ...top, w: top.w * 0.62 }, { pad: 8, d: 0 });
  });
  await pause(page, 2000);
  await glide(page, diff.getByRole("navigation").getByRole("button").nth(1));
  await pause(page, 2000);

  const merge = diff.getByRole("button", { name: "Merge PR #42" });
  await focus(page, diff, { pad: 8, hold: 500 });
  await glide(page, merge);
  const confirm = page.getByRole("alertdialog", { name: "Merge PR #42?" });
  await expect(confirm).toBeVisible();
  await pause(page, 300);
  await focus(page, confirm, { pad: 60, minW: 720, hold: 1400 });
  await glide(page, confirm.getByRole("button", { name: "Merge PR #42" }));
  changed();
  await pause(page, 300);
  await hidePointer(page);
  const header = await band(diff, 0, 0.16);
  await focus(page, { ...header, w: header.w * 0.45 }, { pad: 8, minW: 640, hold: 700 });
  phase = "merged";
  changed();
  say("Merged", "Your runner merges it, with its own GitHub login.");
  await pause(page, 3200);
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
  await begin(page, "Autopilot", "A goal becomes a graph of tasks that run themselves.");
  say("Start from a goal", "Its tasks, and their order, come from the graph.");
  await pause(page, 2600);
  await focus(page, page.getByText("Ship the rate-limited public API").first(), { pad: 140, minW: 760, hold: 500 });
  const drawer = await openNode(page, "Ship the rate-limited public API");
  const pill = drawer.getByRole("button", { name: /^Autopilot/ });
  say("Open its autopilot", "The goal's Autopilot pill opens it in Station.");
  await focus(page, pill, { pad: 120, minW: 720, hold: 1400 });
  const table = page.locator(".station-goal").getByRole("table");
  await cut(page, async () => {
    await glide(page, pill);
    await expect(table).toBeVisible();
    await reveal(table, "end");
    await pause(page, 900);
    await hidePointer(page);
    await focus(page, table, { pad: 24, d: 0 });
  });
  say("Tasks run in order", "Each starts once the tasks it needs are done.");
  await pause(page, 3000);
  step = 1;
  advance();
  say("Other models review the work", "Implement, CI, then a review by two other agents.");
  await pause(page, 3200);
  step = 2;
  advance();
  say("You have the last word", "After a revise round, the task waits for you.");
  await pause(page, 800);
  const goalPanel = page.locator(".station-goal");
  const approveButton = goalPanel.getByRole("button", { name: "Approve" });
  await expect(approveButton).toBeVisible();
  await focus(page, [goalPanel.getByText("Waiting for your review"), approveButton], { pad: 20, hold: 400 });
  await glide(page, approveButton, { click: false });
  await pause(page, 2800);
  await wide(page, 1.0, 1400);
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
  await begin(page, "Usage", "What every run and agent session spends, in tokens and dollars.");
  const modes = page.getByRole("navigation", { name: "Project view" });
  const settings = modes.getByRole("button", { name: "Settings" });
  say("Usage lives in Settings", "Next to runners, routing and the daily budget.");
  await pause(page, 1200);
  await focus(page, modes, { pad: 80, minW: 640, hold: 600 });
  const parts = page.getByRole("navigation", { name: "Settings" });
  const region = page.getByRole("region", { name: "Usage" });
  await cut(page, async () => {
    await glide(page, settings);
    await expect(parts).toBeVisible();
    await pause(page, 300);
    await focus(page, parts, { pad: 40, minW: 720, d: 0 });
  });
  await glide(page, parts.getByRole("button", { name: "Usage", exact: true }));
  await expect(region).toBeVisible();
  await pause(page, 600);
  say("By day, agent and model", "Tokens and API-equivalent cost for the last 30 days.");
  await focus(page, region, { pad: 16, hold: 1600 });
  const bars = region.locator(".usage-chart-plot .usage-hit");
  await focus(page, region.locator(".usage-chart-plot"), { pad: 60, minW: 760, hold: 300 });
  for (const index of [8, 14, 21, 27]) {
    await glide(page, bars.nth(index), { click: false });
    await pause(page, 750);
  }
  await pause(page, 600);
  say("Every session and run", "See which piece of work spent the tokens.");
  const sessionsTab = region.getByRole("tab", { name: "Sessions" });
  await glide(page, sessionsTab);
  await pause(page, 500);
  await focus(page, [sessionsTab, region.getByRole("table")], { pad: 24, within: region, hold: 2400 });
  say("Prices you can edit", "Add the price of a model the table is missing.");
  await glide(page, region.getByRole("tab", { name: "Prices" }));
  await pause(page, 500);
  await focus(page, region, { pad: 16, hold: 2400 });
  say("A daily budget", "Past the limit, the hub refuses new runs.");
  const budget = parts.getByRole("button", { name: "Daily budget" });
  await glide(page, budget);
  const budgetRegion = page.getByRole("region", { name: "Daily budget" });
  await expect(budgetRegion).toBeVisible();
  await pause(page, 400);
  await focus(page, [budgetRegion.getByText(/^Limits on your runs/), budgetRegion.getByRole("button", { name: "Save budget" })], {
    pad: 40,
    hold: 2800,
  });
  await wide(page, 1.0, 1200);
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
  await begin(page, "Agent network", "See the other agents at work, and what they tell each other.");
  const fab = page.getByRole("button", { name: "Open Agent Network" });
  say("Every agent session, in one place", "Claude Code, Codex and OpenCode on the same project.");
  await pause(page, 1400);
  await focus(page, fab, { pad: 160, minW: 720, hold: 400 });
  await glide(page, fab);
  const panel = page.getByRole("dialog", { name: "Agent Network" });
  await expect(panel).toBeVisible();
  await pause(page, 400);
  await hidePointer(page);
  await focus(page, await band(panel, 0, 0.55), { pad: 8, hold: 2600 });
  const heldSection = panel.getByRole("region", { name: /Held messages/ });
  if (await heldSection.isVisible().catch(() => false)) {
    say("Hold a session's inbox", "Read what other agents send it before it gets there.");
    await focus(page, heldSection, { pad: 40, minW: 640, hold: 2800 });
    say("Approve to deliver", "The message goes through to the agent.");
    await glide(page, heldSection.getByRole("button", { name: "Approve" }));
    await pause(page, 700);
    await hidePointer(page);
    await focus(page, await band(panel, 0.15, 0.85), { pad: 8, hold: 2800 });
  }
  await wide(page, 1.0, 1200);
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
  await begin(page, "Two-factor and device links", "Lock your account down, then link a new runner.");
  const modes = page.getByRole("navigation", { name: "Project view" });
  const parts = page.getByRole("navigation", { name: "Settings" });
  say("Your account, in Settings", "Turn on an authenticator app as a second factor.");
  await pause(page, 1200);
  await focus(page, modes, { pad: 80, minW: 640, hold: 400 });
  await cut(page, async () => {
    await glide(page, modes.getByRole("button", { name: "Settings" }));
    await expect(parts).toBeVisible();
    await glide(page, parts.getByRole("button", { name: "Your account" }));
    await pause(page, 600);
    await hidePointer(page);
    await focus(page, page.getByRole("button", { name: "Set up" }), { pad: 200, minW: 760, d: 0 });
  });
  await pause(page, 800);
  await glide(page, page.getByRole("button", { name: "Set up" }));
  const qr = page.getByRole("img", { name: "QR code for your authenticator app" });
  await expect(qr).toBeVisible();
  await pause(page, 300);
  say("Scan, then type the code", "Any authenticator app works.");
  const code = page.getByLabel("6-digit code from the app");
  await focus(page, [qr, code], { pad: 32, hold: 1800 });
  await typeSlowly(page, code, "482913");
  await pause(page, 400);
  await glide(page, page.getByRole("button", { name: "Turn on" }));
  say("Recovery codes, shown once", "Keep them somewhere safe.");
  const saved = page.getByRole("button", { name: "I've saved them" });
  await expect(saved).toBeVisible();
  await pause(page, 300);
  await focus(page, [saved, page.getByText("K7QM-4XTR-9PWD-2HNA")], { pad: 40, hold: 2600 });
  await glide(page, saved);
  await pause(page, 900);

  say("Link a new runner", "A one-time device link from Settings, Runners.");
  const runners = page.getByRole("region", { name: "Runners" });
  await cut(page, async () => {
    await glide(page, parts.getByRole("button", { name: "Runners" }));
    await expect(runners).toBeVisible();
    await pause(page, 500);
    await hidePointer(page);
    await focus(page, runners, { pad: 16, d: 0 });
  });
  await pause(page, 1200);
  await glide(page, runners.getByRole("button", { name: "Link a new device" }));
  const link = page.getByRole("dialog", { name: "Link a new device" });
  await expect(link).toBeVisible();
  await pause(page, 300);
  await focus(page, link, { pad: 24, hold: 500 });
  await typeSlowly(page, link.getByLabel("Runner name"), "laptop");
  await pause(page, 400);
  await glide(page, link.getByRole("button", { name: "Create link" }));
  const verify = page.getByRole("dialog", { name: "Verify it's you" });
  await expect(verify).toBeVisible();
  say("Verify it's you", "Sensitive steps now ask the authenticator, not email.");
  await pause(page, 300);
  await focus(page, verify, { pad: 32, hold: 1400 });
  await typeSlowly(page, verify.getByLabel("Authenticator code"), "730164");
  await pause(page, 300);
  await glide(page, verify.getByRole("button", { name: "Verify" }));
  const doneButton = verify.getByRole("button", { name: "Done" });
  await expect(doneButton).toBeVisible();
  await pause(page, 900);
  await glide(page, doneButton);
  await expect(link.getByText("Link for laptop")).toBeVisible();
  say("One link, used once", "Paste it into nodevas on the new machine.");
  await pause(page, 300);
  await focus(page, link, { pad: 24, hold: 3400 });
  await wide(page, 1.0, 1000);
});
