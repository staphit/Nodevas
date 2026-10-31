import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { markServerReachable, useConnectivity } from "./connectivity";
import { isNetworkFailure, req, RequestError } from "./http";

// Behind a reverse proxy, a stopped hub does not refuse the connection: the
// proxy answers 502/503/504 with a page of its own. That is the hub being out
// of reach, not the hub saying no, so it counts as offline — unless the body
// is the hub's own JSON error (it does send 503 itself, e.g. no mail
// transport), which stays an ordinary refusal.

beforeEach(() => markServerReachable());
afterEach(() => vi.restoreAllMocks());

function answer(status: number, body: string, contentType: string) {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(body, { status, headers: { "Content-Type": contentType } }),
  );
}

describe("a proxy's gateway error while the hub is down", () => {
  it.each([
    [502, "<html><body><h1>502 Bad Gateway</h1></body></html>", "text/html"],
    [503, "", "text/plain"],
    [504, "error code: 504", "text/plain; charset=utf-8"],
    [502, '{"message":"upstream unavailable"}', "application/json"],
  ])("counts %i (%j) as offline", async (status, body, contentType) => {
    answer(status, body, contentType);
    const error = await req("/api/graph").catch((caught: Error) => caught);
    expect(error).toBeInstanceOf(Error);
    expect(isNetworkFailure((error as Error).message)).toBe(true);
    expect(useConnectivity.getState().offline).toBe(true);
  });

  it("keeps the hub's own 503 an ordinary refusal", async () => {
    answer(503, JSON.stringify({ error: "no mail transport", code: "UNAVAILABLE" }), "application/json");
    const error = await req("/api/auth/start").catch((caught: Error) => caught);
    expect(error).toBeInstanceOf(RequestError);
    expect((error as RequestError).status).toBe(503);
    expect((error as RequestError).message).toBe("no mail transport");
    expect(useConnectivity.getState().offline).toBe(false);
  });

  it("leaves other error statuses online", async () => {
    answer(500, "<html>oops</html>", "text/html");
    await req("/api/graph").catch(() => undefined);
    expect(useConnectivity.getState().offline).toBe(false);
  });
});
