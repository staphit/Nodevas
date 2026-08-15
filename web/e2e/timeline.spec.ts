import { type Page, type Locator } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// Every timeline test edits the schedule, so each gets its own project.
async function openApp(page: Page) {
  await freshProject(page);
}

const timeline = (page: Page) => page.locator(".lane-wrap.timeline");

/** Cell for one node on one day; the timeline grid is keyed by both. */
function cellFor(page: Page, date: string, columnIndex = 0): Locator {
  return timeline(page)
    .locator(`.board-row[data-date="${date}"] .board-cell`)
    .nth(columnIndex);
}

/** Today plus `days`, as the YYYY-MM-DD key the grid uses. */
function dayKey(days: number): string {
  const date = new Date();
  date.setDate(date.getDate() + days);
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-");
}

/** HTML5 drag-and-drop is not what these cards use — they use pointer events. */
async function dragTo(page: Page, source: Locator, target: Locator) {
  const from = await source.boundingBox();
  const to = await target.boundingBox();
  expect(from).not.toBeNull();
  expect(to).not.toBeNull();
  await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2);
  await page.mouse.down();
  // Two moves: the first starts the drag, the second lands it.
  await page.mouse.move(to!.x + to!.width / 2, to!.y + to!.height / 2, { steps: 8 });
  await page.mouse.move(to!.x + to!.width / 2, to!.y + to!.height / 2);
  await page.mouse.up();
}

async function scheduleMilestone(page: Page, date: string) {
  await cellFor(page, date).click({ button: "right" });
  const menu = page.locator(".lane-context-menu");
  await expect(menu).toBeVisible();
  // Menu entries carry role="menuitem", so locate them by text.
  await menu.locator("button", { hasText: "設為開始" }).first().click();
  await expect(menu).toHaveCount(0);
}

test.describe("timeline", () => {
  test("schedules an expected milestone from the cell menu", async ({ page }) => {
    await openApp(page);
    const date = dayKey(1);

    await scheduleMilestone(page, date);

    await expect(cellFor(page, date).locator(".plan-card")).toBeVisible();
  });

  test("keeps a scheduled milestone after a reload", async ({ page }) => {
    await openApp(page);
    const date = dayKey(2);

    await scheduleMilestone(page, date);
    await page.reload();

    await expect(cellFor(page, date).locator(".plan-card")).toBeVisible();
  });

  test("moves a milestone by dragging it to another day", async ({ page }) => {
    await openApp(page);
    const from = dayKey(3);
    const to = dayKey(4);

    await scheduleMilestone(page, from);
    const card = cellFor(page, from).locator(".plan-card");
    await expect(card).toBeVisible();

    await dragTo(page, card, cellFor(page, to));

    await expect(cellFor(page, to).locator(".plan-card")).toBeVisible();
    await expect(cellFor(page, from).locator(".plan-card")).toHaveCount(0);
  });

  test("removes a milestone from the cell menu", async ({ page }) => {
    await openApp(page);
    const date = dayKey(5);

    await scheduleMilestone(page, date);
    await expect(cellFor(page, date).locator(".plan-card")).toBeVisible();

    await cellFor(page, date).click({ button: "right" });
    const menu = page.locator(".lane-context-menu");
    await menu.locator("button", { hasText: /移除|刪除/ }).first().click();

    await expect(cellFor(page, date).locator(".plan-card")).toHaveCount(0);
  });

  test("records an actual status and shows it on today's row", async ({ page }) => {
    await openApp(page);

    await timeline(page).locator(".lane-head-cell").first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByLabel("改為").selectOption("started");
    // The status is staged. Unlike the document, it is never written by the
    // auto-save timer — an append to the journal cannot be taken back — so an
    // explicit save is what writes it.
    await page.keyboard.press("Control+s");

    await expect(timeline(page).locator(".snap-card").first()).toBeVisible();
  });

  test("reorders timeline columns by dragging a node heading", async ({ page }) => {
    await openApp(page);

    const heads = timeline(page).locator(".lane-head-cell.timeline-order-handle");
    await expect(heads).toHaveCount(2);
    const firstTitle = (await heads.nth(0).innerText()).trim();

    await dragTo(page, heads.nth(0), heads.nth(1));

    await expect
      .poll(async () => (await heads.nth(1).innerText()).trim())
      .toContain(firstTitle.split("\n")[0]);

    // Order lives in graph.yaml, so it survives a reload.
    await page.reload();
    await expect
      .poll(async () =>
        (await timeline(page)
          .locator(".lane-head-cell.timeline-order-handle")
          .nth(1)
          .innerText()).trim(),
      )
      .toContain(firstTitle.split("\n")[0]);
  });

  test("resizes every date column from the toolbar stepper", async ({ page }) => {
    await openApp(page);

    // The stepper only exists in the horizontal layout.
    await timeline(page).locator("button", { hasText: "橫式" }).first().click();
    const stepper = timeline(page).locator('[aria-label="時間軸欄寬"]');
    await expect(stepper).toBeVisible();

    const readout = stepper.locator("output");
    const before = Number((await readout.innerText()).replace("px", ""));
    await stepper.locator('[aria-label="放大時間軸欄寬"]').click();
    const after = Number((await readout.innerText()).replace("px", ""));
    expect(after).toBeGreaterThan(before);

    // Applies to the whole grid, not one column.
    const widths = await timeline(page)
      .locator(".horizontal-date-label")
      .evaluateAll((nodes) => nodes.map((node) => Math.round(node.getBoundingClientRect().width)));
    expect(new Set(widths).size).toBeLessThanOrEqual(2);
  });

  test("collapses empty days on request", async ({ page }) => {
    await openApp(page);

    const rowsBefore = await timeline(page).locator(".board-row").count();
    await timeline(page).getByLabel(/收合.*空白|空白/).first().check();

    await expect
      .poll(async () => timeline(page).locator(".board-row").count())
      .toBeLessThan(rowsBefore);
  });
});
