import { afterEach, describe, expect, it, vi } from "vitest";
import { writePreference } from "../preferences";
import { streamTurn } from "./copilot";
import { authPost, req } from "./http";
import { workspaceApi } from "./workspace";

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
