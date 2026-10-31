import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// The usage routes are answered here: these tests are about the dialog's
// layout at desktop and phone width, not about the sums behind it.

function bucket(input: number, output: number, cache: number, cost: number, extra = {}) {
  return {
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
  };
}

const byDay = Array.from({ length: 30 }, (_, i) => {
  const date = new Date(Date.UTC(2026, 8, 25 + i));
  const wave = Math.round(40_000 + 30_000 * Math.sin(i / 3));
  return { key: date.toISOString().slice(0, 10), ...bucket(wave * 3, wave, wave * 6, wave / 40_000) };
});

const report = {
  days: 30,
  since: "2026-09-25T00:00:00Z",
  currency: "USD",
  pricesAsOf: "2026-10-06",
  totals: bucket(3_600_000, 1_200_000, 7_200_000, 42.37, { unpricedTokens: 120_000, runs: 14, sessions: 6 }),
  byAgent: [
    { key: "claude", ...bucket(2_400_000, 900_000, 6_000_000, 31.2, { runs: 9, sessions: 4 }) },
    { key: "codex", ...bucket(900_000, 250_000, 1_000_000, 9.1, { runs: 4, sessions: 1 }) },
    { key: "antigravity", ...bucket(300_000, 50_000, 200_000, 2.07, { runs: 1, sessions: 1, unpricedTokens: 120_000 }) },
  ],
  byModel: [
    { key: "claude-opus-5-5", agent: "claude", ...bucket(2_000_000, 800_000, 5_000_000, 29) },
    { key: "claude-haiku-5-5", agent: "claude", ...bucket(400_000, 100_000, 1_000_000, 2.2) },
    { key: "gpt-5.5", agent: "codex", ...bucket(900_000, 250_000, 1_000_000, 9.1) },
    { key: "gemini-3.8-flash-medium-with-a-long-name", agent: "antigravity", ...bucket(300_000, 50_000, 200_000, 0, { unpricedTokens: 120_000 }) },
  ],
  byDay,
  sessions: [
    {
      id: "claude:abc",
      source: "session",
      name: "visual-editor-claude-opus-a-rather-long-session-name",
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
      name: "設計稿",
      actorName: "",
      agent: "codex",
      model: "gpt-5.5",
      models: [],
      nodeId: "alpha",
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
    { model: "gpt-5.5", inputPerMTok: 1.25, outputPerMTok: 10, cacheReadPerMTok: 0.125, cacheWritePerMTok: 0, source: "custom" },
  ],
};

async function mockUsage(page: Page) {
  await page.route(/\/api\/usage\/prices/, (route) => route.fulfill({ json: prices }));
  await page.route(/\/api\/usage\?/, (route) => route.fulfill({ json: report }));
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    return root.scrollWidth - root.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

async function expectInViewport(page: Page, width: number) {
  const dialog = page.getByRole("region", { name: "用量", exact: true });
  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  const gutter = width < 640 ? 15 : 0;
  expect(box!.x).toBeGreaterThanOrEqual(gutter);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width - gutter);
}

async function shot(page: Page, name: string) {
  const dir = process.env.NODEVAS_USAGE_SHOTS;
  if (dir) await page.screenshot({ path: `${dir}/${name}.png` });
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`usage dialog fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockUsage(page);
    await freshProject(page, ["設計稿"]);
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("nodevas-open-usage")));

    const dialog = page.getByRole("region", { name: "用量", exact: true });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("$42.37").first()).toBeVisible();
    await expect(dialog.getByText(/token 沒有價格/)).toBeVisible();
    await expect(dialog.getByRole("img", { name: /每日用量圖/ })).toBeVisible();
    await expectInViewport(page, viewport.width);
    await expectNoHorizontalOverflow(page);
    await shot(page, `${name}-overview`);

    await dialog.locator(".usage-chart-plot .usage-hit").nth(20).hover();
    await expect(dialog.locator(".usage-tooltip")).toBeVisible();
    await shot(page, `${name}-tooltip`);

    await dialog.getByRole("tab", { name: "工作階段" }).click();
    await expect(dialog.getByText("patrick")).toBeVisible();
    await expectNoHorizontalOverflow(page);
    await shot(page, `${name}-sessions`);

    await dialog.getByRole("tab", { name: "價格" }).click();
    await expect(dialog.getByRole("textbox", { name: "模型" })).toHaveValue("gpt-5.5");
    await expectNoHorizontalOverflow(page);
    await shot(page, `${name}-prices`);
  });
}

test("usage opens from the agents panel", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockUsage(page);
  await freshProject(page, ["設計稿"]);
  await page.route((url) => url.pathname.startsWith("/api/agents"), (route) =>
    route.fulfill({ json: { sessions: [], messages: [], asks: [] } }),
  );
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
  });
  // With no agent online and nothing unread the floating button stays away;
  // the shortcut still opens Agent chat.
  await expect(page.getByRole("button", { name: /^開啟 Agent 對話/ })).toHaveCount(0);
  await page.keyboard.press("Control+Shift+A");
  await page.getByRole("dialog", { name: "Agent 對話" }).getByRole("button", { name: "用量" }).click();
  const dialog = page.getByRole("region", { name: "用量", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("$42.37").first()).toBeVisible();
  await shot(page, "desktop-dark");
});
