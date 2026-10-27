import { type Page } from "@playwright/test";
import { mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, freshProject, test } from "./fixture";

// The game views (war room, boss map) against the real binary. The project's
// nodes, dependencies, a logic gate and statuses are written through the real
// API; the routes a runner or an agent would feed (runners, runs, autopilot,
// budget, the agent network) are answered here, so no runner has to exist.
//
// Set GAME_SHOTS_DIR to also write screenshots of both views in both themes.

const TITLES = [
  "DB schema",
  "Auth API",
  "Login form",
  "API docs",
  "Billing",
  "Deploy",
  "Search",
  "Onboarding emails",
  "Metrics",
];

type Ids = Record<string, string>;

/** Writes the board: dependencies, a gate, priorities and statuses. */
async function stageGraph(page: Page): Promise<Ids> {
  const response = await (await page.request.get("/api/graph")).json();
  const graph = response.graph;
  const ids: Ids = {};
  for (const node of graph.nodes) ids[node.title] = node.id;
  const id = (title: string) => ids[title];
  const byId = new Map(graph.nodes.map((node: { id: string }) => [node.id, node]));
  const set = (title: string, fields: Record<string, unknown>) =>
    Object.assign(byId.get(id(title)) as object, fields);
  set("Deploy", { priority: "urgent", requires: `${id("Login form")} and ${id("API docs")}` });
  set("Search", { priority: "high" });
  // `requires` is what blocks a node; the edges are its picture on the board.
  set("Auth API", { requires: id("DB schema") });
  set("Login form", { requires: id("Auth API") });
  set("API docs", { requires: id("Auth API") });
  set("Billing", { priority: "medium", requires: id("DB schema") });
  set("Onboarding emails", { priority: "low", requires: id("Search") });
  graph.edges = [
    { from: id("DB schema"), to: id("Auth API") },
    { from: id("Auth API"), to: id("Login form") },
    { from: id("Auth API"), to: id("API docs") },
    { from: id("DB schema"), to: id("Billing") },
    { from: id("Login form"), to: id("Deploy") },
    { from: id("API docs"), to: id("Deploy") },
    { from: id("Search"), to: id("Onboarding emails") },
  ];
  graph.ui = {
    ...(graph.ui ?? {}),
    logicGates: [
      {
        id: "gate-1",
        operator: "and",
        x: 0,
        y: 0,
        inputs: [id("Login form"), id("API docs")],
        output: id("Deploy"),
        applied: `${id("Login form")} and ${id("API docs")}`,
      },
    ],
  };
  const put = await page.request.put("/api/graph", { data: { graph, baseRev: response.rev } });
  expect(put.ok()).toBeTruthy();
  for (const title of ["DB schema", "Metrics"]) {
    const done = await page.request.post(`/api/nodes/${encodeURIComponent(id(title))}/status`, {
      data: { status: "done", by: "editor", note: "" },
    });
    expect(done.ok()).toBeTruthy();
  }
  return ids;
}

