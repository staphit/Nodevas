// A busy afternoon on the demo workspace (e2e/demo-serve.mjs), for the README
// stills and the feature tour (demo-stills.spec.ts) and the autopilot video:
// two runners, agents at work on public-api's tasks, one waiting to be
// accepted, one that took over after another hit its usage limit, a review,
// terminal sessions, an agent's question, and the other projects' offices.
// The routes a runner or an agent would feed are answered here; the board is
// the real one.

import type { Page } from "@playwright/test";

/** The demo's person: no real name or address. */
export const DEMO_PERSON = { name: "alex", email: "alex@example.com" };

export type Ids = Record<string, string>;

export async function nodeIds(page: Page): Promise<Ids> {
  const graph = await (await page.request.get("/api/graph")).json();
  const ids: Ids = {};
  for (const node of graph.graph?.nodes ?? []) ids[node.title] = node.id;
  return ids;
}

/** Hides the voice copilot's button: a standalone demo has no model for it,
 * and its "set it up" button would sit over what the shot is about. */
export async function hideCopilot(page: Page) {
  await page.route(/\/api\/copilot\/status/, (route) =>
    route.fulfill({ json: { enabled: false, provider: "", model: "", modes: [], allowBypass: false } }),
  );
}

export function demoRunner(id: string, name: string, ownerId: string, os: string, kinds: string[]) {
  return {
    id,
    name,
    ownerId,
    ownerName: DEMO_PERSON.name,
    createdAt: new Date(Date.now() - 7 * 86_400_000).toISOString(),
    lastSeenAt: new Date(Date.now() - 1_000).toISOString(),
    online: true,
    info: {
      version: "1.4.0",
      hostname: `${name}.local`,
      os,
      arch: os === "darwin" ? "arm64" : "amd64",
      capabilities: ["headless", "interactive"],
      agents: kinds.map((kind) => ({ kind, installed: true, models: [] })),
      repos: [{ id: "repo1", name: "public-api", path: "/src/public-api" }],
    },
  };
}

