import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// The hub's run routes are answered here rather than by a runner: these tests
// are about the run view's layout, not about a run actually happening.

const runner = {
  id: "r1",
  name: "desktop",
  ownerId: "",
  ownerName: "",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.3.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless"],
    agents: [
      {
        kind: "claude",
        installed: true,
        version: "2.1.0 (Claude Code)",
        models: [
          { id: "default", label: "CLI default", default: true },
          { id: "opus", label: "Opus" },
        ],
      },
    ],
    repos: [{ id: "repo1", name: "nodevas", path: "H:\\visual-editor" }],
  },
};

const run = {
  id: "run1",
  project: "p",
  nodeId: "n",
  runnerId: "r1",
  runnerName: "desktop",
  agent: "claude",
  model: "opus",
  repoId: "repo1",
  repoName: "nodevas",
  base: "main",
  branch: "agent/a-very-long-node-identifier-that-should-wrap-run1",
  prompt: "Keep the diff small.",
  status: "running",
  exitCode: 0,
  usage: { inputTokens: 120345, outputTokens: 4567, costUsd: 0.4211 },
  createdBy: "u1",
  createdByName: "alice",
  createdAt: "2026-10-24T08:00:00Z",
};

const at = "2026-10-24T08:00:01.000Z";
const events = [
  { seq: 1, type: "log", at, text: "worktree created at C:\\runner\\worktrees\\run1" },
  { seq: 2, type: "thinking", at, text: "Read the router before changing anything." },
  { seq: 3, type: "text", at, text: "I will add the route.\n\nThen run the tests." },
  {
    seq: 4,
    type: "tool_call",
    at,
    tool: "Bash",
    toolId: "t1",
    input: JSON.stringify({ command: "go test ./internal/... -run TestAVeryLongTestNameThatKeepsGoing -count=1" }),
  },
  { seq: 5, type: "tool_result", at, toolId: "t1", text: "FAIL\tinternal/server\t0.123s", isError: true },
  { seq: 6, type: "usage", at, usage: { inputTokens: 120345, outputTokens: 4567, costUsd: 0.4211 } },
];

async function mockRuns(page: Page) {
  // The dialog offers only the viewer's own runners, so the fake one belongs
  // to whoever this server says the browser is.
  const status = await (await page.request.get("/api/auth/status")).json();
  const owned = { ...runner, ownerId: status.actor?.id ?? "" };
  await page.route("**/api/runners", (route) =>
    route.fulfill({ json: { runners: [owned] } }),
  );
  await page.route(/\/api\/runs\/run1\/events/, (route) =>
    route.fulfill({ json: { events, more: false } }),
  );
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [run] } }));
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement;
    return root.scrollWidth - root.clientWidth;
  });
  expect(overflow).toBeLessThanOrEqual(1);
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`run view and dispatch dialog fit on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockRuns(page);
    await freshProject(page, ["設計稿"]);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "執行" }).click();

    await expect(drawer.getByText("執行中").first()).toBeVisible();
    const timeline = drawer.getByRole("list", { name: "執行過程" });
    await expect(timeline.getByText("I will add the route.")).toBeVisible();
    await expect(timeline.getByText("錯誤結果 · Bash")).toBeVisible();
    await expect(drawer.getByRole("button", { name: "取消執行" })).toBeVisible();
    await expectNoHorizontalOverflow(page);

    await drawer.getByRole("button", { name: "派給 agent…" }).click();
    const dialog = page.getByRole("dialog", { name: "派給 agent" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("combobox", { name: "Runner" })).toHaveValue("r1");
    await expect(dialog.getByRole("combobox", { name: "模型" })).toHaveValue("default");
    await expect(dialog.getByText(/要等 runner 那端有人接受/)).toBeVisible();
    const box = await dialog.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(viewport.width < 640 ? 15 : 0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width - (viewport.width < 640 ? 15 : 0));
    await expectNoHorizontalOverflow(page);
  });
}
