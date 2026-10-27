import { type Page } from "@playwright/test";
import { expect, freshProject, test } from "./fixture";

// Station's control center against the real binary. The run and autopilot
// routes a runner would feed are answered here, and so is pause-all: the
// hub's side of it is covered by internal/server/control_test.go; this is
// about the panel, its dialog and its fit at desktop and phone width.

type Ids = Record<string, string>;

async function nodeIds(page: Page): Promise<Ids> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Ids = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

async function stage(page: Page, ids: Ids) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const owner = status.actor?.id ?? "local";
  const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
  const run = (fields: Record<string, unknown>) => ({
    project: "p",
    runnerId: "rn1",
    runnerName: "mac-mini",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "visual-editor",
    base: "main",
    branch: "",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: owner,
    createdByName: "bea",
    createdAt: iso(600_000),
    ...fields,
  });
  const active = [
    run({
      id: "run-impl",
      nodeId: ids["Auth API"],
      startedAt: iso(540_000),
      usage: { inputTokens: 150_000, outputTokens: 32_000, costUsd: 1.25 },
    }),
    run({ id: "run-offer", nodeId: ids["Login form"], agent: "codex", model: "gpt-5", status: "offered", createdAt: iso(60_000) }),
    run({
      id: "run-review",
      nodeId: ids["Login form"],
      kind: "review",
      pipelineId: "pipe1",
      runnerName: "build-box",
      agent: "gemini",
      model: "2.5-pro",
      startedAt: iso(120_000),
    }),
  ];
  const ended = run({
    id: "run-done",
    nodeId: ids["Auth API"],
    status: "succeeded",
    createdAt: iso(1_500_000),
    startedAt: iso(1_400_000),
    finishedAt: iso(900_000),
  });
  const at = iso(3_600_000);
  const pilot = {
    run: {
      id: "ap1",
      project: "p",
      goalNodeId: ids["Ship login"],
      status: "running",
      policy: { implement: [], review: { mode: "agents" }, maxParallel: 2, maxRounds: 2, autoMerge: false },
      counts: { total: 3, success: 1, running: 1, waiting: 0, failed: 0, pending: 1, skipped: 0 },
      createdBy: owner,
      createdByName: "bea",
      createdAt: at,
      updatedAt: at,
    },
    tasks: [
      {
        nodeId: ids["Login form"],
        title: "Login form",
        state: "running",
        stage: "review",
        try: 1,
        runId: "run-x",
        pipelineId: "pipe1",
        stages: [],
        updatedAt: at,
      },
    ],
  };
  await page.route(/\/api\/runners(\?|$)/, (route) => route.fulfill({ json: { runners: [] } }));
  await page.route(/\/api\/runs(\?|$)/, (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get("active") === "1") return route.fulfill({ json: { runs: active } });
    return route.fulfill({ json: { runs: [...active, ended] } });
  });
  await page.route(/\/api\/autopilot(\?|$)/, (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.has("goal")) return route.fulfill({ json: pilot });
    return route.fulfill({ json: { autopilots: [pilot] } });
  });
  const bodies: unknown[] = [];
  await page.route(/\/api\/control\/pause-all$/, (route) => {
    bodies.push(route.request().postDataJSON());
    return route.fulfill({
      json: {
        paused: [{ kind: "autopilot", id: "ap1" }],
        cancelled: [{ kind: "run", id: "run-offer" }],
        skipped: [
          { kind: "run", id: "run-impl", reason: "running" },
          { kind: "run", id: "run-review", reason: "running" },
        ],
      },
    });
  });
  return bodies;
}

async function expectNoHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
  const panel = await page
    .locator(".station-control")
    .evaluate((element) => element.scrollWidth - element.clientWidth);
  expect(panel).toBeLessThanOrEqual(1);
}

async function shot(page: Page, name: string) {
  const dir = process.env.NODEVAS_CONTROL_SHOTS;
  if (dir) await page.screenshot({ path: `${dir}/${name}.png`, fullPage: true });
}

for (const [name, viewport] of Object.entries({
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
})) {
  test(`control center lists runs and pauses everything on ${name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await freshProject(page, ["Auth API", "Login form", "Ship login"]);
    const ids = await nodeIds(page);
    const bodies = await stage(page, ids);
    await page.addInitScript(() => {
      localStorage.setItem("vised-language", "en");
      localStorage.setItem("vised-project-mode", "station");
    });
    await page.reload();

    const panel = page.locator(".station-control");
    await expect(panel.getByRole("heading", { name: "In progress" })).toBeVisible({ timeout: 15_000 });
    await expect(panel.getByTitle("3 active runs")).toHaveText("3");
    const list = panel.getByRole("list", { name: "In progress" });
    // Three live runs and the autopilot, one line each.
    await expect(list.getByRole("listitem")).toHaveCount(4);
    await expect(list.getByText(/Codex gpt-5/)).toBeVisible();
    // The review run belongs to the autopilot's task, by its pipeline.
    await expect(list.getByText(/^Review · .* · Ship login$/)).toBeVisible();
    await expect(panel.getByText("1 of 3 tasks done")).toBeVisible();
    // Its actions sit behind one ⋯.
    const review = list.getByRole("listitem").filter({ hasText: "Review · " });
    await review.getByRole("button", { name: /^Actions for / }).click();
    await expect(page.getByRole("menuitem", { name: "Pause autopilot" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toHaveCount(0);
    // The recently ended run is folded away until asked for.
    const toggle = panel.getByRole("button", { name: /Recently ended \(1\)/ });
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(panel.getByRole("list", { name: "Recently ended (1)" }).getByRole("listitem")).toHaveCount(1);
    await expect(panel.getByText("Succeeded")).toBeVisible();
    await expectNoHorizontalOverflow(page);
    await shot(page, `${name}-control`);

    // The emergency stop is the one button of Station's header.
    await page.getByRole("button", { name: "Pause everything" }).click();
    const dialog = page.getByRole("alertdialog", { name: "Pause everything?" });
    await expect(dialog.getByText("Pause 1 running autopilots")).toBeVisible();
    await expect(dialog.getByText("Cancel 1 runs that have not started")).toBeVisible();
    await expect(dialog.getByText("Leave 2 running runs running")).toBeVisible();
    await expect(dialog.getByRole("checkbox")).not.toBeChecked();
    await shot(page, `${name}-pause-all`);
    await dialog.getByRole("button", { name: "Pause everything" }).click();
    await expect(page.getByText("Paused 1 autopilots and cancelled 1 runs; 2 left alone.")).toBeVisible();
    expect(bodies).toEqual([{ cancelRunning: false }]);
  });
}
