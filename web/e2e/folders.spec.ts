import { type Page } from "@playwright/test";
import { declineTour, expect, test } from "./fixture";

// The whole suite shares one workspace, and earlier specs rename, delete and
// restore the fixture nodes. These tests therefore create the nodes they file,
// with names nothing else uses.

async function openApp(page: Page) {
  await declineTour(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "專案", exact: true })).toBeVisible();
}

async function createNode(page: Page, title: string) {
  await page.getByRole("button", { name: /新增節點/ }).first().click();
  await page.getByLabel("節點標題").fill(title);
  await page.getByRole("button", { name: "建立節點" }).click();
  await expect(nodeRow(page, title)).toBeVisible();
}

function nodeRow(page: Page, title: string) {
  return page.locator(".tree-node-list button").filter({ hasText: title }).first();
}

async function createFolder(page: Page, name: string) {
  await page.getByRole("button", { name: /新增資料夾/ }).first().click();
  const input = page.getByLabel("新資料夾名稱");
  await input.fill(name);
  await input.press("Enter");
  await expect(folderRow(page, name)).toBeVisible();
}

function folderRow(page: Page, name: string) {
  return page.locator(".node-folder-row").filter({ hasText: name });
}

function nodeInFolder(page: Page, folder: string, title: string) {
  return page
    .locator(".node-folder")
    .filter({ hasText: folder })
    .locator(".tree-node-list button")
    .filter({ hasText: title });
}

test.describe("node folders", () => {
  test("files a node into a folder and keeps it there", async ({ page }) => {
    await openApp(page);
    await createNode(page, "資料夾測試甲");
    await createFolder(page, "資料夾甲");

    await nodeRow(page, "資料夾測試甲").dragTo(folderRow(page, "資料夾甲"));
    await expect(nodeInFolder(page, "資料夾甲", "資料夾測試甲")).toBeVisible();

    // Filing a node moves its file and nothing else, so it survives a reload
    // and still opens.
    await page.reload();
    await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible();
    const filed = nodeInFolder(page, "資料夾甲", "資料夾測試甲");
    await expect(filed).toBeVisible();
    await filed.click();
    await expect(page.getByRole("dialog", { name: "節點編輯" })).toBeVisible();
  });

  test("deleting a folder keeps the nodes it held", async ({ page }) => {
    await openApp(page);
    await createNode(page, "資料夾測試乙");
    await createFolder(page, "資料夾乙");

    await nodeRow(page, "資料夾測試乙").dragTo(folderRow(page, "資料夾乙"));
    await expect(nodeInFolder(page, "資料夾乙", "資料夾測試乙")).toBeVisible();

    await folderRow(page, "資料夾乙").getByRole("button", { name: /刪除資料夾/ }).click();
    await expect(folderRow(page, "資料夾乙")).toHaveCount(0);
    // The node came back up to the root rather than going to the trash.
    await expect(nodeRow(page, "資料夾測試乙")).toBeVisible();
  });
});
