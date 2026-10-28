import { type Page, type Route } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// Agent roles and workflow stages, with the hub's run, runner and autopilot
// routes answered here: these tests are about what the UI shows and writes,
// not about a run actually happening.

const standard = {
  preset: "standard",
  stages: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
};

const runner = {
  id: "r1",
  name: "desktop",
  ownerId: "",
  ownerName: "",
  createdAt: "2026-10-23T00:00:00Z",
  lastSeenAt: "2026-10-24T08:00:00Z",
  online: true,
  info: {
    version: "1.4.0",
    hostname: "PC-01",
    os: "windows",
    arch: "amd64",
    capabilities: ["headless", "plan", "roles", "verify", "judge"],
    agents: [
      { kind: "claude", installed: true, models: [{ id: "default", default: true }] },
      { kind: "codex", installed: true, models: [{ id: "gpt-5", default: true }] },
    ],
    repos: [{ id: "repo1", name: "nodevas", verify: true }],
  },
};

async function nodeIds(page: Page): Promise<Record<string, string>> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Record<string, string> = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

/** Serves the graph with the project's stages, and a task's criteria, added. */
async function withStages(page: Page, acceptanceFor?: string) {
  await page.route(/\/api\/graph(\?|$)/, async (route: Route) => {
    if (route.request().method() !== "GET") return route.fallback();
    const response = await route.fetch();
    const body = await response.json();
    body.graph.workflow = standard;
    for (const node of body.graph.nodes ?? []) {
      if (node.title === acceptanceFor) node.acceptance = { items: [{ id: "a1", text: "logs in on Safari" }] };
    }
    await route.fulfill({ response, json: body });
  });
}

const at = "2026-10-24T08:00:00Z";
const head = "a".repeat(40);

function stagedRuns(nodeId: string) {
  const base = {
    project: "p",
    nodeId,
    runnerId: "r1",
    runnerName: "desktop",
    repoId: "repo1",
    repoName: "nodevas",
    base: "main",
    branch: "agent/x",
    prompt: "",
    status: "succeeded",
    exitCode: 0,
    createdBy: "u1",
    createdByName: "alice",
    pipelineId: "p1",
    finishedAt: at,
  };
  return [
    { ...base, id: "run4", kind: "verify", agent: "", model: "", createdAt: "2026-10-24T08:04:00Z",
      verify: { phase: "green", head, exitCode: 0, passed: true, durationMs: 5100, output: "ok" } },
    { ...base, id: "run3", kind: "revise", role: "executor", agent: "claude", model: "default", createdAt: "2026-10-24T08:03:00Z" },
    { ...base, id: "run2", kind: "verify", agent: "", model: "", createdAt: "2026-10-24T08:02:00Z",
      verify: { phase: "red", head, exitCode: 1, passed: true, durationMs: 4200, output: "--- FAIL: TestLogin" } },
    { ...base, id: "run1", kind: "test", role: "tester", agent: "claude", model: "default", createdAt: "2026-10-24T08:01:00Z" },
  ];
}

test("choosing the Standard preset shows its cost hint", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  await page.getByRole("button", { name: /設定|Settings/ }).first().click();
  await page.getByRole("navigation", { name: /設定|Settings/ }).getByRole("button", { name: /^(Agent 與 Runner|Agents)/ }).click();
  const advanced = page.getByRole("region", { name: /進階|Advanced/ });
  await advanced.getByRole("button", { name: /^(進階|Advanced)/ }).click();
  await advanced.getByRole("button", { name: /(工作流程階段|Workflow stages)/ }).click();
  const presets = page.getByRole("radiogroup", { name: /(工作流程階段|Workflow stages)/ });
  await presets.getByRole("radio", { name: /(標準|Standard)/ }).check();
  await expect(presets.getByRole("radio", { name: /(標準|Standard).*≈2–3×/ })).toBeChecked();
  const graph = await (await page.request.get("/api/graph")).json();
  expect(graph.graph.workflow?.preset).toBe("standard");
});

test("writing acceptance criteria on a task", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  await page.getByText("設計稿").first().click();
  const drawer = page.getByRole("dialog", { name: "節點編輯" });
  const criteria = drawer.getByRole("region", { name: /(驗收條件|Acceptance criteria)/ });
  await expect(criteria).toBeVisible();
  await criteria.getByRole("textbox", { name: /(新條件|New criterion)/ }).fill("the mockup is approved");
  await criteria.getByRole("button", { name: /(新增條件|Add criterion)/ }).click();
  await expect(criteria.getByRole("textbox", { name: /a1/ })).toHaveValue("the mockup is approved");
  const ids = await nodeIds(page);
  const graph = await (await page.request.get("/api/graph")).json();
  const node = graph.graph.nodes.find((n: { id: string }) => n.id === ids["設計稿"]);
  expect(node.acceptance?.items).toEqual([{ id: "a1", text: "the mockup is approved" }]);
});