/** Answers the runner, run, autopilot, budget and agent routes. */
async function mockWorkstation(page: Page, ids: Ids) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const owner = status.actor?.id ?? "";
  const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
  const runner = (id: string, name: string, kinds: string[]) => ({
    id,
    name,
    ownerId: owner,
    ownerName: "bea",
    createdAt: iso(86_400_000),
    lastSeenAt: iso(1_000),
    online: true,
    info: {
      version: "1.3.0",
      hostname: `${name}.local`,
      os: "darwin",
      arch: "arm64",
      agents: kinds.map((kind) => ({ kind, installed: true, models: [] })),
      repos: [{ id: "repo1", name: "visual-editor", path: "/src/visual-editor" }],
    },
  });
  const runners = [runner("rn1", "mac-mini", ["claude", "codex"]), runner("rn2", "build-box", ["gemini", "opencode"])];
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
  const active = () => [
    run({ id: "aaaa01", nodeId: ids["Auth API"], usage: { inputTokens: 150_000, outputTokens: 32_000 }, createdAt: iso(900_000) }),
    run({ id: "aaaa02", nodeId: ids["Login form"], agent: "codex", model: "gpt-5", status: "offered", createdAt: iso(60_000) }),
    run({
      id: "aaaa03",
      nodeId: ids["Billing"],
      runnerId: "rn2",
      runnerName: "build-box",
      agent: "gemini",
      model: "2.5-pro",
      retryOf: "aaaa00",
      createdAt: iso(300_000),
    }),
    run({ id: "aaaa04", nodeId: ids["Search"], runnerId: "rn2", runnerName: "build-box", kind: "review", pipelineId: "p1", createdAt: iso(200_000) }),
    run({
      id: "aaaa05",
      nodeId: ids["Search"],
      runnerId: "rn2",
      runnerName: "build-box",
      agent: "codex",
      model: "gpt-5",
      kind: "review",
      pipelineId: "p1",
      createdAt: iso(199_000),
    }),
  ];
  const finished = () => [
    run({
      id: "aaaa00",
      nodeId: ids["Billing"],
      runnerId: "rn2",
      runnerName: "build-box",
      status: "failed",
      failureKind: "rate_limit",
      createdAt: iso(400_000),
      finishedAt: iso(310_000),
    }),
    run({
      id: "aaaa06",
      nodeId: ids["API docs"],
      agent: "codex",
      model: "gpt-5",
      status: "succeeded",
      commit: "c0ffee1",
      filesChanged: 3,
      additions: 40,
      deletions: 4,
      prUrl: "https://github.com/example/visual-editor/pull/7",
      prNumber: 7,
      prState: "open",
      ciStatus: "pending",
      createdAt: iso(1_800_000),
      finishedAt: iso(1_200_000),
      usage: { inputTokens: 64_000, outputTokens: 9_000 },
    }),
    run({
      id: "aaaa07",
      nodeId: ids["Onboarding emails"],
      runnerId: "rn2",
      runnerName: "build-box",
      agent: "opencode",
      model: "",
      status: "failed",
      error: "tests failed",
      createdAt: iso(2_400_000),
      finishedAt: iso(2_000_000),
    }),
    run({
      id: "aaaa08",
      nodeId: ids["Metrics"],
      status: "succeeded",
      prUrl: "https://github.com/example/visual-editor/pull/5",
      prState: "merged",
      createdAt: iso(7_200_000),
      finishedAt: iso(7_000_000),
    }),
  ];
  await page.route(/\/api\/runners(\?|$)/, (route) => route.fulfill({ json: { runners } }));
  await page.route(/\/api\/runs(\?|\/|$)/, (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() !== "GET") return route.fulfill({ json: { ok: true } });
    if (url.pathname === "/api/runs/budget") {
      return route.fulfill({
        json: { budget: { dailyRuns: 0, dailyCostUsd: 0, dailyTokens: 1_000_000 }, used: { runs: 6, costUsd: 0, tokens: 412_000 }, exceeded: "" },
      });
    }
    if (url.pathname === "/api/runs/pipelines/p1") {
      return route.fulfill({
        json: {
          id: "p1",
          nodeId: ids["Search"],
          pipeline: { implement: { agent: "claude", model: "opus" }, review: { agent: "codex", model: "gpt-5" }, maxRounds: 3 },
          round: 2,
          status: "running",
          runIds: ["aaaa04", "aaaa05"],
          createdAt: iso(900_000),
          votes: [{ round: 2, agent: "codex", model: "gpt-5", verdict: "approve", runId: "aaaa05" }],
        },
      });
    }
    if (url.pathname !== "/api/runs") return route.fulfill({ status: 404, json: { error: "not mocked" } });
    if (url.searchParams.get("active") === "1") return route.fulfill({ json: { runs: active() } });
    const nodeId = url.searchParams.get("nodeId");
    const all = [...active(), ...finished()];
    return route.fulfill({ json: { runs: nodeId ? all.filter((item) => (item as { nodeId?: string }).nodeId === nodeId) : all } });
  });
  const autopilot = () => ({
    run: {
      id: "ap1",
      project: "p",
      goalNodeId: ids["Deploy"],
      status: "running",
      policy: {},
      counts: { total: 5, success: 1, running: 1, waiting: 0, failed: 0, pending: 3, skipped: 0 },
      createdBy: owner,
      createdByName: "bea",
      createdAt: iso(1_000_000),
      updatedAt: iso(1_000),
    },
    tasks: ["DB schema", "Auth API", "Login form", "API docs", "Deploy"].map((title, i) => ({
      nodeId: ids[title],
      title,
      state: i === 0 ? "success" : i === 1 ? "running" : "none",
      stage: i === 1 ? "implement" : "queued",
      try: i === 1 ? 1 : 0,
      upstream: [],
      votes: [],
      stages: [],
      updatedAt: iso(1_000),
    })),
  });
  await page.route(/\/api\/autopilot(\?|\/|$)/, (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/autopilot" && !url.searchParams.get("goal")) {
      return route.fulfill({ json: { autopilots: [autopilot()] } });
    }
    return route.fulfill({ json: autopilot() });
  });
  const sessions = () => [
    {
      id: "s-run",
      project: "p",
      actorId: owner,
      actorName: "bea",
      name: "visual-editor-claude-opus-aaaa01",
      agent: "claude",
      model: "opus",
      state: "busy",
      since: Date.now() - 900_000,
      lastSeen: Date.now() - 3_000,
      online: true,
      run: true,
      runId: "aaaa01",
      nodeId: ids["Auth API"],
    },
    {
      id: "s-laptop",
      project: "p",
      actorId: owner,
      actorName: "bea",
      name: "laptop-claude",
      agent: "claude",
      model: "sonnet",
      state: "busy",
      since: Date.now() - 600_000,
      lastSeen: Date.now() - 4_000,
      online: true,
      nodeId: ids["Deploy"],
    },
    {
      id: "s-desk",
      project: "p",
      actorId: owner,
      actorName: "bea",
      name: "desktop-codex",
      agent: "codex",
      state: "idle",
      since: Date.now() - 600_000,
      lastSeen: Date.now() - 6_000,
      online: true,
    },
  ];
  const held = () => ({
    id: "m-held",
    project: "p",
    from: "bea/laptop-claude",
    senderActor: owner,
    senderSession: "laptop-claude",
    to: "desktop-codex",
    body: "Please rebase onto main before you push.",
    createdAt: Date.now() - 30_000,
    status: "held",
  });
  await page.route(
    (url) => url.pathname.startsWith("/api/agents"),
    (route) => {
      const url = new URL(route.request().url());
      if (route.request().method() !== "GET") return route.fulfill({ json: { ok: true } });
      if (url.pathname === "/api/agents") return route.fulfill({ json: { sessions: sessions(), users: [], unreadCount: 0 } });
      if (url.pathname === "/api/agents/messages") {
        if (url.searchParams.get("status") === "held") return route.fulfill({ json: { messages: [held()] } });
        return route.fulfill({
          json: {
            messages: [
              {
                id: "m-bubble",
                project: "p",
                from: "bea/visual-editor-claude-opus-aaaa01",
                senderActor: owner,
                senderSession: "visual-editor-claude-opus-aaaa01",
                to: "laptop-claude",
                body: "Token endpoint is up, refresh next.",
                createdAt: Date.now() - 1_000,
                status: "delivered",
              },
            ],
          },
        });
      }
      if (url.pathname === "/api/agents/asks") {
        return route.fulfill({
          json: {
            asks: [
              {
                id: "ask-1",
                project: "p",
                sessionId: "s-laptop",
                sessionName: "laptop-claude",
                actorId: owner,
                nodeId: ids["Deploy"],
                question: "Deploy to staging first?",
                status: "pending",
                createdAt: Date.now() - 20_000,
              },
            ],
          },
        });
      }
      return route.fulfill({ status: 404, json: { error: "not mocked" } });
    },
  );
}

