import { execFileSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, freshProject, test } from "./fixture";

const PACKS_DIR = join(import.meta.dirname, "..", "..", "examples", "view-packs");

/** Zips an examples/view-packs folder the way a person would: the folder
 * itself at the top of the archive. */
function zipExample(id: string): string {
  const out = join(mkdtempSync(join(tmpdir(), "nodevas-pack-")), `${id}.zip`);
  const script = [
    "import os, sys, zipfile",
    "root, name, out = sys.argv[1], sys.argv[2], sys.argv[3]",
    "with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:",
    "    for d, _, files in os.walk(os.path.join(root, name)):",
    "        for f in files:",
    "            p = os.path.join(d, f)",
    "            z.write(p, os.path.relpath(p, root))",
  ].join("\n");
  execFileSync("python3", ["-I", "-c", script, PACKS_DIR, id, out]);
  return out;
}

// The whole path against the real hub: install from Appearance settings, the
// pack served with its headers, Station drawn with it, then removed.
test("view pack: installed from settings, drawn in Station, removed", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  // Any earlier run's pack would make this one a reinstall.
  await page.request.delete("/api/packs/candy-dungeon");

  await page.getByRole("button", { name: "帳號與設定", exact: true }).click();
  await page.getByRole("menuitem", { name: "設定", exact: true }).click();
  await page.getByRole("navigation", { name: "設定" }).getByRole("button", { name: /^一般/ }).click();
  await page.getByRole("button", { name: /^編輯器、版面與主題包/ }).click();
  const dialog = page.getByRole("region", { name: "編輯器、版面與主題包", exact: true });
  await dialog.locator('input[type="file"][accept*="zip"]').setInputFiles(zipExample("candy-dungeon"));
  await expect(dialog.getByText("已安裝 candy-dungeon.zip。")).toBeVisible({ timeout: 15_000 });
  await expect(dialog.getByText("糖果地城")).toBeVisible();

  // Served as an image only, sandboxed.
  const list = await (await page.request.get("/api/packs")).json();
  const pack = list.packs.find((item: { id: string }) => item.id === "candy-dungeon");
  const boss = await page.request.get(pack.sprites.boss);
  expect(boss.headers()["content-type"]).toBe("image/svg+xml");
  expect(boss.headers()["x-content-type-options"]).toBe("nosniff");
  expect(boss.headers()["content-security-policy"]).toContain("sandbox");

  await dialog.getByRole("button", { name: "在工作站開啟" }).click();
  const stage = page.getByRole("region", { name: "糖果地城" });
  await expect(stage).toBeVisible({ timeout: 15_000 });
  const cake = stage.locator("img.game-boss-art.pack-sprite");
  await expect(cake).toHaveAttribute("src", /\/api\/packs\/candy-dungeon\/files\/images\/cake\.svg/);
  await expect.poll(() => cake.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

  // Removed: Station falls back to the built-in boss map.
  const removed = await page.request.delete("/api/packs/candy-dungeon");
  expect(removed.status()).toBe(204);
  await expect(page.getByRole("region", { name: "頭目地圖" })).toBeVisible({ timeout: 15_000 });
  await expect(page.locator(".station img.pack-sprite")).toHaveCount(0);
});

test("view pack: a pack that breaks the rules is refused with the reason", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  const bad = await page.request.post("/api/packs", {
    data: { zip: Buffer.from("not a zip").toString("base64") },
  });
  expect(bad.status()).toBe(400);
  expect((await bad.json()).error).toContain("invalid pack");
});
