import { type Page } from "@playwright/test";
import { declineTour, expect, freshProject, test } from "./fixture";

async function openApp(page: Page) {
  await declineTour(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible();
}

/** Nothing may push the page sideways: horizontal scrolling hides controls. */
async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    return root.scrollWidth - root.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

test.describe("responsive", () => {
  // 1440 and 1024 are the two widths the plan asks for; the zoom levels are
  // simulated the way a browser applies them — same layout, fewer CSS pixels.
  for (const [name, viewport] of Object.entries({
    "1440 wide": { width: 1440, height: 900 },
    "1024 wide": { width: 1024, height: 768 },
    "1440 at 125% zoom": { width: 1152, height: 720 },
    "1440 at 150% zoom": { width: 960, height: 600 },
  })) {
    test(`stays usable at ${name}`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await openApp(page);

      await expect(page.getByRole("combobox", { name: "專案", exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: "專案設定" })).toBeVisible();
      await expect(page.getByRole("button", { name: /新增節點/ }).first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
    });
  }

  test("the drawer overlays instead of squeezing the board on a narrow screen", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1024, height: 768 });
    // Its own project: relying on whatever the previous spec file left behind
    // makes this test fail whenever that spec changes.
    await freshProject(page, ["設計稿"]);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    const box = await drawer.boundingBox();
    expect(box).not.toBeNull();
    // Overlay: it reaches the right edge and takes most of the width.
    expect(box!.width).toBeGreaterThan(400);
    await expectNoHorizontalOverflow(page);
  });
});

test.describe("theme", () => {
  test("switches light and dark and remembers the choice", async ({ page }) => {
    await openApp(page);

    const root = page.locator("html");
    const initial = await root.getAttribute("data-theme");
    const toggle = page.getByRole("button", {
      name: initial === "dark" ? /切換淺色模式/ : /切換深色模式/,
    });
    await toggle.click();

    const switched = initial === "dark" ? "light" : "dark";
    await expect(root).toHaveAttribute("data-theme", switched);

    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", switched);
  });

  test("reset restores the default layout from settings", async ({ page }) => {
    await openApp(page);

    const handle = page.getByRole("separator", { name: "調整專案總管寬度" });
    await handle.focus();
    await page.keyboard.press("Shift+ArrowRight");
    await expect(handle).not.toHaveAttribute("aria-valuenow", "304");

    await page.getByRole("button", { name: "專案設定" }).click();
    const dialog = page.getByRole("dialog", { name: "專案設定" });
    await dialog.getByRole("tab", { name: "外觀與版面" }).click();
    await dialog.getByRole("button", { name: "重設全部本機偏好" }).click();
    await page.getByRole("button", { name: "重設", exact: true }).click();

    // The reset wipes every local preference — including the pinned zh-TW
    // language — so the dialog has to be found again under its English name.
    // The notice itself was translated at click time, before the language
    // flipped, so it still reads in Chinese inside the English dialog.
    const resetDialog = page.getByRole("dialog", { name: "Project settings" });
    await expect(resetDialog.getByText("已重設全部本機偏好")).toBeVisible();
    await resetDialog.getByRole("button", { name: "Close" }).click();
    await expect(page.getByRole("separator", { name: "Resize project explorer" })).toHaveAttribute(
      "aria-valuenow",
      "304",
    );
  });
});