async function openStaged(page: Page, prefs: Record<string, string> = {}) {
  await freshProject(page, TITLES);
  const ids = await stageGraph(page);
  await mockWorkstation(page, ids);
  await page.addInitScript((values) => {
    localStorage.setItem("vised-language", "en");
    localStorage.setItem("vised-pane-timeline", "0");
    for (const [key, value] of Object.entries(values)) localStorage.setItem(key, value);
  }, prefs);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Nodevas" })).toBeVisible({ timeout: 15_000 });
  return ids;
}

/** The agent panel opens itself on a new ask (the staged agent has one);
 * close it to see the board. */
async function closeAgentPanel(page: Page) {
  const close = page.getByRole("button", { name: "Close Agent Network" });
  await close.click({ timeout: 15_000 });
  await expect(page.locator("#agent-network-panel")).toBeHidden();
}

test("war room: opened in Station from the graph menu, a character opens its node", async ({ page }) => {
  await openStaged(page);
  await closeAgentPanel(page);
  await page.getByRole("button", { name: "More graph tools" }).click();
  await page.getByRole("menuitem", { name: "Station: War room" }).click();
  await expect(page.getByRole("button", { name: "Station" })).toHaveAttribute("aria-pressed", "true");
  const stage = page.getByRole("region", { name: "War room" });
  await expect(stage).toBeVisible();

  const claude = stage.getByRole("button", { name: /Claude opus in mac-mini: working, on “Auth API”/ });
  await expect(claude).toBeVisible();
  await expect(stage.getByRole("button", { name: /Codex gpt-5 in mac-mini: waiting to be accepted/ })).toBeVisible();
  await expect(stage.getByRole("button", { name: /Gemini 2\.5-pro in build-box: working.*Took over from Claude opus/ })).toBeVisible();
  await expect(stage.getByRole("button", { name: /Claude sonnet in Terminals: waiting for your answer/ })).toBeVisible();

  await claude.click();
  await page.getByRole("menuitem", { name: "Open “Auth API”" }).click();
  await expect(page.locator(".drawer").getByText("Auth API").first()).toBeVisible();

  // The same world as a list.
  await stage.getByRole("button", { name: "List view" }).click();
  await expect(stage.getByText(/Claude opus in mac-mini: working, on “Auth API”/)).toBeVisible();
});

