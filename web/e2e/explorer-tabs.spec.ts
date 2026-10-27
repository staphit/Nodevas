import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// E-1: the Explorer's four tabs. The Explorer routes are answered here; the
// hub's own answers are covered by internal/server/explorer_test.go.

const workspace = {
  entries: [
    { path: "nodes", type: "dir", size: 0 },
    { path: "nodes/alpha.md", type: "file", size: 80, nodeId: "alpha", title: "設計稿" },
    { path: "graph.yaml", type: "file", size: 200 },
  ],
  hidden: 0,
  truncated: false,
};

const repos = {
  repos: [
    { runnerId: "rn1", runnerName: "mac-mini", online: true, repoId: "r1", name: "visual-editor", browsable: true },
    { runnerId: "rn2", runnerName: "laptop", online: false, repoId: "r2", name: "game", browsable: true },
  ],
};

const tree = {
  path: "",
  entries: [
    { name: "src", type: "dir", size: 0 },
    { name: "README.md", type: "file", size: 20 },
  ],
  hidden: 1,
  truncated: false,
};

const srcTree = { path: "src", entries: [{ name: "math.js", type: "file", size: 40 }], hidden: 0, truncated: false };

const recentRun = {
  kind: "run",
  id: "run:r1",
  runId: "r1",
  agent: "codex",
  model: "gpt-5.5",
  role: "executor",
  state: "succeeded",
  nodeId: "alpha",
  nodeTitle: "設計稿",
  tokens: { input: 52000, output: 3400, cacheRead: 12000 },
  costUsd: 0.09,
  costEstimated: true,
  startedAt: "2026-10-28T09:00:00Z",
  finishedAt: "2026-10-28T09:04:00Z",
  runnerName: "mac-mini",
  files: [{ path: "src/math.js", mark: "M" }],
};

async function mockExplorer(page: Page) {
  await page.route(/\/api\/explorer\/workspace\/files/, (route) => route.fulfill({ json: workspace }));
  await page.route(/\/api\/explorer\/workspace\/git(\?|$)/, (route) =>
    route.fulfill({ json: { backend: "git", branch: "main", pending: [{ path: "nodes/alpha.md", mark: "M" }], commits: [] } }),
  );
  await page.route(/\/api\/explorer\/repos(\?|$)/, (route) => route.fulfill({ json: repos }));
  await page.route(/\/api\/explorer\/repos\/rn1\/r1\/tree/, (route) =>
    route.fulfill({ json: new URL(route.request().url()).searchParams.get("path") === "src" ? srcTree : tree }),
  );
  await page.route(/\/api\/explorer\/repos\/rn1\/r1\/file/, (route) =>
    route.fulfill({ json: { path: "src/math.js", size: 40, binary: false, truncated: false, content: "export const add = (a, b) => a + b;\n" } }),
  );
  await page.route(/\/api\/explorer\/agents/, (route) => route.fulfill({ json: { live: [], external: [], recent: [recentRun] } }));
  await page.route(/\/api\/runs\/r1\/events/, (route) => route.fulfill({ json: { events: [], more: false } }));
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
}

test("the Explorer switches between Nodes, Files, Git and Agent", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockExplorer(page);
  await freshProject(page, ["設計稿"]);

  const rail = page.getByRole("group", { name: "側邊欄分頁" });
  await expect(rail.getByRole("button", { name: "節點" })).toHaveAttribute("aria-pressed", "true");

  await rail.getByRole("button", { name: "檔案" }).click();
  await expect(page.getByText("alpha.md")).toBeVisible();
  const repo = page.getByRole("region", { name: "Repo · visual-editor" });
  await repo.getByText("src", { exact: true }).click();
  await repo.getByText("math.js").click();
  await expect(page.getByText("唯讀 · 來自 runner「mac-mini」")).toBeVisible();
  await expect(page.getByText("export const add")).toBeVisible();
  await expect(page.getByText("runner「laptop」離線，暫時無法瀏覽這個 repo。")).toBeVisible();

  await rail.getByRole("button", { name: "Git" }).click();
  await expect(page.getByRole("region", { name: "工作區 · main" })).toBeVisible();

  await rail.getByRole("button", { name: "Agent" }).click();
  await page.getByRole("region", { name: "最近完成" }).getByText("設計稿").click();
  await expect(page.getByText("$0.09（估）").first()).toBeVisible();
  await expect(page.getByText("執行者").first()).toBeVisible();

  await page.getByRole("button", { name: "回到看板" }).click();
  await expect(page.getByText("export const add")).toHaveCount(0);
});

test("the Explorer fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockExplorer(page);
  await freshProject(page, ["設計稿"]);
  // A phone starts with the explorer folded away (an overlay); open it.
  const expand = page.locator(".lane-wrap.graph").getByRole("button", { name: "展開專案總管" });
  await expect(expand).toBeVisible();
  await expand.click();
  await page.getByRole("group", { name: "側邊欄分頁" }).getByRole("button", { name: "檔案" }).click();
  await expect(page.getByText("alpha.md")).toBeVisible();
  await expectNoHorizontalOverflow(page);
});
