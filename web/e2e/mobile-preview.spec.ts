/**
 * Not a test: a screenshot run.
 *
 * Each case drives the app into one state and captures it, so the phone and
 * tablet layouts can be looked at without building, signing and installing an
 * iOS app first. Run it with:
 *
 *   npm run preview:mobile
 *
 * Output lands in web/mobile-preview/<device>/<state>.png.
 */

import { test, expect, type Page } from "@playwright/test";
import { declineTour, freshProject } from "./fixture";

function shot(page: Page, name: string, projectName: string) {
  return page.screenshot({
    path: `mobile-preview/${projectName}/${name}.png`,
    fullPage: false,
  });
}

test.describe("mobile preview", () => {
  test.beforeEach(async ({ page }) => {
    // freshProject declines the first-run tour; called here too so the reason
    // this screenshot run is tour-free is stated where it is read.
    await declineTour(page);
    await freshProject(page, ["設計稿", "實作", "驗收"]);
  });

  test("board, menus and panels", async ({ page }, testInfo) => {
    const device = testInfo.project.name;

    await shot(page, "01-board", device);

    // The explorer is a column on a tablet and an overlay on a phone; both are
    // worth seeing open.
    await page.getByRole("button", { name: /專案總管/ }).first().click();
    await page.waitForTimeout(400);
    await shot(page, "02-explorer", device);
    await page.getByRole("button", { name: /專案總管/ }).first().click();
    await page.waitForTimeout(400);

    // Long press opens the context menu on touch. page.mouse would report
    // pointerType "mouse", which the long-press listener deliberately ignores,
    // so the pointer events are dispatched directly with a touch pointerType.
    const card = page.locator(".col-card").first();
    const box = await card.boundingBox();
    if (box) {
      const point = { clientX: box.x + box.width / 2, clientY: box.y + box.height / 2 };
      await card.dispatchEvent("pointerdown", { pointerType: "touch", pointerId: 1, ...point });
      // Longer than LONG_PRESS_MS in src/components/touch.ts.
      await page.waitForTimeout(700);
      await shot(page, "03-long-press-menu", device);
      await card.dispatchEvent("pointerup", { pointerType: "touch", pointerId: 1, ...point });
      await page.keyboard.press("Escape");
      await page.waitForTimeout(200);
    }

    // The drawer is a side panel on a tablet and a bottom sheet on a phone.
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    if (!(await drawer.isVisible().catch(() => false))) {
      await card.click({ force: true });
    }
    await expect(drawer).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(400);
    await shot(page, "04-drawer", device);
    await drawer.getByRole("button", { name: "關閉面板" }).click();
    await expect(drawer).toBeHidden();

    // The new-node form is the tallest thing the board opens, and taller again
    // once 外觀與狀態 is unfolded. Both states are captured: the folded one is
    // what nearly every node is made with, and the open one is the case that
    // has to prove it still fits a 375px screen.
    await page.getByRole("button", { name: /新增節點/ }).first().click();
    await expect(page.getByLabel("節點標題")).toBeVisible();
    await shot(page, "06-node-create", device);
    await page.getByRole("button", { name: /外觀與狀態/ }).click();
    await page.waitForTimeout(300);
    await shot(page, "07-node-create-appearance", device);
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);

    // The timeline pane carries more header controls than the graph's, so it is
    // the one most likely to wrap. On a phone it starts folded and has to be
    // opened; on a tablet both panes are already open and there is no such
    // toggle. The button is named by its ▸ glyph, so it is found by its title.
    const expandTimeline = page.locator('.pane-toggle[title="展開此視窗"]');
    if (await expandTimeline.first().isVisible().catch(() => false)) {
      await expandTimeline.first().click();
      await page.waitForTimeout(500);
    }
    await shot(page, "05-timeline", device);
  });
});