test("boss map: opened in Station from the command palette, a locked monster opens its blocker", async ({ page }) => {
  await openStaged(page);
  await closeAgentPanel(page);
  await page.evaluate(() => window.dispatchEvent(new Event("nodevas-command-palette")));
  await page.getByPlaceholder(/Search projects, nodes/).fill("Boss map");
  await page.keyboard.press("Enter");
  const stage = page.getByRole("region", { name: "Boss map" });
  await expect(stage).toBeVisible();
  await expect(stage.getByRole("meter", { name: "Boss HP" })).toHaveAttribute("aria-valuenow", "7");

  const login = stage.getByRole("button", { name: /Monster “Login form”: locked\. Locked by Auth API/ });
  await expect(login).toBeVisible();
  await expect(stage.getByRole("button", { name: /Door \(AND\) to “Deploy”: shut/ })).toBeVisible();
  await expect(stage.getByRole("button", { name: /Autopilot party for “Deploy”: 1 of 5 done/ })).toBeVisible();
  // The sprites are served from the binary.
  await expect
    .poll(() => stage.locator("img.game-sprite").first().evaluate((img: HTMLImageElement) => img.naturalWidth))
    .toBeGreaterThan(0);

  await login.click();
  await page.getByRole("menuitem", { name: "Open blocker “Auth API”" }).click();
  await expect(page.locator(".drawer").getByText("Auth API").first()).toBeVisible();
});

test("station: the roster, what waits on you and who worked on what", async ({ page }) => {
  await openStaged(page, { "vised-project-mode": "station" });
  // Nothing floats over Station: the agent panel is docked in its column.
  await expect(page.locator("#agent-network-panel")).toHaveCount(0);
  const docked = page.getByRole("region", { name: "Agent Network" });
  await expect(docked).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("heading", { name: "Agents", exact: true })).toBeVisible();
  await expect(page.locator(".station-actor").first()).toBeVisible();
  // The staged agent's question is waiting, and the switch counts it.
  await expect(page.getByRole("button", { name: /^Station/ }).locator(".mode-switch-badge")).toHaveText(/\d+/);
  await expect(page.getByRole("heading", { name: "Who worked on what · last hour" })).toBeVisible();

  // Info is the board as it was: the cards, the explorer toggle back.
  await page.getByRole("button", { name: "Info", exact: true }).click();
  await expect(page.locator(".lane-wrap.graph .board-inner")).toBeVisible();
  await expect(page.getByRole("region", { name: "War room" })).toHaveCount(0);
});

const PACKS_DIR = join(import.meta.dirname, "..", "..", "examples", "view-packs");

/** examples/view-packs packs as GET /api/packs lists them, their images
 * served from the repository. */
async function mockPacks(page: Page, ids: string[]) {
  const packs = ids.map((id) => {
    const manifest = JSON.parse(readFileSync(join(PACKS_DIR, id, "pack.json"), "utf8"));
    const sprites: Record<string, string> = {};
    for (const [key, path] of Object.entries(manifest.sprites as Record<string, string>)) {
      sprites[key] = `/api/packs/${id}/files/${path}?v=1`;
    }
    return { ...manifest, sprites, installedAt: 1, installedBy: "bea" };
  });
  await page.route("**/api/packs", (route) => route.fulfill({ json: { packs } }));
  await page.route("**/api/packs/*/files/**", (route) => {
    const [, id, path] = new URL(route.request().url()).pathname.match(/^\/api\/packs\/([^/]+)\/files\/(.+)$/)!;
    route.fulfill({ body: readFileSync(join(PACKS_DIR, id, path)), contentType: "image/svg+xml" });
  });
}

