import { type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, freshProject, test } from "./fixture";

// Station's war room against the real binary. The project's nodes,
// dependencies, a logic gate and statuses are written through the real API;
// the routes a runner or an agent would feed (runners, runs, autopilot,
// budget, Agent chat, the other projects' offices) are answered here, so no
// runner has to exist.
//
// Set GAME_SHOTS_DIR to also write screenshots of it in every office style,
// in both themes, and on a phone.

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

/** The other projects' offices, as GET /api/station/building answers. */
function buildingProjects(extra: boolean) {
  const now = Date.now();
  const iso = (msAgo: number) => new Date(now - msAgo).toISOString();
  const run = (id: string, title: string, fields: Record<string, unknown> = {}) => ({
    id,
    nodeId: `n-${id}`,
    title,
    agent: "claude",
    model: "opus",
    kind: "",
    status: "running",
    runnerId: "rn1",
    runnerName: "mac-mini",
    createdAt: iso(300_000),
    ...fields,
  });
  const office = (name: string, label: string, fields: Record<string, unknown> = {}) => ({
    name,
    label,
    layout: "workflow",
    officeStyle: "engineering",
    officeStyleSet: false,
    runs: [],
    runsTruncated: false,
    othersActive: 0,
    sessions: [],
    waiting: { asks: 0, held: 0, reviews: 0, total: 0 },
    asks: [],
    reviews: [],
    ...fields,
  });
  const projects = [
    office("billing-app", "billing-app", {
      officeStyle: "studio",
      officeStyleSet: true,
      runs: [run("b1", "Invoice PDF"), run("b2", "Tax rules", { agent: "codex", model: "gpt-5", runnerId: "rn2", runnerName: "build-box" })],
      waiting: { asks: 1, held: 0, reviews: 0, total: 1 },
      asks: [{ id: "ba1", sessionId: "bs1", sessionName: "billing-claude", agent: "claude", question: "Round to cents?", nodeId: "", createdAt: now - 60_000 }],
    }),
    office("handbook", "handbook", { layout: "story", officeStyle: "study" }),
  ];
  if (extra) {
    projects.push(
      office("novel", "novel", {
        layout: "story",
        officeStyle: "study",
        runs: [run("n1", "Chapter 3", { agent: "gemini", model: "2.5-pro", status: "queued", retryAt: iso(-600_000) })],
        sessions: [{ id: "ns1", name: "novel-codex", agent: "codex", model: "o3", state: "idle", nodeId: "", doing: "", runId: "", lastSeen: now - 5_000, online: true }],
      }),
      office("cafe-menu", "café menu", {
        officeStyle: "cafe",
        officeStyleSet: true,
        runs: [run("c1", "Menu board", { agent: "opencode", model: "", status: "offered" }), run("c2", "Prices", { kind: "plan" })],
        othersActive: 2,
      }),
      office("site", "site", { officeStyle: "default", officeStyleSet: true }),
      office("infra", "infra"),
    );
  }
  return { projects, truncated: false, total: projects.length + 1 };
}

async function openStaged(page: Page, prefs: Record<string, string> = {}, extraOffices = false) {
  await freshProject(page, TITLES);
  const ids = await stageGraph(page);
  await mockWorkstation(page, ids);
  await page.route(/\/api\/station\/building$/, (route) => route.fulfill({ json: buildingProjects(extraOffices) }));
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
  const close = page.getByRole("button", { name: "Close Agent chat" });
  await close.click({ timeout: 15_000 });
  await expect(page.locator("#agent-network-panel")).toBeHidden();
}

