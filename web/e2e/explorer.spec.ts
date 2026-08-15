import { expect, test, type Page } from "@playwright/test";
import { freshProject } from "./fixture";

// Playwright's Control+click becomes a context click on macOS. Use the
// platform's primary additive-selection modifier while keeping Shift as the
// range-selection modifier on every platform.
const primaryAdditiveModifier =
  process.platform === "darwin" ? ("Meta" as const) : ("Control" as const);

// Its own project, so the tree contents do not depend on test order.
async function openApp(page: Page) {
  await freshProject(page);
}

test.describe("explorer", () => {
  test("lists the project's nodes and opens one", async ({ page }) => {
    await openApp(page);

    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    await expect(tree).toBeVisible();
    await expect(tree.getByText("設計稿")).toBeVisible();

    await tree.getByText("設計稿").click();
    await expect(page.getByRole("dialog", { name: "節點編輯" })).toBeVisible();
  });

  test("filters the tree by search", async ({ page }) => {
    await openApp(page);

    const search = page.getByPlaceholder(/搜尋|篩選/).first();
    await search.fill("實作");

    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    await expect(tree.getByText("實作")).toBeVisible();
    await expect(tree.getByText("設計稿")).toHaveCount(0);
  });

  test("collapses and restores the explorer", async ({ page }) => {
    await openApp(page);

    await page.getByRole("button", { name: "摺疊專案總管" }).click();
    await expect(page.getByRole("tree", { name: "工作區檔案樹" })).toBeHidden();

    await page.reload();
    // The collapsed state is a local preference, so it survives a reload.
    await expect(page.getByRole("button", { name: "展開專案總管" })).toBeVisible();
    await page.getByRole("button", { name: "展開專案總管" }).click();
    await expect(page.getByRole("tree", { name: "工作區檔案樹" })).toBeVisible();
  });
});

test("exports the whole project as one document from the sidebar", async ({ page }) => {
  await freshProject(page, ["第一節", "第二節"]);

  await page.getByRole("button", { name: "匯出目標" }).click();
  const download = page.waitForEvent("download");
  await page.getByRole("menuitem", { name: /Markdown/ }).click();
  const file = await download;
  expect(file.suggestedFilename()).toContain(".md");

  const stream = await file.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(chunk as Buffer);
  const document = Buffer.concat(chunks).toString("utf8");
  expect(document).toContain("## 第一節");
  expect(document).toContain("## 第二節");
});

test("offers every import source from one menu", async ({ page }) => {
  await freshProject(page);

  await page.getByRole("button", { name: "匯入來源" }).click();
  const menu = page.getByRole("menu").filter({ hasText: "專案封裝" });
  await expect(menu.getByRole("menuitem", { name: /Markdown/ })).toBeVisible();
  await expect(menu.getByRole("menuitem", { name: /JSON Canvas/ })).toBeVisible();
});