test("the dispatch dialog shows the stage chain", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  await withStages(page, "設計稿");
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [runner] } }));
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: [] } }));
  await page.reload();
  await page.getByText("設計稿").first().click();
  const drawer = page.getByRole("dialog", { name: "節點編輯" });
  await drawer.getByRole("tab", { name: "活動", exact: true }).click();
  await drawer.getByRole("button", { name: "派給 agent…" }).click();
  const dialog = page.getByRole("dialog", { name: "派給 agent" });
  const chain = dialog.getByRole("list", { name: /(階段|Stages)/ });
  await expect(chain.getByRole("listitem")).toHaveCount(5);
  await expect(chain).toContainText(/Tester|測試者/);
  await expect(chain).toContainText(/Reviewer|審查者/);
});

test("the pipeline view shows red and green evidence and the guard", async ({ page }) => {
  await freshProject(page, ["設計稿"]);
  const ids = await nodeIds(page);
  await page.route("**/api/runners", (route) => route.fulfill({ json: { runners: [runner] } }));
  await page.route(/\/api\/runs\?/, (route) => route.fulfill({ json: { runs: stagedRuns(ids["設計稿"]) } }));
  await page.route(/\/api\/runs\/run\d\/events/, (route) => route.fulfill({ json: { events: [], more: false } }));
  await page.route(/\/api\/runs\/pipelines\/p1/, (route) =>
    route.fulfill({
      json: {
        id: "p1",
        nodeId: ids["設計稿"],
        pipeline: { implement: { agent: "claude", model: "default" }, review: { agent: "codex", model: "gpt-5" }, maxRounds: 2 },
        round: 1,
        status: "running",
        runIds: ["run1", "run2", "run3", "run4"],
        createdAt: at,
        stages: { preset: "standard", stages: { tester: true, reviewer: true } },
        phase: "review",
        testFiles: ["web/login.test.ts"],
        guard: { testFiles: ["web/login.test.ts"], touched: [] },
      },
    }),
  );
  await page.getByText("設計稿").first().click();
  const drawer = page.getByRole("dialog", { name: "節點編輯" });
  await drawer.getByRole("tab", { name: "活動", exact: true }).click();
  await expect(drawer.getByRole("button", { name: /(紅燈|Red).*exit 1/ })).toBeVisible();
  await expect(drawer.getByRole("button", { name: /(綠燈|Green).*exit 0/ })).toBeVisible();
  await expect(drawer.getByRole("group", { name: /(測試檔防護|Test files guard)/ })).toContainText("web/login.test.ts");
});

test("a goal shows its QA round", async ({ page }) => {
  await freshProject(page, ["目標", "資料表設計"]);
  const ids = await nodeIds(page);
  await page.route(/\/api\/runners/, (route) => route.fulfill({ json: { runners: [runner] } }));
  await page.route(/\/api\/autopilot(\?|\/)/, (route) =>
    route.fulfill({
      json: {
        run: {
          id: "ap1",
          project: "main",
          goalNodeId: ids["目標"],
          status: "running",
          policy: { implement: [], review: { mode: "none" }, maxParallel: 1, maxRounds: 1, autoMerge: false },
          counts: { total: 1, success: 1, running: 0, waiting: 0, failed: 0, pending: 0, skipped: 0 },
          createdBy: "u1",
          createdByName: "patrick",
          createdAt: at,
          updatedAt: at,
          stages: { preset: "full", stages: { goalQa: true }, maxQaRounds: 2 },
          goalPhase: "qa",
          qaRound: 1,
        },
        tasks: [
          { nodeId: ids["資料表設計"], title: "資料表設計", state: "success", stage: "done", try: 1, upstream: [], stages: [], updatedAt: at },
        ],
      },
    }),
  );
  await page.getByText("目標").first().click();
  const drawer = page.getByRole("dialog", { name: "節點編輯" });
  await drawer.getByRole("button", { name: /^自動推進/ }).click();
  await expect(page.locator(".station-goal").getByText(/(QA 驗證|QA verification).*1\/2/)).toBeVisible();
});
