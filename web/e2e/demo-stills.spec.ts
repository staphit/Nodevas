/**
 * Not a test: the README's stills (playwright.demo.config.ts).
 *
 *   station-warroom.png  Station at 1440×900: what waits on you, the war room
 *                        and who worked on what.
 *   game-warroom.png     The war room alone, 1052×759.
 *   tour/*.png           The feature tour's frames, which e2e/demo-tour.mjs
 *                        captions and turns into nodevas-feature-tour.gif.
 *
 * The board is demo-serve.mjs's; what runners and agents would feed is
 * answered by demo-station.ts. Run it with:
 *
 *   npm run demo:stills
 */

import { test, expect, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { hideCopilot, mockBusyStation, nodeIds } from "./demo-station";

// The README's stills are 1:1 screenshots.
test.use({ deviceScaleFactor: 1 });

const SHOTS = resolve(import.meta.dirname, "..", "..", "docs", "screenshots");

async function stage(page: Page, prefs: Record<string, string> = {}) {
  await page.addInitScript((values) => {
    localStorage.setItem("vised-tour-prompt", "declined");
    localStorage.setItem("vised-language", "en");
    localStorage.setItem("vised-theme", "dark");
    localStorage.setItem("vised-agent-workstation", "1");
    for (const [key, value] of Object.entries(values)) localStorage.setItem(key, value);
  }, prefs);
  const projects = await (await page.request.get("/api/projects")).json();
  if (projects.active !== "public-api") {
    const open = await page.request.post("/api/projects/open", { data: { name: "public-api" } });
    expect(open.ok()).toBeTruthy();
  }
  await hideCopilot(page);
  await mockBusyStation(page, await nodeIds(page));
}

/** Station, with its war room drawn and its bubbles settled. */
async function openStation(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 20_000 });
  const room = page.getByRole("region", { name: "War room" });
  await expect(room).toBeVisible({ timeout: 15_000 });
  await expect(room.getByRole("button", { name: /Claude opus in mac-mini: working/ })).toBeVisible();
  await page.waitForTimeout(1_500);
  return room;
}

test("station stills", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await stage(page, { "vised-project-mode": "station", "vised-explorer-collapsed": "1" });
  await openStation(page);
  const out = process.env.DEMO_STILLS_DIR ?? SHOTS;
  mkdirSync(out, { recursive: true });
  // Who is working, unfolded from Today, fills the side column.
  await page.getByRole("button", { name: /working · \d+ in all/ }).click();
  await expect(page.locator(".station-actor").first()).toBeVisible();
  await page.mouse.move(1439, 899);
  await page.waitForTimeout(600);
  await page.screenshot({ path: join(out, "station-warroom.png") });

  // The war room alone, down to its lounge and server room: a window tall
  // enough to hold it all.
  await page.setViewportSize({ width: 1440, height: 1400 });
  await page.waitForTimeout(800);
  const room = page.getByRole("region", { name: "War room" });
  const box = await room.boundingBox();
  const ground = await room.locator(".px-ground").boundingBox();
  if (!box || !ground) throw new Error("stills: no war room");
  await page.screenshot({
    path: join(out, "game-warroom.png"),
    clip: { x: box.x, y: box.y, width: box.width, height: ground.y + ground.height + 12 - box.y },
  });
});

// ---------------------------------------------------------------------------
// The feature tour: a frame per stop, drawn at twice the pixels so a frame
// can be a close-up of part of the window. tour.json lists each frame, the
// part of it to show (CSS pixels, the GIF's aspect) and its caption.

// The window, in CSS pixels: the GIF's 1280×608 shape, a little roomier.
const TW = 1440;
const TH = 684;
const TOUR_ASPECT = TW / TH;

type Rect = { x: number; y: number; w: number; h: number };
type TourFrame = { file: string; crop: Rect; title: string; sub: string };

/** The GIF-shaped rectangle around `box`, with `pad`, inside the window. */
function tourCrop(box: Rect, pad = 24, minW = 820): Rect {
  let { x, y, w, h } = { x: box.x - pad, y: box.y - pad, w: box.w + 2 * pad, h: box.h + 2 * pad };
  if (w / h < TOUR_ASPECT) {
    x -= (h * TOUR_ASPECT - w) / 2;
    w = h * TOUR_ASPECT;
  } else {
    y -= (w / TOUR_ASPECT - h) / 2;
    h = w / TOUR_ASPECT;
  }
  if (w < minW) {
    x -= (minW - w) / 2;
    y -= (minW / TOUR_ASPECT - h) / 2;
    w = minW;
    h = minW / TOUR_ASPECT;
  }
  if (w >= TW) return { x: 0, y: 0, w: TW, h: TH };
  const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
  return { x: Math.round(clamp(x, 0, TW - w)), y: Math.round(clamp(y, 0, TH - h)), w: Math.round(w), h: Math.round(h) };
}

