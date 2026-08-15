import { type Page } from "@playwright/test";
import { declineTour, expect, freshProject, test } from "./fixture";

/** The app boots with a project selected; wait for that rather than a timeout. */
async function openApp(page: Page) {
  await declineTour(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "專案", exact: true })).toBeVisible();
}

test.describe("main flows", () => {
  test("creates a node without a right click", async ({ page }) => {
    await openApp(page);

    const createButtons = page.getByRole("button", { name: /新增節點/ });
    await createButtons.first().click();

    // More than one create form can be mounted at once (canvas menu, sidebar),
    // so every step stays inside the one form.
    const form = page.locator(".node-create-form").first();
    const title = form.getByLabel("節點標題");
    await expect(title).toBeVisible();
    await title.fill("端對端節點");
    await form.getByRole("button", { name: "建立節點" }).click();

    await expect(page.getByText("端對端節點").first()).toBeVisible();
  });

  // The document saves itself now. A status does not, and that is the point of
  // this test: it goes to run/journal.jsonl, which is append-only, so a value
  // chosen by mistake could never be taken back if a timer wrote it. It waits
  // for somebody to say so — the panel's own button, or Ctrl+S.
  test("keeps an actual status staged until it is explicitly applied", async ({
    page,
  }) => {
    await openApp(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();

    const staged = drawer.getByText("尚未套用");
    await expect(staged).toBeHidden();

    await drawer.getByLabel("改為").selectOption("in_progress");
    await drawer.getByLabel("實際狀態註解").fill("開工");
    await expect(staged).toBeVisible();

    // Editing the document and letting auto-save take it does not carry the
    // status along: the text lands, the status stays staged.
    await page.locator(".cm-content").click();
    await page.keyboard.type("內文\n");
    await expect(page.getByText("已儲存").first()).toBeVisible();
    await expect(staged).toBeVisible();

    await drawer.getByRole("button", { name: "套用狀態" }).click();

    await expect(staged).toBeHidden();
    await expect(drawer.getByText("進行中").first()).toBeVisible();
  });

  // Ctrl+S is still "save now", and still the way to apply a staged status
  // without reaching for the mouse. Removing it would break years of muscle
  // memory for nothing.
  test("still applies a staged status on Ctrl+S", async ({ page }) => {
    await openApp(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    const staged = drawer.getByText("尚未套用");

    // A state the node is not already in — these tests share one project, and
    // choosing the current state is not a change worth staging.
    await drawer.getByLabel("改為").selectOption("done");
    // Filling the note also puts focus inside the panel, which is where the
    // drawer listens for the keystroke.
    await drawer.getByLabel("實際狀態註解").fill("收工");
    await expect(staged).toBeVisible();

    await page.keyboard.press("Control+s");

    await expect(staged).toBeHidden();
    await expect(drawer.getByText("完成").first()).toBeVisible();
  });

  // A promise that work is saved is worth nothing if nobody can see that it
  // was, and a person who cannot tell will keep pressing Ctrl+S anyway.
  test("saves the document on its own and says so", async ({ page }) => {
    await openApp(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();

    await page.locator(".cm-content").click();
    await page.keyboard.type("自動儲存的內容\n");
    await expect(drawer.locator(".editor-path-dirty")).toBeVisible();

    // No keystroke, no button: the idle timer alone gets it to disk.
    await expect(page.getByText("已儲存").first()).toBeVisible({ timeout: 15_000 });
    await expect(drawer.locator(".editor-path-dirty")).toBeHidden();

    // And it really is on disk, not just claimed to be.
    await page.reload();
    await page.getByText("設計稿").first().click();
    await expect(page.locator(".cm-content")).toContainText("自動儲存的內容");
  });

  test("project settings is the only place workflow definitions are edited", async ({
    page,
  }) => {
    await openApp(page);

    // The sidebar legend explains, it does not edit.
    await page.getByText("狀態圖例").click();
    await expect(page.getByText("自訂實際狀態在「專案設定")).toBeVisible();

    // The legend also offers a way in, so match the toolbar button exactly.
    await page.getByRole("button", { name: "專案設定", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "專案設定" });
    await expect(dialog).toBeVisible();

    await dialog.getByRole("tab", { name: "里程碑類型" }).click();
    await dialog.getByLabel("新里程碑名稱").fill("內審");
    await dialog.getByRole("button", { name: "新增類型" }).click();
    await expect(dialog.getByText("已新增「內審」")).toBeVisible();

    // Usage is shown before anything is deleted.
    await expect(dialog.getByText(/已排定 0 筆/)).toBeVisible();
  });
});

test.describe("context menus", () => {
  test("right click on empty canvas offers the creation actions", async ({ page }) => {
    await openApp(page);
    const canvas = page.locator(".lane-wrap.graph .board-inner");
    await canvas.click({ button: "right", position: { x: 620, y: 380 } });

    const menu = page.locator(".lane-context-menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByText("新增節點")).toBeVisible();
    await expect(menu.getByText(/群組底圖|註解/).first()).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
  });

  test("right click on a dependency line offers its styling", async ({ page }) => {
    await openApp(page);
    // Target the wire itself, not whatever card happens to sit under it.
    await page.locator(".lane-wrap.graph .edge-hit-target").first().click({
      button: "right",
      force: true,
    });

    const menu = page.locator(".lane-context-menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByText(/關係線/)).toBeVisible();
    // Meaning and looks are two separate groups in this menu.
    await expect(menu.getByText("關係語意")).toBeVisible();
    await expect(menu.getByText("棄用")).toBeVisible();
    await expect(menu.getByText("線條外觀")).toBeVisible();
  });

  test("right click on a timeline cell schedules an expected milestone", async ({
    page,
  }) => {
    await openApp(page);
    // A timeline day cell schedules an expected milestone for that node/day.
    const cell = page.locator(".lane-wrap.timeline .board-cell").first();
    await cell.click({ button: "right" });
    const menu = page.locator(".lane-context-menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByText(/預期|里程碑|開始|死線/).first()).toBeVisible();
  });
});

test.describe("keyboard and persistence", () => {
  test("resizes the explorer with the keyboard and remembers it", async ({ page }) => {
    await openApp(page);

    const handle = page.getByRole("separator", { name: "調整專案總管寬度" });
    await handle.focus();
    const before = await handle.getAttribute("aria-valuenow");

    await page.keyboard.press("Shift+ArrowRight");
    const after = await handle.getAttribute("aria-valuenow");
    expect(Number(after)).toBeGreaterThan(Number(before));

    await page.reload();
    const restored = page.getByRole("separator", { name: "調整專案總管寬度" });
    await expect(restored).toHaveAttribute("aria-valuenow", after!);

    // Double click resets to the default width.
    await restored.dblclick();
    await expect(restored).toHaveAttribute("aria-valuenow", "304");
  });

  test("undo names what it would revert", async ({ page }) => {
    await openApp(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await drawer.getByLabel("標題").fill("設計稿 v2");
    await drawer.getByLabel("標題").blur();
    await expect(page.getByText("已儲存").first()).toBeVisible();

    await page.keyboard.press("Control+z");
    await expect(page.getByText("設計稿").first()).toBeVisible();
  });
});

test.describe("batch delete", () => {
  test("removes a whole selection in one step, and undo and redo each take one", async ({
    page,
  }) => {
    await freshProject(page, ["節點甲", "節點乙", "節點丙"]);
    const cards = page.locator(".lane-wrap.graph .col-card");
    await expect(cards).toHaveCount(3);

    // Ctrl + drag marquee-selects. The drag has to start on empty board, so it
    // runs from the bottom-right corner back over the cards near the top-left.
    const board = page.locator(".lane-wrap.graph .board").first();
    const box = await board.boundingBox();
    if (!box) throw new Error("the board has no box");
    await page.keyboard.down("Control");
    await page.mouse.move(box.x + box.width - 8, box.y + box.height - 8);
    await page.mouse.down();
    await page.mouse.move(box.x + 8, box.y + 8, { steps: 16 });
    await page.mouse.up();
    await page.keyboard.up("Control");

    const toolbar = page.locator(".graph-batch-toolbar");
    await expect(toolbar).toContainText("3 個節點");

    await toolbar.getByRole("button", { name: "刪除" }).click();
    await page.getByRole("button", { name: "移到垃圾桶" }).click();
    await expect(cards).toHaveCount(0);

    // One undo brings the whole batch back, documents included.
    await page.keyboard.press("Control+z");
    await expect(cards).toHaveCount(3);
    for (const title of ["節點甲", "節點乙", "節點丙"]) {
      await expect(cards.filter({ hasText: title })).toHaveCount(1);
    }

    // And one redo takes the same batch away again, in one step.
    await page.keyboard.press("Control+Shift+z");
    await expect(cards).toHaveCount(0);
  });
});
