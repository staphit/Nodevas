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

/**
 * New projects start on the Standard stages (Tester → Executor → Reviewer),
 * whose dispatch dialog is roles.spec.ts's. These tests are about the plain
 * dispatch form, which a project without stages keeps: clear the stages and
 * reload the board.
 */
async function withoutStages(page: Page) {
  const cleared = await page.request.post("/api/graph/ops", {
    data: { ops: [{ kind: "workflow", workflow: null }] },
  });
  expect(cleared.ok(), await cleared.text()).toBeTruthy();
  await page.reload();
  await expect(page.getByText("設計稿").first()).toBeVisible();
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
    await withoutStages(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "活動", exact: true }).click();

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
    await drawer.getByRole("tab", { name: "活動", exact: true }).click();

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

// W4: approve merging the run's PR — the phone case: read the diff, merge.
const headSha = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678";
const greenRun = { ...finishedRun, prHeadSha: headSha };

async function mockMergeableRun(page: Page) {
  const posted: unknown[] = [];
  let pending = false;
  const current = (nodeId: string) => ({
    ...greenRun,
    nodeId,
    ...(pending
      ? {
          merge: {
            id: "m1",
            runId: "run2",
            headSha,
            status: "pending",
            requestedBy: "alice",
            // Now: a pending merge older than 15 minutes reads as stale.
            createdAt: new Date().toISOString(),
          },
        }
      : {}),
  });
  await page.route(/\/api\/runs$/, (route) => route.fulfill({ json: { runs: [] } }));
  await page.route(/\/api\/runs\?/, (route) => {
    const nodeId = new URL(route.request().url()).searchParams.get("nodeId") ?? "n";
    return route.fulfill({ json: { runs: [current(nodeId)] } });
  });
  await page.route(/\/api\/runs\/run2\/events/, (route) =>
    route.fulfill({ json: { events, more: false } }),
  );
  await page.route(/\/api\/runs\/run2\/changes/, (route) =>
    route.fulfill({ json: { changes: runChanges } }),
  );
  await page.route(/\/api\/runs\/run2\/merge$/, async (route) => {
    posted.push(route.request().postDataJSON());
    pending = true;
    const graph = await (await page.request.get("/api/graph")).json();
    const node = (graph.graph?.nodes ?? []).find((item: { title?: string }) => item.title === "設計稿");
    await route.fulfill({ status: 202, json: current(node?.id ?? "n") });
  });
  return posted;
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`merge approval from the diff fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const posted = await mockMergeableRun(page);
    await freshProject(page, ["設計稿"]);

    await page.getByText("設計稿").first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "活動", exact: true }).click();

    // The run's header offers the merge too.
    const detail = drawer.getByRole("region", { name: "執行詳情" });
    await expect(detail.getByRole("button", { name: "合併 PR #42" })).toBeEnabled();
    await expectNoHorizontalOverflow(page);

    await drawer.getByRole("button", { name: "查看改動" }).click();
    const diff = page.getByRole("dialog", { name: "改動" });
    await expect(diff).toBeVisible();
    const mergeButton = diff.getByRole("button", { name: "合併 PR #42" });
    await expect(mergeButton).toBeEnabled();
    await expect(mergeButton).toBeInViewport();
    const buttonBox = await mergeButton.boundingBox();
    expect(buttonBox).not.toBeNull();
    if (viewport.width < 640) expect(buttonBox!.height).toBeGreaterThanOrEqual(44);
    await expectNoHorizontalOverflow(page);

    await mergeButton.click();
    const confirm = page.getByRole("alertdialog", { name: "合併 PR #42？" });
    await expect(confirm).toBeVisible();
    await expect(confirm.getByText("agent/a-very-long-node-identifier-that-should-wrap-run1 → main")).toBeVisible();
    await expect(confirm.getByText("a1b2c3d", { exact: true })).toBeVisible();
    await expect(confirm.getByText(/預設 squash/)).toBeVisible();
    const box = await confirm.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width);
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height);
    await expectNoHorizontalOverflow(page);

    // Escape backs out of the confirmation only; the diff stays.
    await page.keyboard.press("Escape");
    await expect(confirm).toHaveCount(0);
    await expect(diff).toBeVisible();
    expect(posted).toHaveLength(0);

    await mergeButton.click();
    await confirm.getByRole("button", { name: "合併 PR #42" }).click();
    await expect(confirm).toHaveCount(0);
    expect(posted).toEqual([{ headSha }]);
    await expect(diff.getByText("等 runner 合併中…")).toBeVisible();
    await expect(diff.getByRole("button", { name: "合併 PR #42" })).toHaveCount(0);
    await expectNoHorizontalOverflow(page);

    await page.keyboard.press("Escape");
    await expect(diff).toHaveCount(0);
    await expect(detail.getByText("等 runner 合併中…")).toBeVisible();
  });
}

// W5: routing rules in their Settings part, and a dispatch pre-filled by them.
const savedRule = {
  id: "rule-review",
  project: "",
  match: "tag",
  value: "review",
  target: { agent: "codex", model: "gpt-5" },
  fallbacks: [{ agent: "claude", model: "opus" }],
  pipeline: {
    implement: { agent: "claude", model: "opus" },
    review: { agent: "codex", model: "gpt-5" },
    maxRounds: 2,
  },
};

async function mockRouting(page: Page) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const twoAgents = {
    ...runner,
    ownerId: status.actor?.id ?? "",
    info: {
      ...runner.info,
      agents: [
        ...runner.info.agents,
        { kind: "codex", installed: true, version: "0.40.0", models: [{ id: "gpt-5", default: true }] },
      ],
    },
  };
  const puts: unknown[] = [];
  let rules: unknown[] = [savedRule];
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [twoAgents] } }));
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [] } }));
  await page.route(/\/api\/runs\/routing/, async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON();
      puts.push(body);
      rules = body.rules;
    }
    await route.fulfill({ json: { rules } });
  });
  // The hub names the rule, not its tag: the dialog reads the tag from the rules.
  await page.route(/\/api\/runs\/route\?/, (route) =>
    route.fulfill({
      json: {
        ruleId: savedRule.id,
        match: "tag",
        target: savedRule.target,
        fallbacks: savedRule.fallbacks,
        pipeline: savedRule.pipeline,
      },
    }),
  );
  return puts;
}

async function expectInside(page: Page, box: { x: number; width: number } | null, width: number) {
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width);
  await expectNoHorizontalOverflow(page);
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`routing rules editor fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const puts = await mockRouting(page);
    await freshProject(page, ["設計稿"]);

    await page.evaluate(() => window.dispatchEvent(new CustomEvent("nodevas-open-runners")));
    await page.getByRole("button", { name: /^進階/ }).click();
    await page.getByRole("button", { name: /^路由規則/ }).click();
    const dialog = page.getByRole("region", { name: "路由規則", exact: true });
    await expect(dialog).toBeVisible();

    const first = dialog.getByRole("listitem", { name: "規則 1" });
    await expect(first).toBeVisible();
    await expect(first.getByText("標籤 = review → codex/gpt-5")).toBeVisible();
    await expect(first.getByRole("checkbox", { name: "管線：實作 → 審查" })).toBeChecked();
    await expectInside(page, await dialog.boundingBox(), viewport.width);

    await dialog.getByRole("button", { name: "新增規則" }).click();
    const second = dialog.getByRole("listitem", { name: "規則 2" });
    await second.getByRole("combobox", { name: "比對" }).selectOption("kind");
    // The kind is picked from the project's types that agents work.
    await second.getByRole("combobox", { name: "類型" }).selectOption("scene");
    await second.getByRole("combobox", { name: "目標 agent" }).selectOption("claude");
    await second.getByText("Fallback 鏈（0）").click();
    await second.getByRole("button", { name: "新增 fallback" }).click();
    await second.getByRole("combobox", { name: "Fallback 1 agent" }).selectOption("codex");
    await second.getByRole("button", { name: "上移規則 2" }).click();
    await expectInside(page, await dialog.boundingBox(), viewport.width);

    await dialog.getByRole("button", { name: "儲存規則" }).click();
    await expect(dialog.getByText("已儲存")).toBeVisible();
    expect(puts).toHaveLength(1);
    const sent = (puts[0] as { rules: { id: string; match: string; value?: string }[] }).rules;
    expect(sent.map((rule) => [rule.match, rule.value])).toEqual([
      ["kind", "scene"],
      ["tag", "review"],
    ]);
    expect(sent[0]).toMatchObject({
      target: { agent: "claude", model: "default" },
      fallbacks: [{ agent: "codex", model: "gpt-5" }],
    });
    await expectNoHorizontalOverflow(page);
  });

  test(`dispatch pre-filled from a routing rule fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockRouting(page);
    await freshProject(page, ["設計稿"]);
    await withoutStages(page);
    await page.getByText("設計稿").first().click();

    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    await drawer.getByRole("tab", { name: "活動", exact: true }).click();
    await drawer.getByRole("button", { name: "派給 agent…" }).click();

    const dialog = page.getByRole("dialog", { name: "派給 agent" });
    await expect(dialog.getByTestId("dispatch-rule-note")).toHaveText("來自規則：標籤 = review");
    // A pipeline's implementer is the form's own agent and model.
    await expect(dialog.getByRole("combobox", { name: "Agent", exact: true })).toHaveValue("claude");
    await expect(dialog.getByRole("combobox", { name: "模型", exact: true })).toHaveValue("opus");
    await expect(dialog.getByRole("checkbox", { name: "管線：實作 → 審查" })).toBeChecked();
    await expect(dialog.getByRole("combobox", { name: "審查 agent" })).toHaveValue("codex");
    await expect(dialog.getByRole("combobox", { name: "輪數" })).toHaveValue("2");

    const chain = dialog.getByTestId("dispatch-fallbacks");
    await expect(chain).not.toHaveAttribute("open", "");
    await chain.getByText("Fallback 鏈（1）").click();
    await expect(chain.getByRole("combobox", { name: "Fallback 1模型" })).toHaveValue("opus");
    await expectInside(page, await dialog.boundingBox(), viewport.width);
  });
}
