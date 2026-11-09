import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError, NetworkError, onUnauthorized, RequestError, UnauthorizedError } from "./index";
import { useConnectivity } from "./connectivity";

/** A stand-in for the browser's XHR: the test decides when and how it answers. */
class FakeXHR {
  static last: FakeXHR;
  upload: { onprogress: ((event: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = {
    onprogress: null,
  };
  method = "";
  url = "";
  headers: Record<string, string> = {};
  body: unknown = null;
  status = 0;
  responseText = "";
  contentType = "application/json";
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    FakeXHR.last = this;
  }
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? this.contentType : null;
  }
  send(body: unknown) {
    this.body = body;
  }
  answer(status: number, body: unknown, contentType = "application/json") {
    this.status = status;
    this.responseText = typeof body === "string" ? body : JSON.stringify(body);
    this.contentType = contentType;
    this.onload?.();
  }
}

const pack = {
  id: "magic",
  name: { en: "Magic world" },
  version: "1.0.0",
  author: "someone",
  owner: "ann",
  sizeBytes: 2048,
  createdAt: "2026-11-01T00:00:00Z",
  updatedAt: "2026-11-01T00:00:00Z",
  mine: true,
  canDelete: true,
};

const zip = () => new File(["zip"], "magic.zip", { type: "application/zip" });

beforeEach(() => {
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.cookie = "nodevas_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
  useConnectivity.setState({ offline: false });
});

describe("theme pack routes", () => {
  it("lists, reads, deletes and sets the default at the contract's paths", async () => {
    const fetchMock = vi.fn(async (url: string, _init?: RequestInit) =>
      url === "/api/themes"
        ? new Response(JSON.stringify({ packs: [pack], default: "magic" }))
        : url.endsWith("/default")
          ? new Response("{}")
          : _init?.method === "DELETE"
            ? new Response(null, { status: 204 })
            : new Response(JSON.stringify({ schema: 1 })),
    );
    vi.stubGlobal("fetch", fetchMock);

    expect(await api.listThemePacks()).toEqual({ packs: [pack], default: "magic" });
    expect(await api.getThemeManifest("magic")).toEqual({ schema: 1 });
    expect(await api.deleteThemePack("magic")).toBeUndefined();
    await api.setDefaultThemePack("magic");

    const calls = fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? "GET"]);
    expect(calls).toEqual([
      ["/api/themes", "GET"],
      ["/api/themes/magic", "GET"],
      ["/api/themes/magic", "DELETE"],
      ["/api/themes/default", "PUT"],
    ]);
    expect(fetchMock.mock.calls[3][1]?.body).toBe(JSON.stringify({ id: "magic" }));
  });

  it("encodes an id it is given", async () => {
    const fetchMock = vi.fn(async (_url: string) => new Response("{}"));
    vi.stubGlobal("fetch", fetchMock);
    await api.getThemeManifest("a/../b");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/themes/a%2F..%2Fb");
  });
});

describe("importing a pack", () => {
  it("posts the zip as multipart with the headers every write carries, and reports progress", async () => {
    document.cookie = "nodevas_csrf=tok123";
    const progress: Array<[number, number]> = [];
    const result = api.importThemePack(zip(), (sent, total) => progress.push([sent, total]));
    const xhr = FakeXHR.last;

    expect(xhr.method).toBe("POST");
    expect(xhr.url).toBe("/api/themes");
    expect(xhr.headers["X-CSRF-Token"]).toBe("tok123");
    expect(xhr.headers["Accept-Language"]).toBeTruthy();
    // The browser sets the multipart boundary; a Content-Type here would break it.
    expect(xhr.headers["Content-Type"]).toBeUndefined();
    expect(xhr.body).toBeInstanceOf(FormData);
    expect((xhr.body as FormData).get("file")).toBeInstanceOf(File);

    xhr.upload.onprogress?.({ lengthComputable: true, loaded: 10, total: 100 });
    xhr.upload.onprogress?.({ lengthComputable: false, loaded: 20, total: 0 });
    xhr.answer(201, pack);

    await expect(result).resolves.toEqual(pack);
    expect(progress).toEqual([[10, 100]]);
  });

  it("gives the server's refusal in its own words", async () => {
    const result = api.importThemePack(zip());
    FakeXHR.last.answer(400, { error: "theme.json is missing", code: "BAD_PACK" });
    const error = await result.catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(RequestError);
    expect((error as RequestError).message).toBe("theme.json is missing");
    expect((error as RequestError).status).toBe(400);
    expect((error as RequestError).code).toBe("BAD_PACK");
  });

  it("carries the words of a 409 for an id someone else owns", async () => {
    const result = api.importThemePack(zip());
    FakeXHR.last.answer(409, { error: "someone else owns magic" });
    const error = await result.catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ConflictError);
    expect((error as ConflictError).serverMessage).toBe("someone else owns magic");
  });

  it("treats a dropped upload as the server being out of reach", async () => {
    const result = api.importThemePack(zip());
    FakeXHR.last.onerror?.();
    await expect(result).rejects.toBeInstanceOf(NetworkError);
    expect(useConnectivity.getState().offline).toBe(true);
  });

  it("treats a proxy's 502 page as the server being out of reach", async () => {
    const result = api.importThemePack(zip());
    FakeXHR.last.answer(502, "<html>bad gateway</html>", "text/html");
    await expect(result).rejects.toBeInstanceOf(NetworkError);
  });

  it("asks for a sign-in on a 401, like every other call", async () => {
    const listener = vi.fn();
    const stop = onUnauthorized(listener);
    const result = api.importThemePack(zip());
    FakeXHR.last.answer(401, { error: "no" });
    await expect(result).rejects.toBeInstanceOf(UnauthorizedError);
    expect(listener).toHaveBeenCalledOnce();
    stop();
  });
});
