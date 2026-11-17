import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from ".";
import { REDETECT_WAIT_MS } from "./runners";
import { forgetGlobalRuns, rememberGlobalRun, WorkspaceActionError, type RunPreview } from "./runs";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockFetch(body: unknown = {}, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async () => json(body, status));
}

function calls(fetch: ReturnType<typeof mockFetch>) {
  return fetch.mock.calls.map(([url, init]) => ({
    url: String(url),
    method: (init?.method ?? "GET").toUpperCase(),
    body: typeof init?.body === "string" ? init.body : undefined,
  }));
}

beforeEach(() => forgetGlobalRuns());

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  forgetGlobalRuns();
});

describe("previewRun", () => {
  it("posts {} to a global run's workspace preview and reads the answer", async () => {
    const answer: RunPreview = {
      web: true,
      url: "http://localhost:4321/p/0123456789abcdef0123456789abcdef/index.html",
      folder: "C:/runs/g1",
      expiresAt: "2026-11-17T10:30:00Z",
    };
    const fetch = mockFetch(answer);
    rememberGlobalRun("g 1");
    await expect(api.previewRun("g 1")).resolves.toEqual(answer);
    expect(calls(fetch)).toEqual([{ url: "/api/global-runs/g%201/workspace/preview", method: "POST", body: "{}" }]);
  });

  it("posts to a project run's own route", async () => {
    const fetch = mockFetch({ web: false, folder: "C:/runs/r1" });
    await expect(api.previewRun("r1")).resolves.toEqual({ web: false, folder: "C:/runs/r1" });
    expect(calls(fetch)[0].url).toBe("/api/runs/r1/workspace/preview");
  });

  it("throws a WorkspaceActionError that keeps the gate", async () => {
    mockFetch({ error: "preview is off", code: "CONFLICT", blocked: "preview_off" }, 409);
    rememberGlobalRun("g1");
    const error = await api.previewRun("g1").catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(WorkspaceActionError);
    expect(error).toMatchObject({ status: 409, blocked: "preview_off", message: "preview is off" });
  });
});

describe("listDirs", () => {
  it("asks for program files and hidden folders", async () => {
    const fetch = mockFetch({ path: "x", parent: "", home: "C:/Users/me", dirs: [], files: [] });
    const listing = await api.listDirs("x", { files: "claude", hidden: true });
    expect(calls(fetch)[0].url).toBe("/api/fs/dirs?path=x&files=claude&hidden=1");
    expect(listing.home).toBe("C:/Users/me");
    expect(listing.files).toEqual([]);
  });

  it("asks only for the folder by default", async () => {
    const fetch = mockFetch({ path: "", parent: "", dirs: null });
    await api.listDirs();
    await api.listDirs("C:/a b", { hidden: false });
    expect(calls(fetch).map((call) => call.url)).toEqual(["/api/fs/dirs?path=", "/api/fs/dirs?path=C%3A%2Fa%20b"]);
  });

  it("encodes the stem", async () => {
    const fetch = mockFetch({ path: "", parent: "", dirs: null });
    await api.listDirs("", { files: "a&b" });
    expect(calls(fetch)[0].url).toBe("/api/fs/dirs?path=&files=a%26b");
  });
});

describe("setRunnerSettings with the easy start fields", () => {
  function hangingFetch() {
    return vi.spyOn(globalThis, "fetch").mockImplementation(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
  }

  it("sends agentPath and permissionRelay as given", async () => {
    const fetch = mockFetch({ runner: { id: "r1" }, persisted: true });
    await api.setRunnerSettings("r1", { agentPath: { claude: "C:/tools/claude.exe" }, redetect: true });
    await api.setRunnerSettings("r1", { permissionRelay: false });
    expect(calls(fetch).map((call) => JSON.parse(call.body ?? "null"))).toEqual([
      { agentPath: { claude: "C:/tools/claude.exe" }, redetect: true },
      { permissionRelay: false },
    ]);
  });

  it("waits for the fresh look when redetect is set", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const settled = vi.fn();
    const add = api
      .setRunnerSettings("r1", { agentPath: { claude: "C:/tools/claude.exe" }, redetect: true })
      .catch(settled);
    await vi.advanceTimersByTimeAsync(31_000);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(REDETECT_WAIT_MS);
    await add;
    expect(settled).toHaveBeenCalledTimes(1);
  });

  // The hub forces a fresh look whenever a path is set, so the app waits for
  // it even when the caller forgot to ask.
  it("waits for the fresh look the hub forces for a new path", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const settled = vi.fn();
    const add = api.setRunnerSettings("r1", { agentPath: { claude: "C:/tools/claude.exe" } }).catch(settled);
    await vi.advanceTimersByTimeAsync(31_000);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(REDETECT_WAIT_MS);
    await add;
    expect(settled).toHaveBeenCalledTimes(1);
  });

  it("keeps the usual wait without redetect: clearing a path, or the relay switch", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const cleared = vi.fn();
    const relay = vi.fn();
    const clear = api.setRunnerSettings("r1", { agentPath: { claude: "" } }).catch(cleared);
    const flip = api.setRunnerSettings("r1", { permissionRelay: true }).catch(relay);
    await vi.advanceTimersByTimeAsync(31_000);
    await Promise.all([clear, flip]);
    expect(cleared).toHaveBeenCalledTimes(1);
    expect(relay).toHaveBeenCalledTimes(1);
  });
});

describe("run requests carry simple", () => {
  it("sends simple on a global run and a project run", async () => {
    const fetch = mockFetch({ id: "g1", global: true });
    await api.createGlobalRun({
      runnerId: "r1",
      repoId: "repo1",
      agent: "claude",
      model: "default",
      task: "Make a page",
      openPR: false,
      simple: true,
    });
    await api.createRun({ nodeId: "n1", runnerId: "r1", agent: "claude", model: "", repoId: "repo1", simple: true });
    expect(calls(fetch).map((call) => JSON.parse(call.body ?? "null").simple)).toEqual([true, true]);
  });
});
