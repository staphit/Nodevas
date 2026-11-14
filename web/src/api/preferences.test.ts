import { describe, expect, it, vi } from "vitest";
import { AuthError, ConflictError, NetworkError, RequestError, UnauthorizedError } from "./http";
import { classifyPreferenceFailure, preferencesApi, preferenceTransport, remotePreferences } from "./preferences";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("preferences API", () => {
  it("reads the account's values, keeping only strings", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
      json({ values: { "vised-theme": "light", "vised-n": 3 }, updatedAt: { "vised-theme": "2026-11-14T00:00:00Z" } }),
    );
    expect(await preferencesApi.getPreferences()).toEqual({ values: { "vised-theme": "light" } });
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/me/preferences");
    expect(init?.cache).toBe("no-store");
  });

  it("reads a nil map as no values", () => {
    expect(remotePreferences({ values: null })).toEqual({ values: {} });
    expect(remotePreferences(null)).toEqual({ values: {} });
    expect(remotePreferences({ values: ["x"] })).toEqual({ values: {} });
  });

  it("sends a change as one PATCH, with the CSRF token and keepalive when asked", async () => {
    document.cookie = "nodevas_csrf=tok";
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => json({ values: { "vised-theme": "dark" } }));
    const answer = await preferenceTransport.save(
      { set: { "vised-theme": "dark" }, remove: ["vised-language"] },
      { keepalive: true },
    );
    expect(answer).toEqual({ values: { "vised-theme": "dark" } });
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/me/preferences");
    expect(init?.method).toBe("PATCH");
    expect(init?.keepalive).toBe(true);
    expect(JSON.parse(String(init?.body))).toEqual({ set: { "vised-theme": "dark" }, remove: ["vised-language"] });
    expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("tok");
    document.cookie = "nodevas_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
  });

  it("tells sync how to treat each failure", () => {
    expect(classifyPreferenceFailure(new UnauthorizedError())).toBe("signedOut");
    expect(classifyPreferenceFailure(new AuthError(403, "no"))).toBe("unsupported");
    expect(classifyPreferenceFailure(new RequestError(404, "no"))).toBe("unsupported");
    expect(classifyPreferenceFailure(new RequestError(405, "no"))).toBe("unsupported");
    expect(classifyPreferenceFailure(new SyntaxError("<!doctype html>"))).toBe("unsupported");
    expect(classifyPreferenceFailure(new RequestError(400, "too long"))).toBe("refused");
    expect(classifyPreferenceFailure(new RequestError(413, "too big"))).toBe("refused");
    expect(classifyPreferenceFailure(new ConflictError("", ""))).toBe("refused");
    expect(classifyPreferenceFailure(new RequestError(429, "slow down"))).toBe("retry");
    expect(classifyPreferenceFailure(new RequestError(500, "oops"))).toBe("retry");
    expect(classifyPreferenceFailure(new NetworkError())).toBe("retry");
    expect(classifyPreferenceFailure(new Error("timed out"))).toBe("retry");
  });

  it("classifies a real 401 and a server without the route", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementationOnce(async () => json({ error: "sign in" }, 401));
    const signedOut = await preferencesApi.getPreferences().catch((error: unknown) => error);
    expect(classifyPreferenceFailure(signedOut)).toBe("signedOut");

    vi.spyOn(globalThis, "fetch").mockImplementationOnce(async () => json({ error: "not found" }, 404));
    const missing = await preferencesApi.getPreferences().catch((error: unknown) => error);
    expect(classifyPreferenceFailure(missing)).toBe("unsupported");
  });
});