test("view pack: Station draws the war room with an installed pack", async ({ page }) => {
  await mockPacks(page, ["neon-station"]);
  await openStaged(page, { "vised-project-mode": "station" });
  await page.getByRole("button", { name: "Neon station" }).click();
  const stage = page.getByRole("region", { name: "Neon station" });
  await expect(stage).toBeVisible({ timeout: 15_000 });
  await expect(stage).toHaveAttribute("data-pack", "neon-station");

  // The working agent is drawn with the pack's picture, and it loads.
  const working = stage.locator('[data-pose="working"] img.pack-sprite').first();
  await expect(working).toHaveAttribute("src", /\/api\/packs\/neon-station\/files\/images\/astro-working\.svg/);
  await expect.poll(() => working.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
  // The question is still marked by Nodevas, not just by the picture.
  await expect(stage.locator('[data-pose="asking-user"] .game-badge.hand').first()).toBeVisible();
  // The palette reaches the board.
  const ground = await stage.locator(".game-board").evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(ground).toBe("rgb(14, 23, 38)");

  // Remembered across a reload, and the built-in view is one click away.
  await page.reload();
  await expect(page.getByRole("region", { name: "Neon station" })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "War room", exact: true }).click();
  await expect(page.getByRole("region", { name: "War room" })).toBeVisible();
  await expect(page.locator(".station img.pack-sprite")).toHaveCount(0);
});

test("game views: screenshots in both themes", async ({ page }) => {
  const dir = process.env.GAME_SHOTS_DIR;
  test.skip(!dir, "set GAME_SHOTS_DIR to write screenshots");
  mkdirSync(dir!, { recursive: true });
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockPacks(page, ["neon-station", "candy-dungeon"]);
  await openStaged(page, { "vised-explorer-collapsed": "1", "vised-project-mode": "station" });
  for (const [pack, name] of [
    ["neon-station", "Neon station"],
    ["candy-dungeon", "Candy dungeon"],
  ] as const) {
    await page.evaluate((id) => localStorage.setItem("vised-station-pack", id), pack);
    await page.reload();
    await expect(page.getByRole("region", { name })).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(1_200);
    await page.screenshot({ path: join(dir!, `station-pack-${pack}.png`) });
  }
  await page.evaluate(() => localStorage.setItem("vised-station-pack", ""));
  for (const theme of ["light", "dark"] as const) {
    for (const skin of ["warroom", "dungeon"] as const) {
      await page.evaluate(
        ([t, s]) => {
          localStorage.setItem("vised-theme", t);
          localStorage.setItem("vised-stage-skin", s);
        },
        [theme, skin],
      );
      await page.reload();
      const name = skin === "warroom" ? "War room" : "Boss map";
      await expect(page.getByRole("region", { name })).toBeVisible({ timeout: 15_000 });
      // Sprites decode, bubbles and badges settle.
      await page.waitForTimeout(1_200);
      await page.screenshot({ path: join(dir!, `station-${skin}-${theme}.png`) });
    }
  }
  // A phone, light: each view scrolls and zooms inside its pane; the page
  // itself scrolls sideways no more than it does with the cards.
  await page.setViewportSize({ width: 390, height: 844 });
  const pageOverflow = () =>
    page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  await page.evaluate(() => {
    localStorage.setItem("vised-theme", "light");
    localStorage.setItem("vised-project-mode", "info");
  });
  await page.reload();
  // At phone width the panel may or may not have opened itself by now.
  const close = page.getByRole("button", { name: "Close Agent Network" });
  if (await close.isVisible({ timeout: 5_000 }).catch(() => false)) await close.click();
  const baseline = await pageOverflow();
  await page.evaluate(() => localStorage.setItem("vised-project-mode", "station"));
  for (const skin of ["warroom", "dungeon"] as const) {
    await page.evaluate((s) => localStorage.setItem("vised-stage-skin", s), skin);
    await page.reload();
    const stage = page.getByRole("region", { name: skin === "warroom" ? "War room" : "Boss map" });
    await expect(stage).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(800);
    expect(await pageOverflow()).toBeLessThanOrEqual(baseline);
    const right = await stage.evaluate((element) => element.getBoundingClientRect().right);
    expect(right).toBeLessThanOrEqual(390 + 1);
    await page.screenshot({ path: join(dir!, `station-${skin}-phone.png`) });
  }
});
