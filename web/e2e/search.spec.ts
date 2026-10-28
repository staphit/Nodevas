import { type APIRequestContext, request as playwrightRequest } from "@playwright/test";
import { rmSync } from "node:fs";
import { createDatabase, dropDatabase } from "./database";
import { expect, freshProject, portBase, seedWorkspace, startServer, test } from "./fixture";

interface Ranked {
  nodeId: string;
  title: string;
  snippet: string;
  match: string;
}

async function ranked(
  request: APIRequestContext,
  query: string,
  project?: string,
): Promise<Ranked[]> {
  const params = new URLSearchParams({ q: query, mode: "ranked" });
  if (project) params.set("project", project);
  const res = await searchPaced(request, params);
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = (await res.json()) as { results: Ranked[] };
  return body.results;
}

/**
 * GET /api/search, waiting out the expensive-route limiter (cost 2 against an
 * actor burst of 16) when a loop of queries spends it: a 429 is the limiter
 * doing its job, not the answer under test, so it is retried after
 * Retry-After. Any other status is returned as it came.
 */
async function searchPaced(request: APIRequestContext, params: URLSearchParams) {
  for (let attempt = 0; ; attempt++) {
    const res = await request.get(`/api/search?${params}`);
    if (res.status() !== 429 || attempt >= 5) return res;
    const wait = Number(res.headers()["retry-after"] ?? "1");
    await new Promise((r) => setTimeout(r, Math.min(Math.max(wait, 1), 5) * 1000));
  }
}

async function addNode(request: APIRequestContext, title: string, body: string) {
  const created = await request.post("/api/nodes", {
    data: { title, kind: "task", body },
  });
  expect(created.ok(), await created.text()).toBeTruthy();
}

/**
 * Waits until a ranked search names every title in want. Writes reach the
 * index in the background (about a tenth of a second after they return), so a
 * query sent the moment a node is created can rightly miss it; what is under
 * test is the ranking once it is there.
 */
async function indexed(
  request: APIRequestContext,
  query: string,
  want: string[],
  project?: string,
): Promise<void> {
  await expect
    .poll(async () => (await ranked(request, query, project)).map((r) => r.title).sort(), {
      timeout: 5_000,
    })
    .toEqual(expect.arrayContaining(want));
}

async function storyProject(page: Parameters<typeof freshProject>[0]): Promise<string> {
  const project = await freshProject(page, ["港口"]);
  await addNode(page.request, "家", "林雨的妹妹叫林曉，她從小就怕水。姐妹兩人住在老街。");
  await addNode(page.request, "市場", "市場裡人很多，有人在叫賣魚。");
  await addNode(page.request, "林曉", "角色卡。");
  await indexed(page.request, "林曉", ["家", "林曉"], project);
  await indexed(page.request, "市場", ["市場"], project);
  return project;
}

test.describe("ranked search on PostgreSQL", () => {
  test("a whole-sentence question finds the answer, an exact title comes first", async ({
    page,
  }) => {
    const project = await storyProject(page);

    const answer = await ranked(page.request, "林雨的妹妹叫什麼名字？", project);
    expect(answer.length).toBeGreaterThan(0);
    expect(answer[0].title).toBe("家");
    expect(answer[0].snippet).toContain("妹妹");

    const title = await ranked(page.request, "林曉", project);
    expect(title[0].title).toBe("林曉");
    expect(title[0].match).toBe("title");
    expect(title[1]?.title).toBe("家");
    expect(title[1]?.match).toBe("phrase");
  });

  test("search syntax typed into the box is text, not an error", async ({ page }) => {
    const project = await storyProject(page);
    for (const query of [`'a' & !b`, `林雨:*`, `!林雨 | 妹妹`, `NEAR(林雨`, `\\`, `''`]) {
      for (const mode of ["", "ranked"]) {
        const params = new URLSearchParams({ q: query });
        if (mode) params.set("mode", mode);
        const res = await searchPaced(page.request, params);
        expect(res.status(), `${mode || "plain"} ${query}`).toBe(200);
      }
    }
    const answer = await ranked(page.request, "林雨的妹妹叫什麼名字？", project);
    expect(answer[0]?.title).toBe("家");
  });
});

// The index lives in the server's database now: a restarted server on the
// same database answers without the files being re-read first. Its own
// server, so the worker's is not disturbed.
test("ranked results survive a server restart", async ({}, workerInfo) => {
  const port = portBase + 50 + workerInfo.parallelIndex;
  const workspace = seedWorkspace();
  const name = `e2e_restart_${workerInfo.parallelIndex}_${process.pid}`;
  const databaseURL = createDatabase(name);
  try {
    let server = await startServer({ workspace, port, databaseURL });
    let request = await playwrightRequest.newContext({
      baseURL: server.baseURL,
    });
    await addNode(request, "家", "林雨的妹妹叫林曉，她從小就怕水。");
    await indexed(request, "林曉", ["家"]);
    const before = await ranked(request, "林雨的妹妹叫什麼名字？");
    expect(before[0]?.title).toBe("家");
    await request.dispose();
    await server.stop();

    server = await startServer({ workspace, port, databaseURL });
    request = await playwrightRequest.newContext({ baseURL: server.baseURL });
    const after = await ranked(request, "林雨的妹妹叫什麼名字？");
    expect(after.map((r) => r.nodeId)).toEqual(before.map((r) => r.nodeId));
    await request.dispose();
    await server.stop();
  } finally {
    dropDatabase(name);
    rmSync(workspace, { recursive: true, force: true });
  }
});
