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

// W3: a finished run with a commit, an open PR and green CI.
const longLine = `const message = "${"a very long line that will not fit on a phone ".repeat(6)}";`;
const patch = [
  "diff --git a/internal/server/routes.go b/internal/server/routes.go",
  "index 3b18e51..a9c2f04 100644",
  "--- a/internal/server/routes.go",
  "+++ b/internal/server/routes.go",
  "@@ -1,4 +1,5 @@ package server",
  " package server",
  "-var old = 1",
  "+var old = 2",
  `+${longLine}`,
  " ",
  " func routes() {}",
  "diff --git a/docs/logo.png b/docs/logo.png",
  "new file mode 100644",
  "index 0000000..3333333",
  "Binary files /dev/null and b/docs/logo.png differ",
  "",
].join("\n");

const finishedRun = {
  ...run,
  id: "run2",
  status: "succeeded",
  summary: "Added the route.",
  finishedAt: "2026-10-24T08:10:00Z",
  commit: "c0ffee1",
  filesChanged: 2,
  additions: 2,
  deletions: 1,
  prUrl: "https://github.com/example/nodevas/pull/42",
  prNumber: 42,
  prState: "open",
  ciStatus: "success",
};

const runChanges = {
  commit: "c0ffee1",
  base: "main",
  files: [
    { path: "internal/server/routes.go", status: "modified", additions: 2, deletions: 1 },
    { path: "docs/logo.png", status: "added", additions: 0, deletions: 0, binary: true },
  ],
  additions: 2,
  deletions: 1,
  patch,
};

async function mockFinishedRun(page: Page) {
  let changesRequests = 0;
  // The project list feeds the graph badges: answer it with the real id of
  // the board's node, which only the server knows.
  await page.route(/\/api\/runs$/, async (route) => {
    const graph = await (await page.request.get("/api/graph")).json();
    const node = (graph.graph?.nodes ?? []).find((item: { title?: string }) => item.title === "設計稿");
    await route.fulfill({ json: { runs: node ? [{ ...finishedRun, nodeId: node.id }] : [] } });
  });
  await page.route(/\/api\/runs\?/, (route) => {
    const nodeId = new URL(route.request().url()).searchParams.get("nodeId") ?? "n";
    return route.fulfill({ json: { runs: [{ ...finishedRun, nodeId }] } });
  });
  await page.route(/\/api\/runs\/run2\/events/, (route) =>
    route.fulfill({ json: { events, more: false } }),
  );
  await page.route(/\/api\/runs\/run2\/changes/, (route) => {
    changesRequests++;
    return route.fulfill({ json: { changes: runChanges } });
  });
  return () => changesRequests;
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`run changes and diff viewer fit on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const changesRequests = await mockFinishedRun(page);
    await freshProject(page, ["設計稿"]);

    // The graph card badges the node's latest run from one project request.
    // (A phone may draw the board zoomed out, where cards drop their status
    // row, so there it only has to exist.)
    const badge = page.locator(".col-card-run");
    await expect(badge).toHaveCount(1);
    await expect(badge).toHaveClass(/col-card-run-pr-open/);
    if (viewport.width >= 900) await expect(badge).toBeVisible();

    await page.getByText("設計稿").first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "執行" }).click();

    const list = drawer.getByRole("list", { name: "執行紀錄" });
    await expect(list.getByRole("link", { name: /PR #42/ })).toHaveAttribute(
      "href",
      finishedRun.prUrl,
    );
    await expect(list.getByRole("img", { name: "CI 通過" })).toBeVisible();
    await expect(list.getByText("2 個檔案")).toBeVisible();
    await expectNoHorizontalOverflow(page);
    expect(changesRequests()).toBe(0);

    await drawer.getByRole("button", { name: "查看改動" }).click();
    const dialog = page.getByRole("dialog", { name: "改動" });
    await expect(dialog).toBeVisible();
    const files = dialog.getByRole("navigation", { name: "改動的檔案" });
    await expect(files.getByRole("button")).toHaveCount(2);
    expect(changesRequests()).toBe(1);

    if (viewport.width >= 900) {
      // Side by side by default, and the first file is already open.
      const table = dialog.getByRole("table", { name: "internal/server/routes.go" });
      await expect(table).toHaveClass(/is-split/);
      await dialog.getByRole("button", { name: "合併檢視" }).click();
      await expect(table).toHaveClass(/is-unified/);
    } else {
      await expect(dialog.getByRole("button", { name: "並排檢視" })).toHaveCount(0);
      await files.getByRole("button").first().click();
      await expect(dialog.getByRole("table", { name: "internal/server/routes.go" })).toHaveClass(
        /is-unified/,
      );
    }

    // Long lines scroll inside the diff, never the page.
    const scroller = dialog.locator(".run-diff-scroll");
    await expect(scroller).toBeVisible();
    const inner = await scroller.evaluate((element) => element.scrollWidth - element.clientWidth);
    expect(inner).toBeGreaterThan(0);
    await expectNoHorizontalOverflow(page);
    const box = await dialog.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width);

    if (viewport.width < 900) {
      await dialog.getByRole("button", { name: /所有檔案/ }).click();
      await files.getByRole("button").nth(1).click();
    } else {
      await files.getByRole("button").nth(1).click();
    }
    await expect(dialog.getByText("二進位檔案不同")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(drawer).toBeVisible();
  });
}
