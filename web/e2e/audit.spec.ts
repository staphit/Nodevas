import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// The e2e server is a loopback one, with no accounts and so no administrators;
// the sign-in status and the audit routes are answered here. These tests are
// about Settings → Audit log, not about what the hub records.

const actor = { id: "u-ann", name: "ann", email: "ann@example.test", role: "admin" };

const HOSTILE = '<img src=x onerror="window.__pwned=1">mallory@example.test';

const entries = [
  {
    at: "2026-10-26T08:00:00Z",
    actor: "",
    id: "",
    op: "auth.signin.failed",
    target: "",
    ip: "198.51.100.9",
    detail: { email: HOSTILE, reason: "unknown-account" },
  },
  { at: "2026-10-26T07:59:00Z", actor: "ann", id: "u-ann", op: "auth.signin", target: "", ip: "203.0.113.7", detail: { method: "otp" } },
  { at: "2026-10-26T07:58:00Z", actor: "ann", id: "u-ann", op: "auth.stepup", target: "", ip: "203.0.113.7", detail: null },
  { at: "2026-10-26T07:57:00Z", actor: "ann", id: "u-ann", op: "runner.link.create", target: "laptop", ip: "203.0.113.7", detail: null },
];

async function mockAudit(page: Page) {
  let health = {
    status: "degraded",
    writeStatus: "healthy",
    fallbackEvents: 2,
    acknowledgedFallbackEvents: 0,
    unreconciledEvents: 2,
    lastFailureAt: "2026-10-26T06:00:00Z",
    lastError: "database is locked",
  };
  const acknowledged: unknown[] = [];
  await page.route(/\/api\/auth\/status/, (route) =>
    route.fulfill({ json: { mode: "accounts", authenticated: true, actor } }),
  );
  await page.route(
    (url) => url.pathname.startsWith("/api/audit"),
    (route) => {
      const { pathname } = new URL(route.request().url());
      if (pathname === "/api/audit/health/acknowledge") {
        acknowledged.push(route.request().postDataJSON());
        health = { ...health, status: "healthy", acknowledgedFallbackEvents: 2, unreconciledEvents: 0 };
        return route.fulfill({ json: health });
      }
      if (pathname === "/api/audit/health") {
        return route.fulfill({ status: health.status === "healthy" ? 200 : 503, json: health });
      }
      if (pathname === "/api/audit/limiter") {
        return route.fulfill({
          json: {
            active_buckets: 3,
            max_entries: 10000,
            rejected_total: 5,
            rejected_by_class: { "public-auth": 5 },
            semaphores: { heavy: { in_use: 0, capacity: 2 } },
          },
        });
      }
      return route.fulfill({ json: { entries } });
    },
  );
  return acknowledged;
}

async function openAudit(page: Page) {
  await page.getByRole("button", { name: "帳號與設定", exact: true }).click();
  await page.getByRole("menuitem", { name: "設定", exact: true }).click();
  await page.getByRole("navigation", { name: "設定" }).getByRole("button", { name: /^帳號/ }).click();
  await page.getByRole("button", { name: /^稽核紀錄/ }).click();
}

test("an administrator reads the audit trail and acknowledges fallbacks", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const acknowledged = await mockAudit(page);
  await freshProject(page, ["設計稿"]);
  await openAudit(page);

  const trail = page.getByRole("region", { name: "最近的事件" });
  await expect(trail.getByText("auth.signin.failed")).toBeVisible();
  // Typed at the sign-in form by a stranger: shown as text, never run.
  await expect(trail.getByText(HOSTILE)).toBeVisible();
  expect(await page.evaluate(() => (window as { __pwned?: number }).__pwned)).toBeUndefined();
  await expect(trail.locator("img")).toHaveCount(0);

  await trail.getByLabel("事件類別").selectOption("devices");
  await expect(trail.getByText("runner.link.create")).toBeVisible();
  await expect(trail.getByText("auth.signin.failed")).toHaveCount(0);
  await trail.getByLabel("事件類別").selectOption("all");
  await trail.getByLabel("搜尋事件").fill("198.51.100");
  await expect(trail.getByText("共 4 筆，顯示 1 筆")).toBeVisible();

  const health = page.getByRole("region", { name: "稽核狀態" });
  await expect(health.getByText(/有 2 筆事件只寫進了伺服器 log/)).toBeVisible();
  await health.getByRole("button", { name: "確認已核對" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "確認已核對" }).click();
  await expect(health.getByText("已確認。")).toBeVisible();
  await expect(health.getByText("每筆稽核事件都有寫進資料庫。")).toBeVisible();
  expect(acknowledged).toEqual([{ expectedFallbackEvents: 2 }]);

  await expect(page.getByRole("region", { name: "流量限制" }).getByText("被拒：登入")).toBeVisible();
});

test("the audit log fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockAudit(page);
  await freshProject(page, ["設計稿"]);
  await openAudit(page);
  await expect(page.getByRole("region", { name: "最近的事件" }).getByText("auth.signin.failed")).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
});