/** Answers runners, runs, budget, autopilot, Agent chat and the building. */
export async function mockBusyStation(page: Page, ids: Ids) {
  const status = await (await page.request.get("/api/auth/status")).json();
  const owner = status.actor?.id ?? "";
  const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
  const runners = [
    demoRunner("rn1", "mac-mini", owner, "darwin", ["claude", "codex"]),
    demoRunner("rn2", "build-box", owner, "linux", ["claude", "gemini", "opencode", "codex"]),
  ];
  const run = (fields: { id: string } & Record<string, unknown>) => ({
    branch: `agent/run-${fields.id}`,
    project: "public-api",
    runnerId: "rn1",
    runnerName: "mac-mini",
    agent: "claude",
    model: "opus",
    repoId: "repo1",
    repoName: "public-api",
    base: "main",
    prompt: "",
    status: "running",
    exitCode: 0,
    createdBy: owner,
    createdByName: DEMO_PERSON.name,
    createdAt: iso(600_000),
    ...fields,
  });
  const limiter = ids["Add rate limiting middleware"];
  const schema = ids["Design the quota table"];
  const dashboard = ids["Show quota in the dashboard"];
  const docs = ids["Document the limits"];
  const goal = ids["Ship the rate-limited public API"];
  const active = () => [
    run({ id: "a1", nodeId: limiter, usage: { inputTokens: 150_000, outputTokens: 32_000 }, createdAt: iso(1_500_000) }),
    run({ id: "a2", nodeId: dashboard, agent: "codex", model: "gpt-5.5", status: "offered", createdAt: iso(60_000) }),
    run({ id: "a3", nodeId: docs, runnerId: "rn2", runnerName: "build-box", agent: "gemini", model: "2.5-pro", retryOf: "a0", createdAt: iso(900_000) }),
    run({ id: "a4", nodeId: schema, runnerId: "rn2", runnerName: "build-box", agent: "opencode", model: "qwen3-coder", kind: "review", pipelineId: "p1", createdAt: iso(400_000) }),
    run({ id: "a5", nodeId: schema, runnerId: "rn2", runnerName: "build-box", agent: "codex", model: "gpt-5.5", kind: "review", pipelineId: "p1", createdAt: iso(390_000) }),
  ];
  const finished = () => [
    run({ id: "a0", nodeId: docs, runnerId: "rn2", runnerName: "build-box", status: "failed", failureKind: "rate_limit", createdAt: iso(2_400_000), finishedAt: iso(950_000) }),
    run({
      id: "a6",
      nodeId: schema,
      agent: "codex",
      model: "gpt-5.5",
      status: "succeeded",
      commit: "c0ffee1",
      filesChanged: 3,
      additions: 58,
      deletions: 2,
      prUrl: "https://github.com/example/public-api/pull/51",
      prNumber: 51,
      prState: "open",
      ciStatus: "success",
      createdAt: iso(3_000_000),
      finishedAt: iso(420_000),
      usage: { inputTokens: 64_000, outputTokens: 9_000 },
    }),
    run({ id: "a7", nodeId: dashboard, runnerId: "rn2", runnerName: "build-box", agent: "opencode", model: "qwen3-coder", status: "failed", error: "tests failed", createdAt: iso(3_300_000), finishedAt: iso(2_700_000) }),
    run({ id: "a8", nodeId: goal, agent: "claude", model: "sonnet", kind: "plan", status: "succeeded", createdAt: iso(3_500_000), finishedAt: iso(3_200_000) }),
  ];
  await page.route(/\/api\/runners(\?|$)/, (route) => route.fulfill({ json: { runners } }));
  await page.route(/\/api\/runs(\?|\/|$)/, (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() !== "GET") return route.fulfill({ json: { ok: true } });
    if (url.pathname === "/api/runs/budget") {
      return route.fulfill({
        json: { budget: { dailyRuns: 0, dailyCostUsd: 25, dailyTokens: 1_000_000 }, used: { runs: 9, costUsd: 6.4, tokens: 412_000 }, exceeded: "" },
      });
    }
    if (url.pathname === "/api/runs/routing") return route.fulfill({ json: { rules: [] } });
    if (url.pathname === "/api/runs/pipelines/p1") {
      return route.fulfill({
        json: {
          id: "p1",
          nodeId: schema,
          pipeline: { implement: { agent: "codex", model: "gpt-5.5" }, review: { agent: "opencode", model: "qwen3-coder" }, maxRounds: 3 },
          round: 1,
          status: "running",
          runIds: ["a6", "a4", "a5"],
          createdAt: iso(3_000_000),
          votes: [{ round: 1, agent: "codex", model: "gpt-5.5", verdict: "approve", runId: "a5" }],
        },
      });
    }
    const one = url.pathname.match(/^\/api\/runs\/(a\d)(\/events)?$/);
    if (one) {
      const found = [...active(), ...finished()].find((item) => item.id === one[1]);
      if (!found) return route.fulfill({ status: 404, json: { error: "no such run" } });
      if (one[2]) return route.fulfill({ json: { events: one[1] === "a1" ? limiterEvents() : [], more: false } });
      return route.fulfill({ json: found });
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
      project: "public-api",
      goalNodeId: goal,
      status: "running",
      policy: {},
      counts: { total: 4, success: 0, running: 3, waiting: 0, failed: 0, pending: 1, skipped: 0 },
      createdBy: owner,
      createdByName: DEMO_PERSON.name,
      createdAt: iso(3_600_000),
      updatedAt: iso(1_000),
    },
    tasks: [
      ["Design the quota table", "running", "review"],
      ["Add rate limiting middleware", "running", "implement"],
      ["Document the limits", "running", "implement"],
      ["Show quota in the dashboard", "none", "queued"],
    ].map(([title, state, stage]) => ({
      nodeId: ids[title],
      title,
      state,
      stage,
      try: state === "none" ? 0 : 1,
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
  const person = DEMO_PERSON.name;
  const sessions = () => [
    {
      id: "s-run",
      project: "public-api",
      actorId: owner,
      actorName: person,
      name: "public-api-claude-opus-a1",
      agent: "claude",
      model: "opus",
      state: "busy",
      since: Date.now() - 1_500_000,
      lastSeen: Date.now() - 3_000,
      online: true,
      run: true,
      runId: "a1",
      nodeId: limiter,
    },
    {
      id: "s-laptop",
      project: "public-api",
      actorId: owner,
      actorName: person,
      name: "laptop-claude",
      agent: "claude",
      model: "sonnet",
      state: "busy",
      since: Date.now() - 600_000,
      lastSeen: Date.now() - 4_000,
      online: true,
      nodeId: goal,
    },
    {
      id: "s-desk",
      project: "public-api",
      actorId: owner,
      actorName: person,
      name: "desktop-codex",
      agent: "codex",
      model: "gpt-5.5",
      state: "idle",
      since: Date.now() - 600_000,
      lastSeen: Date.now() - 6_000,
      online: true,
    },
  ];
  const held = () => ({
    id: "m-held",
    project: "public-api",
    from: `${person}/laptop-claude`,
    senderActor: owner,
    senderSession: "laptop-claude",
    to: "desktop-codex",
    body: "Please rebase onto main before you push.",
    createdAt: Date.now() - 30_000,
    status: "held",
  });
  const messages = () => [
    {
      id: "m-bubble",
      project: "public-api",
      from: `${person}/public-api-claude-opus-a1`,
      senderActor: owner,
      senderSession: "public-api-claude-opus-a1",
      to: "laptop-claude",
      body: "Token bucket is in; wiring the 429 next.",
      createdAt: Date.now() - 1_000,
      status: "delivered",
    },
    {
      id: "m-docs",
      project: "public-api",
      from: `${person}/laptop-claude`,
      senderActor: owner,
      senderSession: "laptop-claude",
      to: "public-api-claude-opus-a1",
      body: "What does the 429 body look like?",
      createdAt: Date.now() - 1_200_000,
      status: "delivered",
    },
  ];
  await page.route(
    (url) => url.pathname.startsWith("/api/agents"),
    (route) => {
      const url = new URL(route.request().url());
      if (route.request().method() !== "GET") return route.fulfill({ json: { ok: true } });
      if (url.pathname === "/api/agents") return route.fulfill({ json: { sessions: sessions(), users: [], unreadCount: 0 } });
      if (url.pathname === "/api/agents/messages") {
        if (url.searchParams.get("status") === "held") return route.fulfill({ json: { messages: [held()] } });
        return route.fulfill({ json: { messages: messages() } });
      }
      if (url.pathname === "/api/agents/asks") {
        return route.fulfill({
          json: {
            asks: [
              {
                id: "ask-1",
                project: "public-api",
                sessionId: "s-laptop",
                sessionName: "laptop-claude",
                actorId: owner,
                nodeId: goal,
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
  await page.route(/\/api\/station\/building$/, (route) => route.fulfill({ json: building() }));
  await page.route(/\/api\/usage\?/, (route) => route.fulfill({ json: usageReport() }));
}

/** A week of spend, today's matching the budget's $6.40. */
function usageReport() {
  const bucket = (tokens: number, cost: number) => ({
    inputTokens: Math.round(tokens * 0.3),
    outputTokens: Math.round(tokens * 0.1),
    cacheReadTokens: Math.round(tokens * 0.6),
    cacheWriteTokens: 0,
    totalTokens: tokens,
    costUsd: cost,
    reportedCostUsd: 0,
    estimatedCostUsd: cost,
    unpricedTokens: 0,
    runs: 0,
    sessions: 0,
  });
  const costs = [3.1, 7.8, 5.2, 9.4, 4.6, 8.1, 6.4];
  const byDay = costs.map((cost, i) => ({
    key: new Date(Date.now() - (costs.length - 1 - i) * 86_400_000).toISOString().slice(0, 10),
    ...bucket(Math.round(cost * 64_000), cost),
  }));
  const total = costs.reduce((sum, cost) => sum + cost, 0);
  return {
    days: 7,
    since: new Date(Date.now() - 6 * 86_400_000).toISOString(),
    currency: "USD",
    pricesAsOf: "2026-10-06",
    totals: bucket(Math.round(total * 64_000), total),
    byAgent: [],
    byModel: [],
    byDay,
    sessions: [],
  };
}

/** What Claude has done on the limiter so far. */
function limiterEvents() {
  const at = new Date(Date.now() - 600_000).toISOString();
  return [
    { type: "log", text: "worktree created on agent/add-rate-limiting-a1" },
    { type: "thinking", text: "The /api routes are registered in internal/server/routes.go. Read it before adding the limiter." },
    { type: "tool_call", tool: "Read", toolId: "t1", input: JSON.stringify({ file_path: "internal/server/routes.go" }) },
    { type: "tool_result", toolId: "t1", text: 'package server\n\nfunc (s *Server) routes() {\n\ts.mux.Handle("/api/", s.api())\n}' },
    { type: "tool_call", tool: "Write", toolId: "t2", input: JSON.stringify({ file_path: "internal/server/limit.go" }) },
    { type: "tool_result", toolId: "t2", text: "Wrote 48 lines." },
    { type: "text", text: "Token bucket is in; wiring the 429 next." },
  ].map((event, index) => ({ seq: index + 1, at, ...event }));
}

/** The other projects' offices (demo-serve.mjs seeds them), as GET
 * /api/station/building answers. */
function building() {
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
  const office = (name: string, fields: Record<string, unknown> = {}) => ({
    name,
    label: name,
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
    office("billing-app", {
      officeStyle: "studio",
      officeStyleSet: true,
      runs: [run("b1", "Invoice PDF"), run("b2", "Tax rules", { agent: "codex", model: "gpt-5.5", runnerId: "rn2", runnerName: "build-box" })],
      waiting: { asks: 1, held: 0, reviews: 0, total: 1 },
      asks: [{ id: "ba1", sessionId: "bs1", sessionName: "billing-claude", agent: "claude", question: "Round to cents?", nodeId: "", createdAt: now - 60_000 }],
    }),
    office("mobile-app", {
      officeStyle: "cafe",
      officeStyleSet: true,
      runs: [run("m1", "Sign-in screen", { agent: "gemini", model: "2.5-pro", runnerId: "rn2", runnerName: "build-box", status: "queued", retryAt: iso(-600_000) })],
      sessions: [{ id: "ms1", name: "mobile-codex", agent: "codex", model: "gpt-5.5", state: "idle", nodeId: "", doing: "", runId: "", lastSeen: now - 5_000, online: true }],
    }),
    office("handbook", { layout: "story", officeStyle: "study" }),
    office("site", { officeStyle: "default", officeStyleSet: true }),
  ];
  return { projects, truncated: false, total: projects.length + 1 };
}