test("war room: Station draws it straight away, a character opens its node and its run's actions", async ({ page }) => {
  await openStaged(page);
  await closeAgentPanel(page);
  // The palette finds Station by the war room's name too.
  await page.evaluate(() => window.dispatchEvent(new Event("nodevas-command-palette")));
  await page.getByRole("textbox", { name: /Search projects, nodes/ }).fill("war room");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Station" })).toHaveAttribute("aria-pressed", "true");
  const stage = page.getByRole("region", { name: "War room" });
  await expect(stage).toBeVisible();
  // No view to pick: the room is the view.
  await expect(page.getByRole("button", { name: /^View: / })).toHaveCount(0);

  // The project's office: who works at a desk, who is in a meeting with a
  // question, who waits at the door.
  const office = stage.getByRole("region", { name: /^Office / });
  const work = office.getByRole("group", { name: "Work area" });
  const meeting = office.getByRole("group", { name: "Meeting room" });
  const entrance = office.getByRole("group", { name: "Entrance" });
  const claude = work.getByRole("button", { name: /Claude opus in mac-mini: working, on “Auth API”/ });
  await expect(claude).toBeVisible();
  await expect(entrance.getByRole("button", { name: /Codex gpt-5 in mac-mini: waiting to be accepted/ })).toBeVisible();
  await expect(work.getByRole("button", { name: /Gemini 2\.5-pro in build-box: working.*Took over from Claude opus/ })).toBeVisible();
  await expect(meeting.getByRole("button", { name: /Claude sonnet in Terminals: waiting for your answer/ })).toBeVisible();

  // The other projects: a busy one at a glance, an idle one as a door sign.
  const others = stage.getByRole("list", { name: "Other offices" });
  const billing = others.getByRole("button", { name: /^billing-app: 2 working, 1 waiting on you/ });
  await expect(billing).toContainText("✋ 1");
  await expect(stage.getByRole("list", { name: "Quiet offices" }).getByRole("button", { name: /^handbook: nobody in/ })).toBeVisible();

  // A character's menu holds its run's actions, as the list's rows did.
  await claude.click();
  await expect(page.getByRole("menuitem", { name: "Open run" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "View changes" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Cancel run" })).toBeVisible();
  await page.getByRole("menuitem", { name: "Open “Auth API”" }).click();
  await expect(page.locator(".drawer").getByLabel("Title", { exact: true })).toHaveValue("Auth API");

  // The live autopilot is a tile under the room; the tasks waiting on
  // prerequisites fold into one line of tiles.
  await page.keyboard.press("Escape");
  await expect(page.locator(".drawer")).toHaveCount(0);
  const pilots = stage.getByRole("list", { name: "Autopilots" });
  await expect(pilots.getByText("Deploy")).toBeVisible();
  await pilots.getByRole("button", { name: "Actions for Deploy" }).click();
  await expect(page.getByRole("menuitem", { name: "Pause autopilot" })).toBeVisible();
  await page.keyboard.press("Escape");
  const waitingFold = stage.getByRole("button", { name: /tasks waiting on prerequisites/ });
  await waitingFold.click();
  await expect(stage.getByText(/^Waiting on /).first()).toBeVisible();

  // A rack in the server room is the runners' settings.
  await stage
    .getByRole("region", { name: "Server room" })
    .getByRole("button", { name: /^Runner mac-mini: Online/ })
    .click();
  await expect(page.getByRole("region", { name: "Agents", exact: true })).toBeVisible();
});

test("station: what waits on you, the war room, who is working, Agent chat and the last hour", async ({ page }) => {
  await openStaged(page, { "vised-project-mode": "station" });
  // Nothing floats over Station: the agent panel is docked in its column.
  await expect(page.locator("#agent-network-panel")).toHaveCount(0);
  // The staged agent's question waits first, and the switch counts it.
  const waiting = page.getByRole("region", { name: "Waiting on you" });
  await expect(waiting.getByRole("button", { name: "Answer" }).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("button", { name: /^Station/ }).locator(".mode-switch-badge")).toHaveText(/\d+/);
  // Then the war room, drawn straight away, and right under it who worked on
  // what.
  const room = page.getByRole("region", { name: "War room" });
  await expect(room).toBeVisible();
  await expect(room.getByRole("button", { name: /Claude opus in mac-mini: working/ })).toBeVisible();
  const order = await page.locator(".station-center > section").evaluateAll((sections) =>
    sections.map((section) => section.className),
  );
  const at = order.findIndex((name) => name.includes("station-warroom"));
  expect(at).toBeGreaterThan(-1);
  expect(order[at + 1]).toContain("station-activity");
  // Who is working unfolds from Today.
  await page.getByRole("button", { name: /working · \d+ in all/ }).click();
  await expect(page.locator(".station-actor").first()).toBeVisible();
  // Agent chat is one line until opened; open, the panel is docked.
  const messages = page.getByRole("button", { name: /^Agent chat/ });
  await expect(messages).toHaveAttribute("aria-expanded", "false");
  await messages.click();
  await expect(page.getByRole("region", { name: "Agent chat" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Who worked on what · last hour" })).toBeVisible();

  // Info is the board as it was: the cards, the explorer toggle back.
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.locator(".lane-wrap.graph .board-inner")).toBeVisible();
  await expect(page.getByRole("region", { name: "War room" })).toHaveCount(0);
});

const STYLES = ["default", "engineering", "study", "studio", "cafe"] as const;

/** Sets the project's office style through the real graph save. */
async function setOfficeStyle(page: Page, style: string) {
  const response = await (await page.request.get("/api/graph")).json();
  const put = await page.request.put("/api/graph", {
    data: { graph: { ...response.graph, officeStyle: style }, baseRev: response.rev },
  });
  expect(put.ok(), await put.text()).toBeTruthy();
}

test("war room: screenshots in every office style, both themes and on a phone", async ({ page }) => {
  const dir = process.env.GAME_SHOTS_DIR;
  test.skip(!dir, "set GAME_SHOTS_DIR to write screenshots");
  mkdirSync(dir!, { recursive: true });
  test.setTimeout(300_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openStaged(page, { "vised-explorer-collapsed": "1", "vised-project-mode": "station" }, true);
  for (const style of STYLES) {
    await setOfficeStyle(page, style);
    for (const theme of ["light", "dark"] as const) {
      await page.evaluate((t) => localStorage.setItem("vised-theme", t), theme);
      await page.reload();
      const room = page.getByRole("region", { name: "War room" });
      await expect(room).toBeVisible({ timeout: 15_000 });
      await expect(room.locator(`.px-office-room.px-style--${style}`)).toBeVisible();
      // Bubbles and badges settle.
      await page.waitForTimeout(1_200);
      await room.locator(".px-office-room").screenshot({ path: join(dir!, `station-office-${style}-${theme}.png`) });
      if (style === "engineering") {
        await page.screenshot({ path: join(dir!, `station-${theme}.png`) });
        await room.locator(".px-others").screenshot({ path: join(dir!, `station-others-${theme}.png`) });
        await room.locator(".px-ground").screenshot({ path: join(dir!, `station-ground-${theme}.png`) });
        await room.locator(".px-sign").click();
        await room.locator(".px-style-popover").screenshot({ path: join(dir!, `station-style-picker-${theme}.png`) });
        await page.keyboard.press("Escape");
      }
    }
  }
  // A phone, light: the offices stack, the compact ones wrap; the page
  // scrolls sideways no more than it does with the cards.
  await page.setViewportSize({ width: 390, height: 844 });
  const pageOverflow = () =>
    page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  await page.evaluate(() => {
    localStorage.setItem("vised-theme", "light");
    localStorage.setItem("vised-project-mode", "info");
  });
  await page.reload();
  // At phone width the panel may or may not have opened itself by now.
  const close = page.getByRole("button", { name: "Close Agent chat" });
  if (await close.isVisible({ timeout: 5_000 }).catch(() => false)) await close.click();
  const baseline = await pageOverflow();
  await page.evaluate(() => localStorage.setItem("vised-project-mode", "station"));
  await page.reload();
  const room = page.getByRole("region", { name: "War room" });
  await expect(room).toBeVisible({ timeout: 15_000 });
  await page.waitForTimeout(800);
  expect(await pageOverflow()).toBeLessThanOrEqual(baseline);
  const right = await room.evaluate((element) => element.getBoundingClientRect().right);
  expect(right).toBeLessThanOrEqual(390 + 1);
  for (const part of ["px-office-room", "px-others", "px-ground"]) {
    await room.locator(`.${part}`).screenshot({ path: join(dir!, `station-phone-${part.slice(3)}.png`) });
  }
});
