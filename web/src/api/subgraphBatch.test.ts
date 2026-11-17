import { afterEach, describe, expect, it, vi } from "vitest";
import { api, batchViaTaken, ConflictError } from ".";

afterEach(() => vi.unstubAllGlobals());

function answer(body: unknown, status: number) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })),
  );
}

describe("POST /api/graph/subgraph refusals", () => {
  it("names the live batch of a draft that was already created (BATCH_VIA_TAKEN)", async () => {
    answer({ error: "already created", code: "BATCH_VIA_TAKEN", batchId: "b0" }, 409);
    const error = await api
      .createSubgraph({ nodes: [{ key: "a", title: "A" }], batch: { via: "copilot-draft:d1", label: "x · y" } })
      .catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ConflictError);
    expect(batchViaTaken(error)).toBe("b0");
  });

  it("is not a taken via for any other 409", async () => {
    answer({ error: "stale", code: "STALE" }, 409);
    const error = await api.createSubgraph({ nodes: [] }).catch((caught: unknown) => caught);
    expect(batchViaTaken(error)).toBeNull();
    expect(batchViaTaken(new Error("x"))).toBeNull();
  });
});