test.describe("tour", () => {
  test.use({ viewport: { width: TW, height: TH }, deviceScaleFactor: 2 });

  test("feature tour frames", async ({ page }, testInfo) => {
    const dir = testInfo.outputPath("tour");
    mkdirSync(dir, { recursive: true });
    const frames: TourFrame[] = [];
    const shot = async (title: string, sub: string, target?: Rect | import("@playwright/test").Locator, pad?: number, minW?: number) => {
      let crop: Rect = { x: 0, y: 0, w: TW, h: TH };
      if (target) {
        const box = "w" in target ? target : await target.boundingBox();
        if (!box) throw new Error(`tour: nothing to frame for "${title}"`);
        const rect = "w" in box ? box : { x: box.x, y: box.y, w: box.width, h: box.height };
        // Room under it for the caption band, which covers the frame's foot.
        crop = tourCrop({ ...rect, h: rect.h * 1.15 }, pad, minW);
      }
      await page.mouse.move(TW - 2, TH - 2);
      await page.waitForTimeout(500);
      const file = `${String(frames.length).padStart(2, "0")}.png`;
      await page.screenshot({ path: join(dir, file) });
      frames.push({ file, crop, title, sub });
    };

    await stage(page, { "vised-project-mode": "info", "vised-pane-timeline": "0" });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText("Add rate limiting middleware").first()).toBeVisible();
    // The agent panel opens itself on the staged question; the board first.
    const close = page.getByRole("button", { name: "Close Agent chat" });
    if (await close.isVisible({ timeout: 8_000 }).catch(() => false)) await close.click();
    const more = page.getByRole("button", { name: "More graph tools" }).first();
    if (await more.isVisible().catch(() => false)) {
      await more.click();
      await page.getByRole("menuitem", { name: "Fit all" }).click();
    }
    await page.waitForTimeout(800);

    await shot("The board", "Every task is a node; arrows are what it waits on.");
    const tree = await page.getByText("Workspace", { exact: true }).first().boundingBox();
    if (!tree) throw new Error("tour: no explorer");
    await shot("Every project in one workspace", "The explorer: projects, their nodes and files.", { x: 0, y: 40, w: 330, h: tree.y + 300 - 40 }, 0, 760);

    const canvasNode = page.locator(".lane-wrap.graph").getByText("Add rate limiting middleware").first();
    await canvasNode.click();
    const drawer = page.getByRole("dialog", { name: "Node editor" });
    await expect(drawer).toBeVisible();
    await page.waitForTimeout(600);
    await shot("Open a node", "Its page, what it depends on, and its runs.", drawer, 0);
    await drawer.getByRole("tab", { name: "Activity", exact: true }).click();
    await expect(drawer.getByRole("list", { name: "Run activity" })).toBeVisible();
    await page.waitForTimeout(600);
    await shot("Agents work on it", "A coding agent on your own PC, streamed live.", drawer, 0);

    await drawer.getByRole("button", { name: "Close panel" }).click();
    await expect(drawer).toBeHidden();
    await page.getByRole("button", { name: /^Open Agent chat/ }).click();
    const chat = page.getByRole("dialog", { name: "Agent chat" });
    await expect(chat).toBeVisible();
    await page.waitForTimeout(800);
    await shot("Agent chat", "Every session, their questions, and what they tell each other.", chat, 0);
    await page.getByRole("button", { name: "Close Agent chat" }).click();

    await page.getByRole("button", { name: /^Station/ }).click();
    const room = page.getByRole("region", { name: "War room" });
    await expect(room).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(1_500);
    await shot("Station", "What waits on you, and who is at work.");
    await room.locator(".px-office-room").first().scrollIntoViewIfNeeded();
    await page.waitForTimeout(600);
    await shot("The war room", "Every agent at its desk, in this project's office.", room.locator(".px-office-room").first(), 8, 760);

    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const parts = page.getByRole("navigation", { name: "Settings" });
    await expect(parts).toBeVisible();
    await parts.getByRole("button", { name: /^Agents/ }).click();
    await expect(page.getByRole("region", { name: "Agents", exact: true })).toBeVisible();
    await page.getByRole("button", { name: /^Advanced/ }).click();
    await page.waitForTimeout(600);
    await shot("Settings", "Runners, budgets and routing; this project; your account.");

    writeFileSync(join(dir, "tour.json"), JSON.stringify({ width: 1280, height: 608, frames }, null, 1));
  });
});
