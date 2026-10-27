import { afterEach, describe, expect, it, vi } from "vitest";
import { auditApi } from "./audit";
import { AuthError, ConflictError, RequestError } from "./http";

afterEach(() => {
  vi.restoreAllMocks();
});

function mockFetch(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
  );
}

const DEGRADED = {
  status: "degraded",
  writeStatus: "degraded",
  fallbackEvents: 2,
  acknowledgedFallbackEvents: 0,
  unreconciledEvents: 2,
};

describe("audit API", () => {
  it("asks for the server-wide trail by default", async () => {
    const fetch = mockFetch({ entries: [] });
    await auditApi.getAudit({ limit: 500 });
    expect(fetch.mock.calls[0][0]).toBe("/api/audit?scope=server&limit=500");
    await auditApi.getAudit({ scope: "project" });
    expect(fetch.mock.calls[1][0]).toBe("/api/audit");
  });

  it("reads a degraded health from its 503", async () => {
    mockFetch(DEGRADED, 503);
    await expect(auditApi.getAuditHealth()).resolves.toEqual(DEGRADED);
  });

  it("still fails a 503 that is not a health report, and a 403", async () => {
    mockFetch({ error: "Service Unavailable" }, 503);
    await expect(auditApi.getAuditHealth()).rejects.toBeInstanceOf(RequestError);
    mockFetch({ error: "administrator access required" }, 403);
    await expect(auditApi.getAuditHealth()).rejects.toBeInstanceOf(AuthError);
  });

  it("acknowledges by count and surfaces the 409 code", async () => {
    const fetch = mockFetch({ error: "audit fallback count changed", code: "audit_fallback_count_changed" }, 409);
    const failure = await auditApi.acknowledgeAuditHealth(2).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ConflictError);
    expect((failure as ConflictError).code).toBe("audit_fallback_count_changed");
    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe("/api/audit/health/acknowledge");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({ expectedFallbackEvents: 2 });
  });
});
