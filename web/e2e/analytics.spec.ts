import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// E-2: Settings › Agents › Advanced › Agent performance. The analytics
// routes are answered here; nothing is applied without the confirm dialog.

const change = {
  type: "routing_rule_target",
  ruleId: "main",
  from: { agent: "claude", model: "sonnet" },
  to: { agent: "codex", model: "gpt-5.5" },
};

const insights = {
  days: 30,
  since: "2026-09-29T00:00:00Z",
  currency: "USD",
  totals: { costUsd: 3.6, estimatedCostUsd: 0.6, runs: 8, units: 3, delivered: 2, costPerDelivered: 1.8, firstPassRate: 0.3333, reworkRounds: 0.3333 },
  byRole: [{ key: "executor", costUsd: 3, runs: 5, delivered: 2, costPerDelivered: 1.5 }],
  byAgent: [],
  byModel: [],
  byNodeType: [],
  waste: { costUsd: 0.5, failedCostUsd: 0.5, retriedCostUsd: 0.1, abandonedCostUsd: 0.5, hotspots: [] },
  suggestions: [{ key: "cheaper_equal_tier:main:codex/gpt-5.5", kind: "cheaper_equal_tier", params: { role: "executor", saving: 40 }, change }],
};

const scorecards = {
  days: 30,
  since: "2026-09-29T00:00:00Z",
  minSamples: 10,
  rows: [
    {
      role: "executor", track: "all", agent: "codex", model: "gpt-5.5", runs: 12, verdicts: 0, samples: 12,
      successRate: 0.92, reliability: 1, delivery: 0.9, deliveryAdjusted: 0.91, firstPassRate: 0.75, reworkRounds: 0.25,
      costUsd: 10.8, costPerDelivered: 0.9, costEstimated: true, medianSeconds: 420, tddRate: 1, accuracy: null,
      score: 0.89, tier: "mid",
    },
  ],
};

const tiers = {
  days: 30,
  minSamples: 10,
  seniorMinSamples: 20,
  thresholds: { senior: 0.8, mid: 0.6 },
  required: { executor: "mid", tester: "mid", reviewer: "senior", qa: "senior", planner: "mid", researcher: "probation" },
  rows: [{ role: "executor", agent: "codex", model: "gpt-5.5", samples: 12, score: 0.89, tier: "mid" }],
};

async function mockAnalytics(page: Page, captured: { apply: unknown[]; settings: unknown[] }) {
  await page.route(/\/api\/analytics\/insights/, (route) => route.fulfill({ json: insights }));
  await page.route(/\/api\/analytics\/scorecards/, (route) => route.fulfill({ json: scorecards }));
  await page.route(/\/api\/analytics\/tiers/, (route) => route.fulfill({ json: tiers }));
  await page.route(/\/api\/analytics\/route-picks/, (route) => route.fulfill({ json: { picks: [] } }));
  await page.route(/\/api\/analytics\/retention/, (route) => route.fulfill({ status: 403, json: { error: "admins only" } }));
  await page.route(/\/api\/analytics\/settings/, (route) => {
    if (route.request().method() === "PUT") captured.settings.push(route.request().postDataJSON());
    return route.fulfill({ json: { project: "", autoRouting: route.request().method() === "PUT", explorationPct: 10, requiredTiers: {} } });
  });
  await page.route(/\/api\/analytics\/suggestions\/apply/, (route) => {
    captured.apply.push(route.request().postDataJSON());
    return route.fulfill({ json: {} });
  });
}

test("Agent performance shows scorecards and applies a suggestion only once confirmed", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const captured = { apply: [] as unknown[], settings: [] as unknown[] };
  await mockAnalytics(page, captured);
  await freshProject(page, ["設計稿"]);
  await page.getByRole("button", { name: /設定|Settings/ }).first().click();
  await page.getByRole("navigation", { name: /設定|Settings/ }).getByRole("button", { name: /^(代理|Agents)/ }).click();
  const advanced = page.getByRole("region", { name: /進階|Advanced/ });
  await advanced.getByRole("button", { name: /^(進階|Advanced)/ }).click();
  await advanced.getByRole("button", { name: /^Agent 表現/ }).click();

  const row = page.getByRole("region", { name: "Agent 表現" });
  await expect(row).toBeVisible();

  await row.getByRole("button", { name: "成績單" }).click();
  const table = row.getByRole("table");
  await expect(table.getByText("codex")).toBeVisible();
  await expect(table.getByText("中階")).toBeVisible();

  await row.getByRole("button", { name: "洞察" }).click();
  await row.getByRole("button", { name: "套用…" }).click();
  const dialog = page.getByRole("dialog", { name: "要套用這個變更嗎？" });
  await expect(dialog).toBeVisible();
  expect(captured.apply).toHaveLength(0);
  await dialog.getByRole("button", { name: "套用" }).click();
  await expect.poll(() => captured.apply.length).toBe(1);
  expect(captured.apply[0]).toEqual({ key: "cheaper_equal_tier:main:codex/gpt-5.5", change });

  await row.getByRole("button", { name: "等級與分派" }).click();
  await row.getByRole("switch", { name: "這個專案自動分派" }).click();
  await expect.poll(() => captured.settings.length).toBe(1);
  expect(captured.settings[0]).toMatchObject({ autoRouting: true });
});
