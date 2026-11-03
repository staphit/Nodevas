import { expect, freshProject, test } from "./fixture";

// A project's office style against the real binary: chosen from the door
// sign in Station, saved in graph.yaml (office_style) through the graph save,
// kept across a reload and shown in Settings › This project.

test("office style: chosen from the door sign, kept, and shown in Settings", async ({ page }) => {
  await freshProject(page);
  await page.addInitScript(() => {
    localStorage.setItem("vised-language", "en");
    localStorage.setItem("vised-project-mode", "station");
  });
  await page.reload();

  const office = page.getByRole("region", { name: /^Office / });
  await expect(office).toBeVisible({ timeout: 15_000 });
  // Unset, the office has its layout's style, picked automatically.
  const sign = office.getByRole("button", { name: /, office style: .+\. Change it$/ });
  await sign.click();
  const picker = page.getByRole("dialog", { name: /^Office style of / });
  await expect(picker.getByRole("radio", { name: /^Automatic \(/ })).toHaveAttribute("aria-checked", "true");
  const saved = page.waitForResponse((response) => response.url().endsWith("/api/graph") && response.request().method() === "PUT");
  await picker.getByRole("radio", { name: "Café" }).click();
  expect((await saved).ok()).toBeTruthy();
  await expect(page.locator(".px-office-room")).toHaveClass(/px-style--cafe/);
  await page.keyboard.press("Escape");
  await expect(picker).toBeHidden();

  await page.reload();
  await expect(page.getByRole("button", { name: /, office style: Café\. Change it$/ })).toBeVisible({ timeout: 15_000 });
  const graph = await (await page.request.get("/api/graph")).json();
  expect(graph.graph.officeStyle).toBe("cafe");

  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("navigation", { name: "Settings" }).getByRole("button", { name: /^This project/ }).click();
  await expect(page.getByRole("button", { name: /^Office style\s*Café/ })).toBeVisible();
});
