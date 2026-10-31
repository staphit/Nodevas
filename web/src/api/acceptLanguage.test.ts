import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { writePreference } from "../preferences";
import { auditApi } from "./audit";
import { streamTurn } from "./copilot";
import { authPost, req } from "./http";
import { workspaceApi } from "./workspace";
import { SOURCE_SCAN_TIMEOUT } from "../sourceScan";

// The hub words its refusals and the titles it makes up (a duplicate's
// "X copy") for the request's Accept-Language. The browser sends its own
// list, so a zh-TW browser with the app set to English got "Beta（副本）" and
// "專案 "MAIN" 已存在". Every request says the app's language instead.

afterEach(() => {
  vi.restoreAllMocks();
  writePreference("language", "en");
});

function stubFetch(body: unknown = {}) {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async () => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));
}

function sentLanguage(fetchMock: ReturnType<typeof stubFetch>): string | null {
  const init = fetchMock.mock.calls[0][1] as RequestInit;
  return new Headers(init.headers).get("Accept-Language");
}

describe("the language the hub is asked to answer in", () => {
  it("is the app's language on an ordinary request, not the browser's", async () => {
    const fetchMock = stubFetch();
    writePreference("language", "en");
    await req("/api/graph");
    expect(sentLanguage(fetchMock)).toBe("en");

    fetchMock.mockClear();
    writePreference("language", "zh-TW");
    await req("/api/graph");
    expect(sentLanguage(fetchMock)).toBe("zh-TW");
  });

  it("is the app's language on a sign-in post", async () => {
    const fetchMock = stubFetch({ ok: true });
    writePreference("language", "en");
    await authPost("/api/auth/otp/request", { email: "a@b.c" });
    expect(sentLanguage(fetchMock)).toBe("en");
  });

  it("is the app's language on an export", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("x", { status: 200 }));
    writePreference("language", "en");
    await workspaceApi.exportDocument({ format: "md" } as Parameters<typeof workspaceApi.exportDocument>[0]);
    expect(sentLanguage(fetchMock as ReturnType<typeof stubFetch>)).toBe("en");
  });

  it("is the app's language on a copilot turn", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 200 }));
    writePreference("language", "en");
    const turn = streamTurn("c1", { text: "hi" } as unknown as Parameters<typeof streamTurn>[1]);
    await turn.next().catch(() => undefined);
    expect(sentLanguage(fetchMock as ReturnType<typeof stubFetch>)).toBe("en");
  });
});

describe("requests made outside req()", () => {
  it("say the app's language on the audit health check, whose refusals are worded", async () => {
    const fetchMock = stubFetch({ status: "healthy" });
    writePreference("language", "zh-TW");
    await auditApi.getAuditHealth().catch(() => undefined);
    expect(sentLanguage(fetchMock)).toBe("zh-TW");
  });

  // Every fetch the app makes either goes through req()/authPost() or sets the
  // language itself. A new hand-rolled fetch that forgets it fails here.
  // Exempt: the health probe and the stale-bundle check (a HEAD of "/"), of
  // which only the status is read, never a word.
  // The sockets cannot set headers at all; the hub words nothing it sends
  // over them, and the client's first message (subscribe) names no language.
  it("set the language in every module that calls fetch() itself", () => {
    const exempt = new Set(["api/ws.ts", "preloadReload.ts"]);
    const root = resolve(process.cwd(), "src");
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) {
          walk(path);
          continue;
        }
        if (!/\.tsx?$/.test(entry.name) || /\.test\.tsx?$/.test(entry.name)) continue;
        const relative = path.slice(root.length + 1).replace(/\\/g, "/");
        if (exempt.has(relative)) continue;
        const source = readFileSync(path, "utf8");
        if (!/(?<![\w.])fetch\(/.test(source)) continue;
        if (!/setRequestLanguage\(|requestLanguage\(\)/.test(source)) offenders.push(relative);
      }
    };
    walk(root);
    expect(offenders).toEqual([]);
  }, SOURCE_SCAN_TIMEOUT);
});
