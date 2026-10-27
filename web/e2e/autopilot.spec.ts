import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// The autopilot routes are answered here: these tests are about the panel's
// layout at desktop and phone width and the card badges, not the hub's
// orchestration.

async function nodeIds(page: Page): Promise<Record<string, string>> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Record<string, string> = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

function view(ids: Record<string, string>) {
  const at = "2026-10-24T08:00:00Z";
  const stage = (name: string, status: string, extra = {}) => ({
    stage: name,
    status,
    at,
    ...extra,
  });
  return {
    run: {
      id: "ap1",
      project: "main",
      goalNodeId: ids["目標"],
      status: "running",
      policy: {
        implement: [],
        review: {
          mode: "agents+human",
          reviewers: [
            { agent: "codex", model: "gpt-5.5" },
            { agent: "claude", model: "sonnet" },
          ],
          quorum: 2,
        },
        maxParallel: 2,
        maxRounds: 2,
        autoMerge: true,
      },
      counts: { total: 3, success: 1, running: 1, waiting: 1, failed: 0, pending: 0, skipped: 0 },
      createdBy: "u1",
      createdByName: "patrick",
      createdAt: at,
      updatedAt: at,
    },
    tasks: [
      {
        nodeId: ids["資料表設計"],
        title: "資料表設計",
        state: "success",
        stage: "done",
        try: 1,
        upstream: [],
        prUrl: "https://github.com/o/r/pull/1",
        ciStatus: "success",
        votes: [
          { agent: "codex", model: "gpt-5.5", verdict: "approve", round: 1 },
          { agent: "claude", model: "sonnet", verdict: "approve", round: 1 },
        ],
        stages: [
          stage("queued", "success"),
          stage("implement", "success"),
          stage("commit", "success"),
          stage("push", "success"),
          stage("pr", "success"),
          stage("ci", "success"),
          stage("review", "failed", { round: 1 }),
          stage("revise", "success", { round: 1 }),
          stage("review", "success", { round: 2 }),
          stage("human_review", "success", { round: 2 }),
          stage("approved", "success"),
          stage("merge", "success"),
          stage("done", "success"),
        ],
        updatedAt: at,
      },
      {
        nodeId: ids["API 實作"],
        title: "API 實作",
        state: "waiting_human",
        stage: "human_review",
        try: 1,
        upstream: [ids["資料表設計"]],
        pipelineId: "p2",
        prUrl: "https://github.com/o/r/pull/2",
        ciStatus: "pending",
        votes: [
          { agent: "codex", model: "gpt-5.5", verdict: "approve", round: 1 },
          { agent: "claude", model: "sonnet", verdict: "changes", round: 1 },
        ],
        stages: [
          stage("queued", "success"),
          stage("implement", "success"),
          stage("commit", "success"),
          stage("push", "success"),
          stage("pr", "success"),
          stage("ci", "running"),
          stage("review", "success", { round: 1 }),
          stage("human_review", "waiting", { round: 1 }),
        ],
        updatedAt: at,
      },
      {
        nodeId: ids["前端畫面"],
        title: "前端畫面",
        state: "none",
        stage: "",
        try: 0,
        upstream: [ids["API 實作"]],
        stages: [],
        updatedAt: at,
      },
    ],
  };
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
}

async function shot(page: Page, name: string) {
  const dir = process.env.NODEVAS_AUTOPILOT_SHOTS;
  if (dir) await page.screenshot({ path: `${dir}/${name}.png` });
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`autopilot panel fits on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await freshProject(page, ["目標", "資料表設計", "API 實作", "前端畫面"]);
    const ids = await nodeIds(page);
    await page.route(/\/api\/autopilot(\?|\/)/, (route) => route.fulfill({ json: view(ids) }));
    await page.route(/\/api\/runners/, (route) => route.fulfill({ json: { runners: [] } }));

    // The goal's node panel says how far Autopilot is, and opens it in Station.
    await page.getByText("目標").first().click();
    const drawer = page.getByRole("dialog", { name: "節點編輯" });
    await expect(drawer).toBeVisible();
    // In the panel's top row, not a tab beside the node's own facets.
    const pill = drawer.getByRole("button", { name: /^自動推進中：已完成 1／3 個任務/ });
    await expect(pill).toBeVisible();
    await expect(drawer.getByRole("tab", { name: "自動推進" })).toHaveCount(0);
    // The task cards on the board carry their autopilot stage.
    await expect(page.getByLabel("自動推進：等你審查 · 人工審查").first()).toBeAttached();
    await pill.click();

    const goal = page.locator(".station-goal");
    await expect(goal.getByRole("heading", { name: "目標：目標" })).toBeVisible();
    await expect(goal.getByText("等待你審查（1）")).toBeVisible();
    await expect(goal.getByRole("table")).toBeVisible();
    await expect(goal.getByRole("row")).toHaveCount(4);
    await expectNoHorizontalOverflow(page);
    await shot(page, `${name}-panel`);
    await goal.getByRole("table").scrollIntoViewIfNeeded();
    await shot(page, `${name}-grid`);

    // And back: the goal is a node of the Info graph.
    await goal.getByRole("button", { name: "在資訊中顯示" }).click();
    await expect(page.getByRole("button", { name: "看板", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("dialog", { name: "節點編輯" })).toBeVisible();
  });
}