test.describe("tree multi-selection", () => {
  test("primary-modifier-selects nodes and deletes them from the right-click menu", async ({
    page,
  }) => {
    await freshProject(page, ["節點一", "節點二", "節點三"]);
    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    const nodeButton = (title: string) =>
      tree.locator(".node-list button").filter({ hasText: title }).first();

    await nodeButton("節點一").click({ modifiers: [primaryAdditiveModifier] });
    await nodeButton("節點二").click({ modifiers: [primaryAdditiveModifier] });
    await expect(tree.locator(".node-list button.selected")).toHaveCount(2);

    // Right-clicking inside the selection acts on all of it.
    await nodeButton("節點二").click({ button: "right" });
    const menu = page.getByRole("menu", { name: /檔案操作/ });
    await expect(menu).toContainText("2 個節點");
    await expect(menu.getByRole("menuitem", { name: /開啟/ })).toHaveCount(0);
    await menu.getByRole("menuitem", { name: /刪除 2 個節點/ }).click();
    await page.getByRole("button", { name: "移到垃圾桶" }).click();

    await expect(tree.locator(".node-list button")).toHaveCount(1);
    await expect(nodeButton("節點三")).toBeVisible();
  });

  test("a right-click outside the selection acts on one node only", async ({
    page,
  }) => {
    await freshProject(page, ["甲", "乙"]);
    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    const nodeButton = (title: string) =>
      tree.locator(".node-list button").filter({ hasText: title }).first();

    await nodeButton("甲").click({ modifiers: [primaryAdditiveModifier] });
    await nodeButton("乙").click({ button: "right" });

    const menu = page.getByRole("menu", { name: /檔案操作/ });
    await expect(menu).toContainText("乙");
    await expect(menu.getByRole("menuitem", { name: /開啟/ })).toBeVisible();
    await expect(tree.locator(".node-list button.selected")).toHaveCount(1);
  });

  test("primary-modifier-selects projects and detaches them in one go", async ({ page }) => {
    await freshProject(page, ["起始工作"]);
    // Two more projects to select alongside the active one.
    for (const name of ["批次甲", "批次乙"]) {
      const created = await page.request.post("/api/projects/open", {
        data: { name, create: true },
      });
      expect(created.ok()).toBeTruthy();
    }
    await page.reload();
    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    const projectRow = (label: string) =>
      tree.locator(".project-tree-row").filter({ hasText: label }).first();
    await expect(projectRow("批次甲")).toBeVisible();

    // The row holds several buttons (expand, open, add child); the name is on
    // the open one.
    const openButton = (label: string) =>
      projectRow(label).locator("button.project-tree-open");
    await openButton("批次甲").click({ modifiers: [primaryAdditiveModifier] });
    await openButton("批次乙").click({ modifiers: [primaryAdditiveModifier] });
    await expect(tree.locator(".project-tree-row.selected")).toHaveCount(2);

    await projectRow("批次乙").click({ button: "right" });
    const menu = page.getByRole("menu", { name: /2 個專案操作/ });
    await menu.getByRole("menuitem", { name: /解除匯入 2 個/ }).click();
    await page.getByRole("button", { name: "解除匯入" }).click();

    await expect(projectRow("批次甲")).toHaveCount(0);
    await expect(projectRow("批次乙")).toHaveCount(0);
  });
});

test.describe("tree range selection", () => {
  test("Shift picks everything between the two clicks", async ({ page }) => {
    // The tree sorts by title, and Chinese numerals sort by code point, not by
    // value — ASCII names keep the expected order.
    await freshProject(page, ["T01", "T02", "T03", "T04", "T05"]);
    const tree = page.getByRole("tree", { name: "工作區檔案樹" });
    const nodeButton = (title: string) =>
      tree.locator(".node-list button").filter({ hasText: title }).first();
    const selected = tree.locator(".node-list button.selected");

    // The range is T02 → T04.
    await nodeButton("T02").click({ modifiers: [primaryAdditiveModifier] });
    await nodeButton("T04").click({ modifiers: ["Shift"] });
    await expect(selected).toHaveCount(3);
    for (const title of ["T02", "T03", "T04"]) {
      await expect(nodeButton(title)).toHaveClass(/selected/);
    }

    // Shift again from the same anchor resizes the range instead of adding.
    await nodeButton("T03").click({ modifiers: ["Shift"] });
    await expect(selected).toHaveCount(2);

    // Primary additive modifier + Shift adds a second range to the selection.
    await nodeButton("T05").click({ modifiers: [primaryAdditiveModifier] });
    await nodeButton("T01").click({
      modifiers: [primaryAdditiveModifier, "Shift"],
    });
    await expect(selected).toHaveCount(5);

    // Shift must not open the document; the drawer stays shut.
    await expect(page.getByRole("dialog", { name: "節點編輯" })).toBeHidden();

    await nodeButton("T03").click({ button: "right" });
    await page
      .getByRole("menu", { name: /檔案操作/ })
      .getByRole("menuitem", { name: /刪除 5 個節點/ })
      .click();
    await page.getByRole("button", { name: "移到垃圾桶" }).click();
    await expect(tree.locator(".node-list button")).toHaveCount(0);
  });
});
