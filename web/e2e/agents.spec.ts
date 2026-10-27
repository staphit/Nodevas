import { type Page } from "@playwright/test";
import { declineTour, expect, test } from "./fixture";

// The hub's agent routes are answered here: these tests are about how the
// panel lays out a held message, a hold toggle and file chips, not about a
// session actually being on the network.

const session = {
  id: "claude:1",
  project: "p",
  actorId: "",
  actorName: "",
  name: "api-claude-with-a-rather-long-session-name",
  agent: "claude",
  role: "worker",
  repo: "nodevas",
  branch: "feat/network-w15",
  state: "busy",
  since: 1700000000000,
  doing: "Wire the hold policy into the inbox long poll",
  lastSeen: 1700000000000,
  online: true,
  inbound: "hold",
  model: "claude-opus-4-1-20250805",
};

const held = {
  id: "h1",
  project: "p",
  from: "bob/web-codex-reviewer",
  senderActor: "bob",
  senderSession: "web-codex-reviewer",
  senderAgent: "codex",
  to: session.name,
  body: "Please run the migration against staging before merging. ".repeat(4),
  createdAt: 1700000000000,
  status: "held",
  files: [
    { name: "a-very-long-migration-file-name-that-should-not-widen-the-panel.sql", size: 20480, sha256: "x" },
  ],
};

const delivered = {
  ...held,
  id: "d1",
  status: "delivered",
  notify: true,
  body: "Here are the notes.",
  files: [{ name: "notes.md", size: 120, sha256: "y" }],
};

async function mockAgents(page: Page) {
  const posts: string[] = [];
  await page.route(
    (url) => url.pathname.startsWith("/api/agents"),
    async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (request.method() === "POST") {
        posts.push(`${url.pathname} ${request.postData() ?? ""}`);
        if (url.pathname.endsWith("/inbound")) {
          return route.fulfill({ json: { ...session, inbound: "open" } });
        }
        if (url.pathname.endsWith("/approve")) {
          return route.fulfill({ json: { ...held, status: "delivered" } });
        }
        return route.fulfill({ json: { ok: true } });
      }
      if (url.pathname === "/api/agents") {
        return route.fulfill({ json: { sessions: [session], users: [], unreadCount: 0 } });
      }
      if (url.pathname === "/api/agents/messages") {
        return route.fulfill({
          json: { messages: url.searchParams.get("status") === "held" ? [held] : [held, delivered] },
        });
      }
      if (url.pathname === "/api/agents/asks") {
        return route.fulfill({ json: { asks: [] } });
      }
      return route.fulfill({ status: 404, json: { error: "not mocked" } });
    },
  );
  return posts;
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    const panel = document.getElementById("agent-network-panel");
    return Math.max(
      root.scrollWidth - root.clientWidth,
      panel ? panel.scrollWidth - panel.clientWidth : 0,
    );
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

for (const [name, viewport] of Object.entries({
  phone: { width: 390, height: 844 },
  "1440 at 150% zoom": { width: 960, height: 600 },
})) {
  test(`agent panel: held message, hold toggle and files fit on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const posts = await mockAgents(page);
    await declineTour(page);
    await page.goto("/");

    // The FAB says what it is and shows the held count, so it reads as
    // waiting before it opens.
    const fab = page.getByRole("button", { name: /^開啟 Agent 對話 · 1 件等你處理/ });
    await expect(fab).toContainText("Agent 對話");
    await expect(fab.locator(".agent-fab-badge.is-waiting")).toHaveText("1");
    await fab.click();
    const panel = page.getByRole("dialog", { name: "Agent 對話" });
    await expect(panel).toBeVisible();

    const heldSection = panel.getByRole("region", { name: "待審訊息（1）" });
    await expect(heldSection).toBeVisible();
    await expect(heldSection.getByText(/a-very-long-migration-file-name/)).toBeVisible();
    await expect(panel.getByText("claude-opus-4-1-20250805")).toBeVisible();
    await expect(panel.getByRole("link", { name: /notes\.md/ })).toHaveAttribute(
      "href",
      "/api/agents/messages/d1/files/notes.md",
    );
    await expectNoHorizontalOverflow(page);

    const approve = heldSection.getByRole("button", { name: "放行" });
    await expect(approve).toBeInViewport();
    const box = await approve.boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(viewport.width < 640 ? 40 : 28);
    await approve.click();
    await expect.poll(() => posts.some((p) => p.startsWith("/api/agents/messages/h1/approve"))).toBe(true);

    const toggle = panel.getByRole("switch");
    await expect(toggle).toHaveAttribute("aria-checked", "true");
    await toggle.click();
    await expect
      .poll(() => posts.find((p) => p.includes("/inbound")))
      .toBe('/api/agents/sessions/claude%3A1/inbound {"inbound":"open"}');

    await expect(panel.getByRole("button", { name: "通知收件者" })).toBeVisible();
    await expect(panel.getByRole("button", { name: "附加文字檔" })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });
}
